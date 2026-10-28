# GO-072c 关注与 Feed 投影读入口

日期：2026-10-28。开始基线 `8c3fb0e0fe1169ad98e19faaf7ae49f907362530`。本报告是实现者定向自测记录，最终提交与整合核验由 GO-072 总报告关联；不覆盖 GO-031—071 已完成的按范围核验，也未开展 GO-074 页面工作。

## 行为与兼容

- `NewService` 默认继续查询业务事实；显式 `WithProjections()` 才启用可选读模型。应用组装由 root 单独负责。本改动仅涉及 `internal/social/{projection.go,follow.go,feed.go,projection_integration_test.go}`，未改 HTTP、迁移、消费者或前端。
- `Relationship.source` 和 `FeedPage.source` 新增可选 JSON 字段，值为 `database` 或 `projection`。前者表示公开粉丝计数的来源，本人 `following/version` 始终读取 `tb_follow`；后者表示候选笔记目录的来源，不代表正文、本人点赞或公共计数都来自投影。
- 关注计数仅在该作者所有 DB 关系都与投影的 pair、active、version 一致，且没有 DB 不存在的额外 active 投影关系时使用投影 `COUNT`。缺 active、缺 inactive/version-zero 墓碑、状态或版本不符、可选表查询错误均回退 DB 计数。完整且没有任何关系时可正确表示 0。
- Feed 先从当前 DB 关系获得作者集合，再核对这些作者的全部公开笔记是否都有相同 noteId、authorId、publishVersion、create_time 的目录记录，不仅检查当前页。任何历史无事件笔记、部分缺口或元数据不符都使整页回退原 DB 查询，不拼接两份已取 LIMIT 的结果。
- 使用目录时仍从 `tb_blog` 装配正文，并在最终 SQL 的 LIMIT 前过滤 `status='PUBLISHED'`、当前 DB 关注关系及目录元数据。滞后关注图不参与缩小作者集合；新关注不必等图投影，取关后下一次查询不再展示该作者，目录中的旧公开记录也不能使草稿公开。没有当前关注作者的空页标记为 `database`。
- 保留原 `(create_time,id)` 倒序复合游标、字符串 ID、一次批量本人点赞查询和现有公共计数缓存。未新增每条卡片单独查询。此实现为公开笔记 catalog 加实时查询，不宣称 per-user fan-out 或其分批恢复已经实现。

启用投影时，本人关系、完整性检查和相关最终 SQL 使用同一个只读 `REPEATABLE READ` 事务，避免“检查完整后又读到另一份版本”造成分裂。没有使用行锁阻止未来取关；已建立的快照可以读到其建立时的公开事实，后续请求才看见后来提交的变更。本结论不是跨存储线性一致性承诺。原公共计数缓存可能通过自身连接回源较新的计数，不作为公开性或权限依据，也不声称该缓存与事务处于同一快照。

## 定向真实验证

专用 MySQL `127.0.0.1:3330` / `project_full_stack_go`、Redis `127.0.0.1:6402` / DB0；环境中配置专用 Kafka `127.0.0.1:9098`，但此组直接使用真实业务写入及 `communityprojection.Repository.Apply`，不把它当作 Kafka 投递或消费者恢复证据。迁移 009—011 已由 root 应用，API 与消费者服务未由本分工启动。整组持有 MySQL 命名锁 `go072_integration`，避免与投影重建夹具并行。

```text
GO_INTEGRATION=1
MYSQL_HOST=127.0.0.1 MYSQL_PORT=3330 MYSQL_DATABASE=project_full_stack_go
REDIS_HOST=127.0.0.1 REDIS_PORT=6402 REDIS_DATABASE=0
KAFKA_BROKERS=127.0.0.1:9098
cd backend
go test -timeout 120s -count=1 -json -run '^TestIntegrationCommunityProjectionQueries$' ./internal/social
```

账号与开发密码由既有环境注入，未写入证据。此处没有连接 3308 或其他项目数据库。

| 证据 | 实际结果 |
| --- | --- |
| [首轮真实日志](queries-initial.jsonl) | 1 个入口、20 个一级子组全部 PASS，清理检查通过 |
| [最终真实日志](queries-final.jsonl) | 移除完整投影路径中不必要的 DB COUNT 后，同一组 20 子组全部 PASS；无 FAIL，清理检查通过 |
| [普通验证](queries-validation.txt) | 四个所属 Go 文件 gofmt 空输出；普通 social 测试、`go vet ./internal/social`、`go build ./internal/social` 通过 |

20 个子组直接覆盖：默认 DB 与完整投影来源；同秒三页无重复或遗漏；混合新数据和无发布事件历史笔记；当前页之外的目录缺口；author/version/time 三种目录错误；草稿在 LIMIT 前过滤；关注 active/version 错误、缺 active、额外 active 和缺 inactive/version-zero；合法零关系；完整性检查及后续计数/目录读取四处可选 SQL 错误；新关注、取关、重新关注不等待图投影；同一个 RR 快照与下一次请求的状态边界。

可选 SQL 错误通过独立 ORM 的查询回调，将指定投影查询定向到不存在的表，让真实 MySQL 返回查询错误；未重命名共享表、修改权限、伪造查询成功响应或改动生产逻辑。完整性检查后的计数查询和目录最终查询也分别命中错误，验证其后可在同一只读事务内安全回退 DB。

RR 子组先建立旧的 active 快照，再通过另一连接提交真实取关及投影应用。旧只读事务中的关系、计数、Feed 继续使用同一旧快照；其后的新请求读取 inactive、计数 0 和空 Feed，证明没有混用两次提交，也没有把旧快照错误描述为锁住未来授权。

## 夹具与清理边界

真实笔记由 `note.Create` 创建，关注/点赞由对应业务 Service 写入，已提交的真实 outbox envelope 交由 Repository 应用。SQL 仅控制自有笔记的同秒排序时间，或制造历史无事件、部分目录、错误元数据和草稿等明确查询夹具；没有用 SQL 伪造 Kafka 投递成绩。额外 active 投影使用规范领域事件构造为不匹配 DB 的自有负例，不代表业务事实。

清理按精确事件 ID、自有用户 pair 和自有笔记 ID 删除 outbox、去重记录、投影、关系、笔记和用户，并检查所测关系/笔记/投影/outbox/去重记录残留为 0；本次公共计数缓存键删除后验证 EXISTS=0。没有清空表、删除共享 Kafka topic/group、重置序列或删除数据卷。日志内不记录手机号、Token、凭据或请求头。

本组未验证 Kafka 效果后 offset 前退出、持久失败重放、broker 故障或投影重建，这些由 GO-072 其他分工提供实际证据。未运行 UI、持续负载、生产规模查询计划或性能对比，不据此声明吞吐、延迟改善或完整 H4 fan-out 能力；完整性检查本身有额外查询成本，GO-073 再进行量化。
