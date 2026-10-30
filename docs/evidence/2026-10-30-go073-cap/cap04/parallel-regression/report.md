# CAP-04 有界并行订单消费者：实现与直接回归

2026-10-30。针对已观察到的单消费者串行服务链限制，仅加入 `ORDER_STREAM_WORKERS`：默认 1，合法整数 1–8，容量对照需显式设置 4。配置错误拒绝启动且不回显值。应用创建多个独立 `NewWorker`，各自随机 consumer identity、PEL/claim 游标和重试表；共享的 Creator 只有数据库池与不变支付时限，Reviewer 只有数据库池。启动 ready 日志记录实际配置数量。

没有修改事务、连接拥有的 GET_LOCK、活动库存行锁、唯一 active 约束、当前事实复查、持久 outbox/audit、ACK/review 或退避逻辑。所有 worker 沿用应用 lifetime context 和既有 WaitGroup，结束后再关闭池。回退将配置设回 1，无 schema 变更。

## 环境与命令

根 agent 先正常停止本轮交易 API 与专用交易容器并保留卷，然后创建独立测试项目 `project-full-stack-cap-opt-tests`，MySQL `127.0.0.1:3452`、Redis `127.0.0.1:6552` DB 0，数据库 `project_full_stack_go`，已执行十条版本 SQL。本 agent 只对该环境测试，没有启动 API、新容器或停止依赖，不访问未知 3308 或交易 3450/6550。凭据来自 Compose 的本地开发环境，未导出完整环境。

精确命令和选定非敏感环境见 [commands.txt](commands.txt)。`GO_INTEGRATION=1`、`GO_ENV=development`、`ORDER_STREAM_WORKERS=4`、`GOMAXPROCS=4`，Kafka/ES/AI 关闭。从 `backend` 执行：

```text
go test -count=1 -p 1 -v ./internal/platform/config ./internal/platform/app ./internal/orderstream ./internal/order ./internal/orderreview
go test -count=1 -p 1 -v ./internal/orderstream
go vet ./internal/platform/config ./internal/platform/app ./internal/orderstream ./internal/order ./internal/orderreview
```

## 结果与保留失败

- `gofmt` 退出 0；相关五包 `go vet` 退出 0，见 [vet-exit.json](vet-exit.json)。
- 首轮 [test-initial.txt](test-initial.txt) 的 config、app、order、orderreview 均通过，含默认/上下界配置、HTTP/worker 关闭顺序、创建幂等/跨 voucher 与拥有者冲突/连接锁释放、支付取消原有强用例，以及 review 中断重放。
- 首轮新 Stream 重复 ID 测试失败：设置的 10 秒异步等待不足。四个消费者同时处理完全相同 ID，原有 GET_LOCK(0) 争用会保留 PEL 并依次经历 2、4、8 秒退避，第四份可能在约 14 秒才 ACK。完整原始失败与总 exit 1 保留，不将其写成通过。
- 只将测试等待上限改为 30 秒，未改生产退避、限流或业务判据。随后 [test-stream-rerun.txt](test-stream-rerun.txt) 的整个 orderstream 包通过，exit 0：四同 ID 收敛测试耗时 14.66 秒；整个真实 Stream 测试 15.91 秒。其余先前通过的包源码未再改动，未重复执行。

新增或扩展的直接验收：

| 行为 | 实际断言 |
| --- | --- |
| 四消费者并发共享 Creator/Reviewer | gate 证实四独立 consumer 同时进入处理；同一活动 8 个唯一 ID/用户，库存 10→2，逐 ID/用户、audit/outbox 各一次，保留匹配 CREATED 投影，PEL 0 |
| 相同 ID 重投和身份冲突 | 四份完全相同消息逐步 ACK，订单/扣库/审计/事件仍各一次；另一用户复用已成单 ID 继续留 PEL；同用户另一 ID 持久拒绝且 ACK；原拥有者未改变 |
| 提交后 ACK 前故障恢复 | 复用原真实 afterCommit barrier，原进程取消后四个独立 consumer 争取 claim；后续已取消订单保持 status 4、库存 10，不重建或二次扣库，PEL 0 |
| worker 生命周期 | 四实例身份/重试状态独立，取消 group 全部返回；真实多 worker fixture 取消并等待全部结束后才清理精确私有行/key，既有 app 关闭池顺序测试通过 |

foreign 冲突测试覆盖早期保留 PEL，不声称穷尽第五次失败后的 durable review 并发竞争。durable review 写入/后 ACK 流程未改；既有 review 中断重放及 DB 不可用不 ACK/恢复测试分别保留。

独立代码审查由 `cap_trade` 完成，检查共享组件、事务/锁/ACK 顺序、消费实例状态、测试 gate 和精确清理顺序，未发现阻断；退避等待修正与原始失败同时交审。以上为 Windows 普通真实依赖回归，未执行本地 race，不代替 Linux race/远端 CI 或独立验收。该实现尚无性能收益结论，必须由根 agent 同资源、同数据、同负载比较 1 与 4 worker，并分别记录首个受限档和持续稳定档。
