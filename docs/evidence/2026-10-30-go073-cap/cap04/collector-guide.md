# CAP-04 一次性瓶颈采证

[capacity-evidence.py](../../../../scripts/capacity-evidence.py) 只读采集本票专用 API/MySQL/Redis 的计数和标准 profile，不生成业务负载、不停止 API、不修改数据/仪器/配置。它不是新的监控平台，也不负责判定或停止压测档位；阶段监督和资源预算由 `capacity-stage.py`、`capacity-resources.py` 及具体负载 harness 处理。

## 使用

仅接受专用 trade（3450/6550/8250/8350）或 read（3451/6551/8251/8351）项目；先校验服务标签、loopback 端口和 immutable container ID，再校验诊断返回的 API PID。必须从操作者当前环境显式提供 `CAP_MYSQL_OBSERVER_USER`、`CAP_MYSQL_OBSERVER_PASSWORD`。本轮探针使用隔离 MySQL 的 root 只读查询权限；脚本通过 `docker exec -e MYSQL_PWD` 传递环境变量名，不在命令参数或证据中写入密码值，不读取或落盘完整容器环境。

```powershell
# 仓库根目录；先核实实际 API PID，并在进程环境设置观察凭据。
# 不应复制旧 PID，也不应在命令行写入密码值。
python scripts/capacity-evidence.py --project project-full-stack-cap-trade --api-pid 68744 --label before-boundary --output docs/evidence/2026-10-30-go073-cap/cap04/before-boundary

# 首个受限档如需 CPU/栈证据，显式添加 --profile；会采样 CPU 10 秒。
python scripts/capacity-evidence.py --project project-full-stack-cap-trade --api-pid 68744 --label limited-profile --output docs/evidence/2026-10-30-go073-cap/cap04/limited-profile --profile
```

以上 PID 仅对应本轮只读探针时的实际 API，不是稳定身份或新的默认值。每次输出目录必须不存在，先前失败和成功记录均保留。读环境只替换 `--project` 为 `project-full-stack-cap-read` 和其实际 PID。不与正式稳态测量无计划地叠加重型 profile；带 profile 的区间须单独标明观测开销，不能将其延迟无条件和无 profile 基线混算。

## 输出与解释

- `diagnostics-before/after.json`：实际进程的 runtime、SQL 和 Redis 池统计；起止时刻与各部分耗时在 manifest 中保存。
- `mysql-digests.json`：本业务 schema 按累计等待总时间排序的前 100 个 normalized digest，literal 已由 MySQL 替换为占位符；保存 count、timer/lock sums、rows 与错误等。不会采集 SQL_TEXT、processlist INFO、lock data values 或 INNODB STATUS。
- `mysql-locks/lock-waits.json`：当前锁类别、状态与事务/线程关系，未保存锁数据值。空快照只能说明采集时未见相应记录，不能证明整个档位没有争用。
- `mysql-consumers/instruments/status/settings.json`：只读检查仪器可用性、相关累计计数与数据库参数；不启用仪器、不清零 digest。
- `redis-*.json`：server/memory/cpu/stats/commandstats/clients/persistence/keyspace 的 INFO 计数，没有 MONITOR、SLOWLOG、键值或命令参数。
- `--profile` 才生成 `cpu-10s.pprof` 与 `goroutine.pprof`；不采 heap 内容、cmdline 或 trace。可在匹配二进制条件下用标准 `go tool pprof -top` 查看函数热点。未自动启用 block/mutex profiling 采样率。

至少在某档开始和结束各采一次，按同 digest 的 count 和累计 sum 做差，并先检查计数复位、仪器状态、digest 丢失和 top-100 截断；max/min/avg 是累计窗口统计，不能直接相减推导该档 P95/P99。digest 也可能含后台 worker 和测量器访问，不能全归给 HTTP。MySQL TIMER 值为皮秒口径，SQL/Redis 池 wait_duration 为纳秒；这些与 HTTP 延迟是不同观测。

脚本每个部分独立保留 available/错误类别和耗时，权限、命令或资料缺失时最终退出 1。原始驱动诊断被抑制，避免带出 SQL 或凭据；不能因其它部分成功而把缺失部分当作零等待、零锁或完整瓶颈证据。它不会根据自身失败去操作 API。

## 已执行的只读探针

2026-10-30，实际 API PID 68744、专用 trade 容器归属和端口校验通过；运行不带 `--profile` 的一次采集至 [readonly-probe-1](readonly-probe-1/manifest.json)，命令退出 0。16 个采集部分全部可用，读到 38 行 normalized digest，锁与锁等待各 0 行；各部分耗时合计约 2846.43 ms。这是空闲/准备阶段的方法探针，不是受限档、容量或“无锁瓶颈”结论，CPU/goroutine profile 分支本次未运行。

18 个 JSON 可解析，有限 Bearer/Provider key/password-value 模式扫描无命中，见 [validation.json](readonly-probe-1/validation.json)；这不证明不存在所有秘密格式。Python 源码语法及 whitespace 检查通过。初次用 PowerShell 内联执行有限隐私扫描时引号解析失败，未执行扫描；随后改用 literal here-string 完成上述扫描，未重跑或覆盖采集结果。
