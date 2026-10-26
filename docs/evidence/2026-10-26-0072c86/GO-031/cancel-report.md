# GO-031b / 本人取消与到期关闭自验报告

日期：2026-10-26。状态：**自测通过待核验**，实现已冻结，供主 agent 单独小提交。依赖 GO-031a 支付；开始 SHA 为 `a5125d6ef8c7189e024a58dc2dd0fbc49cc78c47`。交付 SHA 由主 agent 提交后统一记录，不预填。

本票新增 `Service.Cancel`、`Service.CancelExpired`、取消 HTTP 路由及取消时间/原因 DTO，修改范围为 `order.go/http.go/order_test.go/lifecycle*.go`。已有 Pay 业务逻辑未改；共享查询列增加取消快照。本票不消费 Redis 补偿，不实现核销/退款或库存重建。

## 对外行为与 scanner 合约

`POST /voucher-order/{id}/cancel?reason=...` 从登录上下文取得用户，只允许本人操作；伪造 body 用户字段不改变身份。未登录 401、他人订单和不存在订单均 404，无订单 data。当前状态 1 变为 4；已经状态 4 的同动作重试返回第一次取消的时间和原因，不覆盖、不再次回补。状态 2/3/6 冲突 409，不回传 token。

取消原因先验证有效 UTF-8，再 trim；空白默认为 `USER_CANCEL`，超过 64 个 Unicode rune 时按 rune 截取，保留完整 UTF-8，不按字节切断。非法 UTF-8 为 400。新增响应字段 `cancelTime/cancelReason`，仅实际存在时输出；时间继续使用上海业务格式 `yyyy-MM-dd HH:mm:ss`。取消响应及取消状态的详情不含核销 token。

本人未支付取消不要求仍在支付期限内；过期但还没由 scanner 关闭的未支付单也可由本人取消，审计仍记本人动作。

`Service.CancelExpired(ctx, id) (changed bool, err error)` 是 scanner 的真实业务接口：

| 当前持久情况 | 返回 |
| --- | --- |
| 订单不存在、当前不是状态 1、或当前 DB 时间尚未到既有支付期限 | `false,nil`，不新增状态/库存/审计/事件。 |
| 当前状态 1 且锁后 DB 当前时间达到/超过原支付期限，全部事务提交 | `true,nil`，只发生一次到期关闭。 |
| 数据库、状态版本、库存回补、审计或 outbox 失败/结果未知 | `false,error`，不得据此删除提醒、伪造成功或释放 Redis 资格。 |

到期动作使用 `operator_user_id=0/operator_type=SYSTEM/reason=PAY_TIMEOUT`，订单 payload 中的 `userId` 仍为原购买用户。已到期的重复扫描为 no-op；不把曾经到期的候选视为现在仍可关闭。

## 锁、期限与原子边界

沿用 GO-031a 的定位与当前锁序：先进行非锁定 owner/binding 定位，然后在显式 READ COMMITTED 事务内按 `tb_voucher FOR SHARE` → `tb_seckill_voucher FOR UPDATE` → `tb_voucher_order FOR UPDATE` 读取；当前订单重新核对 owner、活动、门店和状态。定位读不是写入授权或状态依据，不新增订单→库存的反向持锁链。

到期判断在取得所有行锁后，使用新 SQL `pay_deadline_at<=NOW(3)` 读取。最终到期 UPDATE 还再次包含相同期限条件；不使用 Go 进程时间、候选扫描时刻或事务开始时刻代替 DB 边界。

成功取消的同一 tx 包含：

1. 当前 version 必须满足 `0<=version<INT_MAX`。
2. 订单条件更新 `status=1`、既有 version、真实 user ID；写状态 4、`cancel_time=NOW(3)`、原因、`redeem_token=NULL`，version 加一。到期动作再限定 `pay_deadline_at<=NOW(3)`。
3. DB 库存通过 `stock=stock+1 WHERE stock>=0 AND stock<total_stock` 恢复一份；**RowsAffected 必须为 1**。库存已经到总量、关联行异常或 SQL 失败，整笔取消回滚，不能仅返回取消成功。
4. 当前读取更新后订单，确认状态/版本/token 清空，再写 `ORDER_CANCELLED` 审计与该版本的 `ORDER_CHANGED`。
5. 只有提交成功才返回取消 View 或 `changed=true`；提交错误不给伪成功结果。

新取消会清除未支付单上意外残留的 token，测试使用精确 ID 的脏字段夹具验证。状态 4 的 DTO 继续隐去 token；同动作幂等读取不重写历史审计或时间。

审计由服务端确定 USER 或 SYSTEM 操作人；detail 为状态、原因、版本，不含完整 token。事件使用 `ORDER:{id}:{version}:CHANGED`、聚合 ORDER、类型 `ORDER_CHANGED`。payload 与已有协议一致：

```text
orderId, voucherId, shopId, userId, version : 十进制字符串
targetStatus       : "4"
stockRestore       : "1"
releaseEligibility : "true"
```

用户取消与到期关闭共用同一事务转换函数，库存、状态、时间/原因、token、version、审计和 outbox 任一步失败全部回滚。到期条件已不满足时是 no-op，不冒充新转换；人工取消发生状态冲突则 409。

## Redis 与“可重买”的实际边界

本票事务中没有 Redis 网络调用，也没有直接 INCR、SREM 或 HDEL。取消后 DB 生成列 `active_flag` 变 NULL，允许后续已经正确受理的新 DB 订单；Redis 库存、资格集合及 accepted 索引仍等待持久事件处理，不能宣传立即恢复新抢购。

真实夹具验证了：取消 DB 库存回补，取消事件保持 PENDING；Redis 库存仍是预占后的值、购买资格和原 accepted 映射仍在。同用户此时重新调用 Seckill 会找回旧取消单 ID，不会偷偷创建第二条预约。直接调用 Creator 的新 ID 能验证 DB 唯一约束已释放，但这个数据库层测试**不等于已完成前端重新抢购链路**。Redis 补偿、事件去重、缺键核对及受控恢复仍留 GO-033。

旧创建消息重投同一已取消订单，只返回 `state=CREATED/orderStatus=4`，不重新扣 DB 库存，不改回未支付。

## 实际验证与证据

普通 `gofmt`、`go test -count=1 ./internal/order`、`go vet ./internal/order` 均通过。新增普通测试覆盖默认原因、Unicode 64 rune 截断和非法 UTF-8；既有取消路由未开放的 404 预期已按新能力改为无登录 401。

真实测试使用隔离 MySQL `127.0.0.1:3308/project_full_stack_go`、Redis `127.0.0.1:6380`，通过固定连接 `GET_LOCK('go031_integration',30)` 与支付/scanner 真实套串行。凭据仅来自环境，不进入日志。

```powershell
# 先提供隔离 MYSQL_*、REDIS_* 与 GO_ENV=development。
$env:GO_INTEGRATION='1'
$env:GIN_MODE='release'
go test -count=1 -v ./internal/order -run TestIntegrationCancel
```

[原始自验日志](cancel-real-initial.txt)：11 个主组全 PASS；主测试 4.77 秒、包 5.044 秒。这是功能验证时间，不是吞吐成绩。

主 agent 随后独立审读源码，并重跑真实 11 主组、普通 order 测试及 vet，均通过，无本轮阻断；[独立重跑原始日志](cancel-real-final.txt) 保留。此处仍标自测通过待核验，不将本聊天交叉审查替代外部核验。

| 主组 | 实际验证 |
| --- | --- |
| 本人取消与 token 清理 | 真实 HTTP 返回取消时间/默认原因，额外 body 不改 owner；未支付脏 token 清空；库存、审计、事件一次同提交；重试保留原原因/时间。 |
| 20 并发取消 | 同一单 20 个调用均返回同一取消终态，DB 库存只恢复一份，version/取消审计/事件各一次。 |
| 不可见与终态 | 外人/不存在均 404；真实已支付及 3/6 夹具冲突 409，无 token data、无取消库存或审计效果。 |
| 支付/取消竞争 | 独立新单运行 12 轮真实并发，每轮仅支付或取消一方第一次成功；另一方状态冲突，最终库存与状态对应，version/事件/成功审计只一次。 |
| 支付/到期竞争 | 未到期与已到期两侧各 4 轮、共 8 轮真实并发：前者支付、scanner no-op；后者支付拒绝、SYSTEM 关闭，库存不重复回补。 |
| scanner 合约 | 不存在、未来期限、2/3/4/6 为 no-op；等于 DB 当前期限可关闭；重复扫描不再恢复库存，SYSTEM0 与实际 owner payload 分离。 |
| 回滚 | USER/SYSTEM 两入口分别注入审计、事件失败，另设置库存已满形成真实 RowsAffected=0；全部保持原未支付快照，无取消审计/事件或部分库存变动。 |
| 原因 | 真实 API trim 并按 64 Unicode 字符截断，emoji 不断字节；重复调用不改首次原因；非法 UTF-8 400 且未变更订单。 |
| 重投与资格边界 | 旧创建消息保留状态 4；DB active_flag 允许下一正确受理单；Redis 三类预占仍保留、事件 PENDING，没有伪造立即重买。 |
| 版本 | USER/SYSTEM 到期动作在 -1/INT_MAX 均停止，库存和未支付状态保持原值。 |
| 等锁后期限重查 | SQL callback 确认到达被持有的库存锁；另一事务分别将期限从 due 改 future、future 改 due，释放后服务按新当前期限 no-op/关闭，未沿用候选旧快照。 |

并发和等锁窗口通过 start channel、SQL 到达 callback、显式事务锁及持久数据观察控制；不是随机 sleep 猜测命中。新取消功能首次执行已通过；没有发现后修复的 Go Cancel 缺陷，不虚构修复前 FAIL。故障注入中的预期回滚在同一测试组中核验其持久零副作用。

测试以 Creator 准备自己的随机订单/活动，精确清理所属订单、审计、事件和 Redis key。Seckill 只用于现成 accepted 索引的恢复验证，没有新增 Stream entry；未启动 API/worker、未清空 Redis、未删除消费组、未回退 ID 序列。测试后 fixture 和 `go031_integration` 锁已清理。

## 后续范围与限制

- `CancelExpired` 业务服务已通过真实依赖验证，不代表 scanner 调度、重启跨期限、ZSet 丢失兜底或整个应用生命周期已在本报告验收。调度由 GO-031c 组合，后续应接真实服务而非以 processor stub 通过记录代替端到端结果。
- 取消后的 DB 业务成功独立于 Redis 投影；补偿尚未完成时应如实显示更新中，不把原 accepted ID 的恢复算作新抢购。
- 本票没实现核销、退款、review 管理、缺键库存重建；不消费尚存的 ORDER/RESERVATION PENDING 事件。
- 未执行真实网络提交结果未知、故障杀进程、持续负载、Linux race 或远端 CI；事务注入回滚不是这些故障的替代证明。
- 既有 Pay 源码保持不变；本套以实际支付竞争验证组合行为，主 agent 可在小提交前独立重跑 Pay/Cancel 与应用 scanner 再交外部核验。

源码及本报告冻结后等待主 agent 独立核对、提交，不扩展下一票。
