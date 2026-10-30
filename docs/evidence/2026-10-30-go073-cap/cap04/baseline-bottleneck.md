# CAP-04 首个受限档：串行消费基线

本报告仅复算根 agent 已采集的交易证据并审阅实际生产调用链。结果支持“单消费者串行 I/O 服务链”为优先验证的瓶颈假设，足以进行有界并行消费对照；尚未确认磁盘/fsync 根因，也未得到生产环境容量上限。分析者未运行负载、修改业务数据、instrumentation 或产品代码。机器、共享宿主、固定构建及资源条件沿用[实验计划](../plan.md)，基线 API 为[运行记录](../cap01/runtime.json)中的 `32ae24331d7de98d8df004cf854708ec3c59eec4`、PID 68744，二进制 SHA256 见[散列](../cap01/api-sha256.json)。机器可读复算见 [summary.json](summary.json)。

## 受理与持续落单分别判断

| 45 秒稳定档 | 唯一受理 / 最终匹配 | HTTP P95 / P99 ms | 末段 DB / 受理 | 末段 lag 净增 | helper 排空观察 ms | 本档预算 |
| --- | --- | --- | --- | --- | --- | --- |
| [10/s](../cap02/rate-10/result.json) | 450 / 450 | 12.4661 / 13.9301 | 140 / 140 = 1 | 0 | 5 | 通过 |
| [25/s](../cap02/rate-25/result.json) | 1125 / 1125 | 13.4285 / 23.3434 | 307 / 350 = 0.87714 | +43 | 6651 | 未持续达标 |
| [25/s 采 profile 重复](../cap02/rate-25-profile/result.json) | 1125 / 1125 | 12.8065 / 15.3812 | 304 / 350 = 0.86857 | +46 | 6820 | 未持续达标 |

三轮 dropped、系统错误、观察器错误均为 0。含预热的 551、1376、1376 笔均最终逐 ID/用户匹配；库存一致、无重复有效订单、无未决 review、最终 PEL/lag 为 0。最终收敛不使 25/s 变成持续达标档，10/s 的短探索通过也不能替代后续最高稳定档保持实验。

原 25/s 末段样本为 `atMs=1793335756073→1793335770073`，SQL 观察各约晚 2ms：DB 从 893 到 1200，受理响应 350；Redis accepted 从 1000 到 1350，lag 从 106 到 149。受理减 DB 缺口与 lag 均增长 43，两个证据一致；末段观察 DB 速率为 `307/14=21.93/s`，不能称精确提交时延。稳定期首末队列样本 lag 为 7→149、最老排队观察为 304→5974ms，表明积压持续增加，不能只归因端点采样误差。见各档 `queue.ndjson` 和 [CAP-02 独立复算](../cap02/baseline-analysis.json)。

## 资源：固定窗口后再比较峰值

以下为各稳定窗口内 **9 个完整样本**的观测峰值，CPU 为 psutil 单核 100% 口径，容器 CPU 为 Docker 口径；不同来源不相加。5 秒采样可能漏短峰，不能证明瞬时资源从未受限。

| 稳定档 | API CPU % | k6 CPU % | 宿主 CPU % | 宿主内存 % | MySQL CPU % | Redis CPU % | SQL InUse 峰值 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 10/s | 17.5 | 0.6 | 35.9 | 77.6 | 20.47 | 5.39 | 2 |
| 25/s | 30.9 | 8.1 | 40.3 | 77.6 | 36.69 | 9.40 | 3 |
| 25/s + profile | 30.0 | 4.1 | 41.5 | 78.1 | 36.01 | 7.34 | 2 |

各生成器生命周期均有 14 个完整资源样本，包含预热/阶段间隙及端点，**不覆盖后续完整排空**。此更宽窗口的宿主 CPU 峰值分别为 47.6%、47.2%、45.5%，故原 25/s 的“47.2%”与“40.3%”是不同窗口，不能混用。精确采样起止时间、样本数、峰值与累计差均在 summary 中；采样器首点的 CPU 可能尚未完成差分预热。

原 25/s 稳定采样从 `04:48:46.947865Z` 到 `04:49:26.951510Z`：SQL Open 始终 10、InUse 1–3，配置 MaxOpen 20；WaitCount/WaitDuration 增量均 0。Redis pool 的 WaitCount/WaitDuration/Timeouts 增量也为 0；不能把准备阶段留下的绝对等待计数当作本档争用。31 次 GC 的 PauseTotal 增量为 870600ns；API RSS 峰值约 31.10MiB。观测不支持先扩大连接池或调整 GC。

## SQL 与 profile 的支持范围

[SQL 前](rate25-before/manifest.json)至[SQL 后](rate25-after/manifest.json)采集区间约 `04:50:20.521089Z→04:51:42.541926Z`，包括重复档预热、稳定、排空、后台与观测开销，**不是稳定窗口本身**。计数来自各 section 的实际采样时点，顺序采集也不是原子快照：

- COMMIT 增量 3212 次、SUM_TIMER_WAIT 增量 27.492986s，差分均值约 8.55946ms；包含各种事务，不能当订单事务总耗时、单订单提交耗时或 P95/P99。
- START TRANSACTION 增量 3211 次。Innodb_row_lock_waits 增 453、row_lock_time 增 2531ms；buffer_pool_reads、Slow_queries、digest_lost 增量均为 0。
- during 瞬时 lock-waits 为空，但同时有已授予库存/订单/outbox 锁；空等待快照不能否定累计锁等待，更不能定位等待归属于哪条 SQL。`events_waits_current` 未开启，没有 fsync 等待分项。
- 历史 COMMIT `MAX_TIMER_WAIT=18446744061588337616` 接近 uint64 上限，明显不适合本轮计时解释，予以排除。差分 SUM/COUNT 只作辅助；不能引用历史最大值，也不能把累计平均相减。

[10 秒 CPU profile](rate25-during/cpu-top.txt)累计 CPU 样本 1.55s，`runtime.cgocall` 占 CPU 样本 51.61%，不是占墙钟 51.61%；Windows 的系统调用/网络栈可出现在该项，不能由其证明磁盘 fsync。[goroutine 快照](rate25-during/goroutine-top.txt)为 46 个，其中 44 个在 gopark，仅一条 order Creator/Worker 调用链；它是单时点状态，不是阻塞时间分布。profile 重复档仍出现相似积压，支持基线现象可重现，观察器自身开销仍需保留。

## 参数化 SQL 计数盲区与读实验修复

本次 digest 前后只有 COMMIT/START/SELECT NOW/SET 等 8 类增加，订单参数化 SQL 没有对应增量。后续仅做只读的[专用 trade MySQL 核查](prepared-observation.json)，确认 `statement/com/Execute`、Prepare、sql/select/insert/update、abstract/Query/new_packet 均为 YES/YES，statement/current/digest/global/thread 消费开启；不是简单的 instrument 默认关闭。P_S 全局事件 Execute 已约 13.45 万次，当前 prepared 实例却为 0；短期 prepare→execute→close 的语句实例不能用前后两次存活实例快照累计。

[MySQL 8.4 手册](https://dev.mysql.com/doc/refman/8.4/en/performance-schema-prepared-statements-instances-table.html)说明 prepared instruments 默认开启且实例在 close 时移除；[官方历史问题 97496](https://bugs.mysql.com/bug.php?id=97496)说明 binary prepared SQL 不进入 digest 的服务器限制。本机 8.4.13 实查与该历史说明吻合，未将旧版本说明扩大为所有版本保证。

另一个独立盲区是 `performance_schema.global_status` 不提供多数 `Com_*`，而 `SHOW GLOBAL STATUS` 能返回真实值；这是 [8.4 状态表文档](https://dev.mysql.com/doc/refman/8.4/en/performance-schema-status-variable-tables.html)说明的行为。因此未返回的 Com_select/Com_stmt_execute 等绝不能补 0。已按根 agent 指令单独修复 [CAP-03 读 harness](../../../../scripts/go073-cap-read.mjs)：保存完整原始 SHOW 行、全局事件 Execute/Prepare/SQL 类与各自单调增量，缺列/重复/无效整数/回退或 Uptime 不一致即失败。详见[读计划](../read-plan.md)与 [13 项解析/只读探针检查](../cap01/read-counter-checks.json)。

`Com_select` 与 `Com_stmt_execute` 有重叠，不能相加称 SQL 总数；二者均包含后台/观察器。当前无法凭其精确归因 Feed 某条 SQL 或公共计数回源，`publicCountDatabaseLoads=null`；空 digest 不等于无 DB 访问。既有函数级 20/640 对照保留原范围，不移作 HTTP 每请求查询次数。

## 待验证假设、最小候选与直接回归

上述 `32ae243` 基线的生产 [app](../../../../backend/internal/platform/app/app.go)仅启动一个 Stream Worker（后续配置改动不得倒填成本基线）。[Worker](../../../../backend/internal/orderstream/worker.go)一次读一条，串行完成 Create→review 清理→Redis 投影→ACK，并公平轮转新消息/本 consumer PEL/idle claim。成功处理后立即继续；不能把本次约 22/s 简化归因“每单固定 sleep 100ms”。[Creator](../../../../backend/internal/order/creator.go)包含 GET_LOCK、RC 事务、事实复查、库存行锁、多条 INSERT/UPDATE、COMMIT 与 RELEASE_LOCK；[交易 outbox](../../../../backend/internal/outbox/outbox.go)和[补偿 worker](../../../../backend/internal/ordercomp/worker.go)也进行独立持久化工作。当前最合理假设是单链串行 SQL/Redis 往返与事务等待叠加，尚未拆出各项占比。

可安全进行的最小对照是显式 worker 数配置默认 1、合法范围 1–8，先比较 4 个**独立 Worker 实例**，每实例拥有唯一 consumer 和独立游标/重试状态。连接池、SQL、锁、事务持久化、审计/outbox、ACK/review/重放均保持；这是验证并行是否能重叠等待的实验，不预先宣称提速。单活动库存行仍串行，可能只增加争用，需同条件复跑原 25/s 并比较队列、资源、池/锁等待和逐单事实，失败保留默认 1。

直接回归覆盖：多消费者同活动不同用户不超卖/重复；同 ID 重投和跨用户/活动冲突幂等；提交后 ACK 前中断及另一消费者 claim 恢复；poison/review/replay 与新消息公平；取消时全部 Worker 有界退出。不得为数字移除库存/身份锁或降低持久化等级；没有池耗尽证据时不调 pool。
