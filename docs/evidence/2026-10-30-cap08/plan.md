# CAP-08 执行前协议与名额

依据根 [STATUS](../../../STATUS.md) CAP-08授权与[独立CAP-07结论](../../reviews/2026-10-30/cap07-6693539/report.md)。本文件固定执行条件和结束方式，不代表已经运行。旧A1/B1/A2/B2及CAP-06额度保留，不重演旧组，不自动续成CAP-09。B1与更早长尾仍未知；没有证据不改业务。

## 开发时间与阶段

本轮保守开发计时起点为 **2026-10-30 13:10 UTC / 上海21:10**，上限4小时，硬边界17:10 UTC / 上海次日01:10。`budget-start.json`必须由root用`cap08-policy.py init-budget`独占创建，源SHA为开工时完整HEAD。工具按全部wall time保守计入，实际负载与CI等待另外记账但不从该计时中扣除，避免少算。到时不追加定位/代码方案；若有效诊断、回归/精确CI和处置已在限内就绪，可完成已经预定的正式负载与交付，不能借等待扩问题。

| 名额 | 最大次数 | 进入条件与预测 | 结束方式 |
| --- | --- | --- | --- |
| controlled-1 | 1 | 08A选定缺口，固定prediction；共享内容SQL被barrier暂停且第二worker排队 | 释放前区分`cards.cold_sql`与`cards.slot_wait`的活动/最老年龄，实际collector触发并抓栈；失败也占槽 |
| controlled-2 | 1 | 只验证已经出现证据的候选，prediction引用证据并声明observedCandidate | 相同确定性用例修复前后回归属于本场景，不生成新假设系列 |
| diagnostic-1 | 1 | controlled-1接线有效；事前prediction与wiringEvidence | 鉴权multi100/s×180s，仅定位；额外collector明确开销，不当正式成绩 |
| diagnostic-2 | 1 | 首轮后有明确候选/已证修复；声明baselineTag、oneChangeOnly及证据 | 只验证一项受控变化，不作无信息重复；缺采证如实留缺失 |
| repair-1 / repair-2 | 各1 | 每次有证据，prediction和旧FAIL→新PASS或同源码因果对照 | 一问题一commit，相关回归与精确源码CI；无缺陷不凑修复 |
| formal-1 / formal-2 / formal-3 | 1组 | 有效诊断未重现，或已证机制处理并有证明；相关回归/精确CI已过 | 同一最终clean SHA，multi100×180两轮依次过，再hot200×180；任一失败结束，不补成功轮 |
| stair-1—4（可选） | 最多4 | 正式3轮全过，必要工作限内完成 | 仅multi150/200/300/400×45s；首限即停，不开启新诊断/修复 |
| neighbor-1（可选） | 最多1 | 第一受限档是可测SLO失败，存在明确通过/失败括区 | 严格位于括区内的一档×45s；资源/监督失败不作服务容量括区 |
| hold-1 / hold-2（可选） | 最多2 | 最高完整通过档>100 | 同SHA最高档×180s，两轮；首失败即结束可选组，不降档补组。最高仍100复用正式组，hot同样复用 |

`cap08-policy.py reserve --kind controlled|repair`保留非负载名额；`cap08-stage.py`在网络/依赖验证前创建不可变slot和command，真正启动失败、早停、采集失败均消耗名额。每个CLI只做一轮；无隐式retry、probe或自动上探。原始slot、失败command和active lock不删除，不换tag重置次数。未通过同一阶段前置时停止，不因为恰用满前一阶段名额而阻断已经满足依赖的下一阶段。

## 固定环境、业务与判据

project仍为`project-full-stack-cap-read`，仅自有MySQL3451、Redis6551、API8251、loopback diagnostics8351。先核移交的64位CID、compose project/service、已停止、端口、挂载及自有network/private subnet；新随机CAP08卷替换明确自有的两容器，旧新卷保留。runtime为sibling `.runtime-fullstack-cap08-<12hex>`，owner ACL、随机observer与合成会话独立，不复用CAP07凭据或fixture。CAP08固定ROOT/私有路径/phase/active lock与CAP06/07互拒。

沿用6400篇/32作者/32读者/每作者200篇/正文256B/页20，multi640公共key，hot1读者20key；001—013、SQL20/Redis10、GOMAXPROCS4/GOMEMLIMIT1GiB、MySQL4CPU/2GiB、Redis2CPU/512MiB、workers1及持久化保持，Kafka/ES/AI关闭。保留RR、当前关注/公开状态/版本/个人点赞/门店/游标、计数失效和共享任务生命周期/限额。不扩池、延TTL/请求deadline、关闭鉴权/持久化或预热避过期。确证环境问题最多一次明确的自有配置调整，同源码对照并单列，不与代码差量混算，不停CRM等其它服务。

诊断与正式均预热10s/25每秒，pre/max VUs256/512，稳定负载计划180s，探索45s。每响应仍按原分类器核完整20条ID/作者/门店/排序/计数/本人点赞；HTTP200不等于正确。原native判据：计划发起≥99%、成功≥99%、系统/HTTP错误各<1%、内容错误/unknown/dropped均0，全/成功P95≤200ms、P99≤500ms；早停保留计划全时长分母。wrapper退出码、collector有效性、native容量判据分开。

安全预算不变：host内存88%、API RSS1.5GiB、k6 RSS2GiB、自有容器内存85%，host CPU85%连续3个有效样本或连续3次观测不完整即停。原安全sampler5s一次；没有资源stop不排除瞬时停顿。正式期间不开collector/抓栈，且不并行编译、Go测试或其它容量任务。

## 最小观测与采证

继续使用默认关闭的tail recorder和固定阶段；诊断新增请求`feed.cards`/`feed.card_fallback`、组件caller Redis检查与`cards.shared`/`cards.slot_wait`/`cards.recheck`/`cards.cold_sql`/`cards.fill`等。共享任务仍从应用life派生原有2s context，不绑定首caller；不接受用户/token/正文/key/原SQL参数等标签。

每stage输出active、tracked_active、untracked_active、oldest_active_ns、oldest_complete、active_tracking_overflows。固定1024追踪槽超界时明确oldest_complete=false；缺失/不完整年龄不当0，不用恢复栈排除此前等待。请求等待与共享任务阶段嵌套，不能相加为总延迟；SQL/Redis边界包含不可分离的池/执行/传输成本，累计WaitDuration不是单请求耗时。

`cap08-collector.py`只做loopback HTTP且禁止redirect：capacity最多1Hz顺序采样、每轮最多3次goroutine文本抓栈、每次最多1MiB、上次捕获实际结束至下次发起至少15s。触发为active>0且oldest_complete=true且最老年龄≥100ms，或上一成功采样到本次SQL/Redis全池等待正增。保留20s前/至多10s后窗口、每次成功/失败HTTP的发起/返回/耗时/缺失、采样迟到/漏间隔、截断和stage字段缺失，不连续CPU/执行trace。profile和capacity不是原子快照，不同采样时间必须对齐；collector本身不做进程枚举，真实负载/API身份继续由原5s supervisor与stage前后身份核验承担。

第1受控模式仅`127.0.0.1`动态端口、单capture、≤5s，首轮立即采样/捕获后返回；用于Go测试仍被barrier阻塞时的真实采证，不声称180秒覆盖。实际诊断只允许固定8351，待本轮独占stable-process.json出现后采样，done-file或最长360s结束。完整覆盖的事前规则为：稳定k6新PID创建时间到首poll起点、末poll起点到supervisor done均≤2.5s，最大poll起点间隙≤2.5s，且无HTTP/阶段字段失败、无不完整最老年龄/截断或失败栈；只有一条成功poll不叫完整180s覆盖。缺记录不补0，采集失败不叫未重现。正式phase禁tail且不会启动collector。

磁盘只在collector的`slow-records.json`保留最多32条不同实际慢记录，按已观察total_ns最高32条选择、sequence作tie。每秒timeline、trigger-window及诊断stage before/after均删除full slow_traces，只保留该次可见sequence/latest sequence与所有原stage/pool/cards/runtime字段，标明parsed subset。原response SHA来自本次实际HTTP读取字节，不是重新JSON序列化；完整response不落盘，不能称完整response归档或逐响应重建。窗口引用sequence不在最终32条中就明确没有归档payload，app ring/轮询间隔未看到的记录也不填0；字段/sequence异常另记缺失。profile每次1MiB独立计，collector不会每秒重复写32×256 spans形成超大blob。

## 正式放行与交付

formal gate为root明确写入的公开结论，必须包含同最终sourceSha、相关检查/精确CI通过、已证问题已处理、限内工作完成时刻、`diagnosticOutcome=not-reproduced|treated`、`unresolvedReproducedFailure=false`及证据路径。not-reproduced要求全部实际诊断采集覆盖与原生预算通过；treated不让一次捕获缺口永久挡住独立证明的处置，但需至少一轮实际stage采样、公开现场依据和treatmentEvidence中的确定性旧FAIL→新PASS/同源码因果对照（写明source/config及实际结果），不是只有gate布尔主张。同类失败重现而仍未知/无可验证处置，不建成功gate，不靠正式轮赌通过。

各轮保存source/tool/binary/fixture hash、原生summary/分类/资源、内容loader差量和全部失败。loader窗口为预热前至收尾后，含预热/后台，不伪作稳态；全实例Com_select/Com_stmt_execute重叠，分列不相加。无同档A对照只报告最终B实测档，不写缓存提高倍数。原始分位无逐请求样本时只核native，不称离线重建。

最后保持API/依赖可用直至32会话正常logout/Redis EXISTS0和observer删除核0，再按精确API创建身份/exe/hash与不可变CID停止并留卷，精确删除记账private文件；失败保留恢复材料。交功能已核验/已证机制和修复/旧B1未知/正式是否达标的分层结论，以及新卷单代表multi100×180复跑说明。预算耗尽或正式失败即交未通过并停止本轮，不自动追加CAP09。
