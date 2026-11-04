# 工作流与任务状态

更新：2026-11-04。本页是唯一任务状态入口；协作授权见 [长期约定](docs/collaboration-agreements.md)，产品与设计文档见 [文档导航](docs/README.md)。旧票据和执行过程见 [状态历史快照](archive/docs/status-history-20261104.md)，不能作为当前指令。

## 当前目标与操作状态

用户要求取得可写简历的**稳定至少3000成功变更/秒**。本轮仓库归档和源码导航已完成，接下来继续原点赞计划；不新增业务，不用整理、停服或候选失败代替目标完成。

用户已按提示恢复原ECS并提供当前连接信息。已固定原主机指纹接入、核原四CID/镜像/完整挂载/卷/网络和旧清理回执，四依赖按原8CPU/8GiB MySQL等配置正常启动并恢复健康；主库、卷及失败原件保留，未释放或重建。上下文压缩或接续后先读AGENTS、[授权记录](docs/collaboration-agreements.md)、本页、[方案](docs/implementation-plan.md)及[交接](docs/agent-handoff.md)，不重复索取已有授权。

**本次检查点：** 最终c464容量API、新1996关系夹具及`relation-noop-v1`新包已实际部署；PTS三文件核对通过，为4引擎/2000VU。S23、S24均以完整60秒分母观察到约3004.98次实际成功变更/秒，但均为`performance_limited`。S23的P95/P99为266/355ms；S24只将连接池64/64→128/128，P95/P99为198/320ms，API预算通过、候选160ms余量未过，全窗及末15秒下游仍失败。两窗timing/unknown/drop均0、事实终审通过，恢复分别为1.455063/4.183483秒。H2/H3未运行、新正式freeze未建立；这不是稳定3000成绩。

下一单变量为保持pool128、consumer提交批32→64。该API已实际启动，新空载通过60秒/13样本，主机CPU约2.4%、内存约14.5%；S25的authority已通过，候选正在运行，尚未取得结果。空载通过不证明吞吐，后续仍须原完整候选及正式门槛。

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
- observer沿用精确`5c6c8e5`单文件产物，SHA=`3fc4631973775a68878e4e00af8de5a3f5d7a6ef5e222319a53b4b1642591b28`；无内嵌VCS，Go源和模块SHA已与新API干净工作树比对一致。新产物与修复工具已校验复制到同一云机，S23实际使用该API/observer组合，不把工具修复当业务容量收益。
- 八个私有助手v2、九成员源码包及RUNBOOK/五请求模板已离线验证并独审；source set=`8047f15a25f7448890ee781374f6d64de1badef72657e48e127dc0061a52b1e7`。真实boot、旧清理回执SHA、4GiB本次完成证明、最终API环境、新会话、三上传文件及新空载均已有实际原件；八助手字节保持不变。首次失败原件不因显式恢复通过而改写。

## 容量与费用边界

S20—S22实际成功变更速率为2995/2396/1597.333…每秒，均为`generator_limited`、`boundaryEligible=false`。业务正确、unknown/drop=0不抵消时序或下游增长失败；本批仍无有效稳定QPS，正式两轮180秒未跑。旧H1失败、CAP-09失败与旧B1未知均保留，不跨窗口或资源拼PASS。原始数据与限制见[接续规格](docs/evidence/2026-11-01-cap10/cost3000-continuation-plan.md)及[改动与收益档案](docs/evidence/2026-11-01-cap10/offline-improvements.md)。

S23全窗search lag为2027，超过预算1653；末15秒三组收据增长922/950/1237、Kafka lag为631/708/1042，均超过各自预算301.56。原S20的P95/P99为268/358ms、timing134，新S23为266/355ms、timing0：生成器时序问题已在本窗消除，但延迟和下游仍受限。两窗源码、夹具、包及boot不同，不能把SQL6→4或该对照写成容量提升倍率。

S20/S23脱敏原摘要见[云测摘要](docs/evidence/2026-11-01-cap10/cloud-like-S20-S23-summary.json)，保留原grade、不重采样。S24的API预算虽过，候选余量与下游仍失败：全窗三组收据增长5476/4804/5722、Kafka lag为5317/4677/5499，均超各自预算1655.36；末15秒收据增长2952/2728/3240、Kafka lag为2372/2212/2596，均超各自预算301.38。S24详细公开报告后续与新窗口归档，不用观察到的延迟差异替代稳态结论或推算QPS倍率。

后续仍配置3010，以完整60/180秒分母计算，计划发起180299/541499次，约3004.9833/3008.3278次/秒；最终只按真实成功变更计量、两正式取较低值。原能力公式需1890关系，1996关系/4引擎/2000VU可容纳该估算，不保证容量；原50ms、零unknown/drop、候选160/400ms余量、正式200/500ms及下游稳态/恢复门槛保持。

已购60000 VUM，S20—S22六份provider报告合计8704，S23两份实际为1122+2895=4017，已知累计12721。相减47279只是**未扣S24**的计划余量；S24实际消耗待提取，不能称当前余额，账户余额和其它消费未独立查询。下一窗口前按真实报告重核预算，准备noop、屏障及平台生命周期消耗单列；不用全stage墙钟冒充账单。

## 配置试验与下一小票

CAP10-POOL128已实际完成：仅连接池64/64→128/128，consumer32保持；新deployment/进程及新空载通过13样本、CPU约2.6%/内存约13.3%，随后S24结果如上。记录这次配置试验的实际观察，不能宣称它已取得稳定容量。

CAP10-CONSUMER64规格：保持pool128，仅consumer提交批32→64，使用现有配置上限；c464源码及上述API/observer产物、GOMAX8、relay8×32、原资源与双1持久化保持，SQL/ACK正确性门槛不变。新deployment/进程及新空载已通过，S25候选运行中，尚无收益或容量结论。

验收：经原函数显式接线pool128/consumer64 policy并核实际进程配置，不复用前部署身份；配置3010×60秒候选，全部原时序、计数、延迟、下游、事实及native结束后60秒恢复门槛同时通过，才生成同freeze并执行两轮各180秒正式；第一正式失败不跑第二轮。保留新旧原件，若无收益或争用增大如实记录，不降目标或门槛。

## 下一步

1. R1—R4已完成必要核对、独审及分票提交，均已正常同步远端main；原始文件和历史完整保留，不重写Git历史。
2. S23/S24失败保留；不重跑已有有效证据的全套测试，不为文档或现有配置试验重新构建API。S24详细公开报告可后续归档，不阻塞本检查点。
3. CAP10-CONSUMER64新API及新空载已通过，接续S25候选的计量与终审；原三文件/1996关系/4引擎/2000VU保持，先纳入S24真实报告重核费用。
4. 按原authority/runner进行3010候选，只有完整通过后才同freeze正式两轮180秒；保留首FAIL与全部原件，不改门槛。用户此前已授权原计划接续，不因工具本身只准备而额外索取相同许可。

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
