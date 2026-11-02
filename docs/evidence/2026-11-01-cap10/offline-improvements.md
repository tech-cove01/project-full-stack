# CAP-10 改动与收益档案

更新：2026-11-03。唯一任务状态见根 [STATUS](../../../STATUS.md)。用户目标为接近5000稳定成功QPS；当前转入离线优化，没有本批180秒正式成功组。以下把代码、部署资源和测量工具分开，不将目标或离线回归写成吞吐收益。

## 已取得的证据

| 项目 | 改动与提交 | 前后证据及结论 |
| --- | --- | --- |
| Claim访问路径 | `7a0c240`：活跃状态虚拟列与索引，固定索引访问；[原规格](claim-access-path-plan.md) | S5→S6同800/s：Claim平均扫描21380.198→0.965509行/执行、SQL平均11.030663→0.129033ms。累计统计包络130.930/145.153秒，含空轮询/启动/排空，不是纯60秒或端到端QPS倍率。两个窗口下游都未通过，接口P95/P99反而53/95→129/185ms，不能称接口延迟改善。 |
| relay有界并行 | `7aec57e`：默认单路，配置8路独立循环、共享原SQL池及同步Writer；[原规格](relay-worker-pool-plan.md) | 有取消收敛、64事件/8路真实独立空库Claim等正确性证据。紧邻S7计量/恢复失败，不能用它给出可靠的单项吞吐倍率。 |
| offset与LIKE忽略收据微批 | `ad09dcb`、`3135d38`：受控批次32，保留每事件收据与hash/state、重建fence及同步Kafka提交 | 有效组合对照S8→S12同400/s×60秒、128MiB Buffer Pool：各24000真实变更；P95/P99 95/142→4/7ms，三组收据保守增长7577/7642/8880→各26，恢复32.650493→2.497471秒。不是单项收益；表历史继续增长、发压前准入修复也披露，主窗观察合同不变。 |
| Claim隔离级别 | `6e9ef17`，后续测试guard修复`8e855a7`：只有Claim事务显式RC，全局RR不变 | 独立空库真实暂停Claim回归：旧RR下另一owner Done需等gate释放，约503.6ms；RC路径约1ms且gate仍持有。租约/attempt fence/重试/ACK不变。最终容量还同时使用512MiB Buffer Pool，不能把容量差异全部归于RC。 |
| MySQL缓存配置 | Buffer Pool由128→512MiB，仍4CPU/2GiB容器；flush1、sync_binlog1、组提交延迟0及全局RR不变 | 属于部署配置调整。最终RC/512MiB与上述旧128MiB代码对照是两个独立实验，不合成一个因果提升倍率。 |
| 最终旧部署容量观察 | 实际业务版本`8e855a7`、SQL池20/10、relay8、consumer批次32 | S19 800/s×60秒完整48000变更、P95/P99=81/128ms、0unknown/drop/timing、下游通过。S16 1000/s接口60000变更/108/158ms通过，但Outbox保守增长6068、收据各6069，下游未稳态。没有稳定破千或5000结论。 |

S9/S17的发生器时序违例、S10/S13的采集错误、S11/S14/S18/H1等启动/观察失败保留原判；事后完整对账不能消除原unknown或补造原生终态。H1预热0HTTP、没有stable，仍是失败预约。旧CAP-09与B1结论不覆盖。

## 当前离线小票：CAP-10-MYSQL-POOL-1

**问题与基线。** S16真实1000/s主窗内部约55秒，SQL池等待次数增加29416、并发累计借池等待711.713秒，MaxIdleClosed增加8566，IdleTimeClosed/LifetimeClosed不增。旧配置把MaxOpen=20、MaxIdle=10写死，归还后的突发连接被空闲上限关闭；同一较宽SQL包络的SET NAMES新增9239次是重建线索，不能据此计算准确耗时。

**已交付改动。** 提交`a4c323115e1d92cd36037aa3cff4d827842c84d7`：Open仍默认20，Idle默认跟随Open（默认变20）；增加`GO_MYSQL_MAX_OPEN_CONNS`（1–128）及`GO_MYSQL_MAX_IDLE_CONNS`（0–Open）。配置错误在访问依赖前拒绝。DSN、Ping/context、30分钟连接寿命、事务、持久化与ACK规则不变；两份env示例明确Go需要显式导出变量。

**离线验证。** config/database两包test与vet通过，双独立审查通过；随后Go全套普通`go test ./...`与`go vet ./...`通过。使用实际`database/sql`内存driver借满并归还连接，核默认20连接保留、较小Idle的真实MaxIdleClosed、禁用空闲池以及满池取消。另在独立本地MySQL/Redis/Kafka完成原链路集成11顶层通过、2项fresh-topic测试因未配置其专用环境跳过；不触碰原服务或云端。该次20份原件manifest SHA256为`7c84f9da1077bdd338ee622197f0e336727bd28416d325045794c2d6fb2e6eac`。以上均非容量成绩。

**待验证收益。** 当前没有新的真实MySQL/PTS吞吐或P95/P99。预期是减少无谓建连和空闲上限关闭；64/64仅为未测配置候选。下一次若做单项对照，使用同一新源码/工具/资源/场景，显式20/10与20/20，比较主窗MaxIdleClosed、连接创建、借池等待、API分位及下游；扩CPU/改Open池另列配置实验，不和Idle修复混算。

## 已交付驱动往返优化：CAP-10-MYSQL-INTERPOLATE

提交`8d580b92fb01f91d98496c13917299bcce819f55`增加严格true/false的`GO_MYSQL_INTERPOLATE_PARAMS`，默认false。启用时使用锁定mysql driver 1.8.1自带参数插值，不自行拼接业务SQL，不打开multiStatements或改变RowsAffected、utf8mb4、时区、超时、事务与提交规则。API与三个共用MySQLDSN的Go恢复/容量CLI受此开关影响；独立Go observer自行构造DSN，不把它的SQL开销算入本票收益。

两包普通test/vet及双独审通过。独立真实MySQL原资源上3顶层+8子例、0FAIL/0SKIP：两种sql_mode各6条参数化语句，prepare/execute/close计数分别6/6/6→0/0/0，实际SQL操作和值仍完整完成；过client packet预算和含literal问号两种情况仍正常回prepared路径，计数2/2/2不减。引号、反斜杠、NUL/中文/emoji、全字节、uint64、时间、有限浮点、NULL及事务回滚均核对。锁定驱动的`json.RawMessage(nil)`在两种协议下存在NULL/空串差异，已实际留证，不能泛称任意Go值完全等价；当前业务outbox先规范化再转string，活动审计传非nil Marshal结果。该次私有manifest SHA256=`fb1afde5698651384177bcce0b04d5f85fe0876ce0f4ea9c12017344dd83426f`。

另显式true运行原实际GORM→SetLike→事实/计数/Outbox/Redis路径：ExpectedStateLike与CommitCancellation共2顶层+3子例通过，旧IT库12表和Redis结束均0，原1225500历史未写入、自有Redis正常停止。以上是协议操作减少与业务回归，没有新QPS或P95/P99收益，不能把prepare归零说成SQL零调用。

## 已交付仓储微批：CAP-10-RELAY-BATCH-1

提交`c5d0a2a6fe4b5323243acb7ff403e0162c99eb84`增加最多64条的ClaimBatch/DoneBatch仓储接口。认领保留显式RC、活跃索引、SKIP LOCKED、30秒租约、逐条attempt和8次上限；完成事务逐条核对原attempt/lease fence，任一失效全批回滚。后续`03a7781`限制256KiB保守字节前缀，`3c26e83`提供每lane独占同步Writer，`c744072`接ACK/完成/重放，`bf74d58`接应用配置，默认批次1仍走原路径；详见[微批设计与取舍](relay-microbatch-plan.md)。

直接普通测试/vet、双独审通过；独立真实MySQL5顶层+2失效子例通过，覆盖资格/耗尽停车、取消回滚、失效中间项全批回滚、RC锁冲突、8路256条唯一认领。该次20份原件manifest SHA256为`ccf355ffb6cb929c23c050f0aef2ce7b5e04b607a8bd249b1598c38cbbd7db5b`。仅为仓储正确性证据，尚无真实吞吐或延迟改善。

字节前缀真实4顶层+2子例通过，20件manifest SHA256=`4226d9cae4cffdc22f03eee4ee5f6c800b871725aa573d9a26c5a6e37a09d705`；runner真实MySQL/Kafka4顶层通过，20件manifest SHA256=`a9c3f4e3463303ccdeac3c387027cb7eebd2ecd9e4db0bfe7944a2b7c3f4f627`。各次均验证原资源保护、只停本轮自有3CID、卷保留。后端完整普通test/vet通过，包含该后端的精确`ab2d4cc260bebb223988aae5ed19f6148c58c141`[远端CI通过](https://github.com/tech-cove01/project-full-stack/actions/runs/41490639199)。均不等于5000容量通过。

额外DoneBatch set UPDATE研究因真实N64扫描计划失败而未采纳，原失败与候选源码全部存档，当前源已恢复原逐条fence；没有包装成修复或收益。细节与SHA见上述设计文档。

## 已交付严格解码复用：CAP-10-LIKE-DECODE

提交`c084636db64e4309c9ce2db11ef81afbcb1afd98`消除Follow/Feed忽略LIKE批次中第二次严格JSON解析。首次校验仍返回同一次解码的事件，源字段、canonical hash、坐标、重复事件、重建锁、收据事务及Kafka ACK规则不变。直接正反例、输入隔离、相关包race/vet及双独审通过。

同一个新基准程序比较原两次解析与复用：三次样本中位25690→12720ns，6979→3490B、146→73次分配，分别约减少50.5%解析耗时和一半分配。另保留旧程序三样本中位23559ns，不跨程序挑最好值拼倍率。该结果只表示事件解析组件，不能写成接口QPS翻倍；无新业务吞吐。私有基准报告SHA256=`76ba74b390a4f463f3441be9e5a8453c335c64e08861a4b93a425978dbc90189`。

## 测量工具改动独立归档

`0126c0c`与`0cd6316`处理真实零原生取消、进程birth和组合脚本标识；`4f6446a`将JMeter双引擎就绪等待90→300秒，并在稳定主窗末100ms停止计划新的发起；`fdc409a`同步phase准入路径/等待保护，`3f1a919`保留零原生正式启动失败并按新freeze限定两次完整正式窗。它们不是Go业务优化。

原生主窗仍完整60/180秒，50ms迟发、单关系单在途、完整原始计量、零unknown、下游与60秒恢复标准保留。目标5000/s时180秒计划上限899500次，实际最高4997.2222次/s，不能把目标值写成实际成功QPS。新JMX已由用户保存且真实只读准入通过，但整套新工具尚未完整云部署或取得新负载成绩。

## 后续较大链路改动的记录要求

每项在实施前写清旧路径、瓶颈证据、候选解释与可检验预测；实现按小任务提交，保留迁移/回退条件。实施后记录实际源码和二进制SHA、配置、资源、关系池/引擎数、初态和数据历史、主窗边界、全部成功失败与完整原件SHA。

对照必须分别列HTTP实际状态变更吞吐、完整P95/P99、事实/计数/Outbox对账、发布前积压、消费者收据与Kafka lag、停压恢复，以及CPU/连接池/SQL计数的真实观察范围。SQL累计包络、并发累计等待、采样队列不能伪称单请求耗时或瞬时零积压。

若改变同步确认、持久化、租约/attempt fence、幂等或消费者ACK语义，明确指出新旧契约及其代价；不得用Redis异步受理成绩替代原同步MySQL写入成绩。资源扩容、代码改动与发压器变更各自披露；未复测的收益只记“待验证”。

5000准备还需解决512关系池每102.4ms重用与长尾的冲突，不能只调PTS并发数字。维度纯合同`da3058a`已保留历史512/2引擎/513VU，新增显式4096/9/4105及失配拒绝，后续统一fixture/生成器/分片/计量/启动关闭已接线。4096关系在5000/s每819.2ms重用是发压配置，不能保证所有长尾都无迟发，更不是机器并发上限或5000容量保证。

关系池/生成器/collector/phase/资源帧/历史grade现已分别接显式维度并分票提交。实际本地JMeter17关系/3进程/20假PUT接线通过，三个JVM退出0；此前两次受限环境在启动阶段0PUT失败保留，不能归因安全软件。合成899500记录的完整180秒模型核对通过，并提前发现live全量分位重算的最长1.23秒临界区：`23d80b3`/`3228e43`将运行中观察改为精确已落盘计数、最终仍全raw R7，`ab2d4cc`去掉已限定不可变标量记录的逐字段递归复制。同harness低基数组合观察由111.6009→25.1936秒、summary峰1234.061→4.6817ms；这是工具本机模型，并非业务QPS。单帧峰137.9612ms仍超时基风险门槛，高基数模型也发现119.0791ms，故继续限制遥测帧并验证9路HTTP并发，尚未宣布全部发压准备通过。

资源合同`e8d4def`、生命周期`0ba0b4a`与历史绑定`c0d1158`允许显式候选mysql8-v1（MySQL8CPU/8GiB，其他依赖保持原上限），使用新environment-12hex文件保留原metadata SHA；旧缺path始终原environment.json。只有准备代码，没有实际扩资源或新profile准入。新资源必须先完成独立版本化空载证据，不能借旧4CPU预检冒充。

版本化空载`5f6ccac`已接run/grade/phase：新metadata使用独立idle目录与原文件SHA，保留旧失败、已用槽位及旧资源记录；早期初始化失败也会释放本次精确PID/birth的owner锁，保留失败命名空间。纯检查和19份历史原件绑定通过，无新的真实空载观察。

后续工具票`73dd408`把9路HTTP监听排队显式设为64，`974a5cd`将每个遥测帧限制为128条，仍每5秒发送原快照、同body/seq重试、仅ACK后删除前缀；最后完整R7、50ms时序与98ms时钟预算不放宽。代价是更多fsync/协议帧，不能算Go业务收益。`9a839dd`只在实时progress保留最新CPU样本与最近有效区间，完整历史仍在最终summary和journal。完整899500条高基数模型中progress峰119.513→1.9215ms；另一低基数运行仍出现175.528ms帧尖峰，9路HTTP也有失败原件，正在诊断，不以单次通过宣布工具稳定就绪。

百万级历史的SQL观察成本正在独立核查。初始S1本地100000 Outbox/300000收据的原SQL快照1739.252ms，环境1CPU/1GiB/128MiB Buffer Pool，非云机配置、更非业务容量；后续S2构造与P1只读结果见下文，二者失败原因分别留证。驱动参数插值的独立业务回归见上，不登记未测吞吐收益。

### 时基测量与落盘解耦

显式新协议`dedicated-v1`由`3a78b2c`（包绑定）和`e85385f`（collector/JMeter/phase）组合交付；中间包绑定提交依赖后续生成器，按组合版本核验。原包缺字段始终按`embedded-v1`解释，旧配置及原件不重标，新准入不能缺失或降低协议声明。四项原始source hash键保持，JMX的唯一实际Argument、manifest/public packet/private config/binding/admission均须一致；重封JMX SHA/MD5也不能绕过变量核对。

新`/clock`带原Bearer及runId认证，只读绑定身份与宿主UTC，不取遥测记录锁、不tick或写journal。原98ms RTT/50ms不确定度、墙钟与单调时钟差、冻结anchor漂移校验仍严格执行；durable ACK保留seq/hash/fsync、超时与重试，独立计量，不再将落盘排队当作时钟采样。控制、heartbeat、资源、新HTTP开始时间和最终R7规则不放宽。[协议规格](clock-protocol-spec.md)记录其兼容与剩余宿主/GIL/监听队列风险。

必要离线检查47collector、28phase、38Groovy、11bundle、135prepare通过；7项真实本机HTTP检查包含batch实际fsync被阻塞且两层记录锁持有时，专用clock仍能返回。实际新协议17/3 JMeter三进程共20次合成PUT、完整退出0通过。单次9路HTTP检查9216合成durable记录、138次clock最高33.7998ms，control34.7147ms、batch ACK51.1324ms、113次真实fsync、源字节稳定。该检查未执行9个真实PTS引擎或180秒业务窗口，也不单独证明Groovy的全部生命周期；不同检查分别留证。结果SHA256=`d6da3acd76081ef13bdaab71143653bb6180f383b47236347fe878f7892a1c2a`，组合验证摘要SHA256=`35580535a64b89f2afba8564af9038d5f3a3f114b1fa6960f7690ed2fb540e16`。此前所有模型/HTTP尖峰FAIL保留。

### 大历史观察SQL的原始失败

S2构造在原本机1CPU/1GiB/128MiB缓存下触20分钟context上限，精确保留1225500条DONE Outbox及三组各1225500条IGNORED收据，自有连接已drain；不是210万夹具完成，也不是SQL快照超时，原S2/S3预算未测。不续seed或补写完成manifest，随后单列P1只读研究该精确partial历史。

| 同数据、本机原资源的有限probe | 原SQL完整函数 | 合并counts/三个唯一PK LEFT JOIN的候选 |
| --- | --- | --- |
| after=0，1225500事件，30秒总/15秒单读 | 17821.691ms失败；counts2764.009ms，第一receipt15041.949ms触单读超时 | 15035.198ms触单读超时 |
| after=325500，900000事件，4秒总/3秒单读 | 4000.433ms失败；counts1977.837ms后，第一receipt被总context取消 | 3011.158ms触单读超时 |

以上所有view均缺失，不补0，不把较早超时当改善，不归因Go API或云端候选资源。候选计划是Outbox PRIMARY range加三个收据PRIMARY eq_ref；空/混合状态/错误aggregate及case、尾空格、lossy别名等四组语义比较相等，但预算未通过，未采纳到公开observer。语义夹具首次cleanup因BINARY包装破坏PK定位而超时也保留；后用普通主键等值加原BINARY/hash guard精确删除本次11条Outbox/30条收据/2条failure，核本次夹具0、自有会话0、1225500历史及三组收据不变、原资源保护通过。未重跑计时或扩大删除范围。后续缓存配置研究若执行，必须独立声明profile与同卷独占交接，不改旧1GiB环境或把资源收益写成代码收益。

P1的80件私有原件manifest SHA256=`dab15b44a3bf857c8346f8cba2e7f996f1900b873ccfec69c4f957e87578944f`。随后单列P2：原1CPU/1GiB CID正常停止后，同一自有数据卷由新独立1CPU/4GiB、Buffer Pool2GiB CID唯一挂载使用；原CID配置未改，flush1/sync1/group delay0/RR不变。这是本地资源研究，不是拟用云端mysql8-v1或同缓存冷热条件的代码对照。

P2原全史SQL在30秒总预算下30000.871ms失败，MAX1.077ms、counts2925.277ms、follow12370.944ms、feed12358.016ms、search2320.402ms后取消，PARKED及commit未到达，view仍缺失。其准确会话未在5秒内drain，900000后缀按guard明确SKIP、未发查询，不再重试。62件原件manifest SHA256=`e75de432c7a28090a911317b0039fbfdd4fb842dc014f79b22cab156569a768d`。新CID正常停止、PID0/exit0/OOMfalse，原CID仍停、同卷0写入者，四个自有CID全停、5卷保留，原100容器/140卷/38网络及原运行服务保护通过。缓存增大仍失败不证明云机或业务达不到目标，只说明继续反复全史扫描不是已验证方案。

### 新观测路径与接续准备

[版本观测规格](observer-vector-plan.md)按固定事实版本发现事件，保留所有未退休源/收据精确刷新、原4秒总/3秒读，以及独立前后完整审计。`374ccbc`先交纯引擎：普通16顶层/18子例、race/vet、Linux单文件构建与双独审通过，三个hash golden来自实际生产Decode。SQL/main尚未接线，没有新测量或业务收益。`f65d62b`只新增严格模式与固定完整snapshot预算：旧/缺失模式30秒，新显式like-vector-v1为120秒，均仅窗口外；不把增加审计时间当查询提速，不移动原生恢复边界。

`7da6401`以显式namespace保存新packet/binding/admission/API原件，默认旧路径不变；JMX实际Argument、四源hash及MD5均校验，重封哈希也不能绕过。152项检查及独审通过。私有packet接续、参数组装另各7项本地检查通过；包含真实本地Node制包和mock平台响应，0真实云调用，不冒充实际准入。

`22692d6`交资源应用脚本：默认仅静态计划，另显式native只读capture基线或应用同MySQL CID的mysql8-v1与4GiB Buffer Pool。运行前核原挂载/配额/身份/独占及余量，运行后必须取得新鲜实际resize完成、原持久化不变；保留失败中间状态，不自动回退重试。46项离线检查、实际export到原两个登记消费者兼容及独审通过。该脚本未在云端执行，资源调整不计入代码吞吐收益。

### 离线Linux产物

已从干净独立`8d580b9`工作树构建linux/amd64、CGO关闭的API与原observer，无依赖升级、无服务启动。API build info内嵌精确revision且`vcs.modified=false`；observer按单源文件构建不内嵌VCS，原源文件SHA与二进制SHA另绑，不能声称两者都带VCS。API SHA256=`75b36b22d795bc4cb88288698553b7e8d3cf92297aa18c84284852f141012ff2`，observer=`493ea8b8e35cd25ae90019730db94859da9b7d782b300ea34cc23564d3478e2f`，私有产物manifest=`d4b73b0ddd678206ed4e668460528136c585568879fddad11200c21dba48ce82`。仅离线构建，未部署或发压，云端原版本不变。

该精确`8d580b9`的[四项远端CI](https://github.com/tech-cove01/project-full-stack/actions/runs/41505690255)均成功；实际CI响应另行保存SHA256=`a4fd18363775775cb840da88267ff19f6caa1dacf00a25b08bbd5e9ca125573a`。不回写构建时尚未取得CI的manifest。此处observer是原扫描版，后续新观测工具必须另取干净源码构建、重新绑定，不能把这个二进制当like-vector-v1。

## 原件存档

闭合S1–S19、失败H1及repair2静态材料共2202件、147402850原始字节；14.6MB私有归档已下载并逐成员核验，SHA256：`4a6ebbb7d37cd15884bd3124b1759288f067189647f867edd264a1cf54a6c4fa`。不公开凭据、云账号标识、网络地址或真实私有输入。

S16瓶颈分析14份原件与该归档逐SHA交叉匹配，计算摘要SHA256：`eca865ab2808829ef4c83f92fdd8f5d1b07e84a6216f222005c1e0f366f81727`。原始私有归档需在用户本机授权范围内核验；公开摘要与哈希不等于公开了原件。
