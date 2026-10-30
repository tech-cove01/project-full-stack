# GO-073-CAP 容量、瓶颈与必要优化总报告

**状态：交易与读容量自测、内部复算及收尾完成；待外部独立核验。** CI按下方精确提交记录。交易30/s、新767c Feed hot200/s与multi100/s均各完成180秒两轮。所有较高档失败、旧3db数据和本地原生退出保留；这些是本机共同部署的观测边界，不是理论上限、修复收益百分比或外部验收。

## 实测结论与范围

| 范围 | 当前证据支持的结论 | 明确边界 |
| --- | --- | --- |
| 交易 | 4独立消费者、30/s×180秒两轮；各稳定5401笔唯一受理最终匹配，P99为16.1298/17.1614ms | 最近已测35/s×45秒成单落后；31–34未测，不是理论最大值 |
| Feed索引前 | hot160/s、multi150/s各30秒探索通过；175附近出现SQL池等待、错误/丢迭代或SLO限制 | 没有索引前180秒持有，不叫持续容量 |
| Feed索引013 | 代表性完整SQL由扫描6400条公开候选缩为作者范围200条，仍连接门店及Top20排序 | 单次ANALYZE约11.3→0.872ms，不是HTTP提速百分比或20条早停证明 |
| 旧3db/013读记录 | hot300/s×180秒两轮，各54001/54001，P95/P99为20.832/33.461和23.521/113.676ms | 未修复版本的历史实测；保留全部受限与宿主限制，不与新SHA结果拼轮次 |
| 修复版Feed hot | 767c2fc/013，200/s×180秒两轮，36001/36000全部成功，P95/P99为25.3774/43.4507和25.3714/68.0209ms | 300/400/500长持未能重复达标；201–299未测 |
| 修复版Feed multi | 同767c/013，100/s×180秒两轮，18000/18001全部成功，P95/P99为89.1516/188.7851和17.5534/21.8728ms | 200及以上长持受限；101–199未测，不与hot推缓存收益 |
| 整体验收 | 测量器、版本SQL、原始捕获、内部review、真实回归、全量复算与收尾分别落证 | CI按实际提交记录；外部独立QA待复跑 |

机器可读及逐项证据见[交易报告](cap02/report.md)、[交易重算](cap02/summary.json)、[读报告](cap03/report.md)、[读汇总](cap03/summary.json)，索引前原始范围另见[基线报告](cap03/baseline-report.md)及[基线汇总](cap03/baseline-summary.json)。本报告不覆盖或改写原始实测。

## 环境、源码及可比性

本机Windows11、i9-13900H、14物理/20逻辑CPU、约31.63GiB系统内存；Docker VM20CPU/16,572,063,744B。API与k6共用宿主，CRM等已有服务未关闭；交易与读正式负载顺序运行，没有并行发压。[交易环境](cap01/environment.json)、[读环境](cap03/environment.json)保存真实硬件、镜像/容器身份、软件版本与其它在线服务。

MySQL限4CPU/2GiB、Redis限2CPU/512MiB，原事务和AOF行为保留。API为GOMAXPROCS4、GOMEMLIMIT1GiB、SQL池20；Kafka/ES/AI关闭，不追加真实模型请求，也不从本次交易/Feed读推断社区高写容量。进程CPU100%表示一逻辑核，Docker CPU另列，不相加。

原串行交易API为干净 `32ae24331d7de98d8df004cf854708ec3c59eec4`。同版本W1/W4对照、交易持有与旧读阶段均固定干净 `3db296dd0beedd6f85d06cf5479d707b0498ce7e`，API二进制SHA256为 `ef29e9e3838b7ec8ffd1aca5b1a51848fbd8a020ca084228879481adc209bd69`，不是把正在修改的工具工作树称为干净应用构建。013是独立数据库版本迁移；当时查询逻辑不变，工作树仅澄清旧索引计划注释。

借池修复已单独提交并推送 `767c2fc8bdf1be08c5636143a71898eec25081a9`；[新构建记录](cap03/build-767c2fc/build.json)及[Go元数据](cap03/build-767c2fc/go-version-m.txt)确认工作树干净、`vcs.modified=false`，二进制SHA256为 `97a5165e48a74c98ee521eebda01f79d049c44938239e2bd4ed89fe38e25e20c`，精确revision为`capacity-767c2fc8bdf1be08c5636143a71898eec25081a9`。该API复用原3db创建的同一夹具与已应用013的数据库，来源分别记录；旧3db结果不倒填成767c能力，新构建就绪本身也不是HTTP容量通过。

交易主对照使用同3db二进制、25/s、45秒，变化为消费者1→4；负载器GOMAXPROCS/GOMEMLIMIT统一未设。初次W4继承4/1GiB只列次要，已有统一默认配置复跑。读索引对照保持相同API/PID、6400篇数据、页长20、读者/key分布及同档参数，先完整记录before再应用013；更高档预分配VU的调整及宿主波动另列，不包装成纯索引收益。

## 测量器与判据

[事前计划](plan.md)、[读计划](read-plan.md)预固定发起达成≥99%、成功比例≥99%、dropped0、系统错误<1%、全部/成功P95≤200ms/P99≤500ms。交易另验末第三成单/同期受理≥98%、lag净增≤两秒目标量；最高持有还验末一分钟及名义稳定结束到观察收敛≤15秒。触及队列/错误/资源预算即停止新发，降载与最终排空不能掩盖持续不足。

交易使用25,000真实合成会话、每档新HTTP活动、预热/稳定唯一用户分区和充足库存。流式exporter/请求/公开用户池index/SQL/Redis ID映射核对，区分实际受理、窗口内SQL观察下界及最终同批成单。COUNT每2秒、资源每5秒；没有精确数据库提交时延，`queryMs`也不是SQL-only执行时间。12阶段25911条完成响应对应25912个持久订单，唯一差额为50/s停止窗口的已查明回执缺口。

读使用32作者各200篇、32读者各关注一位作者；hot为1读者/20计数key，multi为32读者轮转/640key，不等于单读者关注多作者回归。每次真实HTTP页均核20条ID/作者/门店/计数/本人点赞状态与database来源。started、native HTTP、终态classified、success、unknown、dropped分别记录；早停unknown不补成成功或错误0，名义秒数分母不改为恰好更好看的运行时长。

访问证据保存SHOW GLOBAL STATUS、全局Execute/Prepare事件及Redis commandstats增量；Com_select/Com_stmt_execute有重叠，不能相加。范围为整个隔离实例，包含鉴权、观察器及后台，不冒充Feed逐条SQL次数或公共计数回源数。prepared digest空缺不等于没有DB访问。

## 交易结果与优化依据

原10/20/s×45秒探索通过；原25/s与带profile重复的末段DB/受理为307/350、304/350，lag净增43/46。相同SQL观察边界的Redis受理缺口也增加，支持真实积压，不是轮询边界假象。原profile仅支持串行I/O服务链候选，没有证明磁盘/fsync或唯一驱动根因，见[瓶颈报告](cap04/baseline-bottleneck.md)。

默认仍为1的有界消费者配置允许1–8，实验用4；GET_LOCK、库存锁、事务、审计/outbox、ACK/review/重放均保留。[内部review](cap04/parallel-review.md)、[真实回归](cap04/parallel-regression/report.md)覆盖并行唯一订单、同ID重投/拥有者冲突、提交后ACK前中断及多consumer恢复。首轮测试10秒不足覆盖2+4+8秒退避的失败保留，只把测试等待改30秒。

同3db/25/s对照W1末段313/350、lag+37；W4统一generator为348/350、lag−1，后者探索通过。30/s两轮180秒各5401稳定受理最终一致，全部及成功P95/P99分别12.4042/16.1298ms和12.8033/17.1614ms；末分钟1800/1800、1801/1800且lag净增0。最后预末SQL观察下界均5396，最终稳定5401；观察点早于名义末尾76/85ms，不把快照或最终数写成精确180秒提交数。名义末尾至收敛1021/2278ms，所有库存/review/outbox/PEL最终一致，详细时点见交易重算。

35/s末段427/490而落后；50/s在队列最老超过15秒后REST stopped停止，pause未确认，2398完成响应/2399持久订单。额外订单有完整SQL/Redis ID/user，客户端该次结果未知，不能叫丢单或补写200。原50末段混入排空的测量缺陷已单独修复，原始失败不改写。[停止档分析](cap02/workers4-rate50-stop-analysis.json)与[工具检查](cap02/tooling-interruption-fix/result.json)保留。

交易资源没有完整post-exit排空覆盖；3个短档末点缺生成器、稳定样本仍完整。旧schema首样CPU可能含priming，原峰值不当CPU瓶颈依据；新读采样器按≥1秒有效区间处理，不能跨口径直接比较。基线20只有45秒，禁止据30长持写“持续容量提升50%”。

## 读链路、013与剖析范围

[索引前九档](cap03/baseline-report.md)完整保留hot25/50/100/150/160与multi150的30秒通过、hot175/200及multi175早停。三个受限档unknown为341/512/348；如hot200已分类系统错误149/1390=10.7194%，不能只报149/1902而忽略512未知。池等待以采样首末差分表示，累计并发WaitDuration不是单请求时延。

[013设计](cap04/feed-index-design.md)增加非唯一 `(user_id,status,create_time,id)`，保留007、当前关系EXISTS、公开状态、LEFT JOIN和复合游标。[独立测试库回归](cap04/feed-index-regression/report.md)验证版本迁移/重跑与真实social行为；[before](cap03/baseline-plan/explain-analyze.txt)、[after](cap03/after-index-plan/explain-analyze.txt)是相同完整SQL与数据的阶段间单次计划：候选6400→200，after仍有排序，单次约11.3→0.872ms。不是cover、不是只读20行，也不把这个比值称HTTP吞吐提升。

[读剖析](cap04/read-profile-findings.md)保留400与800诊断、800/15秒偶然通过、700附近宿主波动以及正式受限结果。800 CPU profile只有约8.48秒与稳定k6重叠，其余约1.54秒在退出后；默认goroutine文件也在退出后，另补T+4/6.5/9秒负载内栈。DB.conn排队、SQL池占满及I/O路径支持当前配置限制，不能证明扩大pool就更快。累计alloc_space中的时区成本不是RSS/400窗口增量或提升百分比；这些均为旧3db诊断，没有据此追加时区缓存或驱动调整。

随后[借池设计](cap04/feed-pool-design.md)单列了确定性条件：Feed外层snapshot持连接时，冷计数从同池再次借用，池仅1连接即可复现等待。最小实现让公共counts enrichment在事务归还连接后走原Cache，权限/投影/个人点赞/cursor留原snapshot，Cache的TTL/epoch/失效不动，不把tx绑入共享对象。

[真实复现与回归](cap04/feed-pool-regression/report.md)使用隔离MySQL3452/Redis6552，在正式负载暂停时运行。同一新增用例、同一其余源码，通过Go overlay仅换回旧Feed实现，3.20秒失败且池wait增加1；新实现0.27秒通过，确认冷缓存真实填入Redis、warm公共计数/个人状态、同秒cursor及unfollow语义。social六个真实入口和五个普通入口、cache八个真实入口均通过；原专用函数级测量开关未开的一项SKIP明确保留，vet退出0。测试池1用于确定性证明依赖，正式API池仍20。非作者审读与根agent真实执行各自落证；证明此条件依赖已修复，不证明此前每个drop只由它造成，也不先主张HTTP提升。

同fixture切换API另有测量元数据修复：CAP_SOURCE_SHA始终绑定当前API及精确health；CAP_READ_FIXTURE_SOURCE_SHA显式保留原3db来源，manifest原expectedRevision与字节不改，run/finish保存原manifest SHA及双source/revision。新prepare要求两source相同，不把复用夹具写成新API创建。

767c新API的30秒探索中，hot/multi各400与500/s均通过：[hot400](cap03/post-fix-hot-400-pre256-a-load/result.json)、[multi400](cap03/post-fix-multi-400-pre256-a-load/result.json)各12001/12001，[hot500](cap03/post-fix-hot-500-pre512-a-load/result.json)15001/15001、[multi500独立重试](cap03/post-fix-multi-500-pre512-b-load/result.json)15000/15000，均无unknown/dropped。600/s两模式均受限：[hot600](cap03/post-fix-hot-600-pre512-a-load/result.json)9430发起/9415成功/15未知，P95/P99约218.54/595.92ms；[multi600](cap03/post-fix-multi-600-pre512-a-load/result.json)5197发起/5088成功/12未知、1841 dropped，P95/P99约4095.71/4999.64ms。不同预分配VU和宿主波动仍按阶段保存，不将这些探索当180秒成绩或纯修复收益。

multi500的首次尝试在稳定阶段前发生本地Node原生退出3221226505（0xC0000409），[原退出](cap03/post-fix-multi-500-pre512-a-control/exit-status.json)及[不可计分标记](cap03/post-fix-multi-500-pre512-a-load/unscored-failure.json)保留。无稳定结果/采样，业务计数为null、原因未定；它既不是业务失败计数，也不是容量通过，后续重试不覆盖该记录。

旧3db曾选hot300/s；[第1轮](cap03/hold-hot-300-180-a-load/result.json)与[第2轮](cap03/hold-hot-300-180-b-load/result.json)均完整180秒，started/native HTTP/classified/success各54001，unknown、系统/HTTP/内容错误与dropped均0，资源各37/37完整。全部及成功P95/P99分别20.832/33.461ms、23.521/113.676ms。301–399未测；500/600长持SLO失败、400长持dropped351早停不是资源stop，按共同部署/调度观测限制保留，不能指定唯一API根因。这两轮仍属未修复版本，不挪作新SHA的两轮。

新API的[hot500长持首轮](cap03/hold-fix-hot-500-180-a-load/result.json)已在约141.8秒因原P95预算早停：70888发起、70881成功、7未知、dropped0，P95/P99为201.046/361.923ms；29/29资源样本、host峰83.4%、无资源stop。500/s短测通过不能覆盖这次180秒失败，也不向上取整或修改200ms预算。按预定回退开始400档。

降档后的[hot400 A](cap03/hold-fix-hot-400-180-a-load/result.json)完整通过，但[B轮](cap03/hold-fix-hot-400-180-b-load/result.json)早停，不能用单轮包装可重复容量；[hot300 A](cap03/hold-fix-hot-300-180-a-load/result.json)也因dropped/未知受限。更低档失败不抹去较高档的一次通过，也不支持“修复必然提高稳定QPS”的因果结论。

最终只选择同767c API、同fixture与013的以下四轮；每轮180秒，started/native HTTP/终态/成功四计数相等，unknown、系统/HTTP/内容错误与dropped均0，全部及成功延迟相同。每轮37/37资源样本完整，其中36个CPU区间有效，无资源stop且监督退出0。

| 模式/轮次及原始结果 | 成功/发起 | P95/P99 ms | 有效区间host CPU峰 |
| --- | --- | --- | --- |
| [hot200 A](cap03/hold-fix-hot-200-180-a-load/result.json) | 36001/36001 | 25.3774 / 43.4507 | 50.0% |
| [hot200 B](cap03/hold-fix-hot-200-180-b-load/result.json) | 36000/36000 | 25.3714 / 68.020947 | 61.7% |
| [multi100 A](cap03/hold-fix-multi-100-180-a-load/result.json) | 18000/18000 | 89.151635 / 188.785064 | 80.0% |
| [multi100 B](cap03/hold-fix-multi-100-180-b-load/result.json) | 18001/18001 | 17.5534 / 21.8728 | 42.7% |

multi按原预算从[500](cap03/hold-fix-multi-500-180-a-load/result.json)→[400](cap03/hold-fix-multi-400-180-a-load/result.json)→[300](cap03/hold-fix-multi-300-180-a-load/result.json)→[200](cap03/hold-fix-multi-200-180-a-load/result.json)逐档因SLO早停，unknown分别11/6/6/39，才选择100完成两轮。hot的201–299、multi的101–199没有再缩步测量；只报最高已验证可重复档及最近已测受限档，不声称精确连续边界。

四轮完整样本中API/k6 CPU峰分别为86.5/32.5、86.2/31.9、67.2/31.2、46.2/20.3%，均为一逻辑核100%的口径；API RSS峰44.14–45.28MiB、k6峰170.58–179.47MiB，host内存峰76.4–77.2%。dropped0和这些采样值支持最终档能按计划发出，仍不能排除采样间宿主/生成器调度扰动，不能归因所有受限档为服务端独有瓶颈。

采样起点距stable命令起点约1.01–1.25秒，末个完整样本起点早于命令结束0.283–1.628秒；各section顺序采集，末次采样工作部分跨过命令退出。首末诊断差覆盖约180.02–180.03秒，含启动/收尾边界，不等于逐请求完整180秒、原子快照或完整post-exit服务器恢复覆盖。SQL池WaitCount/累计WaitDuration依表顺序为232/8.499s、334/31.971s、136/3.056s、0/0；Redis池wait为89/1.666s、194/26.982s、40/0.684s、0/0，timeouts全0。GC次数/累计暂停分别483/92.613ms、481/239.638ms、250/36.673ms、250/18.365ms，均取保存的首末完整诊断差。并发累计等待不是单次请求时延；两模式、两轮延迟差也不是缓存因果收益。

本次只读复核对四轮result与原native stable-summary的8项计数、全/成功数值分位、API/fixture来源及原manifest字节hash、metadata hash/PID、监督和资源行数逐项匹配，所有检查通过；没有原逐响应body，离线不能重新解析在线分类或重建HTTP分位。[全量内部审计](cap03/internal-evidence-audit.json)及[审读说明](cap03/internal-review.md)覆盖54个正常结果阶段和1个稳定前原生退出，共7130项检查、0失败、0读入/结构错误、0待结束阶段；CLI只显式选上表四轮767c，不用旧3db补轮次。该审计只读本仓公开原始文件，元数据原绝对路径按固定仓库后缀映射，支持其它checkout复核；它是内部交叉复算，不是外部实跑。

## 失败、收尾及核验门槛

[CAP-01失败记录](cap01/resource-probe-report.md)保存Go子Docker管道拒绝、临时池写入拒绝、早期工具草稿构建失败和手工采样过晚；后续未删除护栏或旧数据。读默认网络地址池耗尽通过仅本项目新CIDR override解决，未清他人网络/卷。CPU priming与读终态计量均各自修复，旧raw不重写。

交易为避免15分钟旧订单到期干扰，窗口外经owner正常cancel收尾并等补偿；8729笔取消后的首次180秒等待仍余1690，继续等到零后才开始持有，见[收尾报告](cap04/settlement-report.md)。这不是容量档15秒排空成绩或批量退款容量。最终[交易退出](cap02/teardown.json)撤销25001会话、删除私有池、自有两容器exit0且卷保留；API为身份核对后Windows terminate，不声称验证信号优雅退出。

读[实际收尾](cap03/cleanup.json)已完成：正常logout32，精确Redis会话键EXISTS由32降至0；仅本轮随机只读observer被删除并核实缺失。API64908经绝对exe、UTC创建时刻及二进制hash守卫后Windows native terminate（wait exit15），8251/8351关闭；原记录的两不可变容器ID正常stop、exit0/OOMfalse，保留MySQL/Redis卷与SQL夹具。两份精确私有env、token input及其空叶目录删除，原fixture字节不变。销毁前对1289份证据文件扫描34个唯一私有值，匹配0；未公开进程环境或native dump。

首次清理在任何变更前因psutil读取exe瞬时NoSuchProcess中断，[原预检失败](cap03/cleanup-preflight-failure.json)、[端口/ready复核](cap03/cleanup-api-absence-preflight.json)、[exe/UTC身份复核](cap03/cleanup-api-identity-recheck.json)均保留；同PID/创建时刻和版本核准后使用同守卫重试完成，没有重启API或放宽身份条件。成功重试的逐步进度和最终记录一致，不能把工具预检异常算作Go请求故障，也不称已验证信号优雅退出。

根agent实查[767c2fc CI](https://github.com/tech-cove01/project-full-stack/actions/runs/41016781664)整体success，[完整记录](cap04/ci-767c2fc.json)精确对应本次修复SHA，含Go Linux race、直接真实依赖及社区Kafka作业；[19eab51 CI](https://github.com/tech-cove01/project-full-stack/actions/runs/41014551616)和[41124ae CI](https://github.com/tech-cove01/project-full-stack/actions/runs/41014339010)也成功。较早[7e33f27记录](cap04/ci-7e33f27.json)仅保留其历史范围。这些都不是本轮Windows压力期间race或最终文档提交的CI。交易原始汇总已提交 `de83df7`，索引前读证据为 `41124aecdff154e8d7a61bd3443f4b142f0b2b4d`；本报告按根agent后续实际文档提交交接，该提交CI另核。外部复跑按 [qa-handoff.md](qa-handoff.md)，需新自有卷、固定脚本护栏、正式负载错峰，不使用实现者的内部review替代外部验收。

### Git归档字节校验

读容量证据已提交`706e3fbb410aa360ea1023c8a37033b17611fb1b`。[Git字节校验](cap05/git-byte-audit.json)对该提交检查1651个文件（含1645份原始捕获及425个声明引用），Git blob、工作区原字节和所核声明散列均零差异；包括最终离线审计的403份输入、当前工具源码与两组SQL计划引用。保留原始CRLF，不格式化原JSON/日志来凑散列。该记录不自哈希，也不覆盖之后仅新增的报告文字；它不替代外部复跑。

## 简历候选句（待外部核验）

- 交易候选：保留鉴权、订单事务、库存锁与审计/outbox，以有界独立消费者在本机固定资源下完成30次/秒、180秒两轮验证，稳定期各5401笔订单最终一致，P99约16.13–17.16ms；通过持续成单和队列验证区分准入与端到端承载。
- Feed候选：以作者前缀复合索引将固定6400篇夹具的代表性完整查询候选缩至200，并消除冷计数缓存嵌套借池依赖；保留权限与分页，在本机固定资源下完成hot200/s、multi100/s各180秒两轮，四轮无错误、未知或dropped，P99分别43.45–68.02ms和21.87–188.79ms。

两句均须附机器、资源、页长/分布或单活动条件，不写理论最大、通用生产SLA、50%持续容量提升或尚未完成的独立验收。
