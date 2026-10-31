# CAP-08 生产观测差量独立审查

审查日期：2026-10-31。审查者：独立源码审查 agent；与主核验者的真实服务复跑、证据核验分工独立。

## 结论与范围

在 `1de173517a54c3be46748779437b592f7bcdbc58..2c1ef04b7b1d1ae10d98767e18e6c12ff697166c` 的本次 Go 生产观测差量中，未发现可复现、需要修复的 P1/P2 问题。审查涵盖 `taildiag` 的 active 时钟及 recorder、`communitycards` 的请求/共享任务埋点、app 注入、social Feed 埋点，以及相应新增测试。没有修改生产代码，没有启动服务、受控场景或压力负载，也没有提交。

该结论只评价上述代码差量和本次独立执行的普通测试。**不能据此将 CAP-08 容量验收判为通过**。交付材料中的应用版本为 `46ddefc2a16d41e4bb01a3d20d08bdfcaeff465f`；其 CI、真实场景、正式负载与原始证据由主核验者另外核对，不算本审查者独立执行。

## 源码核对

- **默认关闭路径。** [app.go](../../../../backend/internal/platform/app/app.go#L55) 只在原有 `FeedDiagnostics` 开关开启时创建 recorder、安装 storage instrumentation；本次只在监听服务前调用 `cards.WithDiagnostics`。关闭时注入 nil。[recorder.go](../../../../backend/internal/platform/taildiag/recorder.go#L150) 的 `Start`/`StartShared` 及 `Begin` 均有 nil/no-trace 快路径，不创建 trace 或 active 时钟。原有查询、缓存、超时和数据返回语义未被改写。此处不是对整个业务路径零分配的承诺。
- **生命周期仍由应用持有。** [cards.go](../../../../backend/internal/communitycards/cards.go#L271) 保留原先的 `cache.life + loadTimeout` 创建共享任务；[runFlight](../../../../backend/internal/communitycards/cards.go#L276) 在这个已有 context 上新建 `cards.shared` trace，没有换成首个请求的 context，也没有放宽 2s 截止时间或并发预算。观测结束先于该函数原有的 `cancel`、`operations.Done` 执行，成功任务不会因自己的收尾 cancel 被误标取消。
- **请求等待与共享任务分开。** [GetMany](../../../../backend/internal/communitycards/cards.go#L178) 记录请求等待的返回错误；共享工作独立记录 `shared_tasks` 和 `cards.shared` scope。[recorder.go](../../../../backend/internal/platform/taildiag/recorder.go#L176) 将共享完成计数与请求完成计数分开。两条 trace 的耗时会重叠，不应相加，也不能用 shared task 数代替 HTTP 请求数。`StartShared` 的 context 独立性由上述调用点保障，不是对任意传入 context 自动剥离所有值。
- **active 登记有界且可解释。** [active.go](../../../../backend/internal/platform/taildiag/active.go#L15) 每阶段保留 1024 个固定时钟槽；超出后仍计 active，同时显式增加 `untracked_active`、overflow。只要当前存在未登记时钟的活动项，`oldest_complete=false`，不会把缺测表示成完整的 0ms。时钟使用相对 recorder 起点的单调时间；`Span.End` 的 `sync.Once` 防止重复释放槽位和重复累计。
- **并发与锁。** 每阶段 mutex 只保护固定数组、active 与 overflow；不跨 Redis/SQL I/O、trace 序列化或业务等待持锁。[Snapshot](../../../../backend/internal/platform/taildiag/recorder.go#L277) 逐阶段采样；trace ring 与每条 trace 使用独立锁，未见相反嵌套锁序。该采样不是各阶段、计数器和连接池的原子总快照，已有 `missing_measurements` 明示此限制。
- **业务分支保持一致。** 新增的 caller Redis、slot queue、recheck、cold SQL、fill 和 Feed fallback span 包住既有操作；没有增加重试、改变 Lua/SQL、缓存版本或回退条件。Redis fill 失败仍沿用既有降级语义，只新增失败观测。`GetMany` 的命名返回值使 defer 能捕获实际返回错误；`runFlight` 中捕获的 `err` 为实际 loader 返回值。
- **隐私与入口。** 新增 stage、scope 均为固定白名单；active 时钟只保存数字，不保存 identity、缓存 key、SQL、命令参数或错误文本。慢 trace 仍为 32 条、每条最多 256 spans；请求与共享任务共用此 ring，可能互相覆盖，不能当完整事件日志。没有新增公开 HTTP 路由；原有配置仍要求诊断使用 development/test 下的数字 loopback 地址，Feed diagnostics 在 production 被拒绝。

## 独立执行的验证

在该隔离 worktree 的 `backend` 中执行：

```powershell
$env:GO_INTEGRATION='0'
go test -count=1 -json ./internal/platform/taildiag ./internal/communitycards
```

命令退出码 0，两个包均通过。覆盖了本次新增的以下普通测试及两包既有普通回归：

- `TestOldestActiveMovesWhenOldestOperationEnds`：最老项结束后年龄切换到下一活动项。
- `TestActiveClockBudgetMarksMissingAgesInsteadOfZero`：1025 个 active 的固定槽上限、overflow、重复 End 与清零。
- `TestSharedObservationHasIndependentScopeAndLifetime`：HTTP/共享 scope、计数与取消相互分离，context 中的任意私有字符串不输出。
- `TestZeroRecorderCanObserveBeforeNew`：零值 recorder 的初始化路径。
- `TestObservationsSeparateSharedSQLAndSlotQueueAfterCallerCancellation`：首请求取消时共享 SQL 与另一个 slot waiter 仍在途，释放后收尾清零且错误归属正确。

真实 MySQL/Redis 集成因 `GO_INTEGRATION=0` 跳过；受控采证还要求独立的 `CAP08_CONTROL_EVIDENCE`，本审查没有启用。未执行 Linux `-race`、浏览器或性能验证，未测默认关闭/开启的性能差值。

## 风险与验收边界

开启诊断会引入固定时钟表、每活动 trace/span 的观测开销，以及阶段采样时的短锁和数组扫描；本审查不将其描述为零开销。`WithDiagnostics` 是启动前配置方法，app 按该约定调用；它不是运行中并发切换 recorder 的 API。

正式第二组在交付记录中已有 `dropped=57`、`unknown=200`，该组失败与未定位的长尾必须保留；不能用普通测试通过、第一组通过或第二组较低的分位数覆盖该结果。本审查没有证明宿主机/虚拟机暂停、数据库重置、publication 或任何其他根因，也没有给后续新增负载、诊断轮次或功能扩展授权。
