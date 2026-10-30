# 对照前的窗口外夹具收尾

原订单支付时限15分钟保持不变。前序4770笔未支付测试订单若在新对照期间到期，会引入不同的超时/补偿后台负载。因此在正式测量外，以同一专用交易卷、固定 `3db296dd0beedd6f85d06cf5479d707b0498ce7e` API、1消费者执行 [收尾工具](../../../../scripts/capacity-settle.py)。这属于合成夹具生命周期，不是容量结果，也不删除或覆盖先前事实。

工具实时核对专用Docker项目、loopback端口、不可变容器ID和API版本，拒绝8260生成器控制监听；只读取该私有池tag、已完整逐单对账的CAP结果与对应fixture/订单文件。对每笔已确认订单使用其真正拥有者会话，经正常 `POST /voucher-order/:id/cancel`，幂等接受已取消订单；不直接改库存、订单状态、超时配置、审计或outbox。仍要求操作者独占且没有其它发压方，端口无监听不是任意外部调用都不存在的证明。

[逐ID取消回执](settle-baseline/orders.ndjson)与[汇总](settle-baseline/summary.json)记录4770/4770成功或已取消，凭据没有导出。逐项flush保留已落证回执，进程异常时可用原始范围重新幂等执行；不能把未收到的响应直接视为失败事务。正常取消后的补偿继续由原worker完成，先查未支付0、未解决review0，再按[排空记录](settle-baseline/drain.ndjson)等到未完成outbox0后才启动同构建1消费者25/s对照。

八个来源均为先前已对账的独立活动：CAP-01手工/自动on/自动off/停止探针，以及CAP-02的10/20/25/25-profile。它们原有库存、SQL/Redis和订单结论对应各自测量结束时点；后续取消是额外正常状态迁移，不能用当前库已取消状态倒改历史原始结果。SQL数据、Stream和卷均保留；不把取消后的回补当成本轮压测的原始受理库存。

## 后续已核清的停止档与持有前收尾

同构建1消费者对照后另收尾1377笔，见 [汇总](settle-compare-w1/summary.json)。4消费者探索后再通过正常接口收尾8729笔，见[逐ID回执](settle-workers4-exploration/orders.ndjson)与[汇总](settle-workers4-exploration/summary.json)。其中50/s停止档保留原始 `reconciliationPassed=false`，以显式 `--accounted-stop` 选择；工具重新读取完整SQL/Redis/请求文件，逐ID/用户和库存核清2399笔持久受理，2398条完成响应、1条可查受理回执缺口、0未知持久去向。其它不变式或ID集合不符仍拒绝；没有将原始失败改成通过。[23项边界检查](accounted-stop-checks.json)与[停止档分析](../cap02/workers4-rate50-stop-analysis.json)保留。

8729笔批量取消后的补偿首次等待180秒仍有1690条未完成，原[时间序列](settle-workers4-exploration/drain.ndjson)和[超时记录](settle-workers4-exploration/drain-first-attempt.json)未覆盖成成功。继续窗口外等待，在[后续记录](settle-workers4-exploration/drain-retry.ndjson)UTC05:27:20实际归零，并再核对旧未支付订单为0后才开始180秒持有。这个批量夹具收尾时间不属于压测阶段15秒排空判据，也不证明取消/退款高负载容量；本轮交易持有测量的是正常受理与创建链路，未混合并发支付、退款或大批到期工作负载。
