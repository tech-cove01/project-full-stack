# GO-031c / DB 超时扫描组件

2026-10-26（Asia/Shanghai），扫描/调度组件、实际 `order.Service.CancelExpired` 的 DB 联调及真实 API 停机后到期/重启自测通过，独立核验仍待执行。开始 SHA `0072c867ab1f34d7219e815d04779c651c55d9d0`；依赖 GO-030 订单快照与 DB 截止时间，以及 GO-031 生命周期事务。交付 SHA 由总报告记录。范围包括 `backend-go/internal/ordertimeout/`、app组装第三个worker、期限配置注释和真实重启脚本；不注册额外 HTTP 或恢复入口。

## 可观察行为

`NewWorker(db, lifecycle)` 接受 `CancelExpired(context.Context, string) (bool, error)`。Run 启动立即扫描，每轮完成后间隔 5 秒再扫描；单轮默认最多 100 条。SQL 使用 `status=1 AND pay_deadline_at<=NOW()`，按 `(pay_deadline_at,id)` 升序，不使用应用时间判断到期，不依赖 Redis client、ZSet 或任何提醒键。

worker 只查询到期候选 ID，不自行修改订单、库存、审计或 outbox，不持有扫描事务/行锁去调用业务服务。真正的状态/版本/期限复核、SYSTEM 身份、库存和审计/outbox 同事务归 `CancelExpired`；`false,nil` 表示其他动作已胜出或当前不应关单。多实例可以重复发现候选，由该业务事务保证最终只有一次转换，不增加分布式锁。

`Poll(ctx) (int,error)` 的数量是实际调用生命周期服务的次数，包含 false 和错误，不能当作成功关单数。单条失败继续尝试本页后续订单，并返回不含 driver 详情的汇总错误；订单仍保留在 DB，后续可重扫。scan 失败不改变游标，也不把数据库错误包装成空成功。

## 公平性与生命周期

游标记录最后**实际尝试**的 `(deadline,id)`；满页后下一轮继续后续候选，而不是反复只取最早 100 条。完整处理不足 100 条的一页或读到空页后回绕，之前失败的订单因此会再被发现。游标只用于单 worker 内存调度，进程重启从头扫描；它不改变 DB 的持久重试来源。

每轮整体 context 预算 10 秒，扫描预算 2 秒，每条生命周期调用预算 5 秒，并继承应用取消。预算在页中耗尽时，游标停在最后已尝试记录，不前移到未处理页尾，后续 Poll 会继续剩余记录。Run 不启动子任务 goroutine；定时等待和业务调用都响应取消。错误日志只记录固定摘要与尝试数，不输出 SQL/driver 错误、核销凭据或原始业务载荷。

生产 batch=100、interval=5s，测试通过包内字段显式缩小时间/页长，没有新增环境故障开关。当前组件遵循调用方单 goroutine 所有权，同一个 Worker 不应并发调用 Run/Poll。

## 已执行验证

隔离 MySQL 3308 / `project_full_stack_go`，凭据仅由进程环境传入。真实测试通过独立连接持有 `GET_LOCK('go031_integration',30)`，与生命周期测试串行，结束释放；API/Vite 保持关闭。测试只连接 MySQL，没有创建 Redis client 或提醒数据，没有停止 Redis 服务。

```powershell
# backend-go/；先显式设置隔离 MYSQL_* / REDIS_* / GO_ENV
go test -count=1 -v ./internal/ordertimeout
go vet ./internal/ordertimeout
gofmt -l internal/ordertimeout
$env:GO_INTEGRATION='1'
go test -count=1 -v ./internal/ordertimeout
```

[普通/格式/vet 原始日志](timeout-unit-final.txt)：7 个普通主组 PASS，另有 Run 的 3 个取消阶段子例；未设置 GO_INTEGRATION 时真实组明确 SKIP。[真实 MySQL 首轮日志](timeout-real-initial.txt)及 [root 重跑日志](timeout-real-final.txt)：6 个真实扫描主组及全部普通测试 PASS。这 6 组使用下述受控 SQL processor，首轮没有失败；新增真实生命周期联调的夹具失败与修复另列，未覆盖已有输出。

| 验证 | 实际断言 |
| --- | --- |
| SQL 到期/状态/复合顺序 | 只选 DB 当前已到期 status=1；同截止时间按 ID，包含恰好截止；未来和 2/4/6 状态不变。大于 2^53 的 ID 精确传入。 |
| 前 100 条持续失败 | 第一轮尝试 100 条失败，第二轮处理第 101 条，第三轮回访前 100 条；失败行仍 status=1/version=0，第 101 条条件更新一次。 |
| 选择后业务状态变化 | 受控 processor 先把候选改为已支付，条件关闭返回 false；扫描不覆盖较新状态。 |
| 两扫描器重叠 | 两实例屏障确保均选中同一批 12 行，共 24 次尝试；受控条件 SQL 仅 12 次成功，各 version=1。 |
| 扫描失败恢复 | 使用独立已关闭 MySQL 池，零业务调用且 cursor 不变，不输出 driver 详情；换回健康池后处理原候选。未模拟物理断库。 |
| 无提醒的启动与停止 | 无 Redis client/ZSet 夹具，Run 立即扫描 DB 候选；应用取消后退出。 |

普通测试另覆盖单行失败不阻断后续、false 不算错误、整体预算耗尽不跳过页尾、每条超时后继续下一条、scan 错误与取消零依赖访问、Run 在启动前/业务中/间隔中取消，以及生产默认值。

真实测试的生命周期 processor **只对本测试精确 owned ID** 执行 `UPDATE ... SET status=4,version=version+1 WHERE status=1 AND pay_deadline_at<=NOW()`，拒绝更新未知行；它明确没有实现库存、审计、outbox、业务锁序或完整权限。订单夹具只用于扫描测试，不能把这些结果称为 `order.Service.CancelExpired` 全链通过。结束按精确 ID 删除并断言剩余 0，不做 DDL、flush、truncate、序列重置或修改未知订单。

## 实际生命周期联调

另增 `lifecycle_integration_test.go`，实际调用 `order.Service.CancelExpired`，与前述 6 组 SQL processor 证据分开。测试复用相同隔离 MySQL 与独立连接命名锁，在 API/Vite 停止期间串行执行。完整自有 merchant/shop/user/voucher/seckill 夹具由本测试创建，订单通过真实 `order.Creator.Create` 提交；之后只对明确 owned 订单调整测试截止时间，以建立已到期与未来对照。此处没有启动 HTTP API、Stream worker 或操作系统进程，也没有创建 Redis client/ZSet，因此不能代替真实停机重启实验。

```powershell
# backend-go/；显式隔离 MYSQL_* / REDIS_* / GO_ENV、GO_INTEGRATION=1
go test -count=1 -v ./internal/ordertimeout -run '^TestIntegrationTimeoutLifecycle$'
```

[首轮原始日志](timeout-lifecycle-real-initial.txt)保留 1 组夹具失败和另 2 组 PASS：Creator 原截止时间为 `DB NOW()+3600s`，夹具同秒再次写入相同截止时间，MySQL 返回 0 changed rows，触发测试严格修改行数断言。仅将未来对照改为 `DB NOW()+1800s`，确保与 Creator 的一小时快照不同；没有修改业务代码。[修复后真实日志](timeout-lifecycle-real-final.txt)的 3 组全部 PASS。所有调用实际生命周期的测试路径再统一加 owned 候选检查，拒绝因其他未来订单临时到期而修改未知行；[最终冻结测试真实日志](timeout-lifecycle-real-final2.txt)再次 3 组 PASS。[最终普通/格式/vet 日志](timeout-lifecycle-unit-final2.txt)的 7 个普通主组 PASS、两种真实套件在普通模式明确 SKIP；[前次普通检查](timeout-lifecycle-unit-final.txt)亦保留。

| 实际联调 | 可核对事实 |
| --- | --- |
| 无 Redis 的真实关闭 | Poll 只发现到期 status=1；实际事务提交 status=4/version=1、`PAY_TIMEOUT`/cancel_time、库存只加 1、SYSTEM/0 审计、携带实际订单 owner 的 PENDING ORDER_CHANGED 恢复事件。未来未支付订单保持原状，实际 Pay 后已到期的对照订单保持 status=2 与原 token。下一 Poll 为 0 次。 |
| 两扫描器相同候选 | 屏障先确保两个实例均已选中同一订单，再调用实际生命周期；共 2 次尝试，仅 1 次返回 changed=true，库存/版本/审计/outbox 各一次。 |
| 持久失败、后续进展和重扫 | 第一行先插入自有相同 event_id、不同 JSON 语义的 outbox 行；实际 CancelExpired 返回 `outbox.ErrConflict`，订单保持 status=1/version=0/cancel_time NULL，取消审计回滚，库存未恢复。相同 Poll 的第二行仍成功关闭。只删除该自有冲突行后，下一 Poll 重扫并成功关闭第一行，再下一 Poll 为 0 次，最终两笔库存各恢复一次。 |

三组结束均精确删除并断言自有 catalog/users/orders/audit/outbox 总剩余 0。原始输出记录大于 2^53 的订单 ID、尝试/提交次数、库存和 actor，不输出 token 或数据库凭据。没有对外部订单执行生命周期操作，没有 DDL 或 Redis 写入。

## 真实 API 停机后到期与重启

root 使用 `scripts/go-order-timeout-restart-smoke.py` 与冻结业务代码二进制 `6701e62-go031-worktree` 执行真实 HTTP 发布、两名用户准入及 Stream 落单，其中一名用户实际 Pay。两个自有 source message 均在停止前完成持久创建与 ACK。第一进程采用新单期限 `10s`，在未支付订单的原 DB 截止时间前完整退出；`afterStop.dbNowEpochMs=1793029467844` 严格小于 `deadlineEpochMs=1793029477000`，退出后才等待数据库时钟越过截止，期间订单保持 status=1。

第二进程改用新单期限 `1h` 启动，实际 scanner 仍按原 10s 快照将旧单关闭为 status=4/version=1，库存只恢复一次，SYSTEM/0 审计与 PENDING 恢复事件各一条。已支付对照订单保持 status=2；迟付返回 409，重复取消及下一生产间隔扫描不增加版本或重复回补。Redis 库存保持预占后的 1，DB 库存恢复为 2，明确没有把 PENDING 事件当作 GO-033 Redis 补偿完成。没有创建 ZSet 提醒。

[重启实验原始证据](restart-initial.json)：18 项检查 PASS，两个进程均 `CTRL_BREAK_EVENT`、exitCode=0、端口可重新绑定，`forcedCleanup=false`、`cleanup=true`，日志没有发现私密值。[启动关闭原始证据](bootstrap-final.json)另有 6 场景 PASS。这里证明正常退出后的停机跨截止与重新启动，不声称操作系统强杀。

## 待联调与限制

本说明证明 P1-4 的 DB 兜底发现/调度、上表实际生命周期 DB 联调及上述真实停机后到期/重启。截止前后支付/超时竞争与事务本身的其他边界见生命周期报告；SQL processor、直接 Service 调用与进程实验分别记录。生命周期事务统一券→活动库存→订单锁序，避免与 Creator 反向持锁。

Redis 补偿、资格释放、review 和库存重建仍归 GO-033；扫描器不会自行恢复 Redis 库存或删除购买标记。未执行真实 Redis 提醒键丢失实验、OS 强杀、Linux race、远端 CI 或持续积压容量测试。没有新 DDL/依赖；回退需先停止本 worker，已由生命周期提交的关单/outbox 不因代码回退撤销，不通过改回状态或删除数据卷回退。
