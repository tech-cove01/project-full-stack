# GO-031a / 本人模拟支付自验报告

日期：2026-10-26。状态：**自测通过待核验**，代码已冻结供主 agent 单独提交。开始 SHA：`0072c867ab1f34d7219e815d04779c651c55d9d0`；交付 SHA 由主 agent 小功能提交后统一记录，不预填。

本票仅实现模拟支付，不连接资金通道，不提前开放取消、自动关单、核销或退款。实现文件为 `backend-go/internal/order/lifecycle.go`、`lifecycle_test.go`、`lifecycle_integration_test.go`，以及现有 `order.go/http.go` 中的错误、私有 token 生成测试入口和支付路由。既有 GO-030 测试中的“支付未迁移”路由预期由主 agent 随新能力更新。

## 交付行为与接口

新增 `Service.Pay(ctx, userID, id) (View, error)` 及 `POST /voucher-order/{id}/pay`。登录中间件提供用户身份；额外 body 中的 `userId/redeemToken` 不参与决策。无登录为 401；其他人的订单与不存在的订单均为 404，且没有订单 data。

本人未支付单仅在当前 DB 时间严格早于既有 `pay_deadline_at` 时可支付。第一次成功将状态 1 改为 2，记录 `pay_time`、生成核销 token、将 version 加一，并在同一事务中写入 `ORDER_PAID` 审计和对应版本的 `ORDER_CHANGED` outbox。支付不扣减或回补 DB/Redis 库存，也不修改旧单金额、核销期或支付截止快照。

本人当前状态 2 的同动作重试返回原已支付快照及已有 token，不重新生成 token、version、审计或事件；若状态 2 缺失应有 token，则作为异常状态返回 503。状态 3/4/6、未支付但已到期的支付请求均为 409。错误或未知事务结果不返回成功 View/token，HTTP 只给安全的通用 503，不暴露数据库错误。

只有本人合法的已支付详情/支付响应包含 token；列表及其他业务状态仍按原 DTO 隐去 token。本票没有向审计、outbox、日志或错误响应写入完整 token。

## 事务、当前读取与期限

事务使用显式 READ COMMITTED，全程统一使用回调中的 tx。先在事务外以 `id AND user_id` 读取定位所需的 `user_id/voucher_id/shop_id`；该查询仅定位资源，不作为最后状态或授权依据。

写事务按以下顺序锁定：

1. `tb_voucher FOR SHARE`，核对实际门店与订单定位信息。
2. `tb_seckill_voucher FOR UPDATE`，获取该活动的商家快照。
3. `tb_voucher_order FOR UPDATE`，再次核对当前 owner、活动、门店和状态。

顺序与 GO-030 新建路径的券→活动库存→订单检查保持一致，避免新增订单→库存的反向持锁链。活动已下架不直接拒绝旧订单支付；履约依据已有订单状态与支付截止。

未支付状态在写入前检查 `0 <= version < INT_MAX`。状态及期限由最终 UPDATE 再约束：

```sql
UPDATE tb_voucher_order
SET status=2, pay_time=NOW(3), redeem_token=?, version=version+1
WHERE id=? AND user_id=? AND status=1 AND version=?
  AND pay_deadline_at>NOW(3);
```

`NOW(3)` 是所有行锁等待结束后新 UPDATE 语句的 DB 时间，不是事务开始时间、Go `time.Now()` 或前端时钟。影响行数必须为 1，否则返回冲突；随后使用当前锁定读取取得实际持久值。配置变更不重算已有 `pay_deadline_at`。

审计操作人为当前登录用户、`operator_type=USER`、`action=ORDER_PAID`，detail 只含业务状态/版本。事件使用 `ORDER:{id}:{version}:CHANGED`、聚合 ORDER、类型 `ORDER_CHANGED`；payload 保持 `orderId/voucherId/shopId/userId/targetStatus/stockRestore/releaseEligibility/version` 兼容字段。支付为 `targetStatus="2"/stockRestore="0"/releaseEligibility="false"`。审计或事件失败使支付、时间、token、version 一起回滚。

## Token 与唯一性

生产默认使用 `crypto/rand` 取得 32 字节随机数，以 64 字符 hex 存入原 `redeem_token VARCHAR(64)` 唯一列，提供 256 位随机输入。不接受客户端指定 token。

只有明确命中 `uk_order_redeem_token` 的 1062 碰撞才允许完整事务回滚后重新生成，最多三次；其他数据库错误直接作为未知结果返回。重试仍重新获取当前行锁并验证当前状态/期限，不复用失败事务。

已有状态 2 订单的同动作重试只要求 DB token 非空，沿用该既有事实，不重新证明旧 token 熵源或强制其为本票的 64 字符 hex，也不在幂等请求中轮换凭证。本票保证新生成 token 的格式和熵源；历史损坏/弱凭证的核对与迁移不据此宣称已经完成。

私有 `newRedeemToken` 仅用于测试注入熵源失败或精确唯一冲突，HTTP 无法设置。真实测试证明一次碰撞后第二次成功仅产生一次支付审计/事件；持续碰撞三次后仍未支付、没有 token、支付审计或新事件。测试不打印 token 值。

## 实际验证

普通格式、测试与 vet 通过：

```powershell
gofmt -w internal/order/lifecycle.go internal/order/lifecycle_test.go internal/order/lifecycle_integration_test.go internal/order/order.go internal/order/http.go
go test -count=1 ./internal/order
go vet ./internal/order
```

真实测试使用隔离 MySQL `127.0.0.1:3308/project_full_stack_go`、Redis `127.0.0.1:6380`，通过 `go031_integration` 连接命名锁与其他生命周期测试串行。凭据来自环境，不写入报告或日志。

```powershell
# 先提供隔离 MYSQL_*、REDIS_* 及 GO_ENV=development。
$env:GO_INTEGRATION='1'
$env:GIN_MODE='release'
go test -count=1 -v ./internal/order -run TestIntegrationPay
```

[首次真实执行日志](pay-real-initial.txt)：8 个主组全部 PASS，主测试 2.68 秒、包 2.936 秒。这里的时间仅为功能测试耗时，不是支付吞吐或容量成绩。

主 agent 随后独立阅读事务/锁/HTTP 并重跑相同真实 8 组，也全部通过，原始日志为 [pay-real-final.txt](pay-real-final.txt)。这仍是本票自验/交叉审查材料，不代替用户交付后的外部核验。

| 主组 | 验证结果 |
| --- | --- |
| 支付快照/审计/事件 | 真实 HTTP 本人支付成功；客户端身份/token 字段被忽略；旧价格/截止保持；DB 库存不变；paid、version、审计、outbox 同提交。 |
| 20 个并发支付 | 同一单 20 次调用全部取得同一已支付结果/token，version 仅加一，支付审计和事件各一条，库存不变。 |
| 本人/外人及终态 | 外人订单和不存在单均 404；3/4/6 支付返回 409，无订单 data/token 或新支付审计。 |
| DB 截止与旧配置 | deadline 等于 DB 当前秒和已过期均不支付；构造新的更长期限配置也不改变旧单结果。 |
| 审计/事件/熵源故障 | 注入三类失败，状态保持 1/version0，pay_time/token 为空，无支付审计/事件，库存保持原值。 |
| Token 唯一与有界重试 | 一次真实唯一索引冲突后新事务成功；持续冲突最多三次，旧单与其他 token 均未改写。 |
| Version 与凭证隐私 | version=-1/INT_MAX 拒绝写入；列表不返回 token，3/4/6 的详情不返回 token。 |
| 等锁跨越真实期限 | SQL callback 证明 Pay 到达被另一事务持有的活动库存锁；测试读取 DB 时间直至实际 deadline 已到，再释放锁，支付仍被拒绝，状态/时间/token/version 无变更。 |

期限竞争是受控新功能回归测试，首次实现已通过，没有发现并修复一个先前 Go Pay 竞态，因此本票不虚构“修复前 FAIL”。窗口由 SQL 到达信号、显式持锁和 DB 时钟观察建立，不用随机终止或仅 sleep 推断命中。

测试使用当前版本 SQL 和真实依赖，沿用精确随机 fixture helpers 清理自己订单/活动/审计/outbox/key。测试只通过 Creator 准备持久单，没有发起 Seckill 或写共享订单 Stream；没有启动 API/consumer，没有清库、删 group 或回退 ID 序列。测试后 fixture 清理、生命周期测试锁已释放。

## 本票边界与后续

- 支付写入已完成不代表真实资金支付；前端按钮/真实浏览器仍由主 agent 随 GO-031 页面工作验证。本报告没有替主 agent 宣称浏览器或整链路通过。
- 创建及支付的 `ORDER_CHANGED` 尚未由本票新增交易 relay 消费；支付事件库存回补为 0。不得把 PENDING 事件描述为已经投递。
- 取消、到期扫描、支付与关单竞争及取消后的资格释放留后续小功能。取消/过期必须沿相同锁序，以 DB 时间和状态条件变更；库存回补影响行数必须严格为 1，失败应整笔回滚。
- 已到期的未支付单当前只能拒绝支付，自动关闭尚未由此票实现。旧单期限、版本和快照需在下一票继续保持。
- 未注入真实网络提交结果未知、进程崩溃、数据库断线、持续负载；这些不能由普通事务回滚测试替代。Linux race、远端 CI 未执行。
- 本报告和 Pay 实现冻结后，不在主 agent 确认前扩展 Cancel/Expired，供这一个可验证功能先独立提交核验。
