# CAP-06A/B 有限诊断：独立只读审读

2026-10-30。复核新观测差量、直接回归记录、三轮公开raw/collector、API与夹具元数据；第1轮结束后用同固定二进制离线解析其goroutine profile。本人未发HTTP/SQL、启动服务/负载或执行测试，未改原始证据。**结论：观测和有限诊断交付无阻断；旧multi100秒级长尾原因仍未确认，CAP-06C/D尚未实施，CAP-07前置未满足。** 不能把诊断轮通过改写成原事故已修或正式容量验收。

## 三轮实际范围

三轮均为相同干净应用`bb03d3fe9172f3bb369cbbaa473a2ab041ec4db5`、[API59564](api-baseline-on.json)、二进制SHA256`a8ef9aa640164b0eaad729139e348491bafbcd19220c6a62b7a97fcc152a6d79`；API metadata原字节hash、相同6400篇新夹具hash`31f9b9b4b9a43372cbb147ef6ae24a716d67f7619f8032919393464f7ac2bc2c`、collector源码hash与命令记录一致。TTL、池、013和固定multi100/s×180秒协议保持，额外1Hz观测与profile有开销，不作正式D轮。

| 原始结果 | started/native/terminal/success | P95/P99 ms | 资源完整 | 额外poll成功/失败 | capture / 本轮新slow trace |
| --- | --- | --- | --- | --- | --- |
| [第1轮](baseline-diag-1-load/result.json) | 各18001 | 26.4453 / 43.7021 | 37/37 | 183/0 | 1 / 39 |
| [第2轮](baseline-diag-2-load/result.json) | 各18001 | 29.2595 / 40.4513 | 37/37 | 183/0 | 0 / 0 |
| [第3轮](baseline-diag-3-load/result.json) | 各18000 | 31.5160 / 40.9273 | 37/37 | 182/0 | 0 / 0 |

unknown、dropped及已分类系统/HTTP/内容错误均0，三次stage/collector退出0；三个exclusive诊断slot已使用，没有第4轮。第2/3轮的ring仍有第1轮sequence8–39的32条历史记录，latest sequence固定39；按本轮起止过滤后fresh为0，不能重复当作新长尾。

三轮collector首末样本之间的cold SQL分别增加462/435/419批，missing输入1324/1377/1369项，Redis写入数分别对应1324/1377/1369，证明确实经过自然过期后的冷填充，没有用全程热命中规避。这里是采样首末增量及missing输入量，不是唯一笔记数、逐请求全窗口或CAP-07内容loader成绩。第2/3轮SQL/Redis池等待首末均无增量；最大fetch约28.86/27.96ms，没有复现原核验诊断迟到约1.889秒。

## 能够和不能够排除的项

[第1轮详审](diagnostic-1-review.md)核实唯一capture由SQL wait+10/154.995ms及新完成200ms以上trace触发，10:03:41.912–41.926 UTC前后生成器均在。39条本轮slow trace无截断，最高237.745ms主要在事实SQL Begin/hold/多个往返；publication wait/hold本进程最大9.742/11.894ms，不能解释该较小脉冲为秒级缓存门阻塞。

这只能排除**本轮已捕获小脉冲**的上述解释，不能排除旧事故的publication跨key结构风险、SQL/Redis网络等待或共同宿主暂停。capture比trigger所见最后慢请求完成晚415ms，SQL in-use已回到2；285个栈中255个HTTP空闲，Feed相关栈各在Redis/MySQL读包，没有当时的DB.conn或publication排队。它是负载内恢复时点，不能伪称峰值栈或用空栈否认此前排队。三轮未出现旧1.137秒现象，不等于其原因已排除；本次宿主背景也不能与旧轮当单变量因果对照。

新观测默认关闭、固定低基数、32×256有界ring及截断标志合理；SQL Begin包含借池和往返、statement包含执行/读取/扫描，Redis整命令包含借池/传输，未伪造纯等待分拆。`feed.http`是handler区间而非完整网络RTT。真实wrapper回归与普通/真实social/cache范围见[回归报告](observation-regression/report.md)，既有Service用例默认未启wrapper的限制已明确。

[已保存CI](ci-1bed127.json)实际对应工具提交`1bed127a532307f2d24b03e1ef07e4b667bdfd59`，四作业成功，包含Linux Go race及真实依赖；其backend与所测bb03源码无差异。它不等于本机诊断期间race、原长尾已修或之后每个文档提交的CI。

## 保留未闭环状态及下一最小诊断

旧[独立失败](../../reviews/2026-10-30/go073-cap-09b4921/report.md)完整保留。当前只可交“有限三轮未复现/原因未确认”，不为完成任务无依据改publication顺序、扩大池、延长TTL或先做CAP-07。C/D需新的可验证因果/受控证据及原正式判据，不能拿这三轮诊断凑两轮D。

若核验后批准下一份最小诊断计划，优先补一次**受控Redis写/删暂停的A键与独立已命中B键实验**，验证计时/负载内栈确能区分publication放大与底层往返；同步核等待后取消和旧加载/Invalidate顺序。它只能确认结构机制与观测有效性，仍不能自行断言旧现场根因。实际偶发长尾需再将未完成请求的阶段年龄/触发时点与依赖往返、宿主调度对齐，以避免仅在请求完成后抓到恢复栈；本审读仅列建议，未新增第4轮、产品修复或观测平台。
