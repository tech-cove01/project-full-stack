# GO-030c / Stream 落单与应用生命周期

2026-10-26（Asia/Shanghai），自测通过待独立核验。GO-030 开始 SHA `e410c71d9d8dfeaf654e203e439fa3f871c9afd4`；准入基础已单独提交为 `f86a97fc563d526dba5c74c9e62f76d6167d3da6`。本说明覆盖 `internal/orderstream/`、应用多 worker 生命周期及其与 DB Creator 的边界；本子功能交付 SHA 由总报告补齐。

最终 [带窗口时间线的真实 Stream 重跑](stream-window-final.txt) 中，8 个主组及 2 个普通单测 PASS，记录了订单 ID、消息 ID、consumer 和时刻，覆盖提交前回滚→重领及提交后→重放。此前输出保留在 [初轮日志](stream-real-initial.txt) 与 [补充时间线前的日志](stream-real-final.txt)。[启动/关闭 6 场景](bootstrap-final.json) 和 [Go 全套验证](go-validation-final.txt) 也已通过，详情见下方生命周期记录。报告定稿时 API/Vite 已停止；依赖停止与最终提交 SHA 由总报告记录。本报告编辑未额外启动消费者或执行会写共享 `stream.orders` 的测试。

## ACK 的持久依据

生产默认 `stream.orders` / group `g1`，consumer 为随机 `order-go-*`。首次使用 `XGROUP CREATE ... 0 MKSTREAM`，`BUSYGROUP` 幂等接受，因此应用启动可能创建 Stream/group，也可能消费已有订单消息。消息保留 `id / userId / voucherId` 三个字符串字段；订单 ID 校验 signed BIGINT 正数范围，用户与活动 ID 校验 uint64 正数范围，不通过浮点数转换。

worker 只在 Creator 返回无错误且 `Outcome.State` 为下列状态时尝试 ACK：

- `CREATED`：同一订单 ID、用户、活动匹配的订单已提交，或读到该已存在订单的当前 DB 状态。首次创建的库存扣减、订单快照、创建审计和 `ORDER_CHANGED` 在同一事务提交；重投不重设状态或再次扣库存。
- `FAILED`：`RESERVATION_REJECTED` 已作为 `RESERVATION` outbox 事件提交，带原订单、用户、活动和后续补偿参数。仅 Redis 中的 `FAILED` 不构成此依据。

DB 错误、提交结果未确认、身份碰撞、非法消息或其他非终态都返回错误并保留 PEL，不回补库存，也不凭 Redis 死信、缓存状态或次数阈值 ACK。Creator 命名锁释放失败即使发生在提交之后，也返回未知结果；重投再查持久事实。ACK 请求自身失败时，消息仍可重投并按同一 ID 查 DB。

`order:reservation:{id}` 的 CREATED/FAILED 写回仅为可选投影。脚本只更新仍属同一用户/活动的现存 Hash，不重建消失的预约，不改变库存；投影失败不能推翻 DB 事实或授权释放库存。订单查询先读取 DB 订单或持久拒绝，不能让缓存终态覆盖这些事实。

## PEL、公平性与重试

调度轮流处理新消息 `>`、本 consumer 的 PEL、`XAUTOCLAIM`，每次最多一条。本人的 pending cursor 按已读消息 ID 前移，到末尾才回到 `0`；claim cursor 从 `0-0` 开始并沿服务端返回值前移。这样不会每次都停在第一条毒消息，也不只扫描固定前 100 条。三步连续空闲后等待 100ms，有消息成功时继续处理。

`XAUTOCLAIM` 生产 `MinIdle=60s`；测试为定位接管窗口将其显式设为 `0`，不代表实测了 60 秒等待。claim 发生错误时保留最后有效 cursor，避免 go-redis 失败响应的空串使后续接管持续报非法 Stream ID；`NOGROUP` 后重新初始化 group。

单消息失败的本地退避为 2/4/8/16/32 秒，之后保持上限；内部指数饱和到 5 不代表停止重试或允许 ACK。本地调度表最多 1024 项，清理/进程重启可能丢失退避状态，持久恢复依据仍是 PEL。没有达到重试次数后自动丢弃、删除或转成 FAILED 的分支；完整持久 review 管理、受控重放和补偿处理归 GO-033。

## 真实依赖验证

环境为隔离 MySQL 3308 / Redis 6380、`project_full_stack_go`，凭据通过环境变量传入，不写入证据。命令在 `backend-go/` 执行：

```powershell
$env:GO_INTEGRATION='1'
go test -count=1 -v ./internal/orderstream
```

| 真实主组 | 已验证的不变式及故障边界 |
| --- | --- |
| DB 事实覆盖缓存 FAILED | 缓存预置 FAILED，真实消息仍创建订单；库存、订单、审计、事件各只产生一次，查询为 CREATED。 |
| 插入后、提交前取消 | 在真实订单 INSERT 后的事务屏障取消 worker context；事务回滚，订单 0、DB 库存 10、pending 1。新 consumer 接管后订单 1、库存 9、审计/事件各 1、pending 0。 |
| 提交后、ACK 前取消 | 屏障先证明订单已提交且 pending 1，再取消 context；新 consumer 重投保持同一订单，不再次扣库存。夹具直接改为 status=4、库存=10 后验证重投仍保留该终态；这不证明 GO-031 取消接口已实现。 |
| DB 不可用超过 5 次 | 使用独立、已关闭的 MySQL 连接池连续失败 8 轮，清空本地退避以快进；pending 保持 1，无订单或伪造拒绝。切回健康池的新 consumer 接管并完成。没有停止 MySQL 服务或模拟物理网络断库，也没有测真实退避总时长。 |
| 毒消息公平性 | 首条非法消息保留 pending；同一 consumer 后续 pending 能处理，新追加消息也能成单，毒消息不被无依据 ACK。 |
| 持久拒绝可 ACK | DB 库存为 0 时提交唯一拒绝事件并 ACK；补偿事件保持 PENDING，Redis 库存夹具值 7 不被本 worker 改变。 |
| claim 错误后恢复 | 对已被其他 consumer 领取的消息，用已关闭 Redis client 令 claim 失败，确认 cursor 未被清空；换回健康 client 后接管并完成。没有实际重启 Redis 服务。 |
| 取消的 Run 不创建 group | 启动前已取消的 context 让 Run 返回，私有 Stream 不被创建。 |

两种事务窗口使用 context 取消及新 worker 实例接管，未执行 OS kill、强杀进程或断电实验。上述测试通过真实 MySQL/Redis 的持久结果和 PEL 断言定位窗口，未把随机终止当成已命中窗口。

测试采用独立 `stream.go030.test.*`、自有随机精确 ID 和对应 DB/Redis 夹具，结束后按精确标识清理，不消费或删除共享 `stream.orders`，不删除数据卷，不重置业务序列。它验证 Stream→DB→ACK，不替代 HTTP/浏览器流程或准入并发实验；准入 20/200 用户短突发结果见 [准入报告](admission-report.md)，不能直接视为最终成单吞吐。

## 生命周期与尚未完成的验证

活动 outbox worker 与订单 Stream worker 使用应用 context。资源释放路径在 HTTP 排空/强制关闭处理后，取消并等待全部后台任务退出，最后关闭 MySQL/Redis 池。信号取消会同时传播给两个 worker，所以后台任务可在 HTTP 排空期间先停止；此时新产生的持久消息留待下次处理。若 Serve 异常而父 context 尚未取消，则由资源释放路径取消 worker。读取/claim 的 context 限 1 秒、Creator 业务调用 context 限 5 秒、投影限 500ms、ACK 限 1 秒；Creator 的命名锁释放另用独立 1 秒 context 收尾。后台任务不持有 Gin Context。

普通测试已覆盖多个 worker 全部退出后才关池；最终 [Go 验证输出](go-validation-final.txt) 确认模块校验、gofmt、vet 和全套普通测试 PASS。[启动/关闭日志](bootstrap-final.json) 复用工程验证脚本，实际运行本次冻结源码构建的 Go 二进制，revision 为 `f86a97f-go030-worktree`：正常启动与关闭、缺 MySQL 密码、缺 MySQL 端口、MySQL 连接拒绝、Redis 连接拒绝、API 端口占用共 6 场景均 PASS。正常场景三个健康端点均为 200，`CTRL_BREAK_EVENT` 后退出码 0，`forced_cleanup=false`，端口可重新绑定；失败场景明确退出，未记录明文密码。该 revision 标识冻结工作区构建，不冒充已提交的 GO-030c SHA；最终提交记录见总报告。

Linux race、远端 CI、MySQL 服务断开/恢复、Redis 正常与异常重启、OS 进程强杀、长时间积压、持续负载及其 RPS/P95/P99 均未由本子功能证明。Redis AOF everysec 不构成断电零丢失承诺；未知结果仍须保留并核对。GO-033 前不开放补偿/review 管理或库存重建，不同时启动 Java 与 Go 消费同一 group。
