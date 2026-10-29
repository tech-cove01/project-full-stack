# GO-073 Kafka 首次建主题与消费者启动修复

## 发现与范围

在功能基线 `bf6b765a9b15a7f66b994ac25e17d07135abe196` 的全新独立 broker 首轮事件实验中，10 篇笔记产生 20 条 `DONE` outbox；实际 topic log end 为 20，三个消费者组成员的分区分配均为 0，projection receipt 和 ES 索引数均为 0。该实际失败保留在 [首轮报告](../2026-10-29-go073/events/initial-report.md) 及其原始快照中，不能将 outbox 已交付视为投影已收敛。

源码审阅发现 kafka-go v0.4.47 的 `NewReader` 立即启动 group；初次分配无分区时，partition watcher 如果在主题出现后才进行第一次读取，会将新分区数作为观察基线，不能据此触发重平衡。该竞态与现场现象一致，但现场并未插桩证明具体调度顺序。

本修复在后台 worker 真正构造 Reader 和加入组之前，通过不允许自动创建主题的 Kafka Metadata 查询等待配置的 topic 至少有一个可用 leader 的分区。缺失或 broker 不可用时有界重试并响应进程取消；API 启动不等待该过程。真正的 topic 创建仍由原有 relay 首次写入触发。消费组名、同步提交、`FirstOffset`（只用于无已提交位点的组）和失败后不确认的机制不变，不删除主题、不重置 offset。运行中管理员删除并立即重建同名主题的情况未覆盖，不能将此修复视为通用 topic 灾难恢复。

## 实际回归

使用专用 Compose 项目 `project-full-stack-go073-kafkafix`、全新 Kafka 3.9 broker 和端口 9122，保留专用数据卷；测试仅使用随机前缀的私有 topic 和组，不删除 topic 或改写 offset。测试使用真实 broker，processor 为测试回调，验证组/提交协议；真实 MySQL/outbox/ES 端到端投影仍由事件实验在修复提交上复验。

- [before.txt](before.txt)：修复前启动三组且没有生产者，独立读取 metadata 后发现 topic 已被消费者创建，测试 FAIL。它证明旧实现的消费者会触发建主题，不单独证明现场零分配竞态。
- [harness-initial.txt](harness-initial.txt)：最早一次测试在首次生产者写入遇到 broker 正在自动建主题的 `UNKNOWN_TOPIC_OR_PARTITION`，属于测试夹具问题，不用作修复前故障证据。随后仅对该明确的未知主题错误进行重试，不重试可能已写入的未知错误。
- [after-first.txt](after-first.txt)：首次尝试仅在 `Poll` 中预检但仍提前构造 Reader，测试仍 FAIL；据此改为就绪后才构造 Reader。
- [after.txt](after.txt)：完整修复后通过。三组启动四秒后 topic 仍不存在；生产者按实际自动创建路径写入后，各组消费第一条并确认各自 offset 为 1；同组关闭重启后只消费第二条（offset 1），未重读已提交的 offset 0。独立测试还确认 broker 不可达和预检取消不会启动 Reader 或阻塞关闭，构造器保留三个独立组的同步提交配置。

命令（已配置隔离 broker）：`GO073_KAFKA_FRESH=1 KAFKA_BROKERS=127.0.0.1:9122 go test -count=1 -run 'Test(IntegrationFreshTopicStartsAllGroupsAndPreservesOffsets|IntegrationUnavailableBrokerCancelsReadiness|TopicReadinessCancellationAndCloseDoNotStartReader|ConstructorUsesSeparateStableSynchronousGroupsAndRejectsUnknownProjection)$' -v ./internal/communityconsumer`。Go 为 1.27.0/windows/amd64；结果及各私有 topic 名见原始输出。另由主 agent 在稳定压测窗口前执行完整 Go 格式、vet、test 和 build；其记录由主报告汇总。

专用 Kafka 已正常停止，卷及测试主题/组保留供审阅，不操作其他项目的容器和数据。本报告为实现者自测，不替代独立核验或精确最终提交的 CI。
