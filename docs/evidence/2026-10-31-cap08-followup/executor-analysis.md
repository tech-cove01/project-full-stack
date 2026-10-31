# CAP08 后续只读排查：生成器中止与时基边界

2026-10-31。本次只读旧工件、已提取的宿主事件和原生工具源码，没有启动 API、Docker 依赖、采样器或 k6，没有新增负载轮，也没有修改旧 CAP08 结果。原正式第2轮仍失败，不能用本篇解释覆盖原判据、补齐成功轮或宣布容量通过。本次原拟增加的未提交 lifecycle 两处修改已精确撤回；没有生成或运行 clocks 工具。

**旧 unknown200 不是已证明的200个 API 失败。** 它表示已有发起计数，却缺少终态计数。现有数据和 k6 的取消机制支持“阈值中止使部分执行没有可归档终态”这一解释；阻塞及排程异常如何形成，仍需与宿主电源记录分开判断。

## 原生事实与计数范围

版本来自旧 [environment.json](../2026-10-30-cap08/environment.json)：`k6.exe v2.2.0 (commit/00a9a1b7f5, go1.26.5, windows/amd64)`。以下值来自原 [stable-summary.json](../2026-10-30-cap08/formal-2-multi100-load/stable-summary.json)、[console](../2026-10-30-cap08/formal-2-multi100-load/stable-console.txt) 和 [result.json](../2026-10-30-cap08/formal-2-multi100-load/result.json)，没有倒填原 result 缺失的 `result` 字段。

| 项目 | 原始值及边界 |
| --- | --- |
| attempted / completed / success / http_reqs | 16133 / 15933 / 15933 / 15933 |
| unknown | attempted−completed=200；不是已确定的 HTTP 状态码或失败数 |
| dropped_iterations | 57；排程中未能开始的迭代，与已开始但缺终态的 unknown 分列 |
| 已分类 system / HTTP / content errors | 0 / 0 / 0；不覆盖缺终态的发起 |
| vus | 最后已采值200，最小1，最大259；是原生周期 gauge，不是精确中止瞬间的请求清单 |
| vus_max | 最后/最大已初始化275；本轮配置 preAllocatedVUs=256、maxVUs=512 |
| 已记录时延 P95 / P99 / max | 24.18684 / 84.048128 / 3656.4646 ms；不能替代缺失终态的时延 |
| stable exit / external stop | 99 / null；console 明确写 dropped_iterations 的 abortOnFail 提前停止 |

旧 [supervisor exit](../2026-10-30-cap08/formal-2-multi100-control/exit-status.json) 另为 `stop_signal=false`、`forced=false`、`errors=[]`，33个资源样本完整。未见 supervisor 发出资源停止信号或强制终止；不能把本次原生阈值中止改称 supervisor 杀进程。

## 为什么中止会留下 unknown 和零分类错误

本仓 [go073-cap-read.js](../../../scripts/load/go073-cap-read.js) 在 HTTP 调用前写 attempted；HTTP `timeout=5s`，返回后才写时延、分类，completed 位于函数末尾。没有一份独立于 VU context 的终态归档。因此已有 attempted 不保证 HTTP 已在 API 端执行完，也不保证最后几个计数已进入 k6 聚合。

k6 v2.2.0 的阈值引擎每2秒检查一次，达到 abortOnFail 条件时调用全测 `abortRun`；本仓 stable 默认启用该行为。`delayAbortEval=10s` 是最初延迟评估，不是首次 dropped 后再等待10秒。[阈值引擎源码](https://github.com/grafana/k6/blob/v2.2.0/internal/metrics/engine/engine.go#L143-L179)

`gracefulStop=10s` 给正常场景结束提供排空时间。全测 abort 取消父 context，同时取消包含该排空期的子 context，执行中的 VU 可以立即被中断，不能认为还一定有10秒供5秒 HTTP 超时和分类完成。[执行上下文与中断统计源码](https://github.com/grafana/k6/blob/v2.2.0/lib/executor/helpers.go#L66-L140)

更关键的是，custom `Metric.add()` 调用 `PushIfNotDone` 后仍返回 true；后者发现 context 已取消就不发送样本。这使中止竞争窗口中的 JS 日志与聚合计数具有不同边界。[Metric.add 源码](https://github.com/grafana/k6/blob/v2.2.0/internal/js/modules/k6/metrics/metrics.go#L110-L120)、[PushIfNotDone 源码](https://github.com/grafana/k6/blob/v2.2.0/metrics/sample.go#L119-L126)

旧 console 的3条 status0 警告可与最终分类 errors0 并存：即使执行到了 console.warn，前面的 add 也可能因取消而没有进入 summary。这是原生机制允许的解释，不是对3条日志逐条证明了具体竞争顺序。它们只证明对应日志观察到了 status0，不能替代200个 unknown 的终态，也不能从 summary 的 errors0 推出没有发生请求失败。即使给 JS 加 finally，取消后 metric 样本仍可能被丢弃，不能把它当作必然修复。

最后周期 `vus=200` 恰与 unknown200 相等，是大量执行在中止边界缺终态的相容证据；该 gauge 是周期采样，不能据此声称精确确认了200个仍在 HTTP 中的 VU，更不能断言这200个 API 均失败或均成功。[原生 VU 周期采样源码](https://github.com/grafana/k6/blob/v2.2.0/internal/execution/scheduler.go#L184-L232)

## dropped57 不等于达到512 VU上限

constant-arrival-rate 在排程时刻尝试 `TryRunIteration`。当时没有可用 VU 就立刻记 dropped，并尝试在后台逐个初始化额外 VU；只有剩余可初始化额度为0时才进入“已达 maxVUs”的告警分支。因此尚未达到 maxVUs 时，也能发生 dropped。[调度源码](https://github.com/grafana/k6/blob/v2.2.0/lib/executor/constant_arrival_rate.go#L264-L338)

本轮275个已初始化 VU 小于配置上限512，不能把 dropped57 直接解释成512上限耗尽，更不能据此盲调上限。执行变慢、暂时没有空闲 VU、初始化速度以及调度追赶均属于需要区分的因素。

同一源码按“计划时刻减 time.Since(startTime)”设置下一次 timer。如果执行暂时不能推进、恢复时计时已前进，过期排程可能快速被尝试，进而形成短时 VU 压力、dropped、阈值中止及缺终态。这是一条与宿主电源转换相容的机制推断；旧工件没有 dropped 的逐时序或每次 VU 分配记录，不能反推出57次的确切时刻或实际暂停时长。`200/100=2秒`也不是测得的暂停时长。

## 电源事件的关联与反证边界

本次 [host-events.json](host-events.json) 的正式2窗口记录：506于 UTC `14:02:49.0378364` 进入 Modern Standby，507于 `14:02:52.4047896` 退出，两条原生消息均为 Reason=Lid；相邻记录相隔3.3669532秒。它们的 ScenarioInstanceId 分别为19与21，不能伪称同一个实例的完整配对。507另有 `SleepEntered=true`、`SleepDurationInUs=2361858`、`DurationInUs=3368132`、`ActiveResidencyInUs=3368132`、`DripsResidencyInUs=0`；这些字段各自保留，不能合并成“VM准确暂停3.367秒”。

这组事件与秒级长尾、时基差在时间上相关，给出了具体宿主电源转换候选。Windows 官方说明 Modern Standby 包含不同准备/活动阶段，不能把进入到退出两个事件之间的全部时间等同于所有软件停止运行。[Microsoft Modern Standby 说明](https://learn.microsoft.com/en-us/windows-hardware/design/device-experiences/modern-standby)

原 [resources.ndjson](../2026-10-30-cap08/formal-2-multi100-control/resources.ndjson) 还给出明确边界：

| 原采样字段 | UTC / 原值 |
| --- | --- |
| 前一外层 host at / monotonic elapsed | `14:02:46.812229` / 155.0222363秒 |
| 最后一行 host at / monotonic elapsed | `14:02:51.989240` / 160.1992477秒 |
| 最后一行内 API diagnostics at / uptime_ms | `14:02:52.0422603` / 510228 |
| 两行 host/monotonic 起点间隔 | 均约5.1770114秒；原定5秒采样 |

最后 host 采样与 API diagnostics 的原始时间均落在上述506/507记录之间，故不能写“整个3.367秒宿主/API所有线程完全暂停”。5秒采样也没有显示额外完整3.367秒的宿主 poll 空洞。外层时刻、HTTP返回和 Docker stats 是顺序采样，不是一个原子时刻；这条反证只限制“全程全停”的主张，不排除更短、分阶段或局部的调度/依赖异常。最后样本的 SQL 16连接全用、Redis wait9次/累计24.7611607秒也不能被忽略，累计并发等待不能当成单个停顿时长。

## MySQL 时基差不能误写成 Linux /proc/uptime

原 [before](../2026-10-30-cap08/formal-2-multi100-load/before.json) / [after](../2026-10-30-cap08/formal-2-multi100-load/after.json) 的 MySQL `SHOW GLOBAL STATUS Uptime` 为2230→2391，即增加161秒；Node `Date.now()` 两个读取窗口的中点相差164.1015秒，绝对差3.1015秒超过既定2.261秒容差，访问计数被拒绝。这条 guard 分支独立于 k6 的 unknown/dropped 失败，仍成立。[原 access-delta](../2026-10-30-cap08/formal-2-multi100-load/access-delta.json)

这里采集的是 MySQL 状态变量，**没有采集 Linux `/proc/uptime`**。MySQL 8.4.0 官方源码中的 `show_starttime` 使用查询起始秒数减 `server_start_time`；它不是 `/proc/uptime`，也不能直接改称 Linux 单调时钟。此源码是8.4实现语义参考，不是对旧容器二进制逐字节溯源的替代。[MySQL 8.4.0 Uptime 实现](https://github.com/mysql/mysql-server/blob/mysql-8.4.0/sql/mysqld.cc#L10242-L10247)

两个域的秒级时间差不满足既定一致性判据，不单独证明发生了数据库重启、计数 reset、虚拟机暂停或特定3.1015秒的停顿。原 helper 的 `counterResets` 是包含时基不一致和负差量的复合标志；本轮原计数均未见负差量，不能将通用失败文案翻译成已证 reset。[原 helper](../../../scripts/go073-cap-read.mjs)

旧 Redis before/after 只有 INFO commandstats，应用 capacity 内的 Redis 字段是连接池状态；均不是 Redis server uptime 或 TIME。不能用它们补出缺失的 Redis 时基，也不能用 MySQL 的时间差替代 Redis 实测。现有电源事件提供了重要外部关联，但本篇不把该关联写成 VM、Redis、MySQL 的完整因果证明，不更改原正式组失败与尚未证明稳定容量的结论。
