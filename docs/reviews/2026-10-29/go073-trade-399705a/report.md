# GO-073 独立复跑：缓存对照与持续交易

2026-10-29，已推送待核验基线 `399705a1fb92e5075192004c1cf1563f120dee47`。在两个新建、专用的 MySQL/Redis 项目上分别实际复跑缓存与交易；本报告不代替其他 agent 对 Kafka/ES 的独立复跑与完整票级结论。

复跑开始时 HEAD 已由另一核验者的纯文档提交推进至 `cc60f5d640e0eeefd5768683ecde42bda8a4e67e`，[检查](backend-diff.json)与 `399705a` 的 `backend/` tracked 差异为空；缓存 test 和交易 API 均使用此时的后端源码。API [构建信息](trade-build.txt)与 [SHA-256](trade-binary-sha256.json)记录实际 revision `cc60f5d`、`vcs.modified=true`，因此不能称交付 SHA 的干净构建。未修改后端产品源码。

## 缓存

使用专用项目 `project-full-stack-go073-verifycache` 的新卷，MySQL/Redis 端口 3400/6500；迁移 003–012 后，在 `backend/` 配置独立 MySQL/Redis 与 `GO073_CACHE_MEASURE=1`，执行 `go test -count=1 -run '^TestGO073CacheMeasurement$' -v ./internal/communitycache`。原始输出 [cache-run.txt](cache-run.txt)，测试 PASS。

100 次冷读 / 100 次热读分别产生 100 / 0 次实际 DB 查询，热读 Redis 命令亦为 0；20 轮、每轮 32 并发同键冷读共 20 次 DB 查询；32 个键冷批量 20 轮共 20 次查询，逐条对照共 640 次，L1 结构性最大 4096 条。冷读 P95/P99 为 6.044 / 9.4385 ms；热读计时全部显示 0，是本次计时分辨率以下，不代表零耗时。它测量进程内计数缓存函数，非完整 Feed HTTP 延迟；没有独立禁用 singleflight 的性能对照。

## 持续交易

在新项目 `project-full-stack-go073-verifytrade` 的新卷上使用 MySQL 3421、Redis 6521、API 8221；活动库存 1145，1045 个独立合成账号，实际登录及活动发布走 Go API。5 次/秒定速，15 秒预热、180 秒稳定，真实业务过程未启用 Kafka、ES 或 AI。定速不是饱和容量测试。

原编排脚本因固定环境护栏无法直接指向新项目，因此临时复制到操作系统临时目录，仅更换项目名与三个端口；[精确差异](isolation-script.patch)和 [脚本散列](script-hashes.json)均保存，k6 脚本沿用交付版本。实际在仓库根目录配置专用 `MYSQL_*`、`REDIS_*`、`API_BASE=http://127.0.0.1:8221`、`GO073_REVISION=go073-trade-cc60f5d640e0eeefd5768683ecde42bda8a4e67e`、`GO073_RATE=5`、`GO073_WARM_SECONDS=15`、`GO073_STABLE_SECONDS=180` 及新的 `GO073_EVIDENCE`，运行临时目录的 `trade.mjs`。二进制使用 `go build -trimpath -ldflags "-X main.revision=go073-trade-cc60f5d640e0eeefd5768683ecde42bda8a4e67e" -o <临时目录>/api.exe ./cmd/api` 构建。

稳定批次 901 次请求均为唯一受理且 901 个订单最终按 ID 和用户匹配；售罄、重复、限购、系统错误、丢弃和未决均为 0。受理耗时 P95/P99 为 24.9932 / 57.4747 ms。SQL 首次观察减响应时间 P95/P99 为 1066 / 1126 ms，包含轮询和查询误差；实际轮询间隔 P95 1012 ms、SQL 查询 P95 170 ms，并非精确提交延迟。

k6 在窗口端点产生额外迭代，稳定 cohort 最终 901/180≈5.006 单/秒是按名义时长归一化；以首个稳定请求近似窗口起点，180 秒内首次观察到 900 个该批订单，约 5 单/秒，仍不是精确提交吞吐。预热与稳定共 976 单；数据库和 Redis 库存均 `1145-976=169`，受理映射 976，Stream PEL/lag 均 0，review、拒绝与额外订单均 0。该批没有非零补偿，不能当成持续负载下补偿故障实测。

原始 [结果](stable/result.json)、[请求](stable/requests.json)、[订单](stable/orders.json)、[轮询](stable/polls.json)、[Stream 快照](stable/settle.json)、[k6 汇总](stable/k6-summary.json)及 [逐条独立复算](independent-recompute.json)保留。请求记录包含分类和必要 ID，但不保存原始 HTTP 响应体，不能离线重判 body 分类；P95/P99 按最近秩，k6 时长仅含发送、等待、接收。

## 收尾与边界

缓存 32 个夹具与匹配 Redis 键均为 0；两个专用项目的四个容器均已停止，退出 0、无 OOM，卷保留；[缓存](cache-exits.txt)、[交易](trade-exits.txt)。交易 API 在校验 PID 与可执行文件路径后用 `Stop-Process` 结束，不能称优雅退出；[临时凭据检查](trade-api-stop.json)显示临时文件和目录均已删除。交易合成数据和独立卷保留供核验，未删除数据卷或改动其他项目。

当前 Windows 11、Go 1.27.0；宿主存在其他本机服务，不是独占机器。有限的 [隐私扫描](privacy-check.json)未命中检查的手机号、Bearer、Provider key 或 authorization 值模式，不代表检查了所有秘密格式。该结果只覆盖本次隔离交易和缓存测量，不证明极限吞吐、生产 SLA 或支付/退款持续流性能。
