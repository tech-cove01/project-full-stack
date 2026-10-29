# GO-073 事件实验首轮：预热失败，未进入稳定负载

2026-10-29，本轮只执行专用环境和预热，**未执行 180 秒稳定负载、物理 ES 故障或受控重放，不提供事件吞吐/延迟结论**。后续修复及重测必须另保留证据，不能覆盖本轮。

本次起始 Git HEAD 为 `bf6b765a9b15a7f66b994ac25e17d07135abe196`，原功能基线为 `ad49718`。Windows Go 1.27 编译的 `backend/bin/go073-event-api.exe` 自报 revision `bf6b765`、`vcs.modified=true`；存在在途测试文件，不能将其写成 clean HEAD 的构建。[实验脚本运行版本](initial-script.mjs) Git blob 为 `71dffdc0deddc4005210a778f869967d1e16abab`。MySQL 8.4、Redis 7.4、Kafka 3.9.0、Elasticsearch 8.19.23 均运行于 Compose project `project-full-stack-go073-event`；分别使用 3410、6510、9120、9220，API 为 8122，AI 关闭。全新 MySQL 显式执行 [003–012 迁移](migrate.txt)，预启动离线索引 [rebuild](rebuild.txt)。

脚本通过 HTTP 在约 10 秒内创建 10 篇合成笔记并各点赞一次，随后等待可查询索引；120 秒后等待超时，[原始结果](run-1.json)保留，用户 logout 成功。MySQL [事实与 outbox](initial-sql.txt)显示 10 篇、10 个 active like 与 20 条 DONE outbox，投影 receipt、停车表没有数据；[topic log end](initial-log-end.txt) 为 20，ES [索引计数](initial-es-count.json)为 0。三组 [follow](initial-follow-members.txt)、[feed](initial-feed-members.txt)、[search](initial-search-members.txt) 各有一个成员但分区数为 0；无已提交位点，不能称为“lag=0”。

[Kafka 日志](kafka-initial-lines.txt)显示三个组约 07:33:33.34 UTC 进入并稳定，随后发出 topic 自动创建请求并于 07:33:33.42 UTC 创建分区；观察到持续无分区分配与业务不收敛，支持“首次订阅时 topic 不存在导致消费组卡在空分配”的故障假设。根因及修复由独立代码工作验证，不通过人工建 topic、重置 offset 或删除事件掩盖。七轮 [位点只读采样](offset-samples-1.json)均属预热超时阶段，不是稳定负载样本；采样器见[首轮快照](initial-offset-sampler.py)。

字符串比较验证：[中途对照](string-id-midrun.txt)中实际笔记 ID 为 1–10，数字/字符串 JOIN 均为 20，错配 0。本轮无大 ID，但原测量脚本对 VARCHAR 使用数字 IN 有潜在精度风险，后续复跑先改为校验后字符串字面量。此前手工检查曾使用 PowerShell `-h127.0.0.1` 导致 mysql 参数解析为 host `127`，命令退出 1 并打印用法；随后采用 `--host=127.0.0.1` 成功，未更改数据。

实验停止自身 API 后，该进程退出码为 1，不能声称完成优雅关闭。仅对专用 Compose project 执行 `stop --timeout 60 mysql redis kafka elasticsearch`；MySQL/Redis 退出 0，Kafka/ES 退出 143，均未 OOM。保留四个数据卷、10 篇合成笔记、点赞与 20 条 outbox/事件，不删除卷、不重置 Kafka offset，便于复核；不声称业务数据已清理。其它服务未由本次 stop 操作指定。后续按最终修复 SHA 重新编译，用独立新项目、新卷错峰重新执行。
