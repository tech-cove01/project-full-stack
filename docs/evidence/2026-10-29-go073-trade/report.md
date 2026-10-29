# GO-073 交易持续负载与对账

2026-10-29，本专项在专用 MySQL/Redis 和真实 Go API 上完成 **15 秒预热 + 180 秒稳定负载**：稳定批次 901 次请求全部唯一受理、最终 901 单逐订单及用户匹配，系统错误、售罄、重复/限购、其他业务拒绝和限流均为 0；k6 dropped iterations 为 0。这是 5 请求/秒的受控定速结果，不是极限吞吐或生产容量。实现者实测与独立只读审查已完成，整票仍需独立核验。

## 可复现条件

- 功能提交 `45928e619a81a0617a062fa49d2a924f05ed12f0`；编译时 tracked `backend/` 差异为空，[构建元数据](final-build.txt)实际记录该 SHA 和 `vcs.modified=true`，不能称干净构建。后续 HEAD `25f8ecc717da3e4b77d1bf2e45df006f95bbfde7` 只改 CI；[构建后二次检查](source-check-after-build.json)相对上述 SHA 的 tracked 和 untracked backend 文件均为空。[二进制 SHA256](final-binary-sha256.json)、[实际测量脚本 SHA256](measurement-script-hashes.json)。
- [主机](host.json)为 Windows 11 64 位，i9-13900H 14 核 20 线程、约 31.63 GiB RAM；Go 1.27.0、Node 24.15.0、k6 2.2.0。Docker Desktop 的 [server 版本](docker-server-version.txt)；[镜像 ID 与停止状态](images-and-exits.txt)。MySQL 8.4.13、Redis 7.4-alpine；容器配置的 [CPU/内存限制](container-limits.txt)均为 0（未单独设置容器限额，不代表 Docker VM 或主机资源无限）。同时存在其他本机服务，不声称物理机独占。
- 专用 Compose 项目 `project-full-stack-go073-trade`，127.0.0.1 的 MySQL 3420、Redis 6520、Go API 8220，独立新卷、独立用户/门店/活动；连接池 MySQL 20 open/10 idle，Redis 10。活动初始可售库存 1145，准备 1045 个独立合成账号；默认订单支付期限 15 分钟。脚本先校验端口、数据库、容器项目/服务标签与 API 版本，之后插入合成用户并走真实验证码/登录，发布活动并等待 ready。真实交易路径不启用 Kafka、ES、AI；这些能力另有专项测量/核验，不能据此推断其整体负载。
- `scripts/go073-trade.mjs` 驱动 `scripts/load/go073-trade.js`：k6 constant-arrival-rate 5/s，预分配 20 VU，上限 60；预热及稳定各使用不重叠的账号池，稳定批次按每次迭代唯一账号发请求。正式运行需显式 MYSQL_*/REDIS_*、`API_BASE=http://127.0.0.1:8220`、`GO073_REVISION=go073-trade-45928e619a81a0617a062fa49d2a924f05ed12f0`、全新 `GO073_EVIDENCE`；`GO073_RATE=5`、`GO073_WARM_SECONDS=15`、`GO073_STABLE_SECONDS=180`，执行 `node scripts/go073-trade.mjs`。运行须先按对应 Compose 项目和端口准备容器、执行版本迁移并启动对应二进制。

## 本批结果和口径

原始 [result.json](stable-1/result.json)、[逐请求及订单对应](stable-1/requests.json)、[k6 请求日志](stable-1/requests.log)、[k6 汇总](stable-1/k6-summary.json)、[SQL 轮询](stable-1/polls.json)、[最终订单](stable-1/orders.json)、[Stream 收敛快照](stable-1/settle.json)；此前短验证和初始失败见 [smoke-report.md](smoke-report.md)，修改后的 [smoke-3](smoke-3/result.json) 用正式功能版本通过。

| 统计 | 预热 | 稳定 |
| --- | ---: | ---: |
| 请求总数 / 不同成功订单 ID | 75 / 75 | 901 / 901 |
| 售罄、重复/限购、其他业务拒绝、限流、系统失败 | 各 0 | 各 0 |
| 最终落单 / 受理订单；用户匹配 / 落单 | 75 / 75；75 / 75 | 901 / 901；901 / 901 |
| 系统错误率（系统失败 / 全部请求） | 0 / 75 | 0 / 901 |
| HTTP 受理请求耗时 P95 / P99 | 31.818 / 34.226 ms | 31.1233 / 73.4555 ms |
| 响应完成至 SQL 首次观察到订单 P95 / P99 | 1102 / 1136 ms | 1060 / 1111 ms |

k6 在稳定时长端点多启动一个迭代，所以 901/180 = 5.006 单/秒是**稳定请求批次最终成单数除名义时长**，并非精确窗口内数据库提交吞吐。以首个稳定请求开始时间近似窗口起点，在之后 180 秒内通过轮询首次观察到该稳定批次中的 899 单，即约 4.994 单/秒；既不能把其他预热订单算入该数，也不能把“首次观察”冒充精确提交时刻。

SQL 每约一秒查询活动下已提交订单，首见时间由宿主机记录查询返回时刻；本轮实际 197 次查询的间隔 P95 1012ms、最大 1016ms，查询耗时 P95 172ms、最大 400ms，查询自身也对系统有开销。首见减 HTTP 响应的值包含轮询间隔、查询和调度误差；允许原始负值并单独计数，本轮稳定负值 0。MySQL 订单 `create_time` 只有秒精度，没有拿它推算毫秒级 commit 时间。P95/P99 使用最近秩；k6 `response.timings.duration` 是发送、等待、接收时间，未包含 blocked/connect。

请求记录保存状态、分类、耗时与必要 ID，未保存完整 HTTP 响应体，因此读者无法离线重新解析原始 body；必须复跑或结合脚本检视分类逻辑。请求系统失败以所有稳定请求为分母；同批最终成单率以稳定唯一受理 ID 为分母。结果并未覆盖大量售罄或重复场景下的性能。

## 业务对账和收尾

同活动预热与稳定合计 976 个唯一受理 ID、976 条订单，逐个订单用户匹配且无额外 ID；MySQL 与 Redis 库存均为 `1145-976=169`，Redis 受理映射 976，持久拒绝事件 0、未解决 review 0。`XPENDING=0`，`XINFO GROUPS` 的 g1 lag=0，last-delivered-id 有原始记录；`XLEN=998` 含此前两轮各 11 条的短测历史，不能当本次批次订单数。消费和对账完成后才停止服务；不能以 PEL=0 单独推断无未投递消息。

[临时凭据检查](stable-1/temporary-cleanup.json)显示本次 token 文件及目录均已删除；Windows mode 参数不保证 ACL，进程被强杀仍可能残留，恢复时只检查 `fixture-progress.json` 所记录的精确临时文件。测试数据及独立卷保留，未清表或删除卷。API 中断退出码 1；Redis 容器 exit 0；MySQL 收到停止信号后 exit 137、OOM=false，[日志](mysql-stop-tail.txt)未见本次 shutdown complete，原因未确认，不声称优雅停止。所有业务对账发生在停止前。

本专项不改变业务功能，未测支付/退款连续流、饱和负载、横向扩容或生产负载。其他主线和 GO-073 整票的故障恢复、热点查询及点赞对账分别按对应证据判断；不把本结果升级为全项目独立验收。
