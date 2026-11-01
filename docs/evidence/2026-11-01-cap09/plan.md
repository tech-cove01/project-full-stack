# CAP-09 完整Feed与真实点赞写入：执行协议

依据根STATUS的CAP-09及2026-11-01用户执行指令。起点干净 `53fa1b34843486dd546ac3212a187e963621890b`；旧CAP-07/08失败、旧B1未知不改，不复用旧名额。本轮完整Feed与真实同步点赞分别出吞吐、全/成功P95/P99、业务对账及下游稳态结果。5000/s只可能是探索档，不作为必须达到的成绩。首页README不放性能或工作流。

## 时间与不可变名额

保守工作段1起点2026-11-01 05:00 UTC（上海13:00），至09:00 UTC；必要时工作段2再占一次4小时，合计上界13:00 UTC（上海21:00）。保守计时不扣负载/CI等待，另记实际等待起止，不利用等待扩问题。最多两个工作段，到限停止新增定位/代码方案，按现有条件交最终结果，不自动CAP-10。

| 每条链路独立记账 | 上限 | 进入与停止 |
| --- | --- | --- |
| 低速工具probe | 一个固定用例，必要修复同用例回归 | 实际变更/幂等/未知回执恢复/对账/原生停止接线；不计成绩，不更名盲试 |
| baseline-stair | 7×45秒 | 参考50/100/250/500/1000/2000/5000；根据受限余量事前替代档，仍占一个槽；首个受限即停并查恢复 |
| baseline-hot | 1×45秒 | 单一热点代表档，与multi分列，不用于推导缓存提升比例 |
| diagnose / repair | 至多2次迭代，每迭代1×45秒诊断 | 事前候选、不同预测；只处置有证机制，一次一个主要变量；诊断开销单列 |
| final-stair | 7×45秒，至多一次阶梯 | 最终固定源码；若完整调用链/装配/共享池/缓存/依赖/配置确证等价，复用已有有效基线档并保留比对 |
| final-hold | 预定一档×180秒两轮 | 不高于最高完整短档，先冻结有余量档与依据；任一失败就结束此组，不降档补两轮 |
| final-hot | 相关代码变化才最多1×45秒 | 同SHA有效旧代表档优先复用 |

每档先独占reservation、输出目录及命令；启动失败/早停/采集失败都占槽，保留失败不换tag。两链路串行，不与编译、CI本地回归或其它容量任务并行。Feed失败不阻止点赞独立执行，反之亦然。正确性错误先停该链路并查明。

## 固定新自有环境

新project `project-full-stack-cap09`，全新随机命名卷，loopback MySQL3471/Redis6571/Kafka9391/ES9271/API8271/diag8371。候选网段10.253.71.0/24已与Docker既有网段只读对比，无重叠；创建前再次核端口/项目/卷为空，不替换任何旧项目，不停止CRM。记录全部四服务不可变CID、挂载、imageID/限制，结束保留所有卷。

首次实态准备发现Kafka镜像还自动创建`/etc/kafka/secrets`与`/mnt/shared/config`两只匿名卷；初始严格单卷检查停止于健康后、迁移前，原始失败保留。按新CID与匿名卷标签独立确认归属后，记录全部三只Kafka卷与精确目标（另三服务仍一只数据卷；MySQL初始化只读bind另列），准备恢复仅从该检查点继续迁移/observer，不重新compose或换卷。原件见preparation-recovery-1.json，所有匿名卷同样保留。

MySQL4CPU/2GiB、Redis2CPU/512MiB/AOF everysec、Kafka2CPU/1GiB（heap256–512MiB）、ES2CPU/1GiB（初始heap512MiB；0 baseline前经资源stop实态修复为固定heap256MiB，见下段）；API SQL20/Redis10/GOMAXPROCS4/GOMEMLIMIT1GiB/workers1，001—013。Kafka relay和实际三组 `community.follow-v1`、`community.feed-v1`、`community.search-v1` 正常开启；ES开启以装配第3组。没有metrics组。LIKE在三组均属IGNORED收据，不冒充APPLIED或Redis投影更新；点赞Redis Mark在HTTP提交后同步路径。[点赞契约](like-contract.md)

同一Windows宿主，20逻辑CPU，Docker约16GiB内存；开始预检宿主内存80.4%、AC供电且电池保护关闭。启动后再次核88%安全预算，余量不足不发压。记录供电/原生盖子和睡眠事件；仅用本任务进程级保持唤醒请求，不改全局电源配置，不人为合盖/暂停Docker。任何实际供电/待机转换保留为失败/无效档。

baseline应用为独立clean checkout构建的完整53fa源码，revision=capacity-完整SHA、vcs.modified=false；backend/infra/CI相对已核验46源码无差量仅作为来源说明，不冒用旧exe的revision。工具/应用/二进制/fixture各自hash，优化按新干净源码重新构建，不把工具工作树称干净应用。

## 数据、请求与对账

Feed为6400篇/32作者/32读者/正文256B/页20，multi均匀读者轮转640key，hot单读者20key。直接SQL准备关注与公开事实，保持当前RR/database路径，不暗补projection完整标记或切成轻查询；检查完整ID/顺序/作者/门店/标题正文图片/游标/公开计数/本人状态。另一个独立点赞cohort不写Feed笔记，两个cohort在基线前都准备完，记录实际表规模。

点赞独立512个liker、512篇multi笔记加1篇hot笔记，正文256B。每VU固定独占一个(user,note)，成功版本+1才继续反向true/false；phase前从MySQL读事实注入私有input，普通PUT不夹bootstrap GET。未知后先GET事实再复用关系，recovery GET单列，原unknown保留。记录实际使用关系数，512只是公开池上限，不把VU或20条/页折成RPS。

点赞分类planned/attempted/completed/success/mutation/no-op/reject/system/HTTP/content/unknown/dropped分列。意外no-op不得算写吞吐；有意同状态重试另用低速probe。对每关系核active/version与迁移次数，对每note核liked=全部active关系数，LIKE事件ID/版本连续/唯一payload，三组每事件IGNORED收据和重复/缺失分列；Redis派生set/hash也核，不自动repair掩盖错误。未知回执通过事实确认的结果另归档，不覆盖原生分类。

每档前后及稳定采样记录outbox PENDING/PROCESSING/DONE/NEEDS_REVIEW、各组receipt/offset/park与lag。完整JSON事实聚合只在降载后，稳态采样只做索引范围内汇总。末分钟新增待办≤同期新增事件1%，PARKED无新增，降载60秒内回预定基线；45秒短档用其完整稳定窗口核同口径净积压，并另看末15秒斜率，不能靠最终排空冒称稳定。事件三份IGNORED不是三次有效投影更新。API档与完整下游档分别分类。

## 通过、安全与解释范围

计划发起≥99%；Feed正确success/attempted≥99%；点赞核实mutation/attempted≥99%、意外no-op0。系统/HTTP错误率各<1%，unknown/drop/content/fact差异0；全/成功P95≤200ms、P99≤500ms，固定45或180名义秒分母，早停不缩短。分位来自每档native summary；未保存逐响应时延分布，不声称离线重建或合并分位。最高短档与两轮稳定档分开。

原5秒安全监督覆盖4容器：宿主内存88%、API RSS1.5GiB、CAP09负载器RSS2GiB保守按k6＋Node负载/分类helper合计、每自有容器内存85%、宿主CPU85%连续3有效样本或连续3不完整样本即停新发并收尾。k6与helper资源分别保留，校验/采样资源另列，客户端受限与后端受限分开；旧cohort的k6单独口径不改。诊断才启现有有界tail/最多3栈，baseline/final关闭额外collector。

优化只针对已测瓶颈，固定数据分布、正文、池/资源/持久化/消费者/TTL等条件，保留同条件代码A/B。换Linux/分离生成器若另选必须重测baseline、与代码收益分开；当前先用已可用环境。改Redis受理＋异步落库需先交具体设计和独立审查确认，不把吞吐压力当语义变更授权。

最后先正常注销两cohort自有会话/Redis剩0、删observer核0，再按完整PID/创建/exe/hash与四CID停自有服务留卷，精确删除登记私有env/token输入；中断保留身份/progress，不按名称全杀或删旧数据。完整SHA、所有失败、对照、对账、下游限制与清理交独立核验，不先更新简历/prepare，不发付费AI或部署。

## 09A 基线前ES堆修复（保留首失败）

首Feed2/s×8秒工具探针因ES工作集89.11%触发85%保护，未进入吞吐阶梯，不归因Feed。真实JVM固定512MiB committed/max，堆外另占约206MB；只将自有ES的Xms/Xmx同时改256MiB，容器1GiB/2CPU及85%门槛保持。经非作者审查后仅ES --no-deps重建，保留数据卷与另三完整CID；初始environment原字节保留environment-pre-es256.json，旧probe记录的hash仍指向该原件，active environment.json记录新ES CID和修复历史。同53fa应用以新label/PID启动，两cohort事实和会话不重造。实态堆256MiB、health green，首修后Docker内存60.99%；冷启动下降不当作Feed优化比例。只允许原固定低速用例1次修复回归新tag，旧失败与名额均保留；两链路后续基线/对照/正式组统一固定256堆。
