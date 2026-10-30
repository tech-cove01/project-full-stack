# GO-073-CAP / CAP-03 HTTP Feed 容量实验计划

计划依据 STATUS 的 GO-073-CAP，尚未启动环境或运行负载，没有容量结果。这里固定脚本接口和预先判据，实际执行记录需另存新目录；不覆盖旧 GO-073 低速报告。

## 测量对象和固定夹具

选择受保护的 `GET /feed/notes?limit=20`，实际包含身份检查、关注事实、笔记/门店批量 SQL、本人点赞批量装配以及公共计数 GetMany 的 L1/Redis/数据库路径。公开 `GET /notes/:id` 的 Detail 本身不使用公共计数缓存，因此不拿它代表缓存链路。

专用项目 `project-full-stack-cap-read`，MySQL 3451、Redis 6551、API 8251；启动前由根 agent 核对标签、端口和卷。API 设置 Kafka/ES/AI 关闭，Feed 响应必须 `source=database`，不把该结果推广成 Kafka 投影容量。资源固定为根计划：API GOMAXPROCS=4/GOMEMLIMIT=1GiB；MySQL CPU 4/内存 2GiB，Redis CPU 2/内存 512MiB；宿主其他服务及 Docker VM 上限由根记录。

夹具在计时外建立：32 作者、32 读者、每作者 200 篇（共 6400 篇）固定 256 字节 ASCII 正文、单门店 1、相同发布时间、发布版本 1、零点赞/评论。按作者轮转插入笔记，ID 与发布时间构成实际排序；每读者仅关注对应一位作者。作者/读者为新合成数据；读者走真实开发验证码/登录流程。直接 SQL 夹具不建立社区事件，不用于宣称社区写容量。保存表索引与每个读者的预期首页 20 个字符串 ID。

两种独立工作负载使用同一数据：

- `hot`：所有请求使用读者 0 的首页，固定 20 个计数 key。
- `multi`：32 个读者均匀轮转各自首页，共 640 个计数 key。

两者只说明不同 key/用户分布下的 HTTP 结果，不能据此推出某项缓存优化的因果提升百分比。L1/Redis TTL 正常运行；每档先预热，正常 TTL 到期也属于稳态，不强行把所有请求叫 L1 命中。

## 预固定负载和预算

建议两种模式分别从 25→50→100→200→400 req/s 探索，每档稳定 30–60 秒；未触界且资源有余量才继续。临近拐点缩小步长，最高达标档保持 180–300 秒并至少重跑一次。每次只运行一个专用压测项目，不能与交易量化并行。

每档预先判据：实际发起/名义计划 ≥99%，dropped=0，系统错误率 <1%，全请求 P95≤200ms/P99≤500ms，页面内容错误为 0。k6 在稳定期内对延迟、错误和 dropped 设置 10 秒后可触发的早停；内容错 2 秒后早停。共享宿主或负载器先达到上限时只报告共同部署观测边界。根资源观察器可以按既定 CPU/内存预算写 `CAP_STOP_FILE`，脚本发现后仅终止本次自己创建的 k6 子进程，保留停止原因和实际命令时长。

到达率为 constant-arrival-rate，不将 VU 数称为 RPS。VU 默认预分配 64、上限 512，可在每轮前固定调整；实际 VU、dropped、数据传输量、负载器 CPU/RSS 必须随结果记录。warm 与 stable 是两个连续 k6 进程，默认预热 10 秒、25 req/s（目标低于 25 时与目标相同）；稳定计数器快照在预热完成后获取。会记录稳定进程墙钟时间，k6 名义窗口与端点迭代单独说明。

## 低扰动证据

容量脚本不逐请求打印，不把 ID、URL 或用户 ID 做高基数 tag。每个响应校验 20 条 ID/作者/门店及零计数、本人的未点赞状态；有界 counter/trend 记录所有请求、成功请求、系统/HTTP/内容错误及全请求/成功延迟分布，最多每个 VU 输出一条不含凭据/body 的异常。没有逐响应 body 归档，完整分类不能离线重新解析 body；可复跑并检查验证逻辑。

稳定阶段前后读取独立 MySQL `performance_schema.events_statements_summary_by_digest` 的单调摘要，不重置全局计数；记录 statements_digest 消费者和 digest_lost，若发生计数重置、摘要丢失/消失则不能声称精确 DB 次数。查询文本可区分 Feed 本体、关系/本人点赞、公共计数 IN 及后台查询；同一实例的后台扫描也会出现在原始增量中，需要逐类报告，不能把总数全归因接口。观察账号需要显式只读 performance_schema 权限；凭据仅从环境传入 MySQL 子进程，不写输出。

Redis 使用 INFO commandstats 前后差，保留 hgetall/expire（鉴权）、mget/get/set（缓存）、xreadgroup 等背景命令的分类。INFO 采样本身、连接和后台命令有开销；不以总命令差直接声称每个请求的缓存命中率。根诊断观察器使用 8351 的 `GET /debug/capacity` 另取实际 API pool/runtime 时间序列，不把观察请求算入业务负载。

## 执行接口（待根 agent 准备隔离依赖）

```powershell
# 与同一固定构建/服务一致；其余 MYSQL_*、REDIS_*、API_BASE 显式配置为上述专用端口。
$env:CAP_SOURCE_SHA='<完整40位构建源码SHA>'
$env:CAP_READ_EXPECTED_REVISION='<API health的精确version>'
$env:CAP_READ_FIXTURE_DIR='<新夹具证据目录>'
node scripts/go073-cap-read.mjs prepare

# 夹具可复用多档，只读负载不写业务；凭据文件位于OS临时目录，manifest仅含其路径。
$env:CAP_READ_FIXTURE='<prepare生成的fixture.json>'
$env:CAP_READ_OBSERVER_USER='<有performance_schema读取权限的本轮账号>'
$env:CAP_READ_OBSERVER_PASSWORD='<仅进程环境加载>'
$env:CAP_READ_MODE='hot' # 或multi，分别记录
$env:CAP_READ_RATE='25'
$env:CAP_READ_WARM_SECONDS='10'
$env:CAP_READ_WARM_RATE='25'
$env:CAP_READ_SECONDS='60'
$env:CAP_READ_PRE_VUS='64'
$env:CAP_READ_MAX_VUS='512'
$env:CAP_READ_EVIDENCE='<每档不存在的新目录>'
$env:CAP_STOP_FILE='<本档独占、不存在的资源停止文件>'
node scripts/go073-cap-read.mjs run

# 全部档位结束且API仍运行时，注销本轮32个真实会话并删除精确临时文件/空目录。
node scripts/go073-cap-read.mjs finish
```

`run` 输出 `warm-summary.json`、`stable-summary.json`、两个有界控制台文件、两份子进程 PID 文件、P_S/Redis 原快照与 `access-delta.json`、`result.json`；`process.json` 始终更新为当前 k6 子进程，含 `helper_pid/k6_pid/k6_started_unix_ms/phase`，供根资源观察器追踪精确 PID。外部资源停止可能使 k6 来不及生成 summary；此时保留实际退出/停止记录并记失败，不补零造通过。源码文件 SHA256 随每轮结果保存。SQL 夹具、卷和原始数据保留，finish 只注销自己的会话并删除自己记录的凭据文件，不清库、不 flush、不删除卷。

准备失败时 `fixture.json` 保留唯一前缀和已写入 ID；不要手工删未知记录。Windows 的文件 mode 不能证明 ACL，强杀也可能使临时文件残留，恢复只按该 manifest 指向的精确文件检查。根 agent 执行和独立审阅前，本计划及脚本自身不构成 CAP-03 容量交付。

## 实施前静态检查（2026-10-30）

以下检查已执行并退出 0，仅验证语法和 k6 场景配置：

```powershell
node --check scripts/go073-cap-read.mjs
node --check scripts/load/go073-cap-read.js
k6 inspect --include-system-env-vars scripts/load/go073-cap-read.js
```

`k6 inspect` 使用操作系统临时目录中的合成输入 JSON（一个假读者、字符串 `static-not-a-credential`，不是登录凭据），进程环境为 `CAP_READ_INPUT=<该临时文件>`、`CAP_READ_RATE=25`、`CAP_READ_SECONDS=30`、`CAP_READ_MODE=hot`、`CAP_READ_PRE_VUS=64`、`CAP_READ_MAX_VUS=512`。实际解析结果为 `constant-arrival-rate`、25/s、30s、64 预分配 VU、512 最大 VU，九项阈值配置可解析；该次未调用 `k6 run` 或业务接口。检查后仅删除这次创建的精确临时文件和空目录。

本阶段没有执行 `prepare/run/finish`、SQL/Redis 观测或任何真实负载，也没有创建/停止容器；因此尚无 HTTP、访问次数、资源或容量成绩。三个新文件已完成源码自审并交根 agent 审阅，真实短探针由根 agent 在新固定构建与专用环境准备后执行；发现运行问题需保留原始失败并追加修复。
