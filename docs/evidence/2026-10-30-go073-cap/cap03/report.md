# CAP-03 真实 HTTP Feed 容量与收尾报告

状态：本机自测与收尾完成，外部独立复跑待交接。Feed条件饥饿修复后的固定新API767在hot200 RPS、multi100 RPS各完整通过180秒两轮，共108002个HTTP请求全部成功分类，unknown/drop/系统/HTTP/内容错误均0。旧API3db的hot300/180两轮只作历史，不计入最终资格。这是本机共同部署条件下可重复观测档位，不称通用生产容量或理论上限。

## 固定条件与改变

当前固定干净API源码`767c2fc8bdf1be08c5636143a71898eec25081a9`，PID64908、version `capacity-767c2fc8bdf1be08c5636143a71898eec25081a9`、二进制SHA见[api-pool-fix.json](api-pool-fix.json)。原[api.json](api.json)完整保留旧3db/45532身份，不覆写。两构建均固定GOMAXPROCS4/GOMEMLIMIT1GiB、订单消费者1、SQL pool20；MySQL4 CPU/2GiB、Redis2 CPU/512MiB。本实验不停止或调整无关服务，共享Windows/Docker宿主负载会波动，详情见[environment.json](environment.json)。这是本配置观测，不能称通用生产容量或理论上限。

同一6400篇固定256字节正文、32作者/32真实登录读者、每页20条。hot固定一位读者/20计数key，multi32读者轮转/640key；每个成功响应都校验固定20个字符串ID/作者/门店/零计数/本人未点赞及`source=database`。Kafka/ES/AI关闭，不推断事件消费或搜索容量，不追加付费AI。

索引前原始基线与失败保留在[baseline-report.md](baseline-report.md)、[baseline-summary.json](baseline-summary.json)。第一阶段在同一3db应用上仅以正式013迁移`5aa658a7c5c65149d3e0c47759d3798e71c7ddfd`增加author/status/time/id索引，保留007全局索引；SQL谓词/EXISTS/连接和API进程/二进制/数据不变，见[index-intervention.json](index-intervention.json)。[before](baseline-plan/manifest.json)/[after](after-index-plan/manifest.json)同SQL同合成hot参数的实际计划由扫6400行变为索引候选200行，after仍hash join+Top20排序，不称读20即停或无排序；单次SQL探针不当HTTP提速倍数。

随后源码审查发现Feed持outer snapshot连接时，计数miss再次向同global池借连接可造成条件饥饿；最小修复经非作者审读与真实回归后，固定构建767。该阶段保留013、同一6400夹具、原始manifest字节及资源，只切换应用构建；原API使用exe/PID/UTC创建时间守卫停止，新身份单独记录，见[旧API停止](api-3db-stop-for-pool-fix.json)、[新API检查](pool-fix-initial-health.json)和[新事前计划](pool-fix-plan.json)。这个真实缺陷不自动证明是旧全部drop的唯一根因，宿主和生成器扰动证据仍保留。

## 最终两模式独立长持

完整机器可读数据落[summary.json](summary.json)。767重新资格计划见[pool-fix-plan.json](pool-fix-plan.json)，600触限后500起长持，失败就按400→300→200→必要100下降，同档必须180秒两轮。此前3db的[原计划](long-hold-plan.json)、[后续扩展](long-hold-plan-extension.md)完整保留，未倒填。不能混合不同构建或档位，也不能省略失败。

| 模式/档位 | 180秒轮次 | 完整分类成功 | P95/P99 ms（全请求=成功请求） | 完整资源样本 | 状态 |
| --- | --- | --- | --- | --- | --- |
| 新API hot200 | A | 36001/36001 | 25.377/43.451 | 37/37 | 通过 |
| 新API hot200 | B | 36000/36000 | 25.371/68.021 | 37/37 | 通过 |
| 新API multi100 | A | 18000/18000 | 89.152/188.785 | 37/37 | 通过 |
| 新API multi100 | B | 18001/18001 | 17.553/21.873 | 37/37 | 通过 |

最终四个原始结果：[hot A](hold-fix-hot-200-180-a-load/result.json)、[hot B](hold-fix-hot-200-180-b-load/result.json)、[multi A](hold-fix-multi-100-180-a-load/result.json)、[multi B](hold-fix-multi-100-180-b-load/result.json)。每份均独立记录767 API/3db夹具来源、原manifest SHA256、命令metadata路径/hash及实际生成器PID；没有混合来源。完整汇总包含54个已结束有业务结果阶段，以及1个不可计分native启动失败。

每轮37/37资源样本完整且观察错误0，其中36个CPU有效区间；首采样起点距稳定命令开始约1.01–1.25秒、最大采样间隔约5秒，最后采样起点距稳定命令结束约0.28–1.63秒。每点按顺序收集多个观测，末点部分内容可能在生成器退出后完成；这不是逐请求的原子覆盖，也不证明完整post-exit服务端排空。约5秒采样不能排除间隔内尖峰，实际spawn与采集范围另见[内部复算说明](internal-review.md)。raw首次CPU保留但按既定priming标记不当稳态CPU；multi B首次API CPU495.7%属于该无效priming读数，未改写raw。

| 轮次 | 有效host CPU峰 | host内存峰 | API RSS峰（bytes） | k6 RSS峰（bytes） | SHOW Com_select | SHOW Com_stmt_execute | Redis EVALSHA / MGET |
| --- | --- | --- | --- | --- | --- | --- | --- |
| hot A | 50.0% | 76.4% | 46866432 | 181673984 | 146129 | 146004 | 36001 / 333 |
| hot B | 61.7% | 77.2% | 46661632 | 188190720 | 146100 | 145977 | 36000 / 211 |
| multi A | 80.0% | 76.9% | 47480832 | 178868224 | 74980 | 74857 | 18000 / 2592 |
| multi B | 42.7% | 76.6% | 46284800 | 179703808 | 74942 | 74817 | 18001 / 2592 |

四轮counter reset均false、uptime检查一致，Redis超时0、资源stop0。MySQL/Redis容器采样内存分别最高31.30%/2.27%，没有触资源预算。multi两轮host峰80.0%/42.7%且延迟有差异，保留这项波动，不将两轮差异或hot/multi不同档位解释成缓存因果收益。完整SHOW/Execute/Redis增量、池等待和GC采样见summary，均是观测器端点间增量，非精确逐请求归因。

通过条件保持原预算：发起/名义计划≥99%、成功比例≥99%、dropped0、系统/HTTP错误率<1%、内容错误0、全与成功P95≤200ms/P99≤500ms，以及无资源stop/未知分类。VU不等于RPS，实际设置和资源随每档保存。[生成器配置](generator-config.md)保留原400/pre64一次drop失败，后续充分预分配配置先落计划；175/200索引前后对照仍固定原配置。raw summary中的threshold布尔不独立当pass，采用数值、exit和harness判据。

## 受限档、观察范围与限制

旧3db阶段的索引前hot/multi175分别早停，同档索引后完整30秒通过。该阶段400/pre64一次drop、800/700/650延迟受限及15秒800诊断偶然通过均保留。旧长持600、500、400和multi300的失败，以及hot300两轮通过均只作历史，不拿30秒探索或旧构建轮次称767的180秒稳定。

767保持原预算和pool，hot/multi400与500的30秒探索均完整通过；600分别延迟/生成器发出受限而早停，没有向800追加负载。hot500首次180秒在约142秒触P95=201.046ms，已完成分类70881、unknown7、drop0，后续按事前计划降400。hot400首轮完整通过、第二轮P95=263.535ms早停；hot300虽已分类P95/P99仅35.274/185.085ms，仍因drop70、unknown274而失败。最终hot200两轮均通过，没有把较低分位覆盖发出缺口。multi500、400、300、200的180秒尝试分别P95/P99约202.194/526.613、202.535/428.637、206.348/282.607、208.370/462.307ms而早停，unknown分别11/6/6/39，最终multi100两轮通过。所有早停名义分母仍保留180秒，不能把由此得到的低速率当持续吞吐。

multi500一次本地native harness启动退出3221226505（0xC0000409），未观察到stable生成器/资源样本且无业务result；原因未知。此阶段[不可计分记录](post-fix-multi-500-pre512-a-load/unscored-failure.json)引用原始监督证据，业务计数保持null；独立b重试完整通过。没有改变Node、API或预算，也未公开进程环境/native内存转储。

旧3db的真实CPU/alloc/goroutine/SQL/锁采证与范围见[CAP04报告](../cap04/read-profile-findings.md)，不将旧profile当767根因证明。旧负载内确有SQL pool20连接获取等待；CPU profile主要是网络/数据库调用路径，不能仅用CPU或空锁快照推出唯一根因。旧宿主有效区间存在85–95%波动；资源预算为CPU85%连续三样本才stop，短档先SLO早停不等于宿主无扰动。767每轮仍单独保存真实资源和池等待，最终结果只能解释为本配置共同部署观测。时区重复加载有累计分配成本，未将累计alloc当RSS/窗口CPU；除后来必要的条件饥饿修复外，未继续改时区、驱动参数或池容量。

MySQL实际SHOW GLOBAL STATUS与Execute、Redis commandstats均保留前后快照。它们含API、鉴权、正常后台扫描及观察器；Com_select与Com_stmt_execute交叠，不相加为SQL总数。prepared SQL digest有盲区，公共计数DB回源保持未知/null，不宣称零回源或由hot/multi推缓存因果提升。未保存逐响应body，离线不能重新解析所有响应分类；每次在线验证和原代码/SHA可复跑。中止请求unknown单列，不能补零或称服务端完成；修复见[终态计量](completion-meter-review.md)。

## Cleanup（已实际完成）

四轮实际结果先交根agent，再由已独立审读的[收尾脚本](finish-and-cleanup.py)重读四轮raw及来源/预算/退出/资源证据并拒绝pending stage。正常logout32后精确Redis EXISTS从32降至0；仅本轮只读observer已drop并核实缺失。API64908经固定metadata路径、绝对exe、原UTC创建时间差0.936ms及二进制SHA核验后Windows native terminate，wait exit15，不称Go graceful shutdown；8251/8351已关闭。只对原environment.json的两个不可变CID正常docker stop，exit0、OOMfalse，两个卷/SQL夹具及原manifest字节完整保留。原/新两精确私有env以及token input与其空叶目录已删除。证据目录1289文件在销毁前对34个唯一私有值扫描，匹配0；未公开进程环境或native内存dump。

首次收尾在任何变更前因psutil读取exe瞬时NoSuchProcess退出；随后只读证实同PID仍健康、exe和原UTC epoch匹配，没有重启或放宽守卫。首次失败、健康/身份复核分别保存在[预检失败](cleanup-preflight-failure.json)、[端口健康](cleanup-api-absence-preflight.json)、[身份复核](cleanup-api-identity-recheck.json)。同守卫重试后所有动作完成，见[逐步进度](cleanup-progress.json)、[最终收尾](cleanup.json)、[32会话finish](fixture-01/finish.json)。成功重试期间未观察到callback/操作异常，首次预检异常保留而非抹除。

内部代码/基线/计划/profile审阅和本机自测不代替外部独立复跑。Linux race/真实依赖CI按其实际SHA单独引用，不能把较早CI当最终报告提交已通过。
