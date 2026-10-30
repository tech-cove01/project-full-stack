# CAP-04 有界订单消费并行：内部代码审阅

审阅日期：2026-10-30。实现者为 `cap_reads`，本记录由非作者 `cap_trade` 对提交前工作树作只读源码审阅；这是内部 code review，不是用户另行安排的独立QA、容量验收或Linux race结果。本次没有启动依赖、发送负载或修改产品代码。

结论：当前改动未发现需要阻止提交的新增缺陷。可以在作者真实依赖回归通过后按单一问题提交；性能收益仍须用相同环境的正式前后对照证明。本次不把基线20/s的45秒探索或25/s受限结果当成并行配置的成绩。

## 范围及检查结果

审阅文件为 [config.go](../../../../backend/internal/platform/config/config.go)、[config_test.go](../../../../backend/internal/platform/config/config_test.go)、[app.go](../../../../backend/internal/platform/app/app.go)、[Stream集成测试](../../../../backend/internal/orderstream/integration_test.go)、[Worker单测](../../../../backend/internal/orderstream/worker_test.go)及 [.env.example](../../../../backend/.env.example)，并追读未修改的Creator、Reviewer、Worker事务/投影/ACK路径与应用关闭顺序。

| 关注点 | 源码结论 |
| --- | --- |
| 默认行为及边界 | 配置加载默认1，显式值经现有整数解析器限制1..8，非法值报字段名而不回显输入；样例仍为1。测试覆盖1/4/8和空值、0、负数、超界、非整数及空白。 |
| Worker可变状态 | app每轮调用NewWorker，消费者identity来自独立随机值；turn、pending/claim cursor、retries和initialized均留在各自Worker中。没有把同一个Worker.Run启动四遍。 |
| 共享Creator/Reviewer | 共享对象只有不可变DB指针/支付超时配置。每次调用从WithContext/Connection/Transaction获得局部操作；没有把事务或调用结果写回共享字段。SQL/Redis连接池自身支持并发。 |
| 订单ID及库存 | 每ID的连接级GET_LOCK、事务内身份核对、活动库存行锁、同用户/活动有效订单检查及order/audit/outbox同事务提交均未改变。GET_LOCK(0)竞争失败仍返回未知结果并保留PEL重试，不将冲突当作已成单ACK。 |
| ACK及后续状态 | 仍只有已提交订单、持久拒绝或持久review之后才ACK；重复回放读取当前订单状态，不再扣库存，也不覆盖后来取消状态。可选Redis投影与review清理没有替代DB事实。 |
| 公平与claim | 每个Worker继续轮转新消息、自有PEL及idle claim，仍为Count1；并行没有改变重试退避或生产claimIdle一分钟，也没有引入跳过失败消息的ACK。测试claimIdle=0仅用于强制恢复窗口。 |
| 关闭 | 各Run沿应用context停止，startBackground等待全部goroutine返回后才关闭诊断/DB/Redis资源。新增method value分别绑定各自Worker；模块Go1.27的循环变量语义也避免测试goroutine误用末次变量。 |

## 测试断言审阅

四消费者共享Creator/Reviewer场景使用gatedCreator：四个consumer必须在释放前各进入一次，避免“代码创建四个对象但实际串行跑完”的假阳性。八个唯一用户/订单在同活动收敛，验证库存10→2、八个有效owner、逐ID订单/审计/outbox恰一次、预约投影为CREATED且PEL归零。

重复场景先等待四条同ID消息的lag与PEL同时归零，再加入不同owner的同ID冲突，以及已有owner的另一订单ID。它逐条核对冲突message仍在PEL、同owner另一ID形成持久拒绝并ACK，原订单/库存/审计/outbox事实保持一次。该场景验证的是早期冲突留PEL，没有等到五次失败后的durable-review分支；不能声称它穷尽了所有既有review竞争时序。

提交后ACK前的既有中断场景改由四个consumer同时claim恢复，仍要求订单只有一条、后来status4不被复位、已恢复库存10不再扣减且最终PEL归零。原消费者在恢复组启动前已经取消并等待退出。

fixture注册的清理按LIFO先取消并等待全部consumer，再删除精确拥有的额外订单/预约，最后删除本活动/Stream。显式stop与t.Cleanup重复调用使用同一个cancel/done，正常退出后可重复等待；没有按前缀删除其它测试数据。超时会报错，不能把超时用作成功收敛。

## 验证边界

本审阅没有自行运行作者的新真实依赖回归，也没有将源码阅读记作测试通过。3452/6552的新隔离依赖由作者负责，实际测试日志/退出码在交付时另行链接。Windows相关回归即使通过，也不能代替Linux race或外部独立QA。四worker的同活动库存行锁仍会串行化持锁段；允许并行只增加非锁段重叠机会，是否改善25/s及更高到达率须以完整队列/成单/资源与逐ID对账确认。
