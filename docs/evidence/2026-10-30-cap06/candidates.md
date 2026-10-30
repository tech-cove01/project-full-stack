# CAP-06 长尾候选与可证边界

2026-10-30，基于本轮开始时应用源码与[独立核验](../../reviews/2026-10-30/go073-cap-09b4921/report.md)作只读分析。本文由非作者审查角色编写，未启动服务、SQL、测试或负载，没有新的性能成绩；状态仍以根STATUS及本轮实际诊断为准。CAP-07依赖CAP-06闭环，本文不提前实施或把候选当已证根因。

## 现场现象能够证明什么

既有新卷multi100档约T+120秒因P99预算早停，11988发起、11987成功、1未知。约T+110秒的最后正常样本SQL in-use2、SQL/Redis等待未增长；T+115秒采到SQL in-use20、WaitCount208及累计WaitDuration22.036秒，Rediswait160/24.172秒。下个样本恢复in-use1，SQL累计WaitCount287/141.141秒。原始来源为[该轮资源时间序列](../2026-10-30-go073-cap/qa-independent-6e6af1e78699/read/multi100-hold-control/resources.ndjson)。这些是跨请求累计等待，不是单次操作延迟，两个采样点也不能确定最先阻塞哪层。

同一异常样本的sampler起点为08:18:41.109082 UTC，API诊断body时间为08:18:42.998335 UTC，相差约1.889秒。正常前后样本差约44–97毫秒。诊断handler本身只读runtime/池统计、不执行SQL或Redis；因此该现象也需要考虑宿主调度、运行时或本机传输延后，不能只看低CPU就排除共同暂停。现有GC累计暂停、CPU与5秒样本不能证明某个唯一根因。

## 源码事实与受控验证

| 候选 | 已确定的源码事实 | 需要的最小证据及能够作出的结论 |
| --- | --- | --- |
| 全局publication跨key队头阻塞 | [counts.go](../../../backend/internal/communitycache/counts.go)的`Get`/`GetMany`先调用`epoch`获取同一个publication门，再检查L1。`publish(true)`持门执行Redis SET；`Invalidate`持门执行DEL。门保护旧加载与失效顺序，不能直接删除 | 用受控Redis hook暂停key A的SET或DEL，在确认进入后读取已经预热的独立key B，并单独取消等待者。旧版B会等待同门，能够证明跨key放大结构；本轮真实trace若同时显示publication持有/等待、SET或DEL耗时与慢请求/栈匹配，才能把它关联到本轮事故。即使修复确定性结构，也不能自动声称原独立事故根因已清楚 |
| 自然过期后的冷miss批次 | `GetMany`为生产Feed路径，L1默认2秒、Redis60秒加0–9秒抖动；miss使用一条IN SQL，但每条missing计数逐条SET，且不合并不同请求的冷batch。单键`Get`的flight不覆盖本生产批量路径 | 记录真实L1/Redis命中数、miss批大小、SQL加载次数/耗时和publication时间，观察自然过期窗口；若注入只改变冷miss延迟并可复现同等待位置，可证明冷加载的放大行为。不得将新的内容卡片singleflight提前当计数miss已合并，也不为避尾延长TTL |
| SQL往返/借连接/持连接延迟 | [projection.go](../../../backend/internal/social/projection.go)仍用RR快照执行目录、当前事实、投影完整性和本人点赞；[feed.go](../../../backend/internal/social/feed.go)在事务返回后才装配计数。池上限20，单次完整Feed有多个SQL往返 | 分开标明事务Begin总耗时、快照持有/查询耗时、计数批量SQL耗时；无法精确分开的driver/借连接时间明确不可用。负载内goroutine可区分`database/sql.DB.conn`、网络读写、GORM扫描或应用门等待。SQL全局WaitCount上升仅证明曾排队，不足以证明查询慢、池太小或某一SQL是根因 |
| Redis共享池/网络延迟 | auth、计数和正常worker共享实际Redis池10；SET/DEL由go-redis同步等待结果，timeout与重试有原配置上限 | 给出Process/ProcessPipeline整段耗时及明确scope、池累计等待快照与payload-free命令类型。普通ProcessHook整体通常包含借池与执行，不能将它标成独立RTT；只有独立可测点才拆借池/网络。若存在本机往返停顿，应按同源码受控环境对照验证，不能偷偷改持久化/池 |
| Windows/Docker本机传输或共同调度暂停 | 独立失败中API、SQL、Redis等待及诊断端点响应时间一起异常；原profile有Windows网络写调用，但profile占比不是阻塞原因证明 | 保存collector每次请求开始/结束/耗时、API body时刻、generator/容器资源及真实抓栈区间。若多个独立端点同时迟到而应用门未持有，保留环境候选。只可调整本票自有传输/部署条件并列同源码前后证据，不停止他人容器或把环境变化包装为代码收益 |

### 需要避免的因果跳步

767c修复已让公共计数装配发生在RR事务归还后。**publication等待本身不持有先前的Feed事务连接**；单独证明该门阻塞B，并不能机械推出当时SQL池20条连接全被它占住。若真实诊断观察到两者同时增长，应另用分段/负载内栈说明SQL连接当时在哪里被持有；两种结构问题可能同时存在，也可能有共同暂停。

现有[旧加载失效回归](../../../backend/internal/communitycache/counts_review_integration_test.go)的SET hook可用于最小故障注入，但该测试只证明失效最终不会被旧SET覆盖；现有取消测试的caller在进入前已经取消，也不证明在门上等待之后取消的行为。新的直接回归应建立显式barrier，不把短暂sleep当作唯一并发证据。

若修复缓存门，必须保留：同key旧publish与Invalidate的次序、新代次不被旧加载回写、L1/Redis的有界TTL，以及独立key B在A的Redis写/删暂停时仍能读取或独立取消。不可以简单把SET搬出门后丢掉栅栏，也不把某请求的事务对象绑入共享Cache。采用分key/有界分片方案时需说明冲突范围、状态回收和内存上限，不能将有意碰撞的两个key也宣称完全隔离。

## 有限诊断的停止条件

沿用STATUS的multi100/s、180秒、最多3轮诊断和每轮最多3次负载内goroutine抓取。新计时默认关闭，固定低基数标签，不输出用户/笔记/token/SQL正文；正式验证关闭重profile且A/B轻量开关相同。触发时要确认稳定generator仍在运行，并保存触发前后样本，超时/缺样也如实记录。未复现不继续无限重跑，交原因未确认、已排除项与下一最小诊断。

只有证据支持的最小修复或明确环境受控对照，才进入CAP-06D的multi100两轮/hot200一轮原预算验证。某一轮失败不得用其它成功轮替换。CAP-07的公共卡片缓存仍在其后，不能用新功能较快的成绩抵消旧长尾未闭环。
