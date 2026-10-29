# GO-073 事件持续测量和 ES 恢复：实现者自测

修复后的真实链路在独立环境中完成本轮测量。它是低速稳定事件实验及单节点受控恢复，不是 Kafka/ES 最大吞吐或生产 SLA；尚待独立核验。[原始逐笔记录](run-2.json)、[七次 Kafka 采样](offset-samples-2.json)与 [可复算汇总](event2-summary.json)均保留。

## 测量条件

Go API 从 `25f8ecc717da3e4b77d1bf2e45df006f95bbfde7` 编译；[二进制构建信息](event2-binary-info.txt)显示 Go 1.27.0、同 revision、`vcs.modified=true`，因为仓库中有在途测量文件；编译时 backend 跟踪源码无差异。后续 `556c5632ea263780c345c333fee7164272b48ae2` 是交易测量证据与脚本，未改 backend。本轮二进制 [SHA-256](event2-binary-sha256.txt)。

独立 Compose 项目 `project-full-stack-go073-event2`、全新卷，MySQL 3411、Redis 6511、Kafka 9121、ES 9221、API 8123；单进程 Go、单分区 Kafka、单节点 ES，真实 MySQL8.4/Redis7.4/Kafka3.9.0/ES8.19.23 [镜像 ID](event2-images.txt)。Go MySQL 池最大 20、空闲 10、连接最长 30 分钟；ES 堆 512 MiB、容器上限 1 GiB；同宿主仍有 CRM 等既存服务，非专用机器。AI 显式关闭。首次库 [迁移](event2-migrate.txt) 003–012 后，在 API 启动前进行 [索引离线重建](event2-rebuild.txt)；没有手工建立 topic 或重置 offset。

脚本先通过真实 API 预热 10 次公开笔记发布并各点赞，再按每秒计划一次发布和一次点赞，稳态实际为 **179.072 秒**（UTC 07:49:14.675–07:52:13.747），成功 **180 篇和 180 次点赞**，随后等待投影与查询收敛。按实际时间计算，两类各约 1.005 次/秒；不是饱和测试。独立只读 Kafka CLI 每约 20 秒采样，共 7 次，仅覆盖稳态的一部分（UTC 07:49:23.868–07:51:30.046），每次三个消费者组顺序访问；采样本身消耗 CPU/网络，与业务负载并存。[执行命令](event2-commands.md)、[脚本快照](event2-script.mjs)、[采样器快照](event2-offset-sampler.py)及 [哈希](event2-script-sha256.txt)与实际执行一致；汇总可用 `python scripts/go073-event-analyze.py docs/evidence/2026-10-29-go073/events/run-2.json docs/evidence/2026-10-29-go073/events/offset-samples-2.json` 复算。百分位按 nearest-rank。

## 稳态观测

| 测量 | 样本 | P50 | P95 | P99 | 最大值 |
| --- | ---: | ---: | ---: | ---: | ---: |
| 发布 HTTP 请求耗时 | 180 | 27 ms | 43 ms | 96 ms | 496 ms |
| 发布事件 → feed APPLIED 收据 | 180 | 143 ms | 393 ms | 747 ms | 1,333 ms |
| 发布事件 → search APPLIED 收据 | 180 | 182 ms | 434 ms | 1,302 ms | 1,366 ms |
| 请求开始 → ES 首次轮询可查 | 180 | 3,079 ms | 5,190 ms | 6,099 ms | 6,147 ms |

收据延迟使用同一 MySQL 服务内 outbox `create_time` 到 receipt `applied_at` 的 `TIMESTAMPDIFF`，精度为字段及数据库时钟精度；它不是 Kafka 单独传输耗时，也不是搜索可见时间。搜索每完成约 5 次发布轮询一次，末尾 1 个在稳定期后收敛轮询中补记；表中是**首次观测上界**，包含发请求、索引处理和轮询等待，不能称为精确索引延迟。余下 LIKE 的 IGNORED 和 follow 组数据见汇总与原始逐事件记录。

七次采样中，follow/feed/search 各组采样最大 lag 均为 **2 条**；各组调用时间不完全相同，不能将样本最大值当作全程峰值。故障前屏障时 190 篇事实、190 条 active 点赞、380 条 DONE outbox，三组位点各为 `380/380`，feed 与 search 各 190 APPLIED + 190 IGNORED，follow 为 380 IGNORED。

## ES 故障与精确重放

| UTC 阶段 | 观察 |
| --- | --- |
| 07:52:20.998 发出停止请求；07:52:24.294 容器停止 | 仅停止校验过 project/service 标签的本轮 ES 容器 |
| 07:52:36.747 停机停车快照 | 新发布及点赞各 10 次；200 篇/200 个 active 点赞、400 条 DONE outbox，search 190 APPLIED + 10 PARKED；三组均 `400/400`、lag 0。自停止请求至这次停车快照 **15.749 秒**，包含发布及三次 Kafka CLI 查询，不能当成精确停车耗时 |
| 07:52:36.747 发出重启请求；07:53:04.455 健康 | 同一容器重启至健康 **27.708 秒**，原 alias 仍指向 `go073-event-notes-build-e8898a3e50b0b41d` |
| 07:53:06.150 搜索收敛 | 健康后由客户端顺序精确重放本轮 10 个停车坐标（offset 380、382、…、398），主动 refresh；从重启请求到 200 个版本 1 文档均可查询 **29.403 秒**。包含 Docker 启动和客户端十次手动重放，不是自动追平 |
| 07:53:12.605 恢复后屏障 | 三组仍 `400/400`；search PARKED 0、RESOLVED 10，search/feed 各 200 APPLIED + 200 IGNORED，follow 400 IGNORED |

对账逐笔检查每篇恰好一条 NOTE_PUBLISHED 和一条 LIKE_CHANGED 的 DONE outbox、每条事件的三组收据及预期 APPLIED/IGNORED、每条笔记的活跃点赞事实与计数、发布版本及实际 ES 版本；200 篇均无差异。Kafka lag 为 0 时仍有 10 个持久停车记录，不能仅据位点判断搜索已恢复。

## 收尾和边界

脚本 `success=true`、零失败，使用的开发登录已 logout。停止 API 时 Ctrl-C 得到进程退出码 1，不能声称优雅退出；仅停止本项目四个容器，MySQL/Redis exit 0、Kafka/ES exit 143，均无 OOM，见 [停止状态](event2-stop.txt)。保留四个专用卷、200 篇合成笔记、点赞、outbox/收据和 RESOLVED 停车记录，不宣称业务数据清理，也不重置 offset。筛选后的 [API 日志](event2-api-filtered.txt)仅供状态核对；本轮未测试多 broker、多分区、持久 Kafka 故障或并发多个业务消费者。首轮失败及保留现场见 [原报告](initial-report.md)。
