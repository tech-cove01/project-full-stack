# CAP-01 实际 API 进程诊断准备

依据 [本票计划](../plan.md) 和 [STATUS](../../../../STATUS.md)，在实际 API 的连接池上增加显式启用的本机诊断。此项只交付观测能力和相关测试，不产出容量或优化成绩；本项没有运行 Docker、业务负载或付费模型。

## 启用和生命周期

`GO_DIAGNOSTICS_ADDR` 默认空，不创建诊断 listener。仅 `GO_ENV=development/test` 接受数字 loopback 地址与非零合法端口，例如交易 `127.0.0.1:8350`、读链路 `127.0.0.1:8351`；production、通配地址、非 loopback、DNS 名称或无效端口在配置阶段明确失败，错误不输出配置值。

启用后诊断使用独立 listener 和 mux，不挂到产品 API。启动失败使 API 返回错误并关闭已取得的资源；正常停止顺序是排空产品 HTTP、取消并等待 worker、在关闭预算内停止诊断、再释放 MySQL/Redis 池。诊断超过预算会强制关闭连接并保留错误，仍继续释放池。

实际代码：[配置](../../../../backend/internal/platform/config/config.go)、[应用组装](../../../../backend/internal/platform/app/app.go)、[诊断 server](../../../../backend/internal/platform/diagnostics/server.go)。

## 快照接口和口径

`GET http://127.0.0.1:8350/debug/capacity` 返回 JSON；读侧只替换端口。字段为：

| 对象 | 字段 |
| --- | --- |
| 顶层 | `at`（UTC）、`uptime_ms`（诊断 handler 建立后的时长）、`pid` |
| `runtime` | `goroutines`、`gomaxprocs`、`heap_alloc_bytes`、`heap_sys_bytes`、`heap_inuse_bytes`、`total_alloc_bytes`、`mallocs`、`frees`、`num_gc`、`pause_total_ns`、`last_gc_unix_ns`、`gc_cpu_fraction` |
| `sql` | `max_open_connections`、`open_connections`、`in_use`、`idle`、`wait_count`、`wait_duration_ns`、`max_idle_closed`、`max_idle_time_closed`、`max_lifetime_closed` |
| `redis` | `hits`、`misses`、`timeouts`、`wait_count`、`wait_duration_ns`、`unusable`、`total_connections`、`idle_connections`、`stale_connections` |

快照使用 `runtime.ReadMemStats`、实际 API 的 `sql.DB.Stats` 和 `redis.Client.PoolStats`，不执行额外 SQL 或 Redis 命令，不返回 DSN、地址、环境变量、查询文本或业务 payload。累计 GC、分配和等待字段须取时间序列差值；`gc_cpu_fraction` 是运行时累计口径，不能当成本次采样间隔的进程 CPU。外部 CPU/RSS 由资源采样器记录。

独立 listener 提供标准 pprof 的 `heap/allocs/goroutine/block/mutex/threadcreate/profile`，只接受 GET，CPU/delta `seconds` 限制为 1–30 秒；不开放 cmdline、symbol 或 trace，拒绝通过 `gc` 参数强制回收。block/mutex 的采样率未自动开启，因此对应 profile 可能为空。pprof 和 MemStats/JSON 本身有开销，容量基线应保留采样条件并做计划中有/无观察短对照，不能把诊断视为零开销。

## 已执行检查

实现时执行以下命令，均退出 0：

```powershell
# backend/；本项仅相关检查，不连接真实依赖
gofmt -w internal/platform/config/config.go internal/platform/config/config_test.go internal/platform/app/app.go internal/platform/diagnostics/server.go internal/platform/diagnostics/server_test.go
gofmt -w internal/platform/diagnostics/server.go internal/platform/diagnostics/server_test.go internal/platform/app/diagnostics_test.go
go test -count=1 ./internal/platform/config ./internal/platform/diagnostics ./internal/platform/app
go vet ./internal/platform/config ./internal/platform/diagnostics ./internal/platform/app
go build ./cmd/api
```

测试覆盖默认关闭、生产和非 loopback 拒绝、实际池计数回调和字段白名单、GET/pprof 时长及路由护栏、慢诊断请求超过关闭预算后取消、listener 释放、幂等关闭，以及诊断关闭失败时依然按顺序关闭依赖池。测试使用本机短生命周期 listener 和计数回调，不以 mock 证明真实数据库容量。

root 随后执行的完整 Go 普通测试已通过，见 [test.txt](test.txt)；全量 build 当时遇到并行 `capacity-trade` 草稿编译失败，原始 [build.txt](build.txt) 保留，不能把那次全量 build 写成通过。`cmd/api` 的单独构建通过；完整构建需待测量器文件收敛后按最终源码另行检查。Windows 普通检查不代替 Linux race 或真实容量实跑。
