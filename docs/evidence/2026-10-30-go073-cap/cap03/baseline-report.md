# CAP-03 索引前真实 HTTP Feed 基线

2026-10-30，固定应用源码 `3db296dd0beedd6f85d06cf5479d707b0498ce7e`，同一 API PID 45532、同一 6400 篇合成笔记/32 作者/32 读者/每页 20 条。API `ORDER_STREAM_WORKERS=1`、`GOMAXPROCS=4`、`GOMEMLIMIT=1GiB`；MySQL 4 CPU/2 GiB、Redis 2 CPU/512 MiB。Kafka/ES/AI 关闭，实际响应逐项验证为数据库 Feed。完整硬件、镜像/ID、连接池、构建和二进制 SHA 见 [environment.json](environment.json)、[api-build.txt](api-build.txt)、[initial-diagnostics.json](initial-diagnostics.json)。这是共享 Windows/Docker 宿主的本轮条件，不是通用生产容量。

根 agent 明确释放交易环境和归档 CPU 后才启动本环境。Docker 默认地址池已满，首次 [启动失败](compose-start.txt) 原样保留；仅本项目通过额外 override 使用经检查无重叠的 `10.253.51.0/24`，不删旧网络/卷、不改全局 Docker。公开 [网络检查](network-selection.json) 仅保留选中网段、30 个 Docker 网段/38 条非默认路由的检查数量和结论，其他原始网络详情在仓外私有目录。observer 经容器 socket root 创建，仅 SELECT 本库与 performance_schema；凭据不落证。此网络修复经 cap_trade 内部独立审查无阻断。

1 req/s、5 秒短探针实际 6/6 正确，2/2 完整资源样本，仅证明测量链路可用，不计容量。以下正式探索原计划各稳定 30 秒、预热 10 秒/25 req/s、64 预分配/512 最大 VU；首限触发原 k6 阈值后提前停止，不强撑时长。hot 为一位读者/20 key，multi 为 32 读者轮转/640 key。

| 模式/目标 req/s | Started / 已分类 / 成功 | 全请求 P95 / P99 ms | unknown / dropped | 结果 |
| --- | --- | --- | --- | --- |
| hot 25 | 751 / 751 / 751 | 25.246 / 29.541 | 0 / 0 | 30 秒通过 |
| hot 50 | 1500 / 1500 / 1500 | 25.636 / 31.554 | 0 / 0 | 30 秒通过 |
| hot 100 | 3001 / 3001 / 3001 | 28.074 / 34.763 | 0 / 0 | 30 秒通过 |
| hot 200 | 1902 / 1390 / 1241 | 5000.191 / 5000.640 | 512 / 491 | 约 12 秒早停 |
| hot 150 | 4501 / 4501 / 4501 | 30.600 / 37.678 | 0 / 0 | 30 秒通过 |
| hot 175 | 1806 / 1465 / 1443 | 3142.938 / 5000.091 | 341 / 288 | 约 12 秒早停 |
| hot 160 | 4800 / 4800 / 4800 | 35.965 / 45.173 | 0 / 0 | 30 秒通过 |
| multi 150 | 4501 / 4501 / 4501 | 37.492 / 61.178 | 0 / 0 | 30 秒通过 |
| multi 175 | 1794 / 1446 / 1427 | 3161.648 / 4999.757 | 348 / 301 | 约 12 秒早停 |

各次实际进程墙钟、成功请求分位、HTTP/系统/内容分类和原始路径见 [baseline-summary.json](baseline-summary.json)，每档 `*-load/result.json`/k6 summary 与 `*-control/resources.ndjson` 原样保留。正常端点额外迭代可能产生计划整数外的一次请求；名义 RPS 分母固定原计划秒数。早停不把该名义比值当持续吞吐，started 也不等于已收到服务端响应或服务端完成。系统/HTTP 错误有交叠，不相加。

首次 hot200 暴露早停未完成调用未显式列出的问题，已新增终态 completed 与 native HTTP 对账，要求完整分类才能通过。旧 raw 不改，旧 Rate 分类独立重算 1390、unknown512；完整修复/范围/内部审查见 [completion-meter-review.md](completion-meter-review.md)。所有通过档都分类完整、错误和 dropped 为 0。受限三档分别有 149、22、19 个已分类系统/HTTP 错误，其余未知数单列，不能补零。

## 瓶颈与实际访问

通过档均有 7/7 完整资源样本，覆盖约 30 秒、约 5 秒间隔；受限档约 12 秒运行，有 3/3 完整样本，其中两个有效 CPU 区间。CPU 仅报告至少 1 秒的有效区间，旧版本排除首个极短 priming 样本；首样原值保留，不将 530.8%/1309% 之类极短区间读数用作 API 稳态峰。新 sampler 的 valid 标志与源码 SHA 同时落证，不改变既定预算。

hot150、hot160、multi150 在资源采样首末间的 SQL pool wait 增量都为 0、采样中最多 4/5/6 个 in-use；MySQL CPU 采样峰约 232%/280%/287%。hot175、hot200、multi175 均采到池占满 20；采样首末间 WaitCount 增量 1145/1572/1192，累计 WaitDuration 约 833/1859/873 秒（跨并发等待累计，不能当单请求延迟），MySQL CPU 采样峰约 417%/427%/398%，接近配置的四核资源。受限档 API/k6 的有效 CPU 采样峰均低于单核 46%/32%，RSS 和宿主内存未触预算；host 有效区间峰低于 61%，没有资源 stop。VU 增长和 dropped 伴随实际 SQL/响应变慢，不能将 VU 数或预设目标直接当吞吐，亦不声称排除了所有共享宿主因素。

阶段之间由 cap_observe 对真实 hot reader35/author3 执行同一完整主 SQL 的 EXPLAIN JSON/ANALYZE，保留 author IN、当前关系 EXISTS、LEFT JOIN shop、公开状态和 LIMIT20。证据见 [baseline-plan/manifest.json](baseline-plan/manifest.json)：单次 ANALYZE 主查询约 11.3 ms，`idx_blog_feed(status)` 读取 6400 行后过滤作者 200 行、连接商铺并 Top20 排序。这个计划直接证实当前工作负载全扫/排序，不仅是累计 rows_examined 的推断；它是阶段间单次 SQL 观测，不是 HTTP 时延或吞吐成绩。未在本基线中改 SQL、索引、池或 instrumentation。

所有档保存实际 SHOW GLOBAL STATUS、statement/com/Execute 和 Redis commandstats 前后增量。例：hot150/multi150 的全实例 Com_stmt_execute 为 18345/18343；mget 为 15/469。Com_select 与 stmt_execute 有交叠，不能相加为总 SQL；参数化 SQL digest 不完整，不能从空 digest 声称零回源。访问计数包括观察器、后台空队列轮询、读事务和鉴权续期；hot/multi 不能当缓存优化的因果提升百分比。各档 Uptime 一致、counter reset 与 digest/prepared lost 增量均 0。

这是未优化探索基线，最高只验证 hot160/multi150 的 30 秒档，尚未宣称 180 秒可持续边界。根 agent 为保持单一主要变量，要求在两模式 150 通过/175 受限和实际计划证据齐备后暂停，先在独立 opt-tests 环境验证正式 013 索引迁移和现有 social 正确性，再对同一数据/API/资源应用索引并重跑 175/200、后续阶梯及最终 180 秒两轮。暂停时 [恢复检查](recovery-after-baseline.json) 为 health UP、原 PID、SQL in-use0、goroutines25。API/依赖和夹具保留，无正式 k6 负载运行。
