# pike

HTTP cache server like `varnish`.

## 测试场景

- [ ] 非GET、HEAD直接请求pass至后端
- [ ] 非文本类数据不压缩
- [ ] POST不可缓存请求，后端返回数据未压缩
- [ ] POST不可缓存请求，后端返回数据已压缩
- [ ] GET不可缓存请求，后端返回数据未压缩
- [ ] GET不可缓存请求，后端返回数据已压缩
- [ ] GET可缓存请求，后端返回数据未压缩
- [ ] GET可缓存请求，后端返回数据已压缩
- [ ] 后端返回数据未添加ETag
- [ ] 后端返回数据已添加ETag
- [ ] 304的处理