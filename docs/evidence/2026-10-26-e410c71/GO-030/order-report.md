# GO-030b / 数据库落单、本人查询与抢购 API 自验报告

日期：2026-10-26。状态：**自测通过待核验**。

本票依赖 GO-021 的活动协议及 GO-030a 的 Redis 准入模块。开始基线为 `e410c71d9d8dfeaf654e203e439fa3f871c9afd4`；整理本报告时 HEAD 为 GO-030a 提交 `f86a97fc563d526dba5c74c9e62f76d6167d3da6`，`backend-go/internal/order/` 为待主 agent 独立提交的本票代码。本报告不提前填写交付 SHA，也不改变 GO-020/021 的外部核验状态。

范围为 `order/` 的 Creator、本人查询/分页、Seckill 服务和三个 HTTP 路由。实际 Stream 消费者、ACK 与应用生命周期由 GO-030c 的报告单独证明；本报告的数据库调用测试不能替代消费者故障窗口或真实浏览器证据。

## API 与外部契约

三个路由均使用现有 `auth.RequireLogin`，用户 ID 来自登录上下文；请求体、查询串及额外身份请求头不能决定操作人。

| 路由 | 本票行为 |
| --- | --- |
| `POST /voucher-order/seckill/{voucherId}` | 先返回本人该活动已有的有效 DB 订单 ID；其次找回 accepted 索引中的稳定 ID；仅新预约再读取启用券及 ACTIVE 活动的版本/销售时间，经 GO-030a Lua 准入。返回字符串 ID 不表示 DB 已落单。 |
| `GET /voucher-order/{id}` | 优先读取本人 DB 订单，返回其当前业务状态；没有订单时才读取持久拒绝，最后检查本人 Redis 预约。其他人的订单、拒绝或预约统一不可见。 |
| `GET /voucher-order?current=1&pageSize=10` | 仅本人 DB 订单；`pageSize` 上限 50，按 `create_time DESC, id DESC` 排序；`data` 始终为数组、`total` 为十进制字符串。远页先根据总数判断，避免乘法溢出。 |

订单 ID 必须为规范的正十进制字符串，范围不超过 MySQL `SIGNED BIGINT` 上限；用户、活动和门店 ID 按相应 unsigned 范围解析。ID、金额始终以字符串输出。时间使用内嵌 IANA `Asia/Shanghai`，DTO 保留 `yyyy-MM-dd HH:mm:ss` 的业务时间格式。

详情字段为 `id/state/message/orderStatus`，DB 订单再包含 `shopId/payAmount/payDeadlineAt/redeemBeginTime/redeemEndTime/createTime/payTime/useTime/refundTime` 中实际存在的值。只有本人、当前 `status=2` 的详情允许返回已有 `redeemToken`；列表从不返回 token，其他状态也不返回 token。

无 DB 订单的 PROCESSING/FAILED 响应明确输出 `orderStatus:null`，不伪造价格、期限或核销凭证；其他不存在的快照字段省略。这按交易设计中的明确 null 契约实现，区别于 Java `NON_NULL` 配置下可能省略该字段的历史行为。

HTTP 401 由登录中间件处理；非法 ID/分页为 400，不可见订单为 404。活动未就绪、售罄、未开始、已结束等预期准入失败沿用 HTTP 200、`success=false`；依赖故障、碰撞或异常状态返回不带内部诊断的 503。

## 持久结果、事务与锁

`NewCreator(db, payTimeout).Create(ctx, id, userId, voucherId)` 只有在完成持久结果后才以 nil error 返回：

| `Outcome.State` | 所依赖的持久事实 |
| --- | --- |
| `CREATED` | 新事务已经提交，或同 ID、同用户、同活动的 DB 订单已经存在；重投返回该单当前业务状态。 |
| `FAILED` | 同事务已经提交唯一的 `RESERVATION_REJECTED` outbox 事实；没有据此执行 Redis 回补。 |
| error / 空 State | 连接、提交、锁竞争、身份碰撞、配置缺失或其他无法确认的结果；调用者必须保留 pending，不得据此 ACK、设 Redis FAILED 或释放库存。 |

订单和拒绝事实分属两张表，仅订单主键不足以拦住“同 ID、不同活动，一方成功、另一方拒绝”的竞争。因此 Creator 先通过 GORM `Connection` 固定一个 `*sql.Conn`，执行 `GET_LOCK('go:order:'+id, 0)`。拿不到锁立即作为未知结果返回，不在锁内退避等待；拿到锁后才在同一连接上执行显式 READ COMMITTED 事务。

命名锁不随事务提交自动释放。退出时使用独立、最长 1 秒的 background context 执行 `RELEASE_LOCK`。获取锁的响应发生错误时，或释放失败/未明确返回成功时，通过该物理连接的 `Raw(... driver.ErrBadConn)` 将其丢弃，防止持锁会话回到连接池。若业务已经提交但释放失败，仍返回未知错误；后续重投重新获取锁并读到已提交单，不重复扣减。

事务中的当前读取与锁顺序如下：

1. 按订单 ID `FOR UPDATE` 查现单，同时检查已有持久拒绝，核对用户/活动绑定；已存在的确定结果直接返回，不改状态。
2. 新建路径按 `tb_voucher FOR SHARE` → `tb_seckill_voucher FOR UPDATE` 读取真实门店、售价、库存及核销期快照。这个活动行顺序与发布/下架路径一致。
3. 活动锁等待结束后，再次当前读取该 ID 的现单/拒绝；随后以 `FOR UPDATE` 检查同用户、同活动的 `active_flag=1` 订单。取消状态 4 的生成列为 NULL，允许后续新单；退款状态 6 仍占用资格。
4. 从同一事务取得一次 DB `NOW()`，计算 `pay_deadline_at=min(DB时间+payTimeout, redeem_end_time)`；条件 `stock>0` 扣一份库存，再插入订单、审计和 outbox。所有操作使用同一 tx，任一步失败全部回滚。

成功新单固定 `status=1/version=0/pay_type=1`，`shop_id/pay_amount/核销期/支付截止/create_time` 均由服务端持久数据及 DB 时间产生，生成列不参与写入。消费者处理已经受理的消息时不再以当前 OFFLINE 或销售结束拒绝落单。即使核销期限已经过去，也保留计算得到的截止时间；自动关单属于 GO-031。

1062 不直接等同于“限购”。先完整回滚本次事务及其扣减，再在同一命名锁保护下进入新的 RC 事务核对，最多三次；同 ID 相同绑定返回现单，绑定不同为碰撞错误，同用户另一有效单才持久化限购拒绝。同单重投不把已支付、核销、取消、退款的当前状态改回 1。

成功创建同时记录 `ORDER_CREATE` 审计，操作人是可信消息中的购买用户、`operator_type=USER`。同事务产生 `ORDER:{id}:0:CHANGED`，聚合 ORDER、版本 0、事件类型 `ORDER_CHANGED`；payload 兼容既有 `orderId/voucherId/shopId/userId/targetStatus/stockRestore/releaseEligibility/version` 字段，创建时 `targetStatus="1"/stockRestore="0"/releaseEligibility="false"`。审计或 outbox 失败时没有半笔订单或库存变更。

## 拒绝事实与恢复边界

无 DB 库存、或同用户已有另一有效单时，事务先确认当前 ID 没有现单，随后写入：

```text
event_id = RESERVED:{id}:REJECTED
aggregate_type = RESERVATION
aggregate_id = {id}
aggregate_version = 0
event_type = RESERVATION_REJECTED
payload = {orderId,userId,voucherId,reason,stockDelta:1,releasePurchase:boolean}
```

库存不足时 `releasePurchase=true`；已有另一有效单时为 false，以保留原单购买资格。重复处理依据该事件返回相同 FAILED；同 ID 不同绑定不另造拒绝事实。

**本票只持久化拒绝，不消费补偿。** 该事件保留 PENDING，Redis 预约库存、资格及 accepted 映射保持预占；没有直接 INCR、删除资格或写 Redis FAILED。普通活动 relay 只认领 ACTIVITY 事件，不会误认领 ORDER/RESERVATION。创建的零回补 `ORDER_CHANGED` 也仍待后续交易 relay 处理。不能将本票描述为取消/拒绝补偿已经完成或库存已经追平。

查询始终让 DB 现单优先；单独的 Redis FAILED/CREATED 值都只能展示 PROCESSING。持久拒绝才允许展示 FAILED。读取 accepted ID 的两个入口——前置 Recover 和 Lua Reserve 的 `Reused` 返回——都会核对现存 DB 订单、持久拒绝及现存 reservation hash 的用户/活动绑定；错误类型或绑定异常保持未知，不返回外人的 ID。

预约 hash 的历史 TTL 为 24 小时，accepted 索引为 7 天。hash 缺失而索引仍在时允许找回稳定 ID，不以此创造终态；若同时没有 DB 订单/拒绝且没有 hash，本人详情缺少绑定证据会不可见。此 TTL 边界不构成失败、补偿或 ACK 的依据，完整持久 review/恢复由 GO-033 补齐。

## 先复现失败，再修复

保留以下原始失败文件，没有用最终 PASS 覆盖：

- [Get 查询竞争原始 FAIL](order-get-race-initial.txt)：GORM Row callback 在首条本人查询已经执行后暂停；另一连接的 Creator 提交本人单；恢复后，原代码第二次 `EXISTS(id)` 将新现单误判为他人单并返回“订单不存在”。修复为第二次读取当前记录并核对 owner，本人立即返回 CREATED，他人仍为 404。
- [accepted 旁路原始 FAIL](order-accepted-race-initial.txt)：四个受控子例均能返回外人或错误类型预约对应的 fixture ID。包含已有外人 pending 索引、Recover 返回空后在 Reserve 前插入外人 committed/pending 索引，以及 hash 为错误类型。修复为两类恢复路径都执行绑定校验，并检查现存 hash；错误仍为未知，不改 Redis/DB 业务事实。

Reserve 窗口由测试 admission wrapper 在真实 Recover 与真实 Lua Reserve 之间插入索引，仍实际执行 Redis 脚本。Get 窗口由 SQL callback 和 channel 建立执行顺序，不用随机 sleep 或随机杀进程冒充命中窗口。

## 实际验证

普通 `gofmt`、`go test -count=1 ./internal/order`、`go vet ./internal/order` 通过。真实测试使用隔离 MySQL `127.0.0.1:3308/project_full_stack_go`、Redis `127.0.0.1:6380`，会话为上海时间；凭据从环境提供，本报告和日志不记录密码。

真实命令在 `backend-go/` 执行：

```powershell
# 先注入隔离 MYSQL_*、REDIS_* 和 GO_ENV=development。
$env:GO_INTEGRATION='1'
$env:GIN_MODE='release'
go test -count=1 -v ./internal/order -run TestIntegrationOrder
```

[最终原始 PASS](order-real-final.txt) 为 12 个主组，其中 accepted 竞争组另含 4 个子组。主测试耗时 2.57 秒，包耗时 2.841 秒，只是功能验证运行时间，不是吞吐成绩。

| 主组 | 核验到的行为 |
| --- | --- |
| 创建快照/下架后落单 | DB 时钟与支付期限裁剪、售价/门店快照、库存一次扣减、创建审计及事件同提交；随后改当前售价/核销期不影响旧单。 |
| 同消息终态重投 | 状态 1/2/3/4/6 均保持当前状态；没有第二次库存扣减、创建审计或创建事件。 |
| 同 ID 并发及有效单唯一 | 8 个同 ID 调用/重试最终取回单一现单；另一 ID 限购有持久拒绝；生成列的取消可重买规则有效。 |
| 售罄持久拒绝 | 重投同一 FAILED、仅本人可见，事件 PENDING，无 DB 订单或实际 Redis 补偿。 |
| 审计/outbox 故障 | callback 注入写入错误，订单、库存、审计、事件整体回滚；故障解除后可以安全重试。 |
| 跨活动 ID 碰撞 | 同 ID 一方可成功、一方可拒绝的真实竞争只留下一个身份事实；另一绑定与另一用户的重投保持 collision。 |
| 命名锁与释放故障 | 拿不到锁仍为未知；强制 RELEASE_LOCK 失败后物理连接被丢弃，独立连接能再次拿锁；重试只取回已提交单。 |
| DB 优先与伪 Redis 终态 | Redis FAILED 或伪造 owner 不能覆盖 DB；未证明的 Redis CREATED/FAILED 均为 PROCESSING，显式 null，不含伪造快照。 |
| Get 两查之间提交 | 可控 SQL 窗口后立即返回刚提交的本人 CREATED。 |
| accepted 恢复/重用绑定 | 4 个子例修复后全部拒绝泄漏 ID；同时保留合法索引在下架后找回原 ID 的行为。 |
| 本人稳定分页与凭证 | 同创建时间按 ID 稳定排序、精确 total、超远页 `[]`；列表无 token，仅本人已支付详情可有 token。 |
| 受理恢复与新准入 | 本人有效 DB 单在 Redis 不可用/活动已下架时仍可找回；accepted 索引优先于下架判断；真实新准入重复调用仅一条消息、一次库存扣减。 |

部分终态及已支付 token 为精确 ID 的测试夹具，不代表本票已经实现对应支付/取消/退款写接口。真实测试没有启动消费者，通过 `go030_stream_integration` 命名锁协调 fixture 窗口；只删自己活动的 Stream entry、订单/事件/审计及 Redis key，不 FLUSH、不删 group、不回退序列和 AUTO_INCREMENT 高水位。测试结束后夹具清理、锁释放，无后台进程。

## 未覆盖与后续注意

- 本票未运行真实进程终止、ACK 竞争、自动 claim 公平性、启动关闭或真实页面；这些由 GO-030c/页面报告分别记录。本报告不引用 Java 历史吞吐或旧测试次数。
- Linux race、远端 CI、持续负载和 Redis 灾难丢失恢复未执行；本地普通测试不替代这些结果。
- GO-031 才实现模拟支付、取消、超时扫描与 Redis 回补衔接；本票不开放对应写路由，也不创建核销 token。当前新单可能超过期限仍停留未支付，不能宣称自动关单已经可用。
- GO-031 的支付/关单必须使用 DB 当前时间、已有快照及状态条件；配置变动不能重算旧单支付期限。每个成功转换只递增一次 version，同事务审计/outbox，不能覆盖创建版本 0 的事件。
- 后续取消/退款将同时访问订单与活动库存。Creator 的新建路径在库存锁后检查有效订单，新增生命周期事务应统一锁序，避免一方“订单→库存”、另一方“库存→订单”的循环等待；必要的死锁重试要有界，不能把死锁错误判成明确拒绝。
- 完整拒绝/取消/退款补偿、事件去重、缺键核对、持久 review、受控重放及库存重建属于 GO-033。特别不能消费当前 PENDING 拒绝时凭空 INCR 缺失库存，或在取消后误删后续新预约的映射。
- 每 ID 的连接命名锁对本票 Go Creator 实例之间有效；旧 Java 写入者不参与该协议。正式接管前仍须停止旧写入/消费者并按 GO-071 核对状态，不能同时运行双消费者后声称本票的碰撞屏障覆盖了所有旧代码。

本报告整理过程中仅进行源码和现有日志只读检查，没有再运行会写 `stream.orders` 的测试，以免干扰主 agent 正在执行的 API/浏览器核验。
