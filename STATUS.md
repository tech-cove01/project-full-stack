# 工作流与任务状态

更新：2026-11-04。本页是唯一任务状态入口；协作授权见 [长期约定](docs/collaboration-agreements.md)，产品与设计文档见 [文档导航](docs/README.md)。旧票据和执行过程见 [状态历史快照](archive/docs/status-history-20261104.md)，不能作为当前指令。

## 当前目标与操作状态

用户要求取得可写简历的**稳定至少3000成功变更/秒**。本轮仓库归档和源码导航已完成，接下来继续原点赞计划；不新增业务，不用整理、停服或候选失败代替目标完成。

用户已按提示恢复原ECS并提供当前连接信息。已固定原主机指纹接入、核原四CID/镜像/完整挂载/卷/网络和旧清理回执，四依赖按原8CPU/8GiB MySQL等配置正常启动并恢复健康；主库、卷及失败原件保留，未释放或重建。上下文压缩或接续后先读AGENTS、[授权记录](docs/collaboration-agreements.md)、本页、[方案](docs/implementation-plan.md)及[交接](docs/agent-handoff.md)，不重复索取已有授权。

**本次真实接续进度：** 已校验安装28件API/observer/输入原件与9成员助手包，云端工具从实际0ce8非强制接续到已审ce3。BOOT首次执行在observer-account阶段以ValueError拒绝，原失败与部分状态保留：本boot缓存128MiB→4GiB已有实际完成原件，新观察账号和受限SELECT权限已建立，凭据及登记在CREATE前持久保存。首次最后复核未保存其失败瞬间字段，不补称精确原因。独审后的独立恢复sidecar已实际通过：同boot/原CID/完整原事实、账号认证和精确权限、两次连续空闲0均核实；不重跑resize/CREATE，单独发布新资源证明并显式绑定首FAIL。新容量API已健康启动，新1996关系夹具和1996/4引擎/2000VU新三文件已生成，`relation-noop-v1`准备协议已绑定；等待上传新包、最终API重绑及本boot新空载。尚未预约或发压，仍无稳定3000成绩。

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
- 生成器三票`a4ca69b`/`c061153`/`09053eb`已交同线程同HTTP节点的独立noop准备、持久确认及Start前协议绑定。17关系/3个本机JMeter进程与准备503反例通过，属于替身接线；真实四引擎尚待新包验证。每phase新增1996次noop，warm+stable合计3992次单列成本。
- 最终Linux API实际版本为`capacity-c464c1fa4d153806fb44ac746312552655ebda63`，SHA=`4565c2422d24ac5ea34ac180890ec80ff767e8084d765f4eb9a809d9078516d1`；正式manifest SHA=`3f8f50b9cd5e7ca28c50a0e8d3fda030778ad6f3df05d628f69db0e491b1e9a2`。原始成功CI响应SHA=`e8cb25f9873cefd54529e0444f9febd76a2553efc2f33d20e424d43b4e626d05`。首产物b5d6缺capacity版本前缀、1b877不是部署schema，均拒用并保留；只重建产物，未重跑已通过的测试。
- observer沿用精确`5c6c8e5`单文件产物，SHA=`3fc4631973775a68878e4e00af8de5a3f5d7a6ef5e222319a53b4b1642591b28`；无内嵌VCS，Go源和模块SHA已与新API干净工作树比对一致。新产物与修复工具已校验复制到同一云机，API已运行，新生成器尚未在PTS发压。
- 八个私有助手v2、九成员源码包及RUNBOOK/五请求模板已离线验证并独审；source set=`8047f15a25f7448890ee781374f6d64de1badef72657e48e127dc0061a52b1e7`。真实boot、旧清理回执SHA、4GiB本次完成证明、新bootstrap PID、新Token和三上传文件均已取得；最终API环境、PTS实传核对及新空载待完成。首次失败原件不因显式恢复通过而改写。

## 容量与费用边界

S20—S22实际成功变更速率为2995/2396/1597.333…每秒，均为`generator_limited`、`boundaryEligible=false`。业务正确、unknown/drop=0不抵消时序或下游增长失败；本批仍无有效稳定QPS，正式两轮180秒未跑。旧H1失败、CAP-09失败与旧B1未知均保留，不跨窗口或资源拼PASS。原始数据与限制见[接续规格](docs/evidence/2026-11-01-cap10/cost3000-continuation-plan.md)及[改动与收益档案](docs/evidence/2026-11-01-cap10/offline-improvements.md)。

后续建议配置3010，以完整60/180秒分母计算，计划发起180299/541499次，约3004.9833/3008.3278次/秒；最终只按真实成功变更计量、两正式取较低值。原能力公式需1890关系，1996关系/4引擎/2000VU可容纳该估算，不保证容量；原50ms、零unknown/drop、候选160/400ms余量、正式200/500ms及下游稳态/恢复门槛保持。

已购60000 VUM，六份S20—S22 provider报告合计8704，用户本轮确认消耗约为此数。按此计划约余51296 VUM，预计够原候选与两正式，当前无加购必要；**实际账户余额和其它消费未独立查询**，减法不是账单证明。准备noop、屏障及平台生命周期消耗单列，依真实新报告持续核预算，不再新增探档或用全stage墙钟估算冒充账单。

## 下一步

1. R1—R4已完成必要核对、独审及分票提交，均已正常同步远端main；原始文件和历史完整保留，不重写Git历史。
2. 原ECS已接通，25个助手/公共依赖SHA、最终API及原资源身份/清理事实已复核，BOOT显式恢复和APP/bootstrap/新夹具/新包已实际通过。不重跑已有有效证据的全套测试，不为整理重新构建API。
3. 交付新三文件供用户替换保存；核PTS实际文件和引擎准入，重绑最终API环境，执行全新60秒/13样本空载，不复用跨boot旧idle或旧Token。
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
