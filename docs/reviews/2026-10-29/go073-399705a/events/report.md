# GO-073 事件链路独立实跑核验

2026-10-29；结论：**本专项通过**，范围为下面的单机单分区链路、低速稳态及受控 ES 故障恢复。不是整体 GO-073、饱和性能或生产 SLA 的结论。未调用付费 AI。

## 版本、隔离和执行

本次核验交付 SHA `399705a1fb92e5075192004c1cf1563f120dee47`；实际编译时 HEAD 为 `cc60f5d640e0eeefd5768683ecde42bda8a4e67e`，后者仅新增核验证据，使用 `git diff 399705a cc60f5d -- backend scripts/go073-event-measure.mjs scripts/go073-event-offset-sampler.py` 检查无文件差异。编译时 `backend/` 无跟踪或未跟踪改动；因为仓库有在途核验文件，[Go 构建信息](api-build.txt)的 `vcs.modified=true`，不得描述为干净构建。[API/重放二进制散列](binary-hashes.json)已保留；健康接口的 version 为 `unknown`，因未注入版本参数，实际编译身份以构建信息为准。

全新 Compose 项目 `project-full-stack-go073-independent-events`；启动前确认同名容器、卷不存在。MySQL 3431、Redis 6531、Kafka 9131、ES 9231、API 8231，四个独立新卷；MySQL 8.4、Redis 7.4、Kafka 3.9.0、ES 8.19.23，Windows Go 1.27。三个消费者组和 topic 使用全新隔离 broker，无手工建 topic、删除记录或重置 offset。迁移 [003–012](migrations.txt) 和 [搜索离线初始化](rebuild.txt)完成，API 真实连接本项目。交易及缓存核验结束后才启动本项目，CRM 等已有本机服务仍可能竞争共享宿主机资源。

为避免对原脚本放宽保护或连接旧数据，原运行脚本及采样脚本仅替换专用项目名称与端口，具体变化与运行时 SHA256 在 [脚本清单](script-manifest.json)、[测量脚本差异](measure.mjs.diff)、[采样器差异](offset-sampler.py.diff)，实际副本为 [measure.mjs](measure.mjs)、[offset-sampler.py](offset-sampler.py)。运行参数保留原脚本：预热 10 次后，按每秒一篇发布及一次点赞计划执行 180 个稳定迭代；实际首尾耗时 179.061 秒。7 次只读 Kafka 采样约每 20 秒一次，三组顺序查询，采样自身带来一定开销。

执行过程：

```powershell
$env:GO_MYSQL_PORT='3431'; $env:GO_REDIS_PORT='6531'
$env:GO_KAFKA_PORT='9131'; $env:GO_ES_PORT='9231'
docker compose -p project-full-stack-go073-independent-events -f compose.go.yaml --profile kafka --profile search up -d --wait mysql redis kafka elasticsearch
# 显式指定 MYSQL_HOST/PORT/DATABASE/USER/PASSWORD、REDIS_HOST/PORT、KAFKA_BROKERS、ES_URL/INDEX、GO_ENV=development 和 AI_ENABLED=false。
node scripts/migrate.mjs
# go build 分别构建 cmd/api、cmd/note-search；设置 GO_SEARCH_REBUILD_OFFLINE=1 后运行 search.exe rebuild，再启动 API。
# API_BASE、KAFKA_CONTAINER、ES_CONTAINER、GO_SEARCH_BINARY 以及新 EVENT_OUTPUT 均指定本核验项目。
node docs/reviews/2026-10-29/go073-399705a/events/measure.mjs
# 稳定阶段开始后单独执行：
python docs/reviews/2026-10-29/go073-399705a/events/offset-sampler.py docs/reviews/2026-10-29/go073-399705a/events/independent-offsets.json
```

上面是本次主要命令和顺序，详细输入白名单、运行配置与结果保留在脚本和原始文件中；重新执行须先创建新的项目、端口、卷和输出路径并调整白名单，不能复用本轮卷。

## 实际结果

[原始事件记录](independent-run.json)、[7 次位点采样](independent-offsets.json)、[汇总](independent-summary.json)、[逐事件核对](independent-assertions.json)。

- 10 次预热和 180 次稳定发布及点赞成功，故障阶段再发布并点赞 10 次；总计 200 篇事实、200 条活跃点赞。400 条社区 outbox 均 DONE，独立逐条核对 400 个不同事件各自三组收据，共 1200 条，状态与预期一致。200 条点赞事实/计数、发布版本和最终 ES 文档 ID/version=1 对齐，差异为 0。
- 稳定发布事件至 search APPLIED 收据 P95/P99 为 **425/790 ms**，由同库时间戳相减，包含 outbox、Kafka 和投影处理，不等于单独网络传输耗时；发布请求到 ES 首次轮询可查 P95/P99 **5086/6080 ms**，包含轮询等待，只是首次观测上界。7 次采样中 follow/feed/search 最大 lag 分别 **0/1/2**，只能代表采样结果而不是全程峰值。
- 独立 ES 容器停止后，新增 10 篇笔记有 **10 PARKED**，三个组各已提交 offset `400/400`、lag=0。恢复原容器及索引代次后，按源坐标 `380,382,...,398` 顺序人工重放，10 次均返回 committed；PARKED=0、RESOLVED=10，三组仍 `400/400`，200 个 ES 文档均可查询。重启请求至健康 **28.302 秒**，至手动重放后可查 **29.920 秒**，包括容器启动及客户端主动重放和 refresh，不是自动追平时间。

## 清理和限制

登录已 logout。API 已核对 PID 和绝对路径后用 `Stop-Process` 停止；系统未返回 exit code，未证明优雅停止。首次路径比对因路径分隔符不同而安全拒绝停止，规范化并复核真实路径后才操作。四个专用容器均已停止，MySQL/Redis exit 0、Kafka/ES exit 143，均 OOM=false，见 [退出状态](container-exit-states.txt)；五个专用端口无监听。专用卷、合成笔记、事件和已解决停车记录保留供复核，未删除卷或重置 offset；临时二进制与 API 日志在 [runtime-dir.txt](runtime-dir.txt) 记录的专用目录中，不作为已提交日志。

本次复用现有测量脚本并检查了副本差异与原始逐笔数据，不等于独立实现另一套负载生成器。稳定速率约每秒 1 对事件，未测多 broker、多分区、持续 Kafka 物理故障、生产规模或同名主题运行中删除重建；搜索可见性为轮询上界，所列观测不可解读为容量上限。
