# GO-073-CAP 并行消费与诊断：独立源码审查

审查日期：2026-10-30（Asia/Shanghai）。审查者 `cap_code_review`，不是本批实现者。目标提交为 `09b49211f81389fb4075396f318f8d4b48e30cf1`，差量基线为 `6faf9a93006969410fa05f710ce82706a8271bd7`。先读取根 AGENTS、STATUS 和共享交付约定，审阅已提交差量，并检查主工作区的暂存、未暂存及未跟踪文件；首次检查主工作区干净。本报告落在核验者隔离 worktree，不改实现者原始证据。

**结论：本次范围未发现新增 P1/P2 阻断缺陷。** 这是独立源码与测试断言审查，不是容量实跑通过、Linux race 复跑或当前提交远端 CI 的独立确认。本审查没有启动依赖、API 或负载，也没有修改产品代码、运行测试或提交 Git。

## 检查范围与结论

| 关注点 | 独立源码结论 |
| --- | --- |
| 配置默认和上界 | [config.go](../../../../backend/internal/platform/config/config.go) 的 `OrderStreamWorkers` 默认 1，显式值使用原整数解析器约束 1–8。配置测试覆盖 1/4/8，以及空值、0、负值、越界、非整数和空白；错误不回显非法输入。 |
| 独立 Worker | [app.go](../../../../backend/internal/platform/app/app.go) 每轮调用 `orderstream.NewWorker`，把各实例自己的 `Run` method value 加入后台任务。没有并发调用同一个 Worker。随机 consumer identity、turn、pending/claim cursor、initialized 和 retries 均在各实例内，Worker 测试验证身份与重试表分离。 |
| 共享组件 | [Creator](../../../../backend/internal/order/creator.go) 和 [Reviewer](../../../../backend/internal/orderreview/review.go) 共享的是不可变 DB 指针/支付期限配置；每次调用使用局部 WithContext、Connection 和 Transaction，没有把事务、结果或游标写回共享字段。SQL 和 Redis 池保持原并发安全用法。 |
| 锁、事务与幂等 | 连接级每订单 ID 的 `GET_LOCK`、库存 `FOR UPDATE`、当前事实/owner 复核、有效订单唯一性以及订单/审计/outbox 同事务均未删改。锁释放失败仍丢弃物理连接，不将持锁会话回池。不同订单仍竞争同活动库存行，增加消费者只允许非持锁阶段重叠。 |
| 持久结果与 ACK | [worker.go](../../../../backend/internal/orderstream/worker.go) 仍仅在确认提交订单、提交拒绝或持久 review 后 ACK。未知结果保留 PEL；可选 Redis 投影和 review Resolve 不替代持久事实。重复消息读取现有状态，不重新扣库存，也不把后来取消状态回写为新建。 |
| 取消与关闭 | 每个 Run 接收应用生命周期 context；后台 WaitGroup 等待全部任务返回。新增 applicationResources 先关 diagnostics，再关 DB/Redis 池；诊断关闭失败仍继续关闭池并保留错误。 |
| 诊断启用边界 | [config.go](../../../../backend/internal/platform/config/config.go) 默认关闭诊断，拒绝 production、非数字地址及非 loopback；[diagnostics.Start](../../../../backend/internal/platform/diagnostics/server.go) 再次验证数字 loopback、真实池和正关闭预算。诊断使用独立 listener/mux，没有挂到产品 API。 |
| 诊断数据和代价 | capacity JSON 只输出进程/runtime/池计数白名单，不输出 DSN、用户名、口令或 SQL 内容；统计调用不发 SQL/Redis 命令。pprof 只注册必要标准 profiles，cmdline/symbol/trace 不可达；GET-only、禁止 forced GC、seconds 限 1–30，设置 header/read/write/idle 及关闭预算。 |

## 测试断言复核

审阅 [Stream 集成测试](../../../../backend/internal/orderstream/integration_test.go)、[Worker 测试](../../../../backend/internal/orderstream/worker_test.go)、[配置测试](../../../../backend/internal/platform/config/config_test.go)、[诊断测试](../../../../backend/internal/platform/diagnostics/server_test.go)、[应用诊断关闭测试](../../../../backend/internal/platform/app/diagnostics_test.go)及已有应用生命周期测试。

- 四消费者 gate 必须在释放前收到四次进入，验证真实并行进入共享 Creator；八笔同活动唯一用户/订单最终逐 ID 匹配，库存、有效 owner、审计、outbox、预约状态及 PEL 都有断言。
- 四条同 ID 重投收敛后再加入错 owner 消息及原 owner 的另一个订单 ID；断言错 owner 消息仍在 PEL，已有有效订单的另一 ID 形成持久拒绝，原库存/订单/审计/outbox 不重复。
- 提交后 ACK 前的中断测试，由四消费者 claim 恢复，仍检查后续取消状态不复位、库存不再扣减、订单不重复且 PEL 收敛。原消费者在恢复组启动前已取消并等待返回。
- 诊断测试覆盖白名单、实际计数回调、拒绝其他 pprof 面、非法时长/forced GC/方法、关闭超时取消活动请求、listener 释放和重复关闭。应用测试检查 diagnostics 先于池关闭，且前者失败不跳过后者。

新增同 ID 回放测试最初 10 秒等候失败，原失败日志保留；之后只将测试收敛等待改为 30 秒，能覆盖既有 2+4+8 秒退避。没有修改生产退避或容量阈值。作者 [真实回归记录](../../../evidence/2026-10-30-go073-cap/cap04/parallel-regression/report.md)和其日志支持作者运行结果，但本审查没有把复读日志记为自行执行。

## CI 与保留边界

复读 [767c2fc CI 存档](../../../evidence/2026-10-30-go073-cap/cap04/ci-767c2fc.json)，其 headSha 精确为 `767c2fc8bdf1be08c5636143a71898eec25081a9`，四作业均 success。当前 [workflow](../../../../.github/workflows/ci.yml) 包含普通 `go test -race ./...`，以及真实依赖的 orderstream、order、review 等 race 作业。因此新增四消费者用例在该工作流执行范围内；本子任务没有联网独立确认远端运行，也没有由旧绿色推断后续文档提交 CI。

该错 owner 回放用例覆盖早期保留 PEL，没有等到第五次失败后的 durable-review 分支，不声称穷尽所有 review 竞争时序。并行消费没有消除同活动库存行的串行持锁段；是否提高持续落单能力须由同条件负载、积压和最终逐 ID 对账证明。Windows native terminate 的收尾证据也不能当成信号优雅关闭实证。

本次未重复审查 Feed/013 生产变更；其结论由另一独立审查及主核验报告给出。本报告仅覆盖并行 Worker、配置、diagnostics 及相关测试/关闭路径。

## 对性能解释的源码约束

订单处理包含 GET_LOCK、事务/当前事实复查、库存行锁、多条持久写入、COMMIT、RELEASE_LOCK 和后续投影/ACK。[基线瓶颈报告](../../../evidence/2026-10-30-go073-cap/cap04/baseline-bottleneck.md)支持串行 I/O 服务链作为待验证候选，不证明磁盘/fsync 是唯一根因。完整持续最终成单吞吐，不能与单纯 Redis/Lua 准入或轻量读的 QPS 直接比较；不能依据本审查声称继续加 Worker 或扩大池就会达到某个预定数字。
