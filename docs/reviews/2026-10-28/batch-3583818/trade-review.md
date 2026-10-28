# GO-031 / GO-032 / GO-033 独立交易核验

基线 `358381843d0fe5efb98dfb75cac34c089f0777b8`；本分工使用独立 MySQL `project_full_stack_review_trade`（3321）和 Redis6393 DB1，无应用消费者。只核验既定 Go 交易，不操作 Java、其他项目或开发服务；提交由主核验者统一完成。

## 实际发现并修复

1. **商家写授权竞态（P1）**：核销/退款仅在事务外查成员。该查询后撤权，原请求仍能在事务里改订单。真实 Row 回调仅在前置查询后提交停用，再执行真实业务事务，旧代码核销/退款均错误成功。`authorization.go` 在事务内按 shop→merchant→member→STAFF scope 锁定当前授权，与活动管理保持一致，再按原券→库存→订单锁序写入；事务提交前持续持有授权锁，活动实际商家也必须一致。
2. **明确失败预约补偿断链（P1）**：Creator 写 `RESERVATION / RESERVATION_REJECTED`，ClaimOrder 仅领取 `ORDER_CHANGED`；payload 还是另一套 `stockDelta/releasePurchase` 字段。失败预约永久占库存/资格。修复为精确认领两类已知事件、保留其他worker范围，转换已持久的拒绝payload，若相同orderId存在真实订单则停在NEEDS_REVIEW，确认无订单才执行原一次性恢复。
3. **补偿 Lua 预检及所有权（P1）**：`1.5`、`-1`、`01` 库存、缺失/非法最大库存及错误release参数可写marker/库存；旧取消还会删除已属于新预约的资格。补齐数量、别名、规范整数、ID、release、marker值、类型与界限预检，再写；accepted已指向新订单时保留其映射及资格，只回补旧预约自己的库存。真实8个失败用例分别验证7类非法输入零写与旧事件不误删新资格，重投不重复回补。
4. **重建漏失败补偿（P1）**：blockers只看ORDER，漏了RESERVATION。真实PENDING拒绝存在时重建仍成功。现在同样阻挡PENDING/PROCESSING/NEEDS_REVIEW的两类交易补偿，继续使用当前锁定读。
5. **重建部分成功后不可重试（P2）**：Redis已写N+2，随后DB审计故障回滚到N，重试Close(N+1)恒报stale。修复从当前DB与Redis观测版本最大值继续单调关闭/准备，绝不回退版本或提前开放。通过真实Redis+DB审计回滚窗口验证重试恢复；新OPEN仍由提交的outbox负责。
6. **review中断不可重放（P2）及身份错误证据（P1）**：Claim提交REQUEUED后进程退出或XADD结果未知，命令永久拒绝再次执行；同orderId另一身份Record也会拿既有行当持久证据。Claim现在允许OPEN/REQUEUED重复发布相同ID，RESOLVED仍不可重放；Record事务锁定并核对原user/voucher，不一致必须失败，不能授权ACK。命令同步接受REQUEUED。
7. **无consumer group的未读Stream被当作空闲（P1）**：XINFO GROUPS返回空列表就直接放行；丢失group但保留消息会据DB库存重建覆盖预约。现先验证类型；真正缺失Stream为空闲，存在消息且无group为UNREAD，任何非零或未知Lag均阻挡，错误类型/不可达不虚构空闲。私有Stream真实覆盖missing→unread→pending→ACK后idle。

## 新旧证据

- [trade-initial](trade-initial.jsonl)：12个新失败行为（授权2、拒绝/重建2、Lua8），修复前真实FAIL。
- [trade-final-first](trade-final-first.jsonl)：上述12个修复后全部PASS。
- [recovery-initial](recovery-initial.jsonl)：review中断重放FAIL；此文件中的重建失败是初版测试挂错GORM回调导致“fault did not roll back”，不是业务复现。该夹具错误已纠正，未覆盖初轮证据。
- [rebuild-retry-baseline](rebuild-retry-baseline.jsonl)：纠正为Create回调后，使用Go `-overlay` 编译基线reconcile源码（工作树不回写），真实命中审计回滚后第二次重建报stale，确认业务缺陷。
- [review-binding-initial](review-binding-initial.jsonl)：另一身份复用持久review的真实FAIL。
- [reconcile-stream-initial](reconcile-stream-initial.jsonl)：缺失Stream误报错误；[no-group initial](reconcile-no-group-initial.jsonl)：继续执行确认未读消息无group时误判idle。
- [trade-final](trade-final.jsonl)：merchantorder/ordercomp/orderreview/outbox/activity/order/orderstream/ordertimeout八包普通+真实测试全部PASS，包含76个真实集成一级子组，另有review顶层集成。原GO-031支付8、取消11、扫描SQL6/真实scanner3与GO-030落单/消息窗口均由当前源码重跑，不引用旧成绩。
- [reconcile-final](reconcile-final.jsonl)：最后Stream门槛修复后，只定向重跑activity私有Stream和merchantorder新交易组；全部PASS。核销/退款20并发对账最多一次终态审计、最终库存1或2与订单3或6一致，同一订单总3条事务事件。
- 全部分工源码gofmt、相关vet、git diff --check通过。[汇总](trade-summary.json)；精确自有用户/商家/券/review行清理为0见[cleanup](trade-cleanup.json)。没有清空表、重置序列或删除未知Redis组。

## 已覆盖与没有因此证明的范围

本轮覆盖：持久事务原子性/幂等与RC锁序、期限后支付/取消与扫描、核销退款竞争、当前成员拒绝窗口、明确拒绝补偿可投递/一次效果、输入错误零部分写、新预约所有权、失败/待核对补偿阻挡重建、重建审计回滚重试、review重复claim及身份绑定、无group未读消息阻挡。真实故障窗口仍是测试屏障/回调和context取消，不冒充物理断电。

本分工没有执行：OS强杀、实际COMMIT回包丢失、生产60秒接管等待、物理MySQL/Redis停机、重建与并发取消/relay交错的全部窗口、运维命令作为独立子进程中途强杀再重启、持续容量/延迟。未验证的Redis灾难丢失不宣称零丢单；Lua已预检的可预计错误与OOM等不可预检故障继续分开。根核验者可结合自己真实故障/页面/CI结果判定对应任务状态，不能只因本分工普通测试PASS就宣称所有GO-033灾难恢复验收完成。

## 分步提交建议（根统一执行）

- 当前授权：merchantorder authorization/redeem/refund及对应真实窗口。
- 拒绝补偿与Lua：outbox ClaimOrder、ordercomp worker/restore及真实拒绝/零写/所有权证据。
- 库存重建：activity/reconcile和私有Stream门槛回归、审计回滚真实证据。
- review恢复：orderreview repository/真实集成、cmd/review。

merchantorder的batch集成文件复用了同一精确夹具同时验证这几条贯通链，提交时应连同已实现依赖一起保留，避免提交一个依赖尚未落盘而固定失败的中间状态。
