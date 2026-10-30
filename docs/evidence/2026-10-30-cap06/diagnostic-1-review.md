# CAP-06B 第1轮独立只读复核

2026-10-30，复核`baseline-diag-1`的公开原始文件及固定API二进制下的离线goroutine profile；未发HTTP/SQL、启动服务或负载。应用为干净`bb03d3fe9172f3bb369cbbaa473a2ab041ec4db5`、API59564，原TTL/池/013/6400篇新夹具未变。它是有额外1Hz观测与抓栈的诊断轮，不能充当CAP-06D正式通过或证明原事故已修。

[原result](baseline-diag-1-load/result.json)为multi100/s、原计划180秒，18001次started/native/terminal/success一致，unknown/各类错误/dropped0，P95/P99=26.4453/43.7021ms、HTTP最大242.1976ms。原资源37/37完整。未复现旧独立核验P99约1.137秒的失败，不能用本轮通过覆盖它。

## 覆盖、触发及实际冷填充

[collector manifest](baseline-diag-1-diagnostic/manifest.json)与[timeline](baseline-diag-1-diagnostic/timeline.ndjson)一致：183次poll成功、0失败，首末采样起点跨度182.023秒、最大间隔1.045秒，最大fetch约27.951ms。stable命令区间10:00:53.448–10:03:56.815 UTC为183.367秒，包含k6进程初始化/收尾；这不是逐请求180秒的原子覆盖。每个poll的fetch起止及响应内app时刻分别保留，没有原样本中约1.889秒的诊断响应迟到。

首末观测间`counts.cold_sql`增加462批、missing输入1324项，`counts.redis_write`增加1324次，`counts.redis_delete`未增。主要cold批次在稳定命令开始后约50–69秒、110–136秒以及173秒后再次出现，与保留原TTL/抖动的自然过期重建路径一致；不是全程只预热命中。missing输入不等于去重笔记数或内容加载数，首末观测也不严格等于全部请求窗口。

样本169在10:03:41.908049 UTC读取到SQL累计WaitCount从0增至10、累计WaitDuration增154.9949ms，以及新完成的多条200ms以上trace，因此按事前阈值触发唯一一次capture。其[前后窗](baseline-diag-1-diagnostic/trigger-1-window.json)有19个前样本与11个触发/后样本，早窗起10:03:22.912306、末点10:03:51.896713 UTC。抓栈10:03:41.912450–41.926214 UTC，耗时13.7785ms、6013字节，[profile](baseline-diag-1-diagnostic/goroutine-1.pprof)与声明SHA256一致，k6 PID43332在前后守卫均存活。

## 因果边界

39条不同sequence的slow trace均起止于本轮区间，均未截断。最大237.7452ms的trace10主要为`feed.facts`215.034ms，其中SQL Begin总长46.150ms、事务hold168.883ms，多个SQL call/statement约20–54ms；这些是嵌套区间，不相加。慢请求主要落在事实SQL链路，不能把Begin包含的借连接/网络拆成精确单请求池等待。

本进程观测到的publication wait/hold最大9.742/11.894ms、计数cold SQL最大12.141ms、Redis write最大11.397ms、Redis command最大31.727ms。它们不足以解释本轮较小脉冲为秒级publication阻塞，也未给原独立事故的唯一原因。全局门跨Redis I/O的源码风险仍成立，不能因本轮未触发就当它已消失。

触发poll读到的SQL in-use已经回到2。离线`go tool pprof -top/-traces <同固定API> goroutine-1.pprof`见285个goroutine，其中255个处于HTTP连接空闲读；与Feed相关的两个栈分别在MGET等待Redis回复、`feedCatalogComplete`等待MySQL读包，没有该时点的DB.conn或publication epoch等待。抓栈比触发poll所见慢请求最后完成约晚415ms，属于负载内恢复时点，不能称峰值栈或据此排除此前曾经排队；`processPendingPushNotificationWithReader`也在普通Redis命令读取回复路径中，不能仅凭函数名断言push通知/驱动故障。

**结论：第1轮未确认旧秒级长尾根因，未实施修复，CAP-06仍未闭环。** 第2轮保持同构建、夹具、TTL/池、负载和诊断开关，仍有机会捕获自然过期与较长等待，具备信息价值；它消耗既定最多3轮中的第2个名额，不为换成功结果。若剩余有限轮仍无因果证据，交“原因未确认”、已排除范围与下一最小诊断，不无限重跑，也不进入CAP-07抵消原失败。
