package custommiddleware

import (
	"github.com/labstack/echo"
	"github.com/labstack/echo/middleware"
	"github.com/vicanso/pike/cache"
	"github.com/vicanso/pike/vars"
)

type (
	// CacheFetcherConfig cache fetcher配置
	CacheFetcherConfig struct {
		Skipper middleware.Skipper
	}
)

// CacheFetcher 从缓存中获取数据
func CacheFetcher(config CacheFetcherConfig, client *cache.Client) echo.MiddlewareFunc {
	// Defaults
	if config.Skipper == nil {
		config.Skipper = middleware.DefaultSkipper
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Logger().Debug("cache fetcher middleware")
			if config.Skipper(c) {
				return next(c)
			}
			pc := c.(*Context)
			if pc.Debug {
				c.Logger().Info("cache fetcher middleware")
			}
			done := pc.serverTiming.Start(ServerTimingCacheFetcher)
			status := pc.status
			if status == 0 {
				done()
				return vars.ErrRequestStatusNotSet
			}
			// 如果非cache的
			if status != cache.Cacheable {
				done()
				return next(pc)
			}
			identity := pc.identity
			if identity == nil {
				done()
				return vars.ErrIdentityNotSet
			}
			resp, err := client.GetResponse(identity)
			if err != nil {
				done()
				return err
			}
			pc.resp = resp
			done()
			return next(pc)
		}
	}
}
