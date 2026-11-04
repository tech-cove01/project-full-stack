# CAP-10 改动与收益档案

更新：2026-11-04。唯一任务状态见根 [STATUS](../../../STATUS.md)。用户当前目标为可写简历的稳定3000成功QPS，原接近5000要求保留为较早历史；当前离线修复中，没有本批180秒正式成功组。以下把代码、部署资源和测量工具分开，不将目标或离线回归写成吞吐收益。

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

### P3与OBS-V2首轮：尚未闭合的预算

P3仍用本地独立1CPU/4GiB、Buffer Pool2GiB和原1225500事件历史。首次schema前置额外COUNT(DISTINCT projection)在5秒预算失败，实际5061.462ms；两EXPLAIN和主query均未发起，原件62件manifest=`315b52a875c25e5780ac7e0b6e9852800f4e386dc11314ea501007b48750975f`。随后去掉该前置全史扫描，两个实际EXPLAIN分别表明旧receipt先ALL扫描估4099431行再查Outbox唯一键，新候选从Outbox ALL估1208931行、实际projection去重、按receipt完整PRIMARY eq_ref；估计505个projection不是实际基数。

旧receipt唯一一次计时30050.348ms超时，结果null，会话约1294.539ms的5次检查仍活动，后续候选/CTE均SKIP并正常停自有CID；102件manifest=`23428986eed9dee07bdfca9dcb0118b1baa9c7e1e7397f339c1f787625f0ee46`。单独候选接续重启后唯一一次计时30054.851ms也失败，随后真实等待1971.750ms、8次检查证实会话drain；canonical/alias两项只读CTE通过，包含未知projection和错误aggregate不隐藏、case/尾空格/lossy eventId别名不匹配，但不含hash或完整审计。98件manifest=`746af968ce46458229d41813e15ba9db5cc3277c0f2cc07d77fd0fe1badc7d33`。不重跑旧query，不拼缓存冷热不同的耗时倍率；三轮原件、旧417条seal及原资源保护通过，正常停全部自有CID后另交OBS-V2生命周期。

OBS-V2首个真实矩阵使用独立IT空库、同一1CPU/4GiB/2GiB资源，5顶层正确性通过，覆盖延后提交低ID、真实RR、pending完整刷新、9个污染反例、3个事务/取消故障与真实索引计划。第6项用实际生产Decode产生的4096配对/32768事件、98304收据，seed5.616秒；原32分支query在4.000411秒deadline，读29434/32768、924查询、显式source prepare1，SQL未commit、引擎未推进。不能把部分读取说通过，prepare1也不是会话总Com_stmt_prepare。小/大实际source唯一键和三组完整PK计划通过，事实小规模ref、大4096行range；未查询原1225500历史，不声称任意事实表都O(pool)。

该次测试逐行删除4096事实也触30秒清理预算，原失败、三份源v4与6份raw已保存，随后按原oracle4096精确配对批清理闭合，12张IT表及本次oracle为0，原1225500历史守恒。原raw manifest=`0b06c6b83def0c003a2a847d6d6ae1572bfc292a56fed232e958b265603557b0`，分析=`f7d5adca888358b51c0dd74319ff47d04ab26044b16430186365a49b20463972`。后续候选将重复整join的32分支改为256个键驱动一次source/receipt join，以减少SQL调用和规划开销；首轮当时只有预测；后续有限对照结果如下，不改判首轮FAIL。主窗4秒总/3秒读不放宽。

完整审计还发现原Node只计收据数量，未校hash，且未知组未显式拒；新模式须补完整源/字节身份与实际Decode canonical hash校验，原legacy证据不改判。120秒是窗口外审计预算，不能把这些待交付能力说成已有完整审计或借预算提升包装SQL收益。

### OBS-V2有限对照与同步发布等待

`d6ac1e5`接续仅一次同oracle对照：同一独立MySQL CID、1CPU/4GiB/2GiB配置，4096配对/32768源事件/98304收据的生产Decode oracle SHA256=`098e9b0f89a2a4f6fc50ea436a622a3939d7ee2881469b4694d76cac1f488a22`。seed5.439秒，完整SQL读取1.349935秒、135查询、显式source prepare1，事务实际commit；6顶层真实回归全部通过。实际N256键materialized、source唯一键及三组完整PK eq_ref，N1源const。未放宽4秒总/3秒读。同CID未重启，重seed、缓存历史与新AUTO ID差异明确记录；旧超时未完成，不能计算完整耗时改善倍率，也不能外推API QPS。

本次12张IT表/oracle/自有会话精确为0，原历史及配置不变，四个自有CID正常停止、5卷保留；原100容器/140卷/38网络及14个运行服务保护通过。6份raw manifest=`575bcbcd4009faf810fe0e34697a4b153840bc47707b4e47da15018c5ed13f5f`，对照分析=`475405ae97d00aa0fe64f6901c8d8c764747a96d3451976c3105bc927d0a11a6`，112份闭合manifest=`15af4e89643bbcc71ff06bb753d6f465159fcbbbb55204f61da9aa3e55e034cc`。首轮失败、清理失败及精确修复原件全部保留。

`e50eac0`按[同步发布规格](relay-publisher-flush-plan.md)把opt-in批发布器的分区凑批等待10ms缩至1ms。8个独占同步Writer各自一次调用已包含完整有界批，原10ms无法等来同lane下一调用；RequireAll、同步ACK、超时、字节上限、关闭drain及默认旧路径保持。有限同程序实际SDK加内存协议transport（不连broker/业务/SQL），3组配对×每组12次已测调用，中位数的中位数10.5673→1.66095ms，每variant384条已测记录/24次Produce均核；不把组件时延换成业务吞吐。原始结果SHA256=`4015866aa6d8c7dd7a5b9e81d461a7557ae7239c688d4dff98aaf6998b384c74`。相关普通/race/vet、独审及[精确CI](https://github.com/tech-cove01/project-full-stack/actions/runs/41523762678)通过。更短等待在调度暂停时可能增加wire batch，不承诺每partition只发一次Produce。

### 离线Linux产物

已从干净独立`8d580b9`工作树构建linux/amd64、CGO关闭的API与原observer，无依赖升级、无服务启动。API build info内嵌精确revision且`vcs.modified=false`；observer按单源文件构建不内嵌VCS，原源文件SHA与二进制SHA另绑，不能声称两者都带VCS。API SHA256=`75b36b22d795bc4cb88288698553b7e8d3cf92297aa18c84284852f141012ff2`，observer=`493ea8b8e35cd25ae90019730db94859da9b7d782b300ea34cc23564d3478e2f`，私有产物manifest=`d4b73b0ddd678206ed4e668460528136c585568879fddad11200c21dba48ce82`。仅离线构建，未部署或发压，云端原版本不变。

该精确`8d580b9`的[四项远端CI](https://github.com/tech-cove01/project-full-stack/actions/runs/41505690255)均成功；实际CI响应另行保存SHA256=`a4fd18363775775cb840da88267ff19f6caa1dacf00a25b08bbd5e9ca125573a`。不回写构建时尚未取得CI的manifest。此处observer是原扫描版，后续新观测工具必须另取干净源码构建、重新绑定，不能把这个二进制当like-vector-v1。

### 批量认领、完整审计与跨语言接线收口

`bd35e2b1b3b464d312790259b60fe6b458a4e645`保留原RC/byte前缀/停车截止与attempt fence，以按主键驱动的有界点更新合并lease写入。真实32条正常认领从32次UPDATE降为1次multi UPDATE，混合停车2次；prepare/execute/close从各33次降为2或3，显式插值时双方均0，SELECT/COMMIT仍各1。10项真实功能、24个实际EXPLAIN、独立1顶层/8子例协议计数通过；首轮harness漏计Com_update_multi的FAIL保留。935条有限事件精确清理，原历史/配置不变、自有服务正常停；不是吞吐成绩。闭合224件manifest=`a11690003d63fbd5a2935512e5acce7c0fc1153d60f0a9f4a43971e3c88ca370`。

`15f8eb558498455b033e193182e4394cec75f591`交[严格完整审计](full-audit-plan.md)。实际生产Decode的32项可存储身份/hash、12项收据反例、5源/4组聚合及3计划通过；1055分组FAIL与JSON字段顺序解析FAIL均保留，后者只解析原raw。唯一1225500源/三组收据完整RR查询106077.0271ms、Exit0/无timeout/完整COMMIT，符合单次120秒；该历史造数共3676500个hash不匹配且缺笔记/事实，业务对账失败、无Redis或新QPS。104件闭合manifest=`9701a337c197217d7a6050e395d5d0596a9465fcb5d34348abeb1d1d4475f90d`。后续累计200万以上历史仍有预算风险，不能按8CPU外推SQL8倍。

`743686f`的实际N2互通：原Node提取snapshot/factErrors/derived/reconcile，真实MySQL/Redis生产Mark Lua得到E2完整基线；原Go源fresh RR factory接受NULL/非NULL控制行和present v0，受控改变事实后拒绝旧基线并恢复。两源/六收据/两事实与笔记、三控制值、四初始不存在的Redis键精确恢复，自有服务正常停。151件manifest=`823b862408ec94fc84aeb25359b61abac5900cadc21fb3a6ac81d2fdbc869ea5`。未运行Node/observer main、Kafka、API或PTS，不冒称整段阶段验证。

`5c6c8e5`仅在完整SQL/Kafka样本Accept成功后发布同RR事实/控制摘要，未增加SQL；7项新顶层/11子例、总36顶层/89子例race/vet/Linux及独审通过。`e53534f33b368bb26e246ae0f0cc5ac2e48395fe`交[阶段去重](phase-audit-plan.md)：原基线/新鲜轻读/单条接受样本绑定，原生恢复后最多一次完整终审；不以反复全史扫描拖入setup60，不改原时间或正确性标准。10新阶段/10审计/39原Node、9文件绑定/71runner检查通过。首版k6时间入口、PID登记及纳秒截断发现已修复留档；原39VM上下文漏自由变量的FAIL保留后仅补上下文，未改原断言。旧“legacy SQL字面SHA”提取标签不准亦单独更正，完整legacy SQL实际SHA=`c8d0ff84604c7523fcaee21e1cd55440d1d82ad57f8731db46d35302aa7329cb`，新旧全文相同；不改旧原件。

### Done候选暂缓与最终候选产物

Done点更新候选完成24个实际计划、7顶层/37子例、638条有限事件及28个旧/新组件观察。较大批次减少SQL，但部分小批次变慢；每case仅一次观察，不给统计倍率或稳定阈值。用户明确暂不实施分档，公共六文件草稿已逐字归档、原生产文件精确恢复HEAD，未采纳到API。草稿patch SHA=`807ee059ca1c6f4f8fc7126499eac944c6c5f01efc36f5f858727f340843e882`，151件真实闭合manifest=`258358edf5c20674252600197f89b05a4d11b83193bc87a9c0ad4415bbe626ac`；不抹去研究，也不当已交付收益。

新的可用Linux API来自干净15f8eb5，实际ELF完整40位capacity版本已核，SHA=`f29fe6a9c76d54a5ddd1cbd8b3ae143c53a4d348176b1a98bd7b7291949b4499`，manifest=`342d410777cdf4fa5b9b7d28299a5e618259fa968438de59ab2ca17bcf9178e2`；首次短7位版本产物已拒绝存档，不能混用。新observer来自干净5c6c8e5，SHA=`3fc4631973775a68878e4e00af8de5a3f5d7a6ef5e222319a53b4b1642591b28`，单文件无内嵌VCS，绑定原Go源SHA=`1e79e3d4d6d54bdde4d77e0005568e295f825f1c8b7cf6f5df31c1f8eac407f9`及相同go.mod/sum。observer manifest=`3dbba23de51cc524f36ad98f9dc7f46a55ed797bb11f31b03d7f68a38df2db15`捕获时CI仍pending，随后完成证明另存=`eb0262ee7363ad1093b675e05bf91235a611a7996428b30177068fd70934c7f8`，不改旧pending。

API15f与observer5c6各自[精确API CI](https://github.com/tech-cove01/project-full-stack/actions/runs/41576389620)、[精确observer CI](https://github.com/tech-cove01/project-full-stack/actions/runs/41577337428)四项成功。两份产物只读校验、未部署；工具HEAD/脚本、API源码和observer源分别绑定。私有配置、夹具/包准备、参数组装、执行与双夹具清理已有直接离线检查，真实资源/进程/空载/9引擎/逐窗准入仍待同一ECS恢复后执行。接续步骤与开机前风险见[清单](offline-resume-checklist.md)，不自动新增功能或负载批次。

## 11-04：新三窗、必要修复与未采用的调优

S20/S21/S22均保留`generator_limited`：实际成功变更速率2995/2396/1597.333每秒，P95/P99分别268/358、264/369、116/205ms，调度违规134/63/26；业务终审、unknown/drop0不抵消时序或积压失败。原帧复算证明三窗违规均在首轮关系线程的首秒，不能据此确诊JIT或否认后段HTTP长尾。

- `a4ca69b`/`c061153`/`09053eb`修发生器的首次执行边界：同线程同HTTP节点先有限noop准备，独立记录全部准备原件/成本，全部持久确认后才冻结完整native Start。17关系/3 JVM和准备503故障的有限替身验证通过，不是云端容量提升。
- `aeb0c96`采用已封存的Done点驱动路径：N1–7仍旧事务，N8–64从N次UPDATE改为两个SELECT加一次有成对fence的UPDATE，仍一次COMMIT；32条的事务内完成语句由32条变3条，BEGIN/COMMIT各一次另计；插值关闭时驱动prepare/execute/close往返还须分别计量。新真实MySQL2顶层/2子例通过，旧638事件/24实际计划仅引用，未重复执行。67件新封档manifest SHA256=`0d0c62879c855690f1ed40d011e835351e9e64c97d242327de5d9c444e1c5267`。不宣称本票单独足以稳定3000。

另做一次`binlog_group_commit_sync_delay`的0→1000µs有限组件研究，保持双1持久化、提交顺序和原资源。官方说明此参数可能聚合同步，也可能增加延迟或争用，须实际验证，见[MySQL 8.4手册](https://dev.mysql.com/doc/refman/8.4/en/replication-options-binary-log.html#sysvar_binlog_group_commit_sync_delay)。

本机1CPU/4GiB、2GiB缓存、SQL池64/64、插值true，同一exe两次fresh进程，每臂64个独占关系并发，每关系内串行切换32次，共4096次真实`social.SetLike`；未接Redis/HTTP/Kafka或后台共享池竞争。两臂正确性、逐版本Outbox、清理和参数还原均通过，结果如下：

| 有限组件指标 | delay0 | delay1000µs |
| --- | ---: | ---: |
| 2048次完成时间 | 5.3550666秒 | 5.1451221秒 |
| SetLike P95/P99 | 219.223730 / 279.227272ms | 211.812700 / 221.739520ms |
| 实际Commit平均 | 60.606172ms | 62.315420ms |
| 实际Commit P95/P99 | 102.634200 / 112.199696ms | 102.470500 / 110.590755ms |

**不采纳1000µs，不继续扫描旋钮。** Commit平均反升、P95基本不变；完整调用尾部的单次差异受固定先后顺序、新ID和缓存影响，不作纯因果倍率，也不当60/180秒稳定QPS。两臂native借池等待均0，不能据此称云端共享池问题解决。server计数包络另为14.959444/14.596061秒，含seed/audit/cleanup，不冒充native区间；缺失COMMIT基线保留unknown。实际恢复delay0、IT12与自有会话归零、原历史守恒、MySQL正常停止及原资源保护通过。193件manifest SHA256=`4d1a43680b1b144e6fff90825154c52aeb48b022d714ef5d3b04a4cdffcf876f`，分析SHA256=`8039229f1211e2b0e8e5f696021c4d23ec6a3c4ccc183602fc15874f3296611a`。

## 11-04：Like正确性修复与SQL往返精简

`f32bc313ee13ff1ab01ca43fb1f3d3f9d4c6c057`单独修复无符号计数减法：外层`GREATEST(0,liked+(-1))`来不及阻止先发生的下溢，改为先保护输入再减1。两驱动真实公共服务回归验证零计数取消赞、版本/两事件与重复请求noop；70件manifest=`13b2f4518e5e3de57884423fcfd26f95072b8693b0035113917c2bcdaae676b6`。这是正确性修复，不是QPS收益。

`c464c1fa4d153806fb44ac746312552655ebda63`将笔记PK+关系UK两次锁点读合并、同一笔记/关系/用户/旧版本的两个UPDATE合并。现有关系变更的业务SQL实际6→4：SELECT3→2、两单表UPDATE→一个multi UPDATE、INSERT仍1；BEGIN/COMMIT/隔离SET仍各1。保原同步提交、RC、版本fence、取消回滚、Outbox和提交后新鲜状态读取。真实两驱动2顶层/16子例覆盖并发与点锁、陈旧版本拒绝、取消回滚、提交后ACK错误及正溢出；详见[规格与实际结果](like-tx-rpc-plan.md)。

首私有计数将raw prepare当客户端命令，7/6/6误差导致整矩阵FAIL；93件原件manifest=`96b8042bc911473140c44e873856d208a4c7aabc83f19fe46de429b9d1777f0c`保留。根据[MySQL8.4官方计数定义](https://dev.mysql.com/doc/refman/8.4/en/server-status-variables.html)，服务端reprepare也增加prepare。另一次只对精确遗留做事务清理、独立排空/空库核对，再跑8次私有调用：关闭插值原like raw7/reprepare1、派生客户端prepare6，新like/unlike各raw4/reprepare0；execute/close各6→4。开启插值双方均0，SQL级PREPARE/EXECUTE/DEALLOCATE全0。不是把第一件缺失字段补值或重判PASS；87件新manifest=`261fd16540f66382931fff12452de0c93a0db561a349181e8ef67ff0f3fc3d11`。

这些是同一有限程序的语句/命令计数，不能当全部网络往返33%下降、延迟倍率或稳定3000；COMMIT成本仍在，云端效果待同条件实测。IT12/会话归零、原历史与配置守恒、正常停机及原资源/卷保护均闭合。最终源码已正常推送，干净Linux构建/精确CI见STATUS；尚未在云机部署。

## 原件存档

闭合S1–S19、失败H1及repair2静态材料共2202件、147402850原始字节；14.6MB私有归档已下载并逐成员核验，SHA256：`4a6ebbb7d37cd15884bd3124b1759288f067189647f867edd264a1cf54a6c4fa`。不公开凭据、云账号标识、网络地址或真实私有输入。

S16瓶颈分析14份原件与该归档逐SHA交叉匹配，计算摘要SHA256：`eca865ab2808829ef4c83f92fdd8f5d1b07e84a6216f222005c1e0f366f81727`。原始私有归档需在用户本机授权范围内核验；公开摘要与哈希不等于公开了原件。
