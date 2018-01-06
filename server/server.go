package server

import (
	"bytes"
	"encoding/json"
	"strconv"
	"time"

	"../cache"
	"../director"
	"../dispatch"
	"../performance"
	"../proxy"
	"../util"
	"../vars"
	"github.com/valyala/fasthttp"
)

var hitForPassTTL uint32 = 300

// PikeConfig 程序配置
type PikeConfig struct {
	Name                 string
	Cpus                 int
	Listen               string
	DB                   string
	DisableKeepalive     bool `yaml:"disableKeepalive"`
	Concurrency          int
	HitForPass           int           `yaml:"hitForPass"`
	ReadBufferSize       int           `yaml:"readBufferSize"`
	WriteBufferSize      int           `yaml:"writeBufferSize"`
	ReadTimeout          time.Duration `yaml:"readTimeout"`
	WriteTimeout         time.Duration `yaml:"writeTimeout"`
	MaxConnsPerIP        int           `yaml:"maxConnsPerIP"`
	MaxKeepaliveDuration time.Duration `yaml:"maxKeepaliveDuration"`
	MaxRequestBodySize   int           `yaml:"maxRequestBodySize"`
	Directors            []*director.Config
}

// getDirector 获取director
func getDirector(host, uri []byte, directorList director.DirectorSlice) *director.Director {
	var found *director.Director
	// 查找可用的director
	for _, d := range directorList {
		if found == nil && d.Match(host, uri) {
			found = d
		}
	}
	return found
}

func doProxy(ctx *fasthttp.RequestCtx, us *proxy.Upstream) (*fasthttp.Response, []byte, []byte, error) {
	resp, err := proxy.Do(ctx, us)
	if err != nil {
		return nil, nil, nil, err
	}
	body, err := dispatch.GetResponseBody(resp)
	if err != nil {
		return nil, nil, nil, err
	}
	header := dispatch.GetResponseHeader(resp)
	return resp, header, body, nil
}

func setServerTiming(ctx *fasthttp.RequestCtx, startedAt time.Time) {
	v := startedAt.UnixNano()
	now := time.Now().UnixNano()
	use := int((now - v) / 1000000)
	desc := []byte("0=" + strconv.Itoa(use) + ";" + string(vars.Name))
	header := &ctx.Response.Header

	serverTiming := header.PeekBytes(vars.ServerTiming)
	if len(serverTiming) == 0 {
		header.SetCanonical(vars.ServerTiming, desc)
	} else {
		header.SetCanonical(vars.ServerTiming, bytes.Join([][]byte{
			desc,
			serverTiming,
		}, []byte(",")))
	}
}

func handler(ctx *fasthttp.RequestCtx, directorList director.DirectorSlice) {
	startedAt := time.Now()
	host := ctx.Request.Host()
	uri := ctx.RequestURI()
	found := getDirector(host, uri, directorList)
	defer setServerTiming(ctx, startedAt)
	errorHandler := func(err error) {
		dispatch.ErrorHandler(ctx, err)
	}
	responseHandler := func(data *cache.ResponseData) {
		dispatch.Response(ctx, data)
	}
	if found == nil {
		// 没有可用的配置（）
		errorHandler(vars.ErrDirectorUnavailable)
		return
	}
	us := found.Upstream
	// 判断该请求是否直接pass到backend
	isPass := util.Pass(ctx, found.Passes)
	status := vars.Pass
	var key []byte
	if !isPass {
		key = util.GenRequestKey(ctx)
		s, c := cache.GetRequestStatus(key)
		status = s
		if c != nil {
			status = <-c
		}
	}
	switch status {
	case vars.Pass:
		resp, header, body, err := doProxy(ctx, us)
		if err != nil {
			errorHandler(err)
			return
		}
		respData := &cache.ResponseData{
			CreatedAt:  util.GetSeconds(),
			StatusCode: uint16(resp.StatusCode()),
			Compress:   vars.RawData,
			TTL:        0,
			Header:     header,
			Body:       body,
		}
		responseHandler(respData)
	case vars.Fetching, vars.HitForPass:
		resp, header, body, err := doProxy(ctx, us)
		if err != nil {
			cache.HitForPass(key, hitForPassTTL)
			errorHandler(err)
			return
		}
		statusCode := uint16(resp.StatusCode())
		cacheAge := util.GetCacheAge(&resp.Header)
		compressType := vars.RawData
		// 可以缓存的数据，则将数据先压缩
		// 不可缓存的数据，`dispatch.Response`函数会根据客户端来决定是否压缩
		if cacheAge > 0 {
			gzipData, err := util.Gzip(body)
			if err == nil {
				body = gzipData
				compressType = vars.GzipData
			}
		}
		respData := &cache.ResponseData{
			CreatedAt:  util.GetSeconds(),
			StatusCode: statusCode,
			Compress:   uint16(compressType),
			TTL:        cacheAge,
			Header:     header,
			Body:       body,
		}
		responseHandler(respData)

		if cacheAge <= 0 {
			cache.HitForPass(key, hitForPassTTL)
		} else {
			bucket := []byte(found.Name)
			err = cache.SaveResponseData(bucket, key, respData)
			if err != nil {
				cache.HitForPass(key, hitForPassTTL)
			} else {
				cache.Cacheable(key, cacheAge)
			}
		}
	case vars.Cacheable:
		bucket := []byte(found.Name)
		respData, err := cache.GetResponse(bucket, key)
		if err != nil {
			errorHandler(err)
			return
		}
		responseHandler(respData)
	}
}

func adminHandler(ctx *fasthttp.RequestCtx, directorList director.DirectorSlice) {
	ctx.Response.Header.SetCanonical(vars.CacheControl, vars.NoCache)
	switch string(ctx.Path()) {
	case "/pike/stats":
		stats, err := json.Marshal(performance.GetStats())
		if err != nil {
			dispatch.ErrorHandler(ctx, err)
		}
		ctx.SetContentTypeBytes(vars.JSON)
		ctx.SetBody(stats)
	case "/pike/directors":
		data, err := json.Marshal(directorList)
		if err != nil {
			dispatch.ErrorHandler(ctx, err)
		}
		ctx.SetContentTypeBytes(vars.JSON)
		ctx.SetBody(data)
	}
}

// Start 启动服务器
func Start(conf *PikeConfig, directorList director.DirectorSlice) error {
	listen := conf.Listen
	if len(listen) == 0 {
		listen = ":3015"
	}
	if conf.HitForPass > 0 {
		hitForPassTTL = uint32(conf.HitForPass)
	}
	s := &fasthttp.Server{
		Name:                 conf.Name,
		Concurrency:          conf.Concurrency,
		DisableKeepalive:     conf.DisableKeepalive,
		ReadBufferSize:       conf.ReadBufferSize,
		WriteBufferSize:      conf.WriteBufferSize,
		ReadTimeout:          conf.ReadTimeout,
		WriteTimeout:         conf.WriteTimeout,
		MaxConnsPerIP:        conf.MaxConnsPerIP,
		MaxKeepaliveDuration: conf.MaxKeepaliveDuration,
		MaxRequestBodySize:   conf.MaxRequestBodySize,
		Handler: func(ctx *fasthttp.RequestCtx) {
			path := ctx.Path()
			// health check
			if bytes.Compare(path, vars.PingURL) == 0 {
				ctx.SetBody([]byte("pong"))
				return
			}
			// 管理界面相关接口
			if bytes.Compare(path[0:len(vars.AdminURL)], vars.AdminURL) == 0 {
				adminHandler(ctx, directorList)
				return
			}
			performance.IncreaseConcurrency()
			defer performance.DecreaseConcurrency()
			handler(ctx, directorList)
		},
	}
	return s.ListenAndServe(listen)
}
