# CAP-06A 同二进制观测开关短探针

2026-10-30。实际API为干净`bb03d3fe9172f3bb369cbbaa473a2ab041ec4db5`，SHA256 `a8ef9aa640164b0eaad729139e348491bafbcd19220c6a62b7a97fcc152a6d79`，关闭/开启分别PID39952/59564；同一新卷、原6400篇/32读者夹具、API SQL池20/Redis池10、GOMAXPROCS4/GOMEMLIMIT1GiB未变。off经exe/PID/UTC创建时间/hash守卫停止后才启动on，没有两个API同时消费本票依赖。详见[off元数据](api-baseline-off.json)、[off停止](api-baseline-off-stop.json)、[on元数据](api-baseline-on.json)及[夹具来源](fixture-provenance.json)。

只改变`GO_FEED_DIAGNOSTICS=false/true`；额外1Hz collector和profile在四个短探针中均未运行，原5秒安全sampler保留。probe预热1秒，稳定参数如下；各次started/native HTTP/终态/success均完全一致，unknown、dropped及系统/HTTP/内容错误均0。off/on resource各样本的`tail.enabled`实值与开关一致，关闭分支stages/slow ring为null，开启分支有固定低基数stage计数。

| 稳定短探针 | 实际完整成功 | 全部/成功P95 / P99 ms | 完整资源样本 |
| --- | --- | --- | --- |
| off 1/s，5秒 | 6/6 | 36.5727 / 38.7465 | 2/2 |
| off 25/s，15秒 | 376/376 | 35.9898 / 42.8965 | 4/4 |
| on 1/s，5秒 | 5/5 | 65.1470 / 66.1599 | 2/2 |
| on 25/s，15秒 | 376/376 | 34.6750 / 54.8452 | 4/4 |

[机器可读汇总](switch-probes.json)保留每档原result、分位、资源数及开关实值；所有原始load/control目录保留。nominal窗口边缘可能有一次额外迭代，1/s两档的6/5不是少算或把unknown补成功。

这组短测验证配置、夹具、分类、真实访问/资源与观测接线，不是容量或正式CAP-06D。先off后on、宿主噪声以及复用Redis数据后的自然缓存状态不同，不能以表中差异算观测开销百分比或断言“无开销”。实际观测成本将在诊断的poll墙钟/响应大小/采样缺失中继续披露；不据短测放宽原HTTP/资源预算。

一次纯本地汇总初稿把关闭分支合法的null stages当可迭代列表而退出，发生在输出文件写入前；改为明确的空列表投影后完成原raw复算，没有改业务或原始数据、没有因此重跑请求。后续三轮有限诊断分别记录，原核验multi100秒级长尾失败不由本探针覆盖。
