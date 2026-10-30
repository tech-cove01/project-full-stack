# CAP-01 实机观测与准备失败记录

API 从独立干净 worktree `32ae24331d7de98d8df004cf854708ec3c59eec4` 构建；[构建信息](api-build.txt)记录 `vcs.modified=false`，[二进制 hash](api-sha256.json)、[运行身份](runtime.json)、[初始健康与进程池计数](initial-health.json)保留。诊断 PID 与实际 API PID 均为 68744，GOMAXPROCS=4，SQL 最大连接20。[环境记录](environment.json)保存本机/Docker资源、两容器限制及14个其它在线服务名，没有关闭其它服务。

## 不发业务请求的观测缺失探针

运行 `scripts/capacity-resources.py`，显式指定本票 API PID/8350诊断、两自有容器及新输出/信号路径，采样间隔2秒、上限15秒；故意令受控生成器PID文件不存在。该探针没有启动k6或发送业务请求，用于验证“缺少观察不能算达标”的停止路径。

[原始资源采样](resource-blind-probe/resources.ndjson)有3次样本，均读到实际API PID，Docker资源可读；唯一缺失项是生成器PID文件。第三次按预固定规则发出 [停止信号](resource-blind-probe/stop.json)，原因为连续三次观测不完整。采样器只写信号，没有结束任何进程；完整监督器接通需另用短业务探针验证。

三次采样墙钟耗时约2164/1997/2002ms，主要包含Docker stats子进程等候；正式默认间隔5秒。墙钟耗时不是采样CPU占用，完整时间序列另记采样器自身CPU/RSS。探针时主机内存使用约79.9–80.2%，不能假定宿主独占或压测机有无限余量。修改采样器增加了受控PID/创建时间与 `is_running()` 检查、不可变容器ID、有限采样间隔、不同信号路径，以及连续缺观测停止；不枚举/终止其它同名k6进程。

## 用户池准备的运行环境失败

第一次与第二次 `capacity-trade prepare` 均在容器归属检查阶段返回 `dedicated container inspection failed`；此时未创建私有池、用户或活动。它们的输出目录 `prepare-25000`、`prepare-25000b` 为空，不当成容量样本。

随后加入安全错误分类，以新目录重试，原始诊断在 [c](prepare-25000c/docker-inspect-diagnostic.json) 和 [d](prepare-25000d/docker-inspect-diagnostic.json)。d确认Docker可执行文件定位正确，子进程退出1，named pipe `dockerDesktopLinuxEngine` 访问被拒。相同提权shell直接Docker和Python/Node的只读子进程inspect均成功，API健康UP。这是特定Go子进程的Docker访问失败，不能归因成数据库/API过载。

备用方案保留默认实时检查，仅在已识别的 named-pipe 权限拒绝且显式启用 launcher 时，由外层Python每次实时读取本票两容器归属/运行/端口/不可变ID，给Go工具一份15秒内有效的专用快照；应用版本与数据库/Redis地址仍须验证。快照不保存 Config.Env，每次调用重新检查并只删除本次快照。未删除护栏或复用旧核验卷。[e 的快照证据](prepare-25000e/external-container-preflight.json)表明该检查已通过，但准备仍退出1；最初 launcher 在 Windows 隐藏子进程窗口时没有可靠转发输出，现改为显式捕获并回传工具的脱敏 stdout/stderr。e、f原目录保留，不当成成功准备。

[g 的文件诊断](prepare-25000g/pool-filesystem-diagnostic.json)确认后续阻塞发生在私有池路径独占预留：`*fs.PathError`、操作 `open`、`Access is denied`。此预留在写入用户/商家前执行，因此这几轮不属于数据库碰撞、业务拒绝或API容量失败。

## 成功准备与私有临时文件限制

将本次工具进程的 `TEMP`/`TMP` 显式指向共享工作区内、所有项目Git仓库外的新 `.runtime-fullstack-cap-trade-<nonce>` 目录，令池文件和launcher快照均位于该进程实际 `os.TempDir()` 下，仍保留临时路径检查。原账号C盘Temp的文件权限拒绝没有通过放宽端点、移除检查或把凭据写入证据来绕过。[私有位置记录](prepare-private-location.json)只保存路径与原因，不含会话内容。

[h 的公开进度](prepare-25000h/pool-progress.json)确认 `complete=true`，计划用户、登录完成用户和已取得真实会话均为25000，并发16；UTC 04:41:37.5275376至04:41:53.6636904，共16.136秒。合成夹具为merchant3、shop4、tag `captrade-efd89f5b7ed7`。私有池另行只读核实25000个userID、25000个非空token及owner会话存在，未把token或手机号复制到证据。此耗时仅为窗口外夹具准备时间。

该仓库外目录仍继承 `BUILTIN\Users` / `Authenticated Users` 的访问权限，Windows文件模式0600不证明独占ACL，因此只适用于本次本地合成会话，不保证仅当前操作者可读。后续调用必须沿用记录中的实际TEMP/TMP及池路径。全部阶段结束后仍需通过工具对池内精确token键logout，再删除该已知私有池文件；本报告尚未声称已完成注销或删除。[工具说明](../../../../backend/cmd/capacity-trade/README.md)记录调用方式与该限制。

## 首次真实交易短探针

`probe-observer-on` 使用正常创建活动HTTP接口、真实会话及k6到达率模型：单活动1003、初始库存240、目标10次/秒，预热2秒、稳定8秒。其用途是验证测量器与逐单落证，稳定时长低于预固定30秒门槛。

[结果](probe-observer-on/result.json)、[请求记录](probe-observer-on/requests.ndjson)、[最终订单](probe-observer-on/orders.ndjson)及[Redis受理映射](probe-observer-on/redis-accepted.ndjson)相符：共102次完成请求（预热21、稳定81），102个唯一受理ID与102行真实订单逐ID/用户匹配，未发现未落证的持久订单、重复有效订单或错误映射。MySQL/Redis剩余库存均138，满足240−102；未解决review、未完成订单/补偿outbox、最终PEL及lag均0。k6原始请求计数与自定义记录均102、dropped=0，内部队列观察错误0。[队列时间序列](probe-observer-on/queue.ndjson)保留SQL观察时点与耗时，不能据此推断精确数据库提交时延。

该轮 `reconciliationPassed=true`，但 `loadCriteriaMet=false`、`passed=false`；末三分之一队列评估也未取得足够观测窗口。拒绝响应未被当成成功吞吐，短探针通过逐单核对不构成容量成绩。

外部资源采样由手工在负载后段启动，[原始资源文件](probe-observer-on/resources.ndjson)实际包含1次完整样本（UTC04:42:51），随后k6退出，3次样本缺少受控生成器，最终写出[观测不完整停止信号](probe-observer-on/resource-stop.json)。它没有完整覆盖预热/稳定窗口，且停止信号晚于k6结束，不能证明负载期间监督闭环已接通；该次观测集成未通过，不能使用这一次完整样本支持资源余量或容量判断。随后自动监督探针另留新目录，结果如下，不覆盖这次失败。

## 自动监督与观察器开关短探针

工具代码提交为 `4e35830`。自动supervisor先接通资源采样，再监督明确PID/创建时间的k6；两轮均以10次/秒、2秒预热、8秒稳定、20个预分配/最大VU运行，各用一个新活动。两轮均为21次预热、81次稳定、102次完整请求，102个唯一受理ID与全活动102行SQL订单/Redis映射全部匹配，库存138/138，重复有效订单、未解决review、未完成outbox、PEL/lag均0。各有5次完整资源样本，没有观测缺失或停止信号，harness与sampler退出0，监督状态为 `completed_without_supervision_failure=true`。原始 [on结果](probe-auto-observer-on/result.json)、[on监督](probe-auto-observer-on-control/exit-status.json)、[on资源](probe-auto-observer-on-control/resources.ndjson)及 [off结果](probe-auto-observer-off/result.json)、[off监督](probe-auto-observer-off-control/exit-status.json)、[off资源](probe-auto-observer-off-control/resources.ndjson)分别保留。

[重算汇总](method-probes-summary.json)重新流式读取原始k6 exporter，逐记录比对requests文件，再按公开用户池的index核对用户，将每个响应orderId分别查入全活动SQL文件与Redis映射；同时检查导出/SQL/Redis中未被请求覆盖的ID，全部为0。稳定阶段延迟按原始记录重新用最近秩 `ceil(n×p)` 计算，与helper结果一致；k6摘要采用插值，不能把两种算法的P99混成同一个值。六份已结束的exporter随后无损gzip归档，原始字节/压缩字节及双方SHA256见[归档清单](metrics-archives.json)，总大小26,091,070B→739,545B；重算汇总的source已更新到对应 `.ndjson.gz`，仍保留压缩前的原始字节与hash。requests、orders、Redis映射等较小逐单证据继续保留原文。

| 短探针 | 稳定请求/受理 | 稳定P95/P99（ms） | 队列观察墙钟均值/最大（ms） | 主机内存样本范围 | 采样器CPU峰值/单核、RSS峰值 |
| --- | --- | --- | --- | --- | --- |
| SQL观察on | 81/81 | 31.447 / 150.042 | 7.746 / 13.676 | 77.7–79.9% | 2.9%，31,592,448B |
| SQL观察off | 81/81 | 15.780 / 19.648 | 4.673 / 6.880 | 77.4–78.4% | 4.8%，31,895,552B |

`queryMs` 是整个队列观察的墙钟时间，含Redis命令，on时另含索引COUNT，不能写成SQL-only执行耗时。on从观察开始到SQL结果收到的毫秒时钟差为2–7ms，含调度、往返与时钟取整；off没有SQL计数。资源采样自身单次墙钟最大约2166/2188ms，主要包含Docker stats等待，不等于2秒CPU占用；CPU按单逻辑核100%计，RSS与主机内存均为5秒离散样本峰值，不代表连续高水位。

这是一轮一档、每轮仅81个稳定请求的工具对照，活动不同、共享宿主背景会变化，也未控制Go构建等后台任务；不能从两组P95/P99或均值差推导观察器增加多少延迟或优化百分比。它验证索引聚合/Redis观察的毫秒量级墙钟成本、合法用户计量及完整监督落证，未测出有因果依据的observer overhead。两轮仍因稳定不足30秒而 `passed=false`，off还明确关闭了窗口内SQL观察，因此不能提供容量PASS。

## 负载器与导出器短响应校准

独立对 `/health/live` 运行15秒校准，保留与交易工具相同的逐完成唯一record标签及缓冲JSON exporter。它没有登录、准入、SQL事务、库存或订单消费路径，只用于检查本机短响应下的生成/导出能力。原始 [100次/秒](generator-health-100/summary.json)、[400次/秒](generator-health-400/summary.json)、[100资源](generator-health-100/resources.ndjson)、[400资源](generator-health-400/resources.ndjson)及各自metrics文件可按重算汇总中的SHA256定位。

| 目标到达率 | 原始HTTP/自定义记录数 | dropped/系统错误 | 重算P95/P99（ms） | k6 CPU峰值/单核、RSS峰值 | 导出文件大小 |
| --- | --- | --- | --- | --- | --- |
| 100/s | 1501/1501 | 0/0 | 0.5939 / 0.8824 | 11.8%，99,450,880B | 4,972,799B |
| 400/s | 6000/6000 | 0/0 | 0.5285 / 0.7172 | 37.5%，112,750,592B | 19,814,922B |

两轮各4个完整资源样本，主机内存分别77.1–77.2%及77.1–77.5%，采样器CPU峰值3.6%/6.6%单核、RSS峰值31,535,104/31,748,096B。原始记录index唯一、HTTP状态均200且健康版本匹配。100/s在15秒名义窗口产生1501次是到达器端点调度现象，未把目标率强写成请求条数。这只能说明当前15秒校准中生成器可发出相应请求并完整导出，不能推出交易达到400次/秒，也不能证明长时间高基数导出没有内存增长。

## 人工停止信号与REST回退路径

[停止探针](probe-safety-stop/result.json)预设10次/秒、2秒预热、30秒稳定，由[人工信号](probe-safety-stop/external-stop.json)提前终止；该信号是测试夹具，不代表触及真实资源预算。helper的通用 `stopReason=external resource budget` 应结合该原始信号理解。

实际 `pauseConfirmed=false`、`stopConfirmed=true`，k6输出明确记录REST停止并退出103，没有kill兜底。没有保存pause失败的HTTP状态/响应体，因此无法判定是此k6/执行器不支持还是其它拒绝；**不能声称pause后等12秒排空的路径通过**。本次验证的是pause未确认后直接REST `stopped`的回退，以及后续完整活动对账。[监督结果](probe-safety-stop-control/exit-status.json)中wrapper预期退出1、`completed_without_supervision_failure=false`来自已发停止信号；harness退出0、sampler退出0，4个资源样本全部完整，没有强制结束进程或观察错误。

共完成60次请求（预热20、稳定40），k6 HTTP/record计数、逐请求文件、SQL订单及Redis映射均60，全部逐ID/用户匹配；库存400/400，符合初始460−60，重复、review、未完成outbox、PEL/lag均0。REST直接停止理论上可能中断在途响应，不能用缺少响应日志判断丢单；本次全活动SQL与受理映射中未发现未落证的持久去向，导出记录完整。生成器退出后立即开始的排空观察为7ms，最后已完成响应到观察收敛上界1018ms；它不是从发出停止信号起算的精确排空时延，也不代表超载积压排空性能。稳定窗口被截断，名义稳定末尾的排空字段不用于容量判断，最终 `passed=false`。

## 工具直接检查

[最终检查记录](tooling-check-final/result.json)的Go test、vet、build退出码均0；[四项单测](tooling-check-final/go-test.log)覆盖逐请求导出读取/字符串ID与截断拒绝、临时路径及固定队列判据、Docker错误脱敏、外部预检15秒时效与精确归属。[vet](tooling-check-final/go-vet.log)与[build](tooling-check-final/go-build.log)无错误输出。验证二进制写入仓库外临时目录，SHA256记录在检查结果中，没有替换正在运行的API。

本报告交付的是CAP-01准备、失败保留与短探针证据；尚无交易容量档位、最高稳定边界或优化收益成绩。
