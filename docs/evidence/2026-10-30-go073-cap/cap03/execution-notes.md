# CAP-03 实际执行记录

## 本地准备（尚未启动环境）

2026-10-30。根 agent 正在运行交易 W1/W4 对照，本 agent 等待明确的“交易环境已释放”后再启动任何读依赖、API 或业务请求。当前只创建仓库外独占 `.runtime-fullstack-cap-read-2c79ddf4f58b` 和私有环境 JSON，合成只读观察账号的随机密码仅在该文件内，不写入证据。选定无凭据配置见 [preparation.json](preparation.json)。

根 agent 后续明确两道执行门槛：交易环境 release 后可启动专用读依赖/API、夹具及 5 秒短探针；正式 30 秒及更长读容量阶段另等交易证据压缩/归档的 CPU 工作结束通知。估计等待时长不是授权，不按经过时间自行启动。

固定应用为干净源码 `3db296dd0beedd6f85d06cf5479d707b0498ce7e` 的现成二进制，预期 health version 为 `capacity-3db296dd0beedd6f85d06cf5479d707b0498ce7e`；读侧设置 `ORDER_STREAM_WORKERS=1`、`GOMAXPROCS=4`、`GOMEMLIMIT=1GiB`。MySQL/Redis/API/诊断分别为 3451/6551/8251/8351。仓库测量脚本已包含 SHOW GLOBAL STATUS/Execute 真实访问计数和成功请求 SLO 修正，应用产品源码保持该固定版本。

最初本地私有文件写入命令使用了 PowerShell `Set-Content` 不支持的 `-NoClobber`，在私有环境文件写入前退出 1；当时只有本次空 runtime/tmp 目录，未启动进程、依赖或业务。随后改用 `FileMode.CreateNew` 排他写入并成功，没有输出密码。此为准备命令问题，不是压测结果。

计划先 1 req/s、预热 1 秒、稳定 5 秒短探针核对响应内容、实际分母、计数与采样，不把其 `meetsBudget` 当容量。之后 hot/multi 各自从 25/50/100/200/400 req/s、每档稳定 30 秒探索；每档结合实际发出率/dropped、全请求与成功请求延迟、成功比例/错误、完整资源样本和 SQL SHOW/Redis 增量检查，条件允许才继续 800/1600 等。首次受限附近缩步，最高通过档固定稳定 180 秒，至少两轮。所有阶段与失败保留独立新目录，不覆盖原数据或放宽事先预算。

MySQL 参数化 SQL 未出现在当前 digest 视图，SHOW 的 Com_select 与 Com_stmt_execute 单独报告且存在交叠，不能相加为总 SQL，也不能从空 digest 推论公共计数回源为零。Redis 访问包含鉴权、TTL、缓存及后台任务。hot/multi 仅是不同 key/用户分布，不作为缓存优化提升因果对照。

`cap_trade` 在启动前只读复核当前测量脚本和本计划，未发现阻断；校验 20 条页面内容、用户/key 分布、成功与全请求延迟约束、SHOW/Execute 口径及稳定阶段精确 PID。源码 `auth/service.go` 的 SessionTTL 为 25 天且鉴权刷新，足够本次逐档窗口。k6 failureRatio 的 `<1%` 在恰好 1% 的边界比报告成功比例 `>=99%` 更严，执行时保留更严条件，不放宽。此审查未启动依赖或发送业务请求，不等于实际容量核验。

## 启动和未优化基线已执行

后续两道门槛均获根 agent 明确放行，实际启动、隔离子网问题及九档索引前探索见 [baseline-report.md](baseline-report.md)。API PID45532，原固定二进制/资源和6400篇夹具不变；已完成短probe、hot25/50/100/200/150/175/160以及multi150/175。受限175/200按既有阈值早停并保留unknown，页面内容错误为0，恢复检查通过。

一次派生汇总命令在 hot160 的 supervisor 尚未结束时读早了，因 `exit-status.json` 尚未生成而失败；没有修改该轮 raw 或重新发送请求。随后等待实际 supervisor exit0 再汇总成功，并改为仅确认子阶段退出后调用汇总。此为分析调度问题，不是业务或测量窗口失败。

根 agent 指定在索引前两模式150通过/175受限与完整SQL计划齐备后暂停正式负载，等待独立opt-tests环境完成正式013迁移/既有social回归并释放CPU，然后同数据/同API/同资源仅增加索引，再做同档对照及最终阶梯/180秒两轮。当前暂停不等于CAP03完成；不提前写优化收益或可持续容量。

## 正式索引与后续应用修复

013在独立opt-tests完成正式迁移、重复运行skip及social真实回归后，正式应用到同一读库；before/after同SQL和同数据计划证据及所有探索/失败保留。旧3db应用曾完成hot300/180两轮，但multi尚未同档两轮通过时，源码审查与隔离回归发现并修复Feed持snapshot连接又从同池借连接的条件饥饿。旧结果没有删除，也不计作修复后资格。

根agent提供干净767构建并释放测试CPU后，本agent用原PID/绝对exe/UTC创建时间身份守卫停止旧45532，保留原api.json、原私有env及fixture manifest全部字节，新建api-pool-fix.json和独立私有env。新PID64908、API源码767与原fixture源码3db分别严格声明并与health/manifest绑定；来源拆分测量器由非作者审读后单独提交61cd54a。新事前计划pool-fix-plan.json明确最终四轮资格清零，原SQL pool20、资源/鉴权/错误和延迟预算不变。

新API短probe及400/500的两种分布30秒探索均通过；600两种分布均触原预算，原数据保留，不向更高数字扩档。一次multi500-a在本地native harness启动阶段退出3221226505（0xC0000409），没有稳定生成器、资源样本或业务result，原因未知，计数为null。原监督退出保留，并增加unscored-failure.json明确不计分，未采进程环境或native内存转储。只进行一次独立b重试，真实完整30秒通过后立即开始500/180长持；没有更换Node或放宽预算。

## 新API实际长持与收尾完成

767最终hot200/180 A/B分别36001、36000成功，multi100/180 A/B分别18000、18001成功；四轮均完整180秒、unknown/drop/系统/HTTP/内容错误0，37/37完整资源样本及36有效CPU，无resource stop。54个有业务结果阶段和1个不可计分native失败完整保留；新hot500/400第二轮/300、multi500/400/300/200所有受限尝试均保留，并未因低分位或短轮通过省略失败。完整值及SQL/Redis范围见report.md/summary.json。旧3db两轮未计入767资格。

四轮实际结束后先将精确四目录/数值/资源覆盖发根agent，再按此前授权运行已独立审读的finish-and-cleanup.py。第一次在任何变更前读取固定API exe时瞬时psutil.NoSuchProcess，保留预检失败后只读复核：同PID仍运行、两端口UP、ready精确767、exe一致、原UTC创建时间差0.936ms。没有重启API，也未放宽守卫；同一脚本重试全部完成。32会话正常logout并精确Redis剩0、本轮只读observer已drop核0、API原身份/hash再核后Windows native terminate（exit15，不声称Go graceful）、只正常stop两个原CID且exit0/OOMfalse、卷与SQL夹具保留，原/新私有env和精确token input/空叶目录删除。逐步progress和最终cleanup证据均落盘，首次预检异常保留。

销毁凭据前扫描1289证据文件、34个唯一私有值，匹配0；没有全机按进程名停止、递归删除、改无关依赖、改全局Docker配置或发付费AI。读环境已释放，此后不再发业务负载。完成后的核验只读本地证据；外部独立复跑仍待交接。
