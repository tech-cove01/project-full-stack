# 工作流与任务状态

更新：2026-11-05。本页是唯一任务状态入口；协作授权见 [长期约定](docs/collaboration-agreements.md)，产品与设计文档见 [文档导航](docs/README.md)。旧票据和执行过程见 [状态历史快照](archive/docs/status-history-20261104.md)，不能作为当前指令。

## 当前目标与操作状态

用户要求取得可写简历的**稳定至少3000成功变更/秒**。本轮仓库归档和源码导航已完成，接下来继续原点赞计划；不新增业务，不用整理、停服或候选失败代替目标完成。

**当前指令：暂停云测，明早继续。** 用户已保存新JMX，并要求约10分钟后关闭ECS，本次停机准备截止18:08:40 UTC；今天不再发压、不再调用PTS Start，也不启动bootstrap或新API。停机前只读核验、项目停服和增量证据下载已实际完成；用户已获告知可在控制台选择“停止→节省停机模式”，但ECS实际停止状态尚未获用户回报，agent也无状态查询权限。目标未达成，旧S28候选PASS和H2正式FAIL均保留。

停机回执SHA=`1eed7539a4a9ad329c137b4e6c7652bd7f5740535ef26a30ffbbf699e571062b`：API端口关闭，原四容器均已退出、无OOM；MySQL/Redis的exitCode=0，Kafka/Elasticsearch为143，保留原信号退出码，不写成全0。原CID/卷保留，未删除会话、夹具或数据卷。本地私有增量包69101423字节，SHA=`5623ed8cdeebf10712f8fc68e79fd36460ecf455ebb2159f2d28601dc55a818e`，manifest SHA=`8ad2d5fa8eec5f66a708362423d9b6060810f18255c3a06c71b4cfec6eccaf9b`；独审逐一核1259文件的bytes/SHA、成员集合及路径安全均通过（原manifest另占1成员）。这是选定增量，依赖原2736件基线备份，不是整机镜像；小型authority/slot补包未执行，仍保留于原云盘，明早只读补核，不为补包延长本次云窗口。私有归档不进入公开仓库。

本次此前已固定原主机指纹接入，核原四CID/镜像/完整挂载/卷/网络和旧清理回执，四依赖曾按原8CPU/8GiB MySQL等配置恢复健康；主库、卷及失败原件保留，未释放或重建。API H现已正常停止，新API尚未启动。上下文压缩或接续后先读AGENTS、[授权记录](docs/collaboration-agreements.md)、本页、[方案](docs/implementation-plan.md)及[交接](docs/agent-handoff.md)，以本次暂停指令为准，不用旧接续段继续发压，也不重复索取已有授权。

**本次检查点：** S28配置3010×60秒的完整候选已实际通过：180299次成功变更/完整60秒=3004.983333次/秒，P95/P99为13/52ms，timing/drop/unknown及HTTP错误均0；事实、全窗及末15秒下游、资源和采集均通过，恢复4.732779秒、`childrenConverged=true`。这是候选结果，尚非正式稳定容量。正式freeze已实际创建（SHA=`aac3026375430c276761bcee6b23bd24751f7195dbfa6a7fc1d22f4b4d712230`），H2按同freeze配置3010×180秒，但已实际中止失败，H3不启动。最终c464 API、1996关系/4引擎/2000VU及原门槛保持，稳定3000的两轮正式目标尚未证明。

S25在pool128、consumer提交批64下已中止：原结果为`correctness_stop`、`batchFatal=true`、`childrenConverged=false`，保持原值。已接受raw中有22068次mutation及1次drop；这不是完整窗口，不计算QPS或P95。一次实际成功HTTP耗时719ms，超过同relation约663ms的调度周期，下一次来不及启动而drop；后续overlap拒帧及原unknown缺失保留。被拒batch未保存完整body，不能推断为“假overlap”。

S25独立真实闭合已通过：原1996关系完整reconcile、两个签名provider EndTime及全部旧PID不存在均已核实，备份原owner字节后仅释放该锁。它不修复旧成绩、不将原`childrenConverged=false`改成true，也不新造terminal。后续S26以同c464、pool128、consumer32、Like诊断开启配置完成3010×60秒诊断：180299次成功变更/完整60秒=3004.983333次/秒，P95/P99为216/333.02ms，drop/timing/unknown均0；API及下游预算失败，原结果为`performance_limited`。事实收敛、原`childrenConverged=true`、恢复6.038893秒及负载闭合均已核实，不是正式容量成绩。

当前已在同一MySQL实例在线将redo容量100MiB→2GiB，配置及验证执行约1.7秒，requested与effective均为2147483648、resize状态OK；没有重启、持久化配置或放宽双1。原诊断API已正常停止，UUID/版本文件只读补证通过，原resize证明不改写。新1996关系夹具及同维度新包已准备，三上传MD5与旧包相同且新namespace只读验证通过，无需用户重新上传。诊断关闭、pool128/consumer32最终API及新60秒/13样本空载通过（CPU中位约2.7%、内存最高约15.9%）。S27实际在warm 250次成功变更后、stable启动前失败：setup原60秒内未匹配同事实摘要，`correctness_stop`、`batchFatal=true`、`childrenConverged=true`；最终facts/derived/downstream对账通过，但没有stable主窗或其资源样本，不能报告主窗QPS/P95或redo收益，该次未进入正式窗。

根因已用原事实文件只读闭环为observer顺序错误：Node和ready摘要按manifest顺序，Go live摘要按内部NoteID词典序；新夹具ID跨十进制位数时，两种顺序不同。在初始版本全0、控制摘要一致且事实未变时，原manifest顺序hash精确匹配ready，词典序hash精确匹配live。原S27 FAIL不追认。顺序修复`b461ec6e9ef60d88176fc199e0bc853cec1e5a66`已提交、正常推送并部署新observer；业务API c464及其原二进制不变，不把API的c464 CI说成新工具CI。

`CAP10-PRESTABLE-CLOSURE`公共闭合入口已本地提交`bb9b0cd85627f622d10eb504bb1aebfc1747abb4`，8项聚焦检查及独审通过，Git bundle已实际部署。原S27布局的只读检查、真实warm签名EndTime/全owner退出和失败后新鲜完整对账均通过，S28原完整authority已实际通过；不回写S27 FAIL，也不造stable报告。新observer空载通过13样本（CPU中位约2.75%、内存最高约16%），实际14条摘要保持manifest顺序一致。S28完整候选通过的[脱敏原摘要](docs/evidence/2026-11-01-cap10/cloud-like-S28-summary.json)已下载并核SHA，保留candidate身份、完整分母与原grade，不改成正式成绩。

H2预热250次成功变更后进入主窗，实际supervisor停止由Kafka容器1GiB内存达到85%保护线触发；在此之前约native第4.7秒，PTS已有4条独立调度违规，偏移为50.10/52.40/54.18/52.87ms，超过原50ms门槛，不能全部归因于后来的Kafka内存停止。已接受raw为32463次mutation、drop为0，但unknown/classificationUnknown为null且非exact；未完成180秒，不计算正式QPS或P95/P99。原结果`correctness_stop`、`batchFatal=true`、`childrenConverged=true`、`factsConverged=true`、`failedStartOrException=null`保持，最终facts/derived/downstream对账通过，不抵消正式失败。Kafka未OOM，H3不运行。

Kafka配额修复已由root实际执行并验证：同CID内存1GiB→2GiB、MemorySwap 2GiB→3GiB，保留原额外1GiB swap allowance；变更时四依赖PID未重启，原mysqlActual与redo2GiB证明保持，随后按暂停指令正常停服。新显式profile为`mysql8-kafka2-v1`，资源metadata SHA=`0319e92dc2a89014f985629f5e83261c411ba523e36611b899516cbcf271b298`。这只证明该boot运行时资源变更完成，尚无新API、新空载或新candidate；不能称它已解决调度抖动或取得容量收益。用户已保存的新JMX及另两上传文件已通过真实provider只读MD5核对，provider处于idle，未调用Start/Stop；新JMX SHA=`1c81410efde56af9e034508657d2f582626ca57d2b1bb569fbd8130a480003b1`，新包/准入仍须绑定明早的实际环境。新权限已用于读取旧H2四引擎INFO日志，各只取首100行且total更大，无启动/GC命中；不能据部分日志排除GC或认定抖动根因。

远端同步：`b461ec6`及`bb9b0cd`均已实际正常推送；`bb9b0cd`先前多次HTTPS TLS EOF原错误保留，随后通过既有SSH同仓库推送成功，未改全局配置、不强推或覆盖历史。既有同目标推送授权保持，不重复向用户索取授权。

后续三张独立工具票已审查、提交并正常推送：Kafka资源合同`fd2d312`、生成器endpoint缓存`ac5752d`、整数校验分配优化`9743ad1`。它们不证明H2的GC或抖动原因、也不构成新正式成绩。失败正式组接续入口`2b402020e2f0076e32d560937ad03633d47bbc9f`也已独审提交并实际正常推送，原私有freeze生产器保留、新副本不执行。云端工具仍为`ac5752d`，明早须同步新工具并核源/产物。新组仍须真实旧组闭合、新资源/部署及完整candidate PASS，不能重跑旧freeze的H3补pair或把H2改判为退休/PASS。

两项准备失败原件保留：BOOT首FAIL的瞬时原因未精确取证，不补称确定原因；后续同boot、原CID/事实、账号认证与权限的只读复核及显式最终化通过，不重跑resize/CREATE。authority准备的H1/S22前后报告同名`FileExists`已精确定位；原H1退休证明经实际public grade通过，以同助手显式新attempt完成准备，不重复assembler/API/idle。八个助手源码字节未改，恢复通过不改写首FAIL。

pool128显式接线使用独立三源副本，只有authority的两处报告路径加入原command标签以避免同名，其余两源字节沿用原件；原八助手不改。准备时将控制台紧凑JSON的SHA当作原pretty结果文件SHA，已在写工作目录/inputs前拒绝；之后使用实际文件SHA经原有CLI接续，未改原driver源码或复写失败。

## 本次仓库整理

| 小票 | 范围与验收 | 当前阶段 |
| --- | --- | --- |
| R1 / REPO-ARCHIVE-WINDOWS-BUILDS | 14个已跟踪Windows构建产物移入archive，逐文件SHA不变；不改Go源码 | 已独审提交`579f12fbb4cb74aeabd43724230875f015916281` |
| R2 / REPO-ARCHIVE-CAP06-08 | 12个旧CAP-06—08工具整组归档，原字节和内部相对关系保留；CAP-10工具不动 | 已独审提交`a98a0642953b6eeda1bfa41783578b089aa69e40` |
| R3 / REPO-DOC-NAV | 根状态、交接与文档导航收敛；完整历史归档，重算相对链接并保留旧锚点 | 277个本地链接及6个锚点核对、差异检查和双独审通过，提交`fc7716a2383dd9dbacc01ad683cb6cc20403684b` |
| R4 / REPO-SOURCE-MAP | 校准26个后端模块与前端route/view/API阅读路线，增加后端、前端、脚本和归档目录入口 | 源码路线112处链接、目录导航81处链接通过，双独审提交`ce3c3e549bbf44f195197588b933f48efac149a3`；未重排包或改业务 |

产品首页README继续只展示项目、功能、技术与运行方式，不加入状态或压测入口。归档不移动[原始证据](docs/evidence/)或[核验报告](docs/reviews/)，不改历史FAIL、命令、原件SHA及CAP-10固定路径。

## 已交代码、工具与最终产物

- 业务源码为`c464c1fa4d153806fb44ac746312552655ebda63`，已正常推送；全套普通Go测试、vet及[精确四项CI](https://github.com/tech-cove01/project-full-stack/actions/runs/41828027736)通过。
- Like现有关系变更的业务SQL实测6→4，BEGIN/COMMIT/隔离SET仍各1。公共正确性2顶层/16子例通过；首私有计数整矩阵FAIL的93件原件保留，独立接续8次计数通过的87件原件另存，不合成一次全通过。机制与计数见[Like事务规格](docs/evidence/2026-11-01-cap10/like-tx-rpc-plan.md)。
- Done点驱动`aeb0c96`已交N≥8路径，N1—7保留原路径；32条事务内完成语句32→3，COMMIT仍一次。无符号计数clamp由`f32bc31`单独修复。相关真实MySQL回归与独审通过，组件改善不等于3000容量。
- 生成器三票`a4ca69b`/`c061153`/`09053eb`已交同线程同HTTP节点的独立noop准备、持久确认及Start前协议绑定。17关系/3个本机JMeter进程与准备503反例通过，属于替身接线；S23真实四引擎已完成准备与主窗，timing为0。每phase新增1996次noop，warm+stable合计3992次单列成本，不计入成功变更吞吐。
- 最终Linux API实际版本为`capacity-c464c1fa4d153806fb44ac746312552655ebda63`，SHA=`4565c2422d24ac5ea34ac180890ec80ff767e8084d765f4eb9a809d9078516d1`；正式manifest SHA=`3f8f50b9cd5e7ca28c50a0e8d3fda030778ad6f3df05d628f69db0e491b1e9a2`。原始成功CI响应SHA=`e8cb25f9873cefd54529e0444f9febd76a2553efc2f33d20e424d43b4e626d05`。首产物b5d6缺capacity版本前缀、1b877不是部署schema，均拒用并保留；只重建产物，未重跑已通过的测试。
- S23—S27原observer为`5c6c8e5`单文件产物，SHA=`3fc4631973775a68878e4e00af8de5a3f5d7a6ef5e222319a53b4b1642591b28`，旧原件保留。S28改用顺序修复后的单文件observer，SHA=`b83da5965ce71453fbe068a342f107f02d42eff056c46cd7bb8aac055b15f1d6`；单文件无内嵌VCS，工具源/实际产物另绑，不将c464 API CI当作新observer CI或容量收益。
- 八个私有助手v2、九成员源码包及RUNBOOK/五请求模板已离线验证并独审；source set=`8047f15a25f7448890ee781374f6d64de1badef72657e48e127dc0061a52b1e7`。真实boot、旧清理回执SHA、4GiB本次完成证明、最终API环境、新会话、三上传文件及新空载均已有实际原件；八助手字节保持不变。首次失败原件不因显式恢复通过而改写。

## 容量与费用边界

S20—S22实际成功变更速率为2995/2396/1597.333…每秒，均为`generator_limited`、`boundaryEligible=false`。业务正确、unknown/drop=0不抵消时序或下游增长失败；当时正式两轮180秒未跑，当前也尚无有效正式稳定QPS，H2失败见上方检查点。旧H1失败、CAP-09失败与旧B1未知均保留，不跨窗口或资源拼PASS。原始数据与限制见[接续规格](docs/evidence/2026-11-01-cap10/cost3000-continuation-plan.md)及[改动与收益档案](docs/evidence/2026-11-01-cap10/offline-improvements.md)。

S23全窗search lag为2027，超过预算1653；末15秒三组收据增长922/950/1237、Kafka lag为631/708/1042，均超过各自预算301.56。原S20的P95/P99为268/358ms、timing134，新S23为266/355ms、timing0：生成器时序问题已在本窗消除，但延迟和下游仍受限。两窗源码、夹具、包及boot不同，不能把SQL6→4或该对照写成容量提升倍率。

S20/S23脱敏原摘要见[云测摘要](docs/evidence/2026-11-01-cap10/cloud-like-S20-S23-summary.json)，保留原grade、不重采样。S24的API预算虽过，候选余量与下游仍失败：全窗三组收据增长5476/4804/5722、Kafka lag为5317/4677/5499，均超各自预算1655.36；末15秒收据增长2952/2728/3240、Kafka lag为2372/2212/2596，均超各自预算301.38。S24详细数据见下方原摘要，不用观察到的延迟差异替代稳态结论或推算QPS倍率。

S24/S26的[脱敏原摘要](docs/evidence/2026-11-01-cap10/cloud-like-S24-S26-summary.json)已从实际输出复制并核SHA一致，分别保留candidate/diagnostic身份、失败grade及provider VUM，不将诊断窗口改成正式成绩。

后续仍配置3010，以完整60/180秒分母计算，计划发起180299/541499次，约3004.9833/3008.3278次/秒；最终只按真实成功变更计量、两正式取较低值。原能力公式需1890关系，1996关系/4引擎/2000VU可容纳该估算，不保证容量；原50ms、零unknown/drop、候选160/400ms余量、正式200/500ms及下游稳态/恢复门槛保持。

已购60000 VUM，实际已核S20—S22合计8704、S23为4017、S24为1046+2761=3807、S25为2061、S26为928+2940=3868、S28为1097+2738=3835，另从已闭合原报告核得S27预热950、H2预热978+stable1075=2053，累计29295。相减30705只是**不含未来消费的计划余量，非账户余额**；账户余额和其它消费未独立查询，中止窗口不记为零成本。准备noop、屏障及平台生命周期消耗单列，不用全stage墙钟冒充账单。

## 配置试验与下一小票

CAP10-POOL128已实际完成：仅连接池64/64→128/128，consumer32保持；新deployment/进程及新空载通过13样本、CPU约2.6%/内存约13.3%，随后S24结果如上。记录这次配置试验的实际观察，不能宣称它已取得稳定容量。

CAP10-CONSUMER64曾保持pool128，仅consumer提交批32→64，源码、GOMAX8、relay8×32、原资源与双1持久化不变。S25原中止与独立闭合分别保留，不能由局部成功计数得出收益或容量结论。

CAP10-REDO2G仅调整同机`innodb_redo_log_capacity`，保原4GiB buffer、8CPU/8GiB MySQL、io_capacity 10000/max20000、双1、delay0、doublewrite、源码c464、relay8×32及consumer32。S27因工具错误未进入stable；S28换修复后observer、独立空载/准入后完整候选已通过，但H2正式窗失败、H3不运行，正式容量尚未证明。S26诊断开启、S27关闭且换新夹具，S28又修复observer，不能将跨窗差异写成纯redo倍率。机制、实际配置与限制见[redo试验记录](docs/evidence/2026-11-01-cap10/offline-improvements.md#11-05同机redo容量试验收益待验证)，工具闭合边界见[规格](docs/evidence/2026-11-01-cap10/prestable-closure-plan.md)。两轮各180秒及所有门槛不变，不拿候选替代正式。

## 下一步

1. R1—R4已完成必要核对、独审及分票提交，均已正常同步远端main；原始文件和历史完整保留，不重写Git历史。
2. S23/S24失败、S25原中止及独立闭合、S26诊断受限与闭合、S27工具setup失败分别保留；不重判旧FAIL，不为工具修复重新构建未改的业务API。
3. 停机前只读核验、选定增量备份与精确停服已完成并有上述原件；等待用户回报ECS节省停机状态，不再SSH/Start/bootstrap或负载。保主库、卷、凭据归属和全部失败原件，authority/slot小元数据明早只读补核。S28候选通过不替代两正式组，H2原FAIL保留、旧组H3不启动。
4. 明早恢复原ECS后先核新boot及原CID/卷、实际资源和MySQL事实。4GiB buffer与2GiB redo都是运行时设置，MySQL/主机重启后须实测并按既有流程重新证明，不能沿用本次idle、authority或freeze。Kafka2GiB也须按实际cgroup复核，不能只凭旧metadata；现失败组接续门绑定本次同boot资源链，跨boot证明接线仍须先核，缺证据继续阻断，不改写旧证明。
5. 再按新资源配置及已审工具完成新API、60秒/13样本空载和显式失败组闭合证明，再经完整candidate通过后建立新freeze；从实际unused H标签建立两轮180秒正式组，第一FAIL终止该组。原50ms、unknown/drop、200/500ms及下游/恢复门槛全部不变，不拼旧窗口。
6. 已审小提交继续按已有授权正常同步，保留原TLS失败，不重复询问已有推送许可；最新工具提交`2b40202`已实际推送，停机前原件及私有备份验证见本页顶部。

## 权威入口

| 要查什么 | 入口 |
| --- | --- |
| 持续约定与具体授权 | [协作记录](docs/collaboration-agreements.md) |
| 当前执行交接与小提交规则 | [agent-handoff](docs/agent-handoff.md) |
| 产品/模块总方案 | [implementation-plan](docs/implementation-plan.md) |
| 新boot步骤与当前限制 | [接续清单](docs/evidence/2026-11-01-cap10/offline-resume-checklist.md) |
| 优化、未采用研究及证据 | [收益档案](docs/evidence/2026-11-01-cap10/offline-improvements.md) |
| 当前产品与源码阅读 | [文档导航](docs/README.md)、[源码路线](docs/source-walkthrough.md) |
| 历史票据与当时指令 | [状态快照](archive/docs/status-history-20261104.md)、[交接快照](archive/docs/agent-handoff-history-20261104.md)、[功能交付快照](archive/docs/go-delivery-history-20261104.md) |
