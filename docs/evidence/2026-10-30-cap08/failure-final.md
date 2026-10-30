# CAP-08E：正式第二轮失败与访问计数拒绝

2026-10-30。本文只读已落formal-2工件及清理身份记录，没有追加采证、运行HTTP/SQL/编译、启动服务或负载。应用仍固定 `46ddefc2a16d41e4bb01a3d20d08bdfcaeff465f`，本轮业务修复0。

**正式第二轮确实失败，不能只称后处理脚本问题。** 原生已有unknown200、dropped57及发起/成功比例不足；另有访问计数时基一致性拒绝，两件事分别保留。按事前规则正式组已停止，不执行hot200、升档或补成功轮；剩余诊断2名额不能用于在正式失败后回头重开定位。本批未达到正式稳定性收尾条件，旧B1原因仍未知。

## 原生失败独立成立

[stage exit](formal-2-multi100-stage-exit.json)与[原result](formal-2-multi100-load/result.json)来源同一正式API PID66608、同46ddefc应用及同新fixture；phase formal、无额外collector，33个资源样本中tail均disabled。native stable于UTC `14:00:10.929`开始、`14:02:54.582`结束（上海22:00:10.929—22:02:54.582），命令墙钟163.653秒，计划窗口仍180秒。

由于访问计数guard抛出断言，result.json没有正常的`result`字段。以下计数直接来自未改动的[native stable-summary](formal-2-multi100-load/stable-summary.json)，不是倒填result或把缺字段当0：

| 项目 | 实际值及原判据 |
| --- | --- |
| 发起 / native HTTP / 终态 / 成功 | 16133 / 15933 / 15933 / 15933 |
| unknown | 16133−15933=200，要求0，失败 |
| dropped | 57，要求0，失败 |
| 已分类system / HTTP / content错误 | 0 / 0 / 0；不能抵消unknown与dropped |
| 实际发起 / 计划18000 | 89.6278%，要求至少99%，失败 |
| 成功 / 发起 | 98.7603%，要求至少99%，失败 |
| 成功 / 计划180秒 | 88.5167/s；保留完整计划分母，不以较短实际时长抬高 |
| 已记录全部/成功时延P95 / P99 | 均24.18684 / 84.048128ms |
| 已记录最大时延 | 3656.4646ms |
| warm / stable exit | 0 / 99 |

[native console](formal-2-multi100-load/stable-console.txt)明确记录上海22:02:54的`dropped_iterations`阈值触发abortOnFail。P95/P99虽在预算内，200个未完成发起的结果/具体耗时并不在已完成时延分布中，不能据此认定整轮达标；最大值已显示本轮部分完成请求出现秒级等待。

console另有3条status0警告，但native终态错误计数仍0；这些警告不等于完整错误分类，不能拿3条替代unknown200，也不能因为已分类错误0写成没有请求失败。原始分类和未知均按其实际范围保留。

## 访问计数拒绝不等于原始计数归零

[access-delta](formal-2-multi100-load/access-delta.json)没有逐项`reasons`字段，保留的事实是`uptimeConsistent=false`、`counterResets=true`，loss为0、missingAfterDigests为空。原helper的`counterResets`是“uptime不一致或任一负delta”等条件的复合标志，不能把通用failure文案直接解释成实际reset。

本轮[before](formal-2-multi100-load/before.json) / [after](formal-2-multi100-load/after.json)可核：

| 原始MySQL字段 | before → after |
| --- | --- |
| Uptime | 2230 → 2391，即+161秒 |
| Com_select | 161984 → 228319，未倒退 |
| Com_stmt_execute | 160932 → 227156，未倒退 |
| Performance_schema_digest_lost / prepared_statements_lost | 均0 → 0 |

全局、statement event、SQL digest和Redis command delta各类均未见负值。MySQL GLOBAL STATUS读取窗口分别UTC `14:00:10.678—14:00:10.801`、`14:02:54.772—14:02:54.910`；Windows两次区间中点相差164.1015秒。它与MySQL Uptime增量的绝对差为3.1015秒，大于同版helper既定2.261秒不确定度，触发时基一致性拒绝。helper原检查与通用断言见[go073-cap-read.mjs](../../../scripts/go073-cap-read.mjs)，本次没有修改容差或覆盖失败。

这些正向原始值可用于解释guard分支，仍不把本轮被拒绝的访问计数写成有效访问量、每HTTP SQL次数或缓存收益。Com_select与Com_stmt_execute本来就重叠，不相加。现有字段不能判定时基差来自宿主、虚拟化、时钟校正、数据库计时或其它具体原因。

**Redis uptime同窗不可核。** 既有before/after只采Redis INFO commandstats（calls/usec等），原5秒capacity中的Redis项是应用连接池统计，均不含`uptime_in_seconds`或Redis server时间。此处明确缺失，没有临时补采或用MySQL时基替代Redis时基。

## 已记录的最后窗口及其边界

[原5秒资源记录](formal-2-multi100-control/resources.ndjson)共有33/33完整样本、无样本errors，API均66608、k6均23536；没有resource stop或强制终止。它记录的是顺序采样，外层时刻/内部capacity字段并不原子，也不保证捕获瞬时峰。

| 正式2已记录点 | SQL pool | Redis pool | 公共卡片 |
| --- | --- | --- | --- |
| elapsed155.022；outer14:02:46.812229，capacity14:02:46.8368488 | 10开/1用，累计wait0 | 8连接/8idle，累计wait0 | flight/active/waiting均0，累计内容SQL856批 |
| elapsed160.199；outer14:02:51.989240，capacity14:02:52.0422603 | 16开/16用，累计wait仍0 | 10连接/0idle，累计wait9次/24.7611607秒，stale1 | flight/active/waiting仍0，累计内容SQL仍856批 |
| 预热前→wrapper收尾后的端点差 | wait+328次/累计180.0832286秒 | wait+368次/累计258.5982841秒 | 最后在途均归0；无loader/Redis/拒绝错误 |

端点差来源为[前](formal-2-multi100-capacity-before.json)/[后](formal-2-multi100-capacity-after.json)，含预热/收尾，不是稳态或单请求耗时。不能拿较早的最后sample当最终累计值，也不能把数百秒并发等待总和当成一次数百秒停顿。最后capacity内部时刻距原生stable命令结束约2.540秒，之后的在途变化未被另一条5秒样本捕获。

公共内容[独立端点差量](formal-2-multi100-cards-delta.json)为408批/1920行/538920字节，joins0、load/Redis/坏值/拒绝错误0；该窗口同样含预热至收尾。最后样本没有cards回源排队，与旧B1的cards8 active/25flight并不相同，不能直接归因为内容loader，也不能把“没有抽到”写成所有时刻均无等待。正式模式依协议没有额外stage collector或在途goroutine栈，本次不补开。

已记录最大host CPU68.4%、内存79.5%，API RSS38.8438MiB、k6 RSS163.5508MiB；MySQL/Redis容器内存最大27.91%/2.15%，均未触事前安全上限。低于采样上限不排除更短的依赖/宿主停顿。

[control exit](formal-2-multi100-control/exit-status.json)为sampler exit0、harness exit1、errors空、stop_signal=false、forced=false；综合成功flag含harness失败条件，不能单独解释成采样器故障。[stage exit](formal-2-multi100-stage-exit.json)为cardsEvidenceValid/toolBytesStable=true、failedStartOrException=null、wrapper exit1、collector未启动。原生负载失败和访问计数后处理拒绝均是真实保留的不同结论。

## 停止与收尾

[停止前身份记录](post-failure-container-identity.json)在UTC14:07:38只为精确清理核两只原CID：restartCount均0、startedAt均约13:22:48、OOMfalse。它支持本轮现存容器未报告重启，不能证明宿主/VM没有暂停或给出旧B1根因。

[cleanup](cleanup.json)记录32会话正常logout/Redis剩0、observer删除核验、API66608按精确身份停止、两CID均exit0/OOMfalse、旧新卷保留、4份登记private env精确移除。Windows terminate仍不称Go优雅关闭；本文不执行任何清理动作。

有效诊断1未重现、正式1通过和正式2失败分别保留。正式组未通过，本批按规则结束，不因还有诊断/修复名额就回头尝试，不补hot或升档、不自动开CAP-09，不盲扩池/延TTL/期限。功能层面沿既有独立核验，当前新增证据只支持本次容量未达标、已观察到排队和时基不一致、具体原因仍未知。
