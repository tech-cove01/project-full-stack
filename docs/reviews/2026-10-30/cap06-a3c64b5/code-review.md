# CAP-06 独立源码审查与 CI 覆盖修复复核

日期：2026-10-30（Asia/Shanghai）。审查者 `cap_code_review`，不是本批实现者。目标交付 `a3c64b5361069ae4491b9de76654b5432ac9f9a8`，生产差量为 `c89da96..a3c64b5`；先读取目标仓库 STATUS、AGENTS，并检查 HEAD、已提交差量和工作区。生产代码首次审查时主工作区干净。本报告新增在主核验者的隔离 worktree，未修改应用、原始证据、main 或 STATUS，未暂存或提交。

**结论：当前应用调用链未发现新增 P1/P2 阻断缺陷、业务语义改写或诊断隐私泄漏。原 CI 存在新真实包装测试未纳入 Linux-race 的覆盖缺口；主核验者的四行专用步骤修复范围准确、最小。** 本子任务只自行运行下述七个无网络普通测试；没有启动依赖、API、浏览器或负载，没有执行本地 race、真实依赖测试或联网确认远端 CI。主核验者的新卷实测及远端运行分别记在总核验材料，不能当成本子任务自行执行。

## 应用差量与业务边界

| 审查项 | 独立源码结论 |
| --- | --- |
| 开关与启动 | [config.go](../../../../backend/internal/platform/config/config.go) 的 FeedDiagnostics 默认 false，只接受显式 true/false；启用要求既有数字 loopback DiagnosticsAddr 且非 production。[app.go](../../../../backend/internal/platform/app/app.go) 在开始接收请求前安装观察器，沿用原 ORM、SQL 池和 Redis 客户端，不增加连接池或业务查询。 |
| 鉴权与 HTTP 区间 | [social/http.go](../../../../backend/internal/social/http.go) 在 Feed 鉴权前启动观察，service 复用相同 context trace，不重复计请求。鉴权失败仍按原响应退出，诊断不进入产品响应。feed.http 围住处理器及 JSON 写出，不包括客户端网络 RTT 或 handler 前等待。 |
| Feed 读取与返回 | [feed.go](../../../../backend/internal/social/feed.go) 只增加有界计时和返回错误观察；原 5 秒业务期限、参数验证、RR 事实范围、组合游标和返回值保持。快照归还连接后补公共计数的顺序没有改变，未把公共计数或个人状态移入新缓存。CAP-07 尚未实施。 |
| 原计数缓存 | [counts.go](../../../../backend/internal/communitycache/counts.go) 沿原边界观察 publication 排队/持有、Redis 读写删除、GetMany 和冷 SQL；未移除门、改变 generation、TTL、回源策略或失效次序。观察结束会有额外原子/锁开销，该开销属于诊断轮范围，不能据观测代码声称性能已改善。 |
| SQL 包装 | [storage.go](../../../../backend/internal/platform/taildiag/storage.go) 在已有池上转发 context、SQL 参数、返回对象和错误；观察 BEGIN、显式 commit/rollback 调用区间、部分 SQL 调用及 GORM statement 总区间。对照锁定 GORM v1.31.1 的 Transaction、Connection、DB 和相关接口实现，当前 Feed 与 Creator 路径未发现新增接口失配或连接持有顺序变化。未声称所有任意第三方 GORM 用法均已穷尽验证。 |
| Redis 包装 | Hook 转发原 command/pipeline 与 context，不读取命令参数或 key，不增加 Redis 指令。只有已启动 Feed trace 的 context 才记录，其他路由及后台 context 不被虚构为 Feed 阶段。默认关闭时不安装存储包装。 |
| 取消、锁与关闭 | Span 不改变原 context 传播或错误分类，观察器没有新增后台工作任务。Recorder 的请求/聚合锁及快照复制未见新增锁序环；publication 本身仍是原门。应用诊断及池关闭顺序沿用先等待后台任务、关闭诊断再释放池。 |

## 资源上界、隐私与测量限制

[recorder.go](../../../../backend/internal/platform/taildiag/recorder.go) 采用固定二十个 Stage、最多 32 条完成慢 trace、每条最多 256 个 spans；超出设 Truncated，聚合继续累计。每个 stage 使用原子计数，trace/ring 有独立互斥；Snapshot 复制内部 slices，未暴露可由读取者修改的引用。

API 只保存固定名称、数值、UTC 时间、批大小及错误/取消布尔值。源码没有导出 token、身份、缓存 key、SQL 文本、命令参数、正文、URL 或错误文本；observedLogger 自身不求值 SQL 文本回调，当前应用的底层 logger 仍为 Silent。环形缓冲与低基数满足本次限定诊断用途，不是新观测平台。

以下缺失与口径已在 Snapshot.missing_measurements 和 [组件 README](../../../../backend/internal/platform/taildiag/README.md) 中说明，审查不能把它们改写成已量出的分段：

- SQL Begin 包含借连接和服务端往返；statement 包含适用的执行、传输和扫描；call_until_rows 早于完整 row 消费结束。没有独立量出每请求纯 SQL 池等待或服务端执行时间。
- Redis command 包含借池、传输与往返，未独立拆分每请求纯池等待或服务端执行；全局池计数不是该请求的精确归因。
- 嵌套阶段不能相加为无重叠总时间，聚合和池快照并非原子快照。慢 trace 只在完成后发布，触发抓栈可能已在恢复阶段，不能伪称排队峰值栈。
- canceled 指外层 observed context，内部 deadline 失败由 stage error 体现；没有导出错误文本来补推原因。

## 本子任务独立执行的普通测试

在目标交付的 backend 目录执行，显式关闭真实依赖入口：

```powershell
$env:GO_INTEGRATION='0'
go test -count=1 -run '^Test(DisabledRecorder|BoundedSlowRing|ActiveCanceled|ConcurrentAggregates|SQLWrapper|StorageHooks|FeedDiagnostics)' ./internal/platform/taildiag ./internal/platform/config
```

实际输出，退出码 0：

```text
ok  github.com/cityloop/fullstack-go/internal/platform/taildiag  0.356s
ok  github.com/cityloop/fullstack-go/internal/platform/config    0.234s
```

该正则选中七个普通测试：disabled no-op/Stage 白名单、有界环与截断/复制/隐私、取消与嵌套 scope、并发计数、SQL 无网络 driver 包装、Redis hook/日志回调及取消、Feed 诊断显式开关。没有命中 TestIntegrationObservedStorageTransactions，故这些结果不是其真实依赖通过证据，也不是 race 或容量成绩。

## 原 CI 缺口及最小修复复核

原 [ci.yml](../../../../.github/workflows/ci.yml) 在 schema 初始化前运行 `go test -race -count=1 ./...`，此处没有 GO_INTEGRATION=1。[storage_integration_test.go](../../../../backend/internal/platform/taildiag/storage_integration_test.go) 的 TestIntegrationObservedStorageTransactions 明确在该变量不为 1 时 Skip；后续设置 GO_INTEGRATION=1 的显式包列表又没有 internal/platform/taildiag。因此原“四作业通过”能支持既有普通 race 及所列真实包，**不能证明该新增 SQL/Redis 包装真实测试已在 Linux-race 执行**。

主核验者在迁移步骤之后、现有业务包真实测试之前加入唯一专用步骤：

```yaml
- name: Feed diagnostic storage wrappers against real MySQL and Redis
  run: go test -race -count=1 -v ./internal/platform/taildiag
  env:
    GO_INTEGRATION: '1'
```

该位置继承 backend-go 的 backend 工作目录、MySQL/Redis 服务及 job 级环境。迁移步骤的 MYSQL_PORT=3306 只作用于迁移步骤，新测试仍使用 job 配置的宿主映射端口3308；它不依赖迁移工具的临时 MYSQL_BIN。test 直接调用 Instrument，故不需要启用产品 GO_FEED_DIAGNOSTICS 或启动 API。只增加四行步骤，无新增服务、依赖、权限或产品行为，能覆盖此前漏掉的真实 commit/rollback、池归还、Redis 命令/取消和原 SQL 池身份断言。

**修复审查通过，但源码审查不等于新远端 CI 已运行通过。** 本报告不把预期工作流效果倒填为已执行，也不把主核验者的新卷普通真实结果称作 Linux race。

## 验收边界

本次属于 CAP-06A 观测及 CAP-06B 有限诊断交付。原秒级长尾没有复现，原因仍未确认；三轮带额外诊断的通过结果不能替代 CAP-06C 的修复因果证据或 CAP-06D 正式持有组，CAP-07 前置仍未满足。不得据本报告宣布原事故已修、提高容量或开始公共卡片缓存实现。
