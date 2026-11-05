# 给实现 agent 的 Go 迁移交接

更新：2026-11-05。接续先读根AGENTS、[协作与授权记录](collaboration-agreements.md)、[当前STATUS](../STATUS.md)和[总方案](implementation-plan.md)。状态仅由根STATUS维护；本页保留执行顺序与交付规则，不另列一套历史状态表。

## 当前交接：本批稳定3000与归档清理完成，用户确认原ECS已释放

用户于2026-11-05确认原ECS已释放；按用户回报记录，未通过ECS API独立核验。当前正由用户逐步留存PTS正式H3/H4报告的官方PDF与页面截图；此操作不依赖原ECS，也不阻止实例释放。现场现已无法经原实例接入；未单独确认关联盘是否随之释放，不推断磁盘已删除或费用已清零。已有备份为选定证据增量，未做整机镜像或MySQL全库备份。

[脱敏收尾摘要](evidence/2026-11-01-cap10/cloud-like-closeout-summary.json) SHA=`fad3ccc22592845c2cad3a70f968a2d179041d7957fd9e0164283cec39a266ce`，只含SHA/计数/状态；用户确认与agent独立查询范围保持区分。

本批S29候选及同新freeze的H3/H4两正式全部PASS，稳定成功状态变更吞吐取低为3008.3278次/秒；两正式各541499次/完整180秒，H3 P95/P99=62/102ms、恢复1.654332秒，H4为55/94ms、恢复1.875416秒。timing/unknown/drop等质量计数均0、unknown精确且DB计数一致，事实/派生、API、下游、资源和采集通过，childrenConverged=true。负载为1996独立关系的点赞与取消变更，warm及准备noop不计主窗。正式下游按原末60秒保守窗口，不把tail15=null写成额外末15秒已测。

新freeze SHA=`b9ad14447b94cc903b427f51b24e9db991cf046e8501d93545e267d35cfe70f1`，两轮保持同API/部署/资源/工具身份；S29完整60秒180299变更、P95/P99=36/72ms、恢复1.637856秒，候选不能顶替正式。见[正式报告](evidence/2026-11-01-cap10/cloud-like-final-report.md)和[脱敏原摘要](evidence/2026-11-01-cap10/cloud-like-S29-H3-H4-summary.json)，摘要SHA=`5eb52fb30c1016336737bab98069519a6dc866add4c7dd8eadcfee6722da928b`。单机16vCPU/32GiB，MySQL容器8CPU/8GiB、buffer4GiB/redo2GiB、Kafka2GiB；不写成最大容量或整机8核8G。 独立PTS四引擎通过阿里云上海VPC内网向该单台ECS发压，引擎不运行在被测ECS上；表内P95/P99仅指私网HTTP主窗，未包含公网用户链路。

本批不再新增票据、负载或云机请求。H4后fresh终审和实际success_close精确清理均已完成，cleanup SHA=`114eb9a12bc1b4ef5263818dc3e03aa26fc1698b559e867b0ed94aa933f7fa93`：当前1996会话注销后余0、credential已删除，63个环境登记及8个upload登记对应目标全部不存在、审计JSON索引保留。API/collector端口8281/8381/8481关闭，四原容器停止/无OOM，MySQL与Redis exit0、Kafka与ES exit143；释放前该次清理未删除主库、卷、网络及旧FAIL；用户原keys保留，未修改或删除。原API stop回执exitCode=null保留，不改为0；后验PID/端口与容器状态另有实际复核。

主395件备份已下载逐395SHA核验，原文件315397060字节，包SHA=`0b56c89dff5753ece53b407e19d5dbc176a3ef3ecfad667fa23522a53e96d9bc`、manifest SHA=`57dc4ae1633e10196f26b0bb62786487c86a9de863d035d1b30d6d01a385d5a1`；收尾22件小包已下载逐22SHA核验，包SHA=`5d088ddad03fc8df1924f3ccaa07bf6a3f0bf29656dbb7fe86a4bece8cb7ed4c`、manifest SHA=`e476e06d8d8f66b52a16d3729f57916ebf2f387c290821af4dfa5f45a4b6ec3b`。选定增量依赖原2736+1259+10份已核原件，不是整机镜像；两正式共1082998条raw经独立重算。仅本次API两活日志允许append-only并保最终SHA，其余原E证据SHA不变。用户现已回报原ECS释放；此前控制台节省停机确认保留为历史时点，agent未通过ECS API独立核验，root不再SSH。旧S28候选、H2 FAIL/unknown=null、CAP-09未过容量及B1未知保持。

本boot资源恢复过程已实际闭合：原四CID/镜像/挂载/卷/网络保留，Kafka2GiB/Swap3GiB；MySQL重启后128MiB buffer/100MiB redo已用两个SET恢复4GiB/2GiB，原UUID/版本、双1、delay0、RR、doublewrite保持，无重复DDL/CREATE。新metadata SHA=`472fa2e997bbc7d72501b4ebb0e63ddc337ab222c32c2485509664fc233c4057`、proof SHA=`d67eb9b4c0c0ba00fab5d850a4aaffd248d520b5c1bd9fcffcde7a60fb7425b1`，不借旧idle。

本批正式使用的新API已实际ready，实际result SHA=`2695341b4a286b61ba16774727358588870a691a013b9a4c784b5d5677e9f42e`，业务仍为c464/4565原产物、observer9502；新60秒/13样本idle通过（CPU中位2.8%、内存最高13.7%）。首prepare因label与资源环境登记同名，在Popen前被FileExistsError拒绝，原stderr为空、无API及端口占用核验保留；显式新label/独立attempt2通过，不覆盖首失败。新packet及三MD5真实Get-only验证通过，与昨晚用户上传相同，无需重复上传。

原H2两真实EndTime和35个owner PID/birth退出已重新核验，本boot恢复后214421 events完整facts/derived/downstream对账通过。失败组proof SHA=`21be111aaba1284a5f4e8522e2179c3bda305d235cfc9f99dfdab3ba6b8ffbb8`已实际登记，旧freeze原字节归档；不将H2改判PASS/retired或把unknown=null改成0。S29及新组H3/H4已按本boot新证明完成，原H2失败不改判、不与新组拼接。

凌晨暂停历史：用户要求取得可写简历的稳定至少3000成功变更/秒，当时目标尚未完成；保存新JMX后要求约10分钟关闭ECS、明早继续，停机准备截止18:08:40 UTC前未再发压、Start或新API。原日志/上传只读核验、增量下载及精确停服完成后已告知可控制台节省停机，当时ECS停止未获独立查询；本次新boot不追写旧查询。R1—R4已完成并同步，1996关系/4引擎/2000VU及原门槛不改，以根STATUS当前恢复指令为准。

凌晨历史停服回执SHA=`1eed7539a4a9ad329c137b4e6c7652bd7f5740535ef26a30ffbbf699e571062b`：当时API端口关闭，原四容器均已退出、无OOM；MySQL/Redis的exitCode=0，Kafka/Elasticsearch为143，原信号退出码保留。原CID/卷保留，未清理会话或夹具。选定增量包69101423字节、SHA=`5623ed8cdeebf10712f8fc68e79fd36460ecf455ebb2159f2d28601dc55a818e`，manifest SHA=`8ad2d5fa8eec5f66a708362423d9b6060810f18255c3a06c71b4cfec6eccaf9b`；已下载本机且独审1259文件逐一bytes/SHA、tar集合和路径安全全通过，另有1份原manifest。它依赖原2736件基线备份，不是整机镜像；当时未执行的authority/slot补包现已补齐10个JSON并逐SHA核验，包SHA=`256f19862c3aeb018f74cc826d5050db1d9b0a709c435fe2ed3518ca97e8bd3c`。所有私有归档不公开，历史暂停段不覆盖本次继续指令。

S23配置3010×60秒，180299次实际成功变更，完整分母为3004.983333次/秒；HTTP/DB计数一致，timing/unknown/drop均0，事实终审通过、恢复1.455063秒。P95/P99为266/355ms，`apiBudget=false`、`candidateMargin=false`，全窗与末15秒下游均失败，最终`performance_limited`。该次未进入正式窗、负载owner已闭合，没有正式稳定3000成绩。全窗search lag为2027>1653，末15秒三组收据增长922/950/1237、Kafka lag为631/708/1042，均超各自预算301.56，不能用全成功覆盖这些失败。

S24仅将pool64/64→128/128、consumer32保持，新进程及新13样本空载通过（CPU约2.6%、内存约13.3%）。候选完整60秒实际约3004.98次成功变更/秒，P95/P99为198/320ms、API预算通过，但候选160ms余量未过，全窗及末15秒下游失败，仍为`performance_limited`；timing/unknown/drop均0、事实终审通过、恢复4.183483秒。全窗收据增长5476/4804/5722、Kafka lag5317/4677/5499均超预算1655.36；末15秒收据增长2952/2728/3240、Kafka lag2372/2212/2596均超预算301.38。该次未运行正式，不能据其调用仅供完整候选PASS的freeze/formal脚本。

当前业务源码c464c1fa4d153806fb44ac746312552655ebda63、容量API、observer和精确CI绑定见[STATUS](../STATUS.md#已交代码工具与最终产物)，S23/S24实际使用该组合。旧计数矩阵FAIL与独立接续PASS分开，组件SQL6→4不代表容量。原S20的268/358ms、timing134与新S23的266/355ms、timing0分别保留，[S20/S23脱敏原摘要](evidence/2026-11-01-cap10/cloud-like-S20-S23-summary.json)不重采样或改grade；源码/夹具/包/boot变化不能拼成纯业务优化倍率。S24详细数据见下方S24/S26原摘要。

准备失败同样保留：BOOT首FAIL瞬时原因未精确取证，之后只读复核和显式最终化通过，不重复resize/CREATE；authority的H1/S22前后报告同名`FileExists`已定位，原H1退休证明经实际public grade通过，以同助手显式新attempt完成准备，不重复assembler/API/idle。八助手字节未变，不改写首FAIL或旧H1失败。

pool128另用独立三源副本：authority两处报告路径加入原command标签避免重名，其余两源沿用原字节，原八源不动。控制台紧凑结果与原pretty文件SHA不同时，原driver已在写work/inputs前拒绝；后用实际文件SHA经原CLI接续，不覆盖失败或改driver。

S25在pool128/consumer64下已中止，原`correctness_stop`、`batchFatal=true`及`childrenConverged=false`保持。已接受raw有22068次mutation、1次drop，非完整窗，不计算QPS/P95；一次实际成功HTTP耗时719ms，超过同relation约663ms周期，后一次来不及启动而drop。后续overlap拒帧及原unknown缺失保留，被拒batch未保存完整body，不能据此推断假overlap。

S25独立真实闭合已通过原1996关系完整reconcile、两个签名provider EndTime及全部旧PID不存在核验；备份原owner字节后唯一释放锁。闭合证明不改变原`childrenConverged=false`或成绩，不补造terminal。S26随后以同c464、pool128/consumer32、Like诊断开启配置完成3010×60秒诊断：180299次成功变更/完整60秒=3004.983333次/秒，P95/P99为216/333.02ms，drop/timing/unknown均0，但API及下游失败，仍为`performance_limited`。事实收敛、原`childrenConverged=true`、恢复6.038893秒及负载闭合已核实，诊断API也已正常停止。

前次boot的[redo2GiB试验](evidence/2026-11-01-cap10/offline-improvements.md#11-05同机redo容量试验收益待验证)的在线配置及验证约1.7秒完成：requested=effective=2147483648、status=OK，不重启、不persist，保双1/delay0/doublewrite、原4GiB buffer及8CPU/8GiB资源。原metadata与resize-before的UUID/版本仅用文件补证通过，不重跑SET或改原证明。新1996夹具/包及新namespace只读验证已通过，三个上传MD5与旧包相同，无需用户重新上传；最终诊断关闭、pool128/consumer32 API及新13样本空载通过。S27实际warm 250次成功变更后、stable启动前setup超时，原`correctness_stop`、`batchFatal=true`、`childrenConverged=true`保持；最终facts/derived/downstream对账通过，但没有stable主窗或其资源样本，不得写主窗QPS/P95，该次未进入正式窗。

已由原baseline事实文件只读证明：Node/ready按manifest顺序，Go live按内部NoteID词典序生成摘要；新ID跨十进制位数时发生顺序差异。初始版本全0、控制摘要相同、事实未变，两种排序的hash分别精确匹配原ready/live，导致setup原60秒始终无法匹配samevector。顺序修复`b461ec6e9ef60d88176fc199e0bc853cec1e5a66`已提交、正常推送并实际部署，新observer空载13样本通过（CPU中位约2.75%、内存最高约16%），真实14条摘要保持manifest顺序一致；它不追认原S27失败或证明负载容量。

公共[pre-stable闭合入口](evidence/2026-11-01-cap10/prestable-closure-plan.md)提交`bb9b0cd85627f622d10eb504bb1aebfc1747abb4`已通过8项聚焦检查及独审，经Git bundle实际部署；S27原warm真实签名EndTime/owner退出、setup失败原件和后续新鲜完整对账已通过公共验证，S28原完整authority实际通过。业务API仍为c464/原4565产物，无需重建。业务Go/module差异为0不等于整个backend目录树相同，历史exe归档与README也会改变目录树；API CI不能借给新工具作CI证明。

S28完整候选已实际通过：180299次成功变更/完整60秒=3004.983333次/秒，P95/P99为13/52ms，timing/drop/unknown及HTTP错误均0；事实、全窗及末15秒下游、资源和采集均通过，恢复4.732779秒、`childrenConverged=true`。[S28脱敏原摘要](evidence/2026-11-01-cap10/cloud-like-S28-summary.json)已下载并核SHA，保留candidate身份，不能写成正式稳定容量。正式freeze已实际创建，SHA=`aac3026375430c276761bcee6b23bd24751f7195dbfa6a7fc1d22f4b4d712230`；H2按同freeze配置3010×180秒，但已中止失败，未接旧freeze的H3补pair，当时稳定3000两正式尚未证明；本批新组结果另见顶部。

H2预热250次成功变更后进入主窗，实际supervisor停止由Kafka容器1GiB内存达到85%保护线触发；更早约native第4.7秒已有4条PTS调度违规，偏移50.10/52.40/54.18/52.87ms，独立超过原50ms门槛，不能全部归因于后来的Kafka停止。已接受raw有32463次mutation、drop为0，但unknown/classificationUnknown为null且非exact；未满180秒，不计算正式QPS或P95/P99。原`correctness_stop`、`batchFatal=true`、`childrenConverged=true`、`factsConverged=true`、`failedStartOrException=null`保留，最终facts/derived/downstream对账通过不抵消正式失败。Kafka没有OOM，未用旧freeze的H3补pair，本批H3/H4属新组。

前次boot已唯一执行并验证同Kafka CID内存1GiB→2GiB、MemorySwap 2GiB→3GiB，保原额外1GiB swap allowance；当时四依赖PID没有重启，原mysqlActual与redo2GiB证明保持，随后按暂停指令停服。profile为`mysql8-kafka2-v1`、旧资源metadata SHA=`0319e92dc2a89014f985629f5e83261c411ba523e36611b899516cbcf271b298`，不借它证明本boot空载或吞吐。新API/空载/资源证明及S29准备的当前状态见顶部。新JMX和另两文件已在本boot新packet上完成真实Get-only三MD5核对；JMX SHA=`1c81410efde56af9e034508657d2f582626ca57d2b1bb569fbd8130a480003b1`，不需重复上传。旧H2四引擎INFO日志各读首100行且total更大，无启动/GC命中，不排除GC或认定抖动根因。

远端同步已实际完成：`b461ec6`及`bb9b0cd`均已push。`bb9b0cd`先前多次HTTPS TLS EOF原错误保留，随后通过既有SSH同仓库正常推送成功，未改全局配置。保留原提交，不强推、不改历史，不重复向用户索取既有同目标推送授权。

Kafka资源合同`fd2d312`、生成器endpoint缓存`ac5752d`、整数校验分配优化`9743ad1`及原失败组接续`2b40202`已独审、提交并正常推送，没有把组件观察当成50ms违规消失的证据。本次[跨boot接续](evidence/2026-11-01-cap10/formal-group-supersession-plan.md)13项聚焦与89项原检查及独审通过，公共提交`3bf1f87`已推送；原CRLF报告私有保留，仅LF格式修正`4ab583d`已推送、源码不变，云端工具HEAD已同步`4ab583d`。新私有三源副本经过独审、4项纯检查和实际shape核验，原八助手不改。原私有freeze producer保持；新副本只允许在新candidate完整PASS后创建新freeze，不改旧H2结果或累计slot/预算。

本次新boot资源、API、13样本空载、旧失败组独立闭合、新candidate和新freeze两正式均已完成。已按[收尾顺序](../STATUS.md#下一步)完成归档、下载核验与精确清理，不加压力、不重判旧FAIL。新K2-only cleanup副本只在原S25适配器profile白名单增加mysql8-kafka2-v1，原917447闭合/owner备份/S26桥与原missing terminal门不变；本次执行前已实际满足同b9ad两正式PASS、H4后fresh审计、无owner、新namespace及原API/资源身份，并先核下载备份。旧三cohort只核已完成receipt，未恢复token/重复logout，释放前该次清理未删除主库、卷或失败原件。

本批两轮完整180秒已达到稳定至少3000目标，原50ms、零unknown/drop、正式200/500ms、下游及native结束后60秒恢复门槛均保持。候选160/400ms余量通过单列；S26/S27/S28/S29的诊断、fixture、observer和boot差异不能合成纯redo/业务SQL收益倍率。

此前实际已核29295 VUM；本批S29为3703、H3为7799、H4为7596，共19098，累计48393。已购60000相减11607仅为未独立查询账户的计划余量，非余额；warm/准备和平台生命周期计入成本，失败中止也不记为零成本。最终provider原ReportId/EndTime已匹配，分项见[正式原摘要](evidence/2026-11-01-cap10/cloud-like-S29-H3-H4-summary.json)，旧摘要保持原字节/grade。本批目标及收尾已完成，不复制私有配置、Token、IP/PID或云资源ID到公开仓库；不新增负载或开机请求。

旧H1/512、未压4096、前1996已正常logout并删除凭据，该次清理时未删除主库/卷；旧环境/上传的登记JSON保留作审计，核其绑定目标缺失，不要求登记数0。不能复活旧Token。原完整历史、旧指令与核验范围见[交接快照](../archive/docs/agent-handoff-history-20261104.md)、[状态快照](../archive/docs/status-history-20261104.md)和[功能交付快照](../archive/docs/go-delivery-history-20261104.md)，均非当前执行授权。

## 单任务格式与完成证据

G0—G7 是迁移里程碑，不是一阶段一提交。任务范围见方案第6节和本轮第9节的GO-*，在根STATUS展开当前票即可，不另建 tracker/tickets 或第二份总清单。

~~~text
任务ID / 名称：
依赖：真实阻塞的任务ID；无则写无
交付行为：完成后能演示或验证的一个结果
范围：必要的数据/API/页面/worker；不混入独立扩展
验收：对应现有验收ID、关键成功和失败行为
验证边界：公开行为及持久DB/Redis不变式
验证结果：实际命令、PASS/FAIL/未执行、原始证据路径
版本：开始SHA、完整交付SHA；有新改动附清单/diff
状态：待做 / 进行中 / 自测通过待核验 / 已验收 / 受阻并写原因
~~~

例如 GO-010 交付真实 Vue 登录/刷新/退出与失效拦截；GO-030 交付准入到 Stream 成单，不把支付/退款/重建一起隐藏在“交易完成”中。任务如仍过大，可追加 GO-030a 等子任务，并明确依赖和可观察结果。

## 自验与独立核验

库存、状态竞争、重复/乱序、权限和 AI 生命周期缺陷先复现失败窗口再修。测试预期来自规格或独立算例，不能复制内部实现判断。优先通过 HTTP/页面/消息边界验证，必要 SQL/Redis 对账继续保留；HTTP200、无异常、Stub有文字不足以证明完成。

Go 执行适用的 gofmt/go vet/go test；有环境的 Linux CI 跑 race。真实 MySQL/Redis/Kafka 用于约束、Lua、ACK、事务与恢复；mock 不能代替这些证据。真实模型评测与 Stub 的取消/失败/权限协议分别报告。与当前修改无关且已有有效证据的重型实验不重复执行；文档/格式改动不新增形式测试。

独立核验分两方面：需求符合度检查漏做/做错/扩大范围/虚报；正确性审查检查事务、并发、权限、资源、迁移和可维护性。复杂批次可由两个只读审查者分别执行。核验者记录基线/HEAD/工作区及实际执行边界，新版本变化后补差量核验，不能拿旧PASS覆盖新代码。

审查至少覆盖本批已提交、暂存、未暂存和相关未跟踪文件：

~~~sh
git rev-parse HEAD
git status --short
git diff <明确基线> HEAD
git diff --cached
git diff
git ls-files --others --exclude-standard
~~~

本项目沿用 docs/ 规范，尚未初始化 OpenSpec；没有安装 Matt Pocock 整套 skills。这里借鉴其 [任务拆分](https://github.com/mattpocock/skills/blob/main/skills/engineering/to-tickets/SKILL.md)、[行为测试](https://github.com/mattpocock/skills/blob/main/skills/engineering/tdd/SKILL.md)、[双维审查](https://github.com/mattpocock/skills/blob/main/skills/engineering/code-review/SKILL.md)，无需实现 agent 再安装才开工。未来使用 OpenSpec 时显式传入 change 路径，不维护重复任务状态。

## 每个小任务自测后独立 commit（用户已授权）

- 一个提交包含该任务必要代码、测试、迁移和说明；无关格式化、其他任务、核验者报告独立提交。
- 暂存明确文件/hunk，提交前检查 status 和 cached diff，不用 git add . / git add -A 收走其他 agent 修改。
- 消息带 ID，例如 `feat(GO-010): implement login and session expiry`。交付记录写父SHA、完整交付SHA、实际命令与证据。
- 自测后状态是“待核验”；核验失败追加修复提交，不为整理历史 amend/rebase 他人的成果。
- 回退优先可追踪 revert，并核对后续依赖；Git回退不会撤销数据库迁移，单独说明数据兼容/恢复，不删卷替代回退。
- 原始证据按日期/SHA另存，保留失败轮次；不覆盖旧报告，不提交Key、登录Token或完整核销凭据。
- 核验者的修复、适用验证、分步提交及向 origin/main 正常推送已获用户持续授权（2026-10-26）。推送时保留其他 agent 未提交内容，不强推；部署/发布及其他外部操作仍依会话授权。

## 文档优先级

范围/技术栈/顺序以 [总方案](implementation-plan.md) 为准（2026-10-28补充范围见第9节），状态以根 [STATUS.md](../STATUS.md) 为准，docs/README仅保留产品与历史详情。业务规则分别查交易、AI、社区设计；验收条件分别查对应 acceptance。旧文档中的 P/H 编号保留为业务场景标识，旧 Java 类名/启动命令仅作参考，不能覆盖最新 Go 决策。

交付包含环境版本、初始化/迁移/启动/停止命令、验收对应关系、脱敏原始证据、未执行项与限制。不要把短突发受理响应当持续QPS，成功/售罄/限购/429/系统错误分开，受理耗时与最终成单耗时分开。
