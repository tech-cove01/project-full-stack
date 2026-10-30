# CAP07 A/B 容量协议记录

2026-10-30，A1→B1→A2→B2 四轮已完成；只读算术审计通过、精确收尾完成。**公共内容缓存功能已有自验与精确源码 CI 通过，但本轮容量量化失败，未证明一致收益，不能标记 CAP07 最终完成。** A两轮完整通过，B1早停/B2通过，B100重复通过条件不成立；`finalBProtocolPassed=false`。按事前协议未执行阶梯、邻近缩步、最高档持有或 hot200，没有追加重试补齐。

## 固定源码、条件与证据范围

- A：`f3d99ee43dfb422a0cba525de93e9266f07c4ee5`，CAP06V 已证计数门修复后、尚未接公共正文缓存，见 [build-a.json](build-a.json) / [build-a.txt](build-a.txt)。
- B：`30b6571227b5508d0f04bc8c4cfc1bcfbc313275`，最终带同批共享 flight 的版本化公共片段缓存，见 [build-b.json](build-b.json) / [build-b.txt](build-b.txt)。A/B 保留同一相关计数门修复，不将其差量算成内容缓存收益。
- 两端在各自 detached clean checkout 用同一 Go/参数构建，VCS revision、modified=false、exe hash 和启动 PID/创建时刻分别记录；API 切换共用本轮新 fixture 与依赖，不覆盖原始证据。
- 固定 project `project-full-stack-cap-read`，loopback MySQL3451/Redis6551/API8251/diagnostics8351，新 CAP07 随机卷且旧卷保留。MySQL4CPU/2GiB、Redis2CPU/512MiB、API GOMAXPROCS4/GOMEMLIMIT1GiB、SQL池20/Redis池10，001–013及持久化不改；Kafka/ES/AI关闭。见 [environment.json](environment.json)、[fixture-provenance.json](fixture-provenance.json)。
- 6400篇/32作者/32读者、正文256B、页长20，每读者关注一作者；multi均匀轮转640个公共key，原生事实源为database。正式负载均计划 multi100/s×180秒，预热10秒/25/s、pre/max VUs256/512；heavy Feed diagnostics=false，仅原5秒资源采样。
- 事前顺序固定 A1→B1→A2→B2，后续档须以两个 B100 完整通过为前提。判据、安全上限和限次见 [plan](plan.md) / [runbook](runbook.md)，本次没有因为早停调宽判据。

本报告作者只读取已落的公开工件、做本地 JSON 算术与静态审查，未复跑认证/SQL/Redis/API/负载，正式执行由根 agent 完成。本记录与 [internal-review](internal-review.md) 都属于实施团队证据，不替代用户另行安排的外部独立核验。

## 正式对照结果

下列分位逐轮取原生 `stable-summary.json`，不跨轮平均、不离线重建逐响应分布。计划分母始终为180秒，即使 k6 早停也不改成实际运行时长。

| 阶段 | 发起 / native HTTP /完成 /成功 | unknown / dropped | 全部P95 / P99（ms） | 成功P95 / P99（ms） | 计划发起比例 | 成功/计划秒 | 结论 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| A1 | 18001 / 18001 / 18001 / 18001 | 0 / 0 | 28.2026 / 36.3832 | 28.2026 / 36.3832 | 100.0056% | 100.0056/s | PASS |
| B1 | 5788 / 5787 / 5787 / 5787 | 1 / 0 | 40.8236 / 1449.5717 | 40.8236 / 1449.5717 | 32.1556% | 32.1500/s | FAIL，早停 |
| A2 | 18001 / 18001 / 18001 / 18001 | 0 / 0 | 29.7205 / 45.3673 | 29.7205 / 45.3673 | 100.0056% | 100.0056/s | PASS |
| B2 | 18001 / 18001 / 18001 / 18001 | 0 / 0 | 29.8148 / 39.8796 | 29.8148 / 39.8796 | 100.0056% | 100.0056/s | PASS |

四轮的 content、system、HTTP 已分类错误均为0；B1仍有1个未完成/未分类发起，不能以已完成请求全成功称为本轮成功。A1/B1/A2/B2 stable命令墙钟分别181.529/59.398/182.874/181.448秒（包含命令启动/退出开销）；B1 stable exit99，原生P99阈值触发早停，非外部资源stop。B1成功/计划秒32.15是计划180秒分母，不能解读为活跃区间吞吐。A2原生max=789.2475ms，即使P99合格，仍不能说所有请求均低于500ms；B2 max=66.7818ms。

原始记录：[A1 result](a1-multi100-load/result.json)、[A1 native summary](a1-multi100-load/stable-summary.json)、[A1 control](a1-multi100-control/exit-status.json)；[B1 result](b1-multi100-load/result.json)、[B1 native summary](b1-multi100-load/stable-summary.json)、[B1 control](b1-multi100-control/exit-status.json)；[A2 result](a2-multi100-load/result.json)、[A2 native summary](a2-multi100-load/stable-summary.json)、[A2 control](a2-multi100-control/exit-status.json)；[B2 result](b2-multi100-load/result.json)、[B2 native summary](b2-multi100-load/stable-summary.json)、[B2 control](b2-multi100-control/exit-status.json)。两个短probe各101次发起/101次成功且原判据通过；**Aprobe尾段曾与B构建重叠，probe只作接线检查，不能作为正式对照或容量成绩**。

## 公共内容 loader 与全实例访问量

独立内容计数只存在于 B。A未接此组件，内容loader量为null，**不能填0、不能据此算正文SQL减少百分比**。B端点差量覆盖“预热前→wrapper收尾后”，包含预热、稳态和后台工作，不能冒充稳态精确内容SQL量。每个 gauge 只是端点/采样时刻的状态，不是次数或绝对峰值。

| 已完成阶段 | B内容SQL批次 / 行 / 内容字节 | B flight started / joins | B L1 hits / Redis hits / miss items | 全实例 Com_select / Com_stmt_execute（稳态快照窗） |
| --- | --- | --- | --- | --- |
| A1 | null | null | null | 74667 / 74544 |
| B1 | 151 / 1192 / 334567 | 151 / 96 | 101004 / 18038 / 1718 | 24182 / 24137 |
| A2 | null | null | null | 74482 / 74359 |
| B2 | 437 / 2005 / 562772 | 437 / 0 | 310620 / 52395 / 2005 | 75034 / 74911 |

B1/B2 loader的load_errors、busy_rejected、redis_errors、invalid_payloads、oversize_uncached均0；收尾均640条L1/227000序列化payload字节，inflight/active/waiting均0。B1预窗已有640条L1，B2重启后的预窗为0；两轮正式阶段均按协议预热10秒，含warm差量不能直接用151对437批比较稳态回源成本。B2 joins0表示该轮未观察到同批并发合并，不否定受控HTTP突发已验证的合并行为。内容字节是实际DB行title/content/原images的UTF8总字节，非wire、RSS或最终响应字节。见 [B1 cards delta](b1-multi100-cards-delta.json) / [B2 cards delta](b2-multi100-cards-delta.json)。

全实例SQL两项计数重叠，分别列示不相加，含API/后台/helper/observer，并非每接口SQL次数；A1与B1实际观测时长不同，不能用24182对74667直接声称缓存减少约三分之二SQL。完整成功档B2的全实例计数也未低于A1/A2，不能宣称全实例SQL次数下降；查询次数本身又不等于正文读取字节。四轮原始访问差量均无counter reset、digest/prepared loss；但这不消除上述范围限制。见 [A1 access delta](a1-multi100-load/access-delta.json) / [B1 access delta](b1-multi100-load/access-delta.json) / [A2 access delta](a2-multi100-load/access-delta.json) / [B2 access delta](b2-multi100-load/access-delta.json)。热内容缓存仍需读取授权/目录/个人态及其它事实，不代表Feed0DB。

## B1 等待现象与安全边界

B1最后一条5秒资源记录的外层时刻为 `11:20:33.790596Z`，其诊断响应内部时刻为 `11:20:34.845713Z`，二者不原子。该样本 SQL in_use=20/20、WaitCount=160、累计WaitDuration=19.7865206秒；Redis WaitCount=76、累计WaitDuration=2.7787627秒。cards inflight=25/64、active=8/8、waiting=37/1024，均未越过组件固定上限。

预热前至wrapper收尾后的端点累计差另为 SQL WaitCount+351 / WaitDuration+115.2335135秒、Redis +1055 / +14.2800203秒；A1/B2同范围差均0。A2同范围也有 SQL +80 / +13.7685399秒、Redis +42 / +2.597011秒，P99仍通过，因此池等待并非只在B出现。**不能拿较早的最后sample当最终累计值，也不能把累计WaitDuration当作单请求延迟。** 收尾后的 loader gauge 归零，与负载中曾有排队可以同时成立。见 [B1 resources](b1-multi100-control/resources.ndjson)、[before](b1-multi100-capacity-before.json) / [after](b1-multi100-capacity-after.json)。

| 观测项 | A1（37个完整样本） | B1（12个完整样本） | A2（37个完整样本） | B2（37个完整样本） | 原安全上限 |
| --- | --- | --- | --- | --- | --- |
| host有效CPU样本最大值 | 43.1% | 39.6% | 53.8% | 36.7% | 85%连续3个有效样本 |
| host内存最大值 | 82.1% | 82.6% | 83.3% | 83.5% | 88% |
| API RSS最大值 | 36.94MiB | 37.42MiB | 37.69MiB | 37.65MiB | 1.5GiB |
| k6 RSS最大值 | 172.65MiB | 162.17MiB | 169.86MiB | 173.02MiB | 2GiB |
| MySQL / Redis内存百分比最大值 | 25.29% / 1.97% | 27.57% / 1.97% | 28.04% / 3.02% | 28.11% / 2.02% | 各85% |

四轮无control stop、无强制终止、sampler exit0，已落完整样本未触原资源上限。B1 harness/wrapper均为1，来自负载判据失败；control的综合成功字段包含harness成功条件，不能把它单独解读为采集器故障。5秒采样不能保证捕获瞬时峰或所有宿主停顿；低采样CPU、池等待与组件有界都不能单独证明根因。当前只能记录B1尾延迟、SQL/Redis等待与回源并发同时出现，**尚不能归因于缓存、数据库、Redis或宿主，也不能宣称 CAP06 的原秒级长尾已修**。不新增探针、抓栈或完整诊断轮来补本轮归因。

## 功能与内部静态审查

[phase1](phase1.md) / [phase2](phase2.md) 与原始真实回归输出记录了公共片段批量冷热、版本不完整一次新RR回退、个人点赞隔离、关注/下架/同秒游标、单SQL连接、共享首caller取消、过期重建、超大不缓存、坏值/错误类型/真实SQL故障及Close收敛自验。它们证明这些受控功能行为，不能替代持续负载的分位/容量结论。

本报告作者 `cap07_delivery` 对他人编写的计数分片门、cards A2/A3、social接入与实际HTTP测试做过非作者静态审查，未发现最终冻结实现的阻断：目录仍控制授权/版本/门店/个人态/游标，RR返回后才等内容cache，版本/Busy仅按原cursor一次全字段新RR回退；同批flight从应用life派生有界context，caller独立取消，短锁只管内存，done同步与逐caller Images复制、准入及L1双预算保持。另提醒并由作者修正过二查cache的命中计数口径，内部二查不计caller命中。容量工具和audit由本报告作者编写，其非作者审查由另一agent承担，见 [internal-review](internal-review.md)，不将自审包装成非作者审查。

精确B源码 `30b6571` 的[远端CI记录](ci-30b6571.json)及[选定测试日志](ci-30b6571-selected-tests.txt)已显示 backend-go/backend/frontend/community-kafka 四job success；Linux普通及真实依赖race、实际HTTP/组件测试的日志也已归档。CI支持正确性与竞态检查，不是本地共享Windows宿主的容量通过证明；后续文档提交也不能借此冒称自身全部CI已跑。

## 只读复算、停止范围与收尾

根agent明确授权后，执行一次 exclusive `scripts/cap07-audit.py --output docs/evidence/2026-10-30-cap07/audit-final.json`，exit0，见 [audit-final](audit-final.json)。六阶段含两个probe的公开工件/脚本/API/exe/fixture hash、原生计数/判据、loader差量及既有协议复算均无结构/算术错误；`artifactErrors=0`、`plan.errors=[]`，但 `finalBProtocolPassed=false`、`highestExploredPassedRate=null`、`aRepeatedPassed=true`。**audit退出0表示工件一致，不表示容量通过。** Aprobe虽然算术可复核，因构建重叠仍仅作接线证据。

原始工件已固定提交 `980d01556951d9ae3da60dc9589b83d018cd62b8`；[raw-manifest](raw-manifest.json)记录213份/1430913字节，[Git字节审计](git-byte-audit.json)逐项核该提交的213个blob与落盘前冻结SHA256一致，failures0。这是原始字节保留检查，不增加容量样本，也不重建逐响应分位。

本组A两轮通过、B一败一过，未证明一致HTTP收益或提高容量；B2成功不能补掉B1失败。受门控约束，stairs/neighbor/最高持有/hot均未执行，不存在本组最高重复通过档或更高容量数字。后续核验可沿[代表档复跑说明](representative-rerun.md)另建独立QA cohort，仅复跑最终B的multi100×180，不重放旧token或覆盖本组证据；新QA结果另记，不能回写本组为通过。

根agent按runbook完成精确清理，见 [cleanup](cleanup.json) / [progress](cleanup-progress.json)：32会话正常logout、Redis sessions 32→0，observer removed-and-verified；API PID14328的创建时刻差0ms、exe/hash匹配后停止，wait exit15，明确不称Go优雅退出。两只记录的完整CID均exited/exit0/OOMfalse，挂载未变，旧新卷和SQL事实保留；只删登记的6份private env及正常logout删除的本轮输入。先前受控测试环境已按精确CID停止并留卷，其单一已知private env随后精确删除，见 [controlled private cleanup](controlled/private-input-cleanup.json)。收尾完成不等于容量量化、外部独立核验或项目最终交付完成。
