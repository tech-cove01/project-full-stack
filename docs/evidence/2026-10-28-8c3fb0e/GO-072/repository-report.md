# GO-072 / 事件校验与持久投影 repository

2026-10-28。开始 SHA：`8c3fb0e0fe1169ad98e19faaf7ae49f907362530`。本模块**自测通过待核验**，交付 SHA 由主 agent 小提交后记录。范围包括 `communityprojection/event.go`、`repository.go`、对应测试及 `009-community-projections.sql`、`010-community-consumer-recovery.sql` 的控制行/恢复表基础；consumer、park/replay、重建 CLI、查询回退和应用生命周期由其他小功能交付。

此处使用 workspace-delivery 和 sql-pro，按仓库的 MySQL/Go 约定实现；没有引入新存储、SDK 或修改社区事实写入路径，也没有修改既有 Redis 点赞投影。

## 选型与接口

两类 read model 分别为 `follow-v1` 的关系图、`feed-v1` 的公开笔记目录，独立消费组各有自己的持久去重收据。关注图可以按正向/反向索引查询关系及统计；笔记目录按作者和发布时间服务 Feed 候选读取。它们仍是 MySQL 可重建投影，原 `tb_follow/tb_blog` 为事实源。

本票选 follow+note catalog，而不重复已交付的 Redis like 集合。目录没有按每位粉丝生成独立队列，不能据此声称完成 H4 原规划的 per-user fan-out、持久 Feed 版本或其批次游标。当前笔记没有独立 `published_at` 字段，目录读取现存 `create_time` 作为发布时间，保持现有 Feed 的 `(create_time,id)` 排序契约。

```go
New(db *gorm.DB) *Repository
(*Repository).Apply(ctx context.Context, projection string, body []byte) error
Decode(body []byte) (Event, error)
```

导出常量 `Follow="follow-v1"`、`Feed="feed-v1"`。`ErrInvalid` 表示格式/契约不合法，`ErrConflict` 表示 ID 或同版本状态异义，`ErrFactUnavailable` 表示笔记事实/控制行尚不可用；错误都不能由调用者直接当作成功消费。

`Event` 保留 relay 已有的六字段 `eventId/eventType/aggregateType/aggregateId/version/payload`，另暴露规范化 `Hash [32]byte` 和解析后的用户、目标、笔记、作者、目标状态。**Apply 不接收 Kafka key/坐标，因此 key==AggregateID、topic/partition/offset 边界由 consumer/runtime 负责核对。**

## 事件校验与版本键

body 最多 1 MiB、有效 UTF-8、必须只有一个完整 JSON 对象；envelope/payload 均拒绝重复字段、缺失字段及未知字段。当前 producer 没有 schemaVersion 或发布时间，消费者不伪造这两个字段，也不强制未发送的新字段。

所有 ID 都是规范的正 uint64 十进制字符串。version 必须是 JSON 整数 `1..MaxInt32`，字符串、分数、指数格式和越界值均拒绝。

| 类型 | 绑定与目标状态 |
| --- | --- |
| FOLLOW_CHANGED | aggregate=FOLLOW，aggregateId=followUserId；eventId=`FOLLOW:{userId}:{followUserId}:{version}`；payload 同版本、不可 self-follow；active 兼容现有字符串 `"true"/"false"` 和 bool，规范化为 bool。 |
| NOTE_PUBLISHED | aggregate=BLOG，aggregateId=noteId；eventId=`NOTE:{noteId}:{version}:PUBLISHED`；payload 的 publishVersion 必须同 envelope version。 |
| LIKE_CHANGED | aggregate=BLOG，aggregateId=blogId；eventId=`LIKE:{blogId}:{userId}:{version}`；liked 为 bool、版本一致。虽然两组都不应用点赞效果，仍完整核验它。 |

只有上述合法、已知且对当前组无关的事件才记 IGNORED。未知类型或损坏 envelope 返回 ErrInvalid，不能静默跳过。

Hash 为规范化六字段 envelope 的 SHA-256，JSON 排序/空白差异、兼容 active 字符串与 bool 表示不会制造重复冲突；同 ID 不同业务目标状态会得到不同 hash。版本比较按实际 `(userId,targetId)` 关系，不把相同作者 aggregateId 当作所有粉丝共享的版本。现有 relay Kafka key 只用作者 ID，虽然能把同一对关系送到相同分区，仍不依赖分区顺序抵御旧事件重发或同版本异义。

## Schema 与事务

009 新增三张表，未改事实表：

- `tb_community_projection_event`：主键 `(projection,event_id)`，身份使用 ASCII binary collation；保存 BINARY(32) hash、APPLIED/IGNORED 状态及时间。
- `tb_community_follow_projection`：主键 `(user_id,target_id)`，保存 active、version、hash、last_event_id；反向索引 `(target_id,active,user_id)`， inactive 关系不删除，保留旧事件版本屏障。
- `tb_community_note_projection`：主键 note_id，保存 author、version、hash、last_event_id、published_at；索引 `(author_id,published_at,note_id)`。

后续 010 由主 agent 提供 `tb_community_projection_control` 并初始化两个组。Apply 的 RC 事务第一步 `FOR SHARE` 锁对应 control 行；不存在返回 ErrFactUnavailable。受控重建持 `FOR UPDATE`，增量 Apply 必须等重建事务结束，不能插入到快照构建/切换之间。009 的已应用校验和保持不变，没有为了控制行改写已应用迁移。

收据和业务投影变化同事务提交：

1. 精确主键 `INSERT ... ON DUPLICATE KEY UPDATE event_id=event_id`，对已有收据先取得排他锁。
2. 已有收据只在 hash 与当前组预期状态完全相同时幂等返回；不同 hash/state 返回冲突。
3. 新收据应用到对应模型。RC 下先 materialize version0 行，再 `FOR UPDATE`，避免两个首次事件都读到不存在后竞态写入。
4. 旧版本没有投影效果，但仍持久记录已成功处理的收据；APPLIED 包括这种 stale no-op，不表示该旧事件现在仍是目标状态。高版本条件更新；同版本仅完全同义幂等。
5. 任何异常整体回滚，不留下“收据 APPLIED，但实际效果失败”的半提交。`New(tx).Apply` 可在外层 replay 事务内使用，外层回滚会连同收据与投影一起回滚。

普通新 note 效果还在 tx 中 `FOR SHARE` 当前 DB 笔记，要求存在、PUBLISHED、作者绑定相同、DB publish_version 不低于事件版本，读取 DB create_time；缺失、下线、外人绑定或未来版本均返回可重试错误，不伪成功。已经持久收据的相同重复投递可直接幂等，不需要把下线事实重新开放；页面仍须实时验证公开状态。

重建允许用 current fact version、zero hash、空 last_event_id 构建 bootstrap 行。合法同版本事件只有在 active/author/time 完全相同时才能填充第一次真实 hash；异状态、作者或时间拒绝。旧事件始终不能降版本。不能随意删除 read model 后仅保留旧收据，再期望 replay 自动补全；删除/重建必须走独立控制屏障与当前事实重建协议。

## 已复现问题与修正

[首次真实失败日志](repository-real-initial.txt) 保留两个失败：

- 实现问题：20 个相同收据并发时，原“INSERT 报重复 → SELECT FOR UPDATE”路径会将多个共享重复键锁升级为排他锁，真实 MySQL 返回 1213 死锁。已改为表上唯一主键对应的精确 no-op duplicate UPDATE，先取得排他锁再核对 hash，未使用会吞其他数据错误的 INSERT IGNORE。
- 测试读取问题：把 BINARY32 直接 Scan 进裸 `[]byte` 不符合 GORM 所需行形状，改为单字段 struct 后再比较摘要；不是产品投影写错 hash。

## 实跑结果

普通 `gofmt`、`go test -count=1 ./internal/communityprojection`、`go vet ./internal/communityprojection` 通过。真实实例为专用 MySQL `127.0.0.1:3330/project_full_stack_go`、Redis `127.0.0.1:6402`；测试不发 Kafka 消息、不启动 consumer，Kafka 实际收取/offset 证据由独立模块提供。凭据仅通过环境提供。

```powershell
# 先提供专用 MYSQL_*、REDIS_*、GO_ENV=development。
$env:GO_INTEGRATION='1'
go test -count=1 -v ./internal/communityprojection -run TestIntegrationProjectionRepository
```

[最终真实日志](repository-real-final.txt)：9 主组全部 PASS，主测试 10.53 秒、包 17.908 秒。测试时间不是吞吐或延迟指标。

| 主组 | 结果 |
| --- | --- |
| follow 重排与作者共享 key | v3→v1→v2→v3 收敛 v3；同作者不同用户各自版本独立。 |
| 首次与重复并发 | 20 个版本并发首次应用收敛 v20；20 次同收据并发幂等，无重复效果。 |
| 同 ID 异义 | 相同 eventId 改 active 返回冲突，既有状态/hash/收据保持。 |
| 两组独立忽略 | 合法非本组事件各记 IGNORED，目标事件分别 APPLIED；未知事件/组拒绝。 |
| note 目录 | 真实作者/时间从 DB 取；乱序重复不退版本，最高版本 hash 保留。 |
| 不可用事实 | 缺笔记、未公开、未来版本、外人作者均无收据/目录半提交；恢复事实后可重试。 |
| 回滚与 replay 外事务 | 注入收据/效果写失败均回滚；外层 tx rollback 同时回滚 New(tx).Apply 的内部效果。 |
| bootstrap | 同版本匹配可补 hash；异 active/发布时间不允许修改当前快照。 |
| 重建屏障 | SQL callback 确认到达 control 锁；持有 FOR UPDATE 时 Apply 不越过，释放后正常应用。 |

测试拿 `GET_LOCK('go072_integration',30)` 协调控制行窗口，所有随机关系/笔记/收据按精确 ID 清理，不 truncate、不重置 group/offset，不改未知业务数据；清理与释放锁完成。

## 读路径与交付边界

root 的读路径必须继续在最终 SQL 中守护当前 `tb_follow.active=1` 和 `tb_blog.status='PUBLISHED'`，且不能在投影候选 LIMIT 后仅靠应用过滤。缺历史事件、消费落后、park 或未重建的情况下需事实回退；仅“投影为空时 fallback”不能发现投影非空但漏头部新笔记。未有完整性水位时，可使用统一排序的 DB/投影合并或始终 DB 校验补全，不能据此宣传性能提升。

本模块只保证 MySQL 内收据与效果原子化，不能单独证明 Kafka offset 提交顺序、可重放 park、消费组独立恢复、rebalance、5 秒追平目标或进程关闭。这些必须由 consumer/runtime 真实 Kafka 测试补证。

Feed 的持久版本、generation 游标重置、真正 fan-out，以及大数据查询计划/持续负载没有在此报告交付。消费者键/分区策略变化、清除投影后的 generation 和重放屏障需要继续保持明确；不宣称 MySQL/Kafka/Redis 跨系统 exactly-once。Linux race、远端 CI 在本模块自验时未执行。
