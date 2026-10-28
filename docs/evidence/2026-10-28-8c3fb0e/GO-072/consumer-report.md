# GO-072 / Kafka 消费与持久结果后提交 offset

2026-10-28（Asia/Shanghai）。开始 SHA `8c3fb0e0fe1169ad98e19faaf7ae49f907362530`；交付 SHA 由总报告记录。本组件自测通过，待独立核验。依赖现有社区 relay、`communityprojection` 的事件/版本事务以及 Runtime 的持久停车与受控重放；不认领交易 outbox，不修改社区事实写 API。适用 EVT-02、EVT-03 的消费恢复子集，不将本机正确性实验作为持续容量或五秒追平 SLA。

## 实现与提交条件

`communityconsumer.NewWorker(brokers, topic, projection, processor)` 只接受 `follow-v1`、`feed-v1`；未知名字返回错误。两者的生产 group 分别固定为 `community.follow-v1`、`community.feed-v1`，独立消费配置中的同一 topic。`Processor.Apply(ctx,projection,Message)` 与 `Park(ctx,projection,Message,cause)` 的 nil 错误必须表示 DB 提交成功；消息结构保留 topic、partition、offset、key、body 和全部 headers，不将 Kafka 客户端耦合到投影存储。Runtime 校验事件域、key/header 一致性，仓储将去重记录与效果放在同一个事务；详情见相邻仓储/恢复报告。

生产代码仅调用 `FetchMessage` 和显式 `CommitMessages`，`CommitInterval=0`。每 worker 单 goroutine 顺序处理；当前消息未得到持久 Apply 或持久 Park 证明时，不处理后续消息，不提交更高 offset。Kafka 客户端内部可能预取，应用不会将预取当作持久效果，也不使用会自动提交的 `ReadMessage`。上述语义已直接核对本地锁定依赖 kafka-go `v0.4.47` 的 reader.go。

Apply 最多连续尝试 3 次；仍失败则调用 Runtime 持久 Park。Park 必须保留原始 source/key/body/headers、内容 hash、固定错误分类 `apply_failed` 与 PARKED 状态，以便追踪与受控重放；内存队列不能替代此证明。DB 不可用时 Park 失败，worker 保留当前消息持续重试停车，不提交 offset。停车恢复之后才可以提交并处理后续消息。该策略可能将已经恢复的瞬时故障事件保留为 PARKED，运维须显式重放；不宣称停车等于效果应用成功。

Apply/Park 已成功、offset 提交失败时，只重试当前 offset 提交，不再次调用 Apply，也不获取下一条。进程在 DB 提交后、offset 提交前中断，broker 会重投；跨实例去重由 MySQL 的事件身份/hash 与版本事务保证。这里是至少一次协议，未宣称跨 DB/Kafka 的 exactly-once 事务。

## 生命周期与资源

每次 Fetch 最多 1 秒，Apply/Park 各最多 5 秒，offset 提交最多 5 秒，全部继承进程取消；重试间隔 200ms。客户端显式设置有限 dial/read 等待、单条预取容量及同步提交。`Run` 在取消后退出并同步 `Close` reader，`Close` 使用一次性保护；应用应先等待 Run/reader 释放，再关闭 DB 连接池，不用后台遗留 goroutine 冒称关闭完成。当前 `Poll/Run` 要求同一 worker 由单 goroutine 持有，`Close` 可以用于取消读与释放 reader。

日志仅写固定摘要和投影名字，不写原始 driver 错误、载荷、Token 或数据库凭据。停车分类采用固定字符串，原始错误文本不进入本 worker 日志。

## 已执行验证与原始证据

[最终普通/格式/vet 日志](consumer-unit-final2.txt)：7 个普通主组 PASS，另有读/业务执行/重试等待阶段取消的 3 个子例；真实套件在普通模式明确 SKIP。普通测试验证持久效果先于 commit、3 次失败后停车失败不越过、commit 错误只重试本条、受控效果后取消/新实例重放、各阶段取消与 reader 只关闭一次、单条超时和日志隐私、两个固定 group 与未知投影拒绝。Fake reader/processor 只用于调度协议验证，不代替真实 broker 与 MySQL 证据。

交叉审查后只调整真实测试 helper 的可选 Kafka 分支：配置本身错误仍 FAIL，`GO_INTEGRATION=1` 但未配置 `KAFKA_BROKERS` 时与现有 relay 测试一致 SKIP。[无 Kafka 的定向日志](consumer-skip-final.txt)明确 SKIP/PASS；该分支在连接 DB/Kafka 前返回，不重新运行无关真实六组，已执行场景逻辑保持冻结。

[真实 Kafka/MySQL 最终日志](consumer-real-final3.txt)：6 主组全部 PASS。使用隔离 MySQL `3330`、Kafka `9098`；配置保留 Redis `6402` 供配置加载，本套件不创建 Redis client。API 保持停止，通过独立 SQL 连接 `GET_LOCK('go072_integration',30)` 与仓储/重建测试串行。每组创建随机私有 topic/group，生产 `community.events` 与两个稳定生产 group 不被测试消费、reset 或清理。消费结果通过 broker `OffsetFetch` 核对 committed next offset，不能仅靠 DB 状态推断提交成功。

| 真实场景 | 持久与 broker 结果 |
| --- | --- |
| 关注重复/逆序 | v3(true)→v2(false)→v1(true)→v3；最终 active=true/version=3，独立事件 receipt 3 条，broker next offset=4。 |
| Feed 重复/逆序 | 已发布事实 v2，消息 v2→v1→v2；共享笔记目录一行 version=2，receipt 2 条，broker next offset=3。 |
| 效果后、offset 前中断 | 实际 Runtime.Apply 提交 receipt/效果后取消 context，关闭旧 reader；broker next offset=-1、receipt=1。新 reader 使用相同私有 group 重投 source offset=0，完成后 next offset=1，receipt/关系版本仍各一次；日志含两个 UTC 时间点。 |
| DB 不可用越过阈值 | 使用独立已关闭 MySQL 连接池，3 次 Apply 及多次 Park 均失败，broker offset 保持 -1、无停车行。换回健康 Runtime 后先持久 PARKED 再提交到 1，随后受控 Replay 得到真实效果并 RESOLVED。不是物理停库实验。 |
| 毒消息与两组独立进展 | 34 headers 超过业务域上限，原始消息可持久停车。故意阻断 feed 组停车时其 broker offset=-1，follow 组真实停车后仍处理下一条正常消息并提交到 2；恢复 feed 的停车后亦提交到 2。两条原始停车记录保留 source/body/key、34 个 headers、32 字节 hash 与分类。 |
| 缺事实受控重放 | NOTE_PUBLISHED 对应业务事实缺失，先持久 PARKED 并提交 offset=1；未补事实时 Replay 失败且保持 PARKED/no receipt/no effect。只补自有业务事实后 Replay 将效果/receipt/RESOLVED 同事务完成，重复重放不增加有效副本。 |

```powershell
# backend/；先显式设置隔离 MYSQL_*、REDIS_*、KAFKA_BROKERS、GO_ENV
$env:GO_INTEGRATION='0'
go test -count=1 -v ./internal/communityconsumer
go vet ./internal/communityconsumer
gofmt -l internal/communityconsumer internal/communityprojection/message.go
$env:GO_INTEGRATION='1'
go test -count=1 -v ./internal/communityconsumer -run '^TestIntegrationCommunityConsumer$'
```

失败轮次均保留：[ordinary 初轮](consumer-unit-initial.txt)捕获并行编写 Runtime 尚缺 gorm 导入的编译错误，补齐后 [ordinary 修复轮](consumer-unit-final.txt)通过。[真实初轮](consumer-real-initial.txt)因本测试 shop 夹具漏必填 merchant_id，6 组在 seed 时失败，事务回滚、没有消息消费；补完整自有 merchant/shop 后，[第二轮](consumer-real-final.txt)只有首组通过，其余新 topic 初次发送失败。[第三轮](consumer-real-final2.txt)加入 DialLeader/ReadLastOffset=0 的确定就绪检查并记录错误分类，证实是 UnknownTopicOrPartition；本地 kafka-go 源码确认默认全局 Transport 的 6 秒全 topic 元数据缓存不包含随后创建的 topic。测试 Writer 改用仅本 topic 的私有 Transport 并显式释放，未重试未知发送或更改消费者逻辑；[最终轮](consumer-real-final3.txt)6 组通过。

各组结束关闭 reader，删除精确自有 SQL receipt/停车/投影/业务夹具，删除私有 group/topic，并释放 transport 与命名锁。未 truncate、flush、重置共享 group 或删除数据库卷。源码冻结后，本组件不继续占用真实依赖供 API 实验使用。

## 限制与回退

未执行操作系统强杀、物理 DB/Kafka 停机、跨 broker 故障转移、持续积压容量与延迟测量；context/reader 实例中断和独立关闭连接池的边界如上，不扩大为这些实验。跨实例同时消费与重建屏障的仓储事务证据见相邻报告，本组件真实集成未额外跑多分区 rebalance 压力。应用启动关闭/全套回归与 Linux race/远端 CI 按本批总报告记录，当前组件日志不宣称它们已执行。

回退先停止 consumer 并等待 reader 退出；已经提交的投影/receipt/停车记录保留，offset 不因代码回退倒退。停车重放仍须依原始 source/hash 与当前事实完成，不删停车表来掩盖未应用效果；不影响交易 Stream/outbox 的处理协议。
