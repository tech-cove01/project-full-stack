# GO-072 / HTTP 到 Kafka 双投影真实贯通

2026-10-28。状态：**自测通过待核验**。本小功能只新增 `backend/internal/communityconsumer/flow_integration_test.go`，不修改 repository、social 查询、relay、consumer 或应用代码。依赖本票此前独立提交的持久投影、消费/恢复与查询模块；交付 SHA 由主 agent 提交后统一记录。

测试复用真实 Gin handlers 和 service，以 `httptest.NewRecorder` 调用，不启动外部 API，也不伪造成功响应。数据库、Redis 和 Kafka 全部是真实专用实例：MySQL `127.0.0.1:3330/project_full_stack_go`、Redis `127.0.0.1:6402`、Kafka `127.0.0.1:9098`。

## 链路与断言

1. 从自己插入的两个用户取得手机号，实际 `POST /user/code` 获取 development 验证码、`POST /user/login` 创建 Redis 登录态，再通过 `/user/me` 核对当前身份。验证码、手机号和 token 均不打印、不入证据。
2. 作者通过真实 `POST /notes` 发布一篇公开笔记；读者通过期望状态 `PUT /authors/{id}/follow` 关注，再经 `PUT /notes/{id}/like` 点赞。核对三条持久社区 outbox 都为 PENDING，非测试事件没有可认领积压。
3. 消费尚未启动时，首次关注已经存在的笔记立即出现在 Feed，source=database、内容与本人 liked 状态正确，不依赖 per-user fan-out。
4. 用真实 `communityrelay.Worker.Poll` 将三条 NOTE_PUBLISHED/FOLLOW_CHANGED/LIKE_CHANGED 投到一个私有 Kafka topic，并核对三条 outbox 为 DONE。
5. 创建两个不同私有 group，分别绑定实际 `follow-v1/feed-v1` Runtime。两个 worker 并发 Poll，各自消费完整三条消息并独立提交。通过 Kafka OffsetFetch 取得两组 `CommittedNextOffset=3`；每组各有 3 条持久收据，其中 APPLIED=1、IGNORED=2，没有 park。
6. 真实 HTTP Feed 返回 source=projection、正确标题/作者/门店及本人 liked/count；Relationship 返回 source=projection、following=true、version1、followerCount1。这里 source 分别指公开笔记目录与公开粉丝计数参与，身份与公开事实仍来自 DB。
7. 精确插入一篇同作者的历史公开笔记，刻意没有 NOTE_PUBLISHED 事件。目录仍只有原篇笔记，Feed 必须 source=database 并完整返回两篇，不能因半目录遗漏历史内容。
8. 两组停在 offset3 不再 Poll，读者实际 HTTP 取关。此时关系图仍 active/version1，但下一次 Feed 已为空、source=database，HTTP 取关返回 DB version2；不等待 Kafka 消费才隐藏已取关作者。
9. relay 再投递这条取关事件，两个 worker 各自再消费一条并提交到 offset4。Relationship 收敛 projection/inactive/version2/count0，Feed 仍为空。每组收据总数 4，测试 outbox 没有未 DONE 行、私有 topic 没有 park。

没有重复本票已有的 poison、DB 故障、offset 提交窗口等六组 consumer 测试。本测试只补真实 HTTP producer → outbox → relay → broker → 两组实际 consumer → 真实 HTTP 查询的贯通证据。

## 执行与原始日志

```powershell
# 先提供专用 MYSQL_*、REDIS_*、KAFKA_BROKERS 与 GO_ENV=development。
$env:GO_INTEGRATION='1'
$env:GIN_MODE='release'
go test -count=1 -v ./internal/communityconsumer -run TestIntegrationCommunityFlow
```

普通 `gofmt`、`go test -count=1 ./internal/communityconsumer`、`go vet ./internal/communityconsumer` 通过。

[首次执行日志](flow-real-initial.txt) 保留 FAIL：在 `consumerDatabase` 获取 SQL 连接前失败，未取得 fixture 锁、未写业务数据或 Kafka。该执行在受限沙箱内无法访问本机三个专用端口，Docker pipe 只读检查也被权限拒绝；没有据此修改业务代码。随后在允许访问本机依赖的执行环境中，使用同一配置重跑得到 [最终 PASS](flow-real-final.txt)。这是执行环境访问边界，不冒充业务失败修复记录。

最终一个完整主组 PASS：主测试 2.73 秒、包 6.302 秒。原始日志含以下受控观察时间：

| 点 | UTC 时间或结果 |
| --- | --- |
| 业务链路开始 | `2026-10-28T07:16:06.2557612Z` |
| relay 三条事件 DONE 后确认点 | `2026-10-28T07:16:07.2526884Z` |
| 双组均 offset3、投影 HTTP 读取通过 | `2026-10-28T07:16:07.4448514Z`，自开始观察 1189 ms |
| 历史回退、延迟取关守护、双组追平与最终对账 | `2026-10-28T07:16:07.9226471Z`，总观察 1666 ms |
| 最终账 | 每组 offset4、各 4 条收据；owned outbox pending=0、owned parked=0 |

“relay 确认点”是在检查 DONE 后记录的测试观察点，不是 broker 内部时间戳。上述毫秒数包含有限夹具、请求、投递和消费，不是持续性能成绩、生产 SLA 或 5 秒追平保证。

## 隔离与限制

通过 `go072_integration` 命名锁与其他合作测试串行。运行前要求整个社区 outbox 无 PENDING/PROCESSING，随后每次 relay 前也核对无非本测试可认领事件，避免全局 relay 把他人工作发到私有 topic。应用后台 relay/consumer 必须停止；命名锁本身不阻止未参与该协议的生产进程。

私有 topic 为 `community.go072.it.*`，group 各带 `.http-follow/.http-feed` 后缀；没有写共享 `community.events` 或重置生产 group 位点。测试结束关闭自己的 relay/reader，按精确用户、笔记、关系、eventId 与 topic 清理 SQL、Redis 登录 key、私有 group/topic。清理会再次枚举本测试身份已提交的事件，覆盖“HTTP 提交但调用失败”时的自身残留；不 truncate、不删数据卷、不回退序列。最终日志记录正常清理完成。

这是进程内真实 handlers 的全依赖贯通，不是浏览器、真实监听端口、应用启动/停止或进程重启证据。未在本测试执行 Linux race、持续负载、broker 停机、真实网络提交未知或新故障场景。已有独立模块报告继续分别证明其对应范围，不用本测试替代。
