# GO-073 缓存冷热对照（实现者实验）

- 功能基线：`bf6b765a9b15a7f66b994ac25e17d07135abe196`；仅新增 [实验代码](../../../backend/internal/communitycache/go073_measure_test.go)，生产代码未修改。本记录为实现者实验，尚待独立核验。
- 2026-10-29 07:32 UTC，Windows 11 Pro 10.0.26300，Intel i9-13900H（14 核 20 线程），内存约 33.96 GB，Go 1.27.0/windows/amd64；Docker 专用 Compose 项目 `project-full-stack-go073-cache`，MySQL 8.4.13/Redis 7.4-alpine，TCP 3400/6500，单独的新卷。MySQL 连接池最多 20、空闲 10；Redis 池 10；应用默认 L1 TTL 2 秒、上限 4096。迁移 003–012 首次成功。
- 实验期间 CRM 原有容器在线，另有 GO-073 交易专用 MySQL/Redis 已完成健康检查并处于未造数状态；宿主机共享资源，没有排除后台噪声，不能据此外推生产容量。该实验耗时 7.41 秒，不是持续 HTTP 压测。
- 同一个进程调用真实 `communitycache.Cache`、真实 MySQL/Redis，32 条固定合成公开笔记，值相同。SQL 查询次数由 GORM `Trace` 捕获实际发出的 `tb_blog` 查询，Redis 命令由客户端 hook 统计；`run1.txt` 包含每个样本延迟、数量和原始输出。下表为最近秩百分位，单次试验，没有置信区间。

| 场景 | 样本数 | DB 查询数 | P50 / P95 / P99（毫秒） |
| --- | ---: | ---: | --- |
| 同一 key 逐次冷读，100 次 | 100 | 100 | 6.0043 / 10.8672 / 48.2813 |
| 同一 key L1 热读，100 次 | 100 | 0 | 均记录为 0，低于本次计时分辨率 |
| 同 key 冷突发，20 轮、每轮 32 并发 | 640 次调用 | 20 | 7.7105 / 14.3885 / 29.1218 |
| 32 key 冷批量读取，20 轮 | 20 批 | 20 | 30.6799 / 58.1357 / 62.7764 |
| 同 32 key 冷逐条读取，20 轮 | 20 批 | 640 | 175.1492 / 201.4174 / 219.5618 |

每轮冷读前主动 `Invalidate`，仅将读取时间纳入样本，报告 Redis 命令总数包含这些重置。热读在上次冷读之后直接进行，100 次共 0 次 DB 查询、0 次 Redis 命令，反映 L1 命中；记录为 0 不能理解为实际零延迟或用于计算速度倍数。突发每轮从清空的相同 key 开始并用 barrier 同时放行，20 轮共 20 次实际 DB 查询；没有另做禁用 singleflight 的对照。批量和逐条均使用同一组数据、相同 20 轮冷状态，但批量缓存方法与逐条方法不同，结论限于本次函数级读取。另以 4100 个内存条目直接调用 L1 写入检查边界，观察最大 4096、结束 4；这是结构性检查，不是 4100 条真实业务读取的性能结果。

复跑需在专用独立库及端口配置 `MYSQL_HOST=127.0.0.1 MYSQL_PORT=3400 MYSQL_DATABASE=project_full_stack_go MYSQL_USER=fullstack`、`REDIS_HOST=127.0.0.1 REDIS_PORT=6500`、适用的本地开发密码，再在 `backend/` 执行 `GO073_CACHE_MEASURE=1 go test -count=1 -run '^TestGO073CacheMeasurement$' -v ./internal/communitycache`。测试默认跳过，显式检查地址与数据库，且只清理成功插入的固定夹具 ID；实际操作前仍应确认隔离实例归属。

初次运行的输出目录错误被保留在 [initial-attempt.txt](initial-attempt.txt)，该次没有可用测量结果。运行完成后 SQL 按范围和标题复核夹具为 0，Redis 精确前缀扫描无匹配；专用 MySQL/Redis 已正常停止，数据卷保留，其他容器未操作。一次容器内 MySQL 命令行转义失败和本机缺少 redis-cli 后，分别改用本机 mysql 与容器内 redis-cli 完成只读复核。
