# CAP-07 独立证据与量化复核

2026-10-30。交付 `6693539ae3715cc28f1b52b7d1583a713659ca4e`。本子审查读取固定review checkout、Git历史、原始JSON/native summary/资源记录、构建产物hash及归档CI/收尾，未启动服务、发HTTP/SQL、压测或付费调用，未改原证据。功能源码、真实回归与主核验者的执行结果另记，不将它们算成本子审查实跑。

**结论：所核原始工件与报告一致，未发现确定的P1/P2统计或来源问题；本组容量确实未通过，也没有一致HTTP收益。** A1/A2通过，B1原生P99失败、1次unknown，B2通过不能补齐失败组。新公共内容缓存与同批合并的功能是否通过，和持续容量是否通过必须分列。

## 方法、范围与来源

读取[事前协议](../../../evidence/2026-10-30-cap07/plan.md)、[本批报告](../../../evidence/2026-10-30-cap07/report.md)、[容量记录](../../../evidence/2026-10-30-cap07/harness-report.md)、`scripts/cap07-stage.py`、`scripts/cap07-audit.py`及六个已执行stage。没有调用会以exclusive方式新建`audit-*.json`的正式audit CLI，以免写入原证据。

实际用一次性PowerShell here-string经`python -X utf8 -`独立复算，没有调用作者audit的计分函数：

1. 按command时间排序，核probe与A1/B1/A2/B2；逐阶段将八项native counter、全部/成功avg/median/P95/P99/max与result匹配。由原始counter、计划分母、原生退出与原预算另算通过条件，不依赖`meetsBudget`布尔。
2. 重新计算fixture原字节、API metadata和七份实际工具文件的SHA256；metadata绝对路径仅用捕获basename映射到本checkout，仍严格核原字节hash/PID/版本，未放宽来源比较。读取两只已保存exe的实际字节，核冻结hash；未执行或重新编译这些exe。
3. 从capacity-before/after重新作cards全部counter差，逐项核保存delta；gauge只比较端点，不算事件次数或峰值。A无cards维持null。重新从before/after SQL快照算Com_select和Com_stmt_execute，各自与access-delta匹配，不相加。
4. 逐行核资源记录完整性、API/诊断PID、有效CPU区间、控制器及sampler/负载退出、stop/forced；另算资源峰值和SQL/Redis池端点累计差。
5. 读取raw-manifest的213份文件，以`git cat-file --batch`读取冻结证据提交的blob，比较实际字节、长度与SHA256；不是只相信已有Git审计JSON。核归档CI精确SHA/作业及cleanup、fixture finish、容器ID/挂载和私有文件记账。

另实际执行的只读Git差量检查：

```powershell
git diff f3d99ee43dfb422a0cba525de93e9266f07c4ee5 30b6571227b5508d0f04bc8c4cfc1bcfbc313275 --stat -- backend/internal/communitycache/counts.go
```

该文件差量为空，A/B保留相同计数分片门修复，不能把它计入内容缓存差量收益。原audit自身字节SHA256 `3ad9550f1ef3140e5ccde72d0487fb23a83a66ddef1a5f07f701c750c0dae393`与保存结果一致；保存的6阶段artifact错误0、plan.errors为空、`finalBProtocolPassed=false`、`aRepeatedPassed=true`和最高档null，与本次独立复算相符。

## A/B结果及失败原因

四个正式阶段都计划multi100/s、180秒，预热10秒/25/s、pre/max VUs256/512，6400篇/32作者/32读者、正文256B、页20。额外重型Feed诊断关闭，仅5秒资源采样。A/B正式次序为A1→B1→A2→B2；两个probe各101次完整成功，只作接线。Aprobe末段与B构建重叠已披露，不列作正式效果对照。

| 阶段 | 发起/native/终态/成功 | unknown/drop | P95/P99 ms | HTTP最大ms | stable命令墙钟s | 独立计分 |
| --- | --- | --- | --- | --- | --- | --- |
| A1 | 各18001 | 0/0 | 28.2026 / 36.3832 | 76.5943 | 181.529 | 通过 |
| B1 | 5788/5787/5787/5787 | 1/0 | 40.82359 / 1449.571716 | 2054.2401 | 59.398 | 失败 |
| A2 | 各18001 | 0/0 | 29.7205 / 45.3673 | 789.2475 | 182.874 | 通过 |
| B2 | 各18001 | 0/0 | 29.8148 / 39.8796 | 66.7818 | 181.448 | 通过 |

四轮已分类系统/HTTP/内容错误均0。B1的stable exit99、wrapper/harness exit1、sampler exit0，无资源stop或forced，符合原生P99阈值早停；不是采样器失败，也不是整轮成功。5788−5787=1是未完成/未分类，只读请求被终止后不能补为成功或已有系统错误。

B1成功/计划秒为5787/180=32.15，不代表活跃58秒内实际承载只有32.15/s或系统容量上限。分位来自原生summary逐项核对，没有逐请求时延可重建，不合并或平均各轮分位。A2的P99通过同时存在max789.2475ms，不能写成A没有任何长尾；B2也没有整体优于两轮A的P95/P99。

预定四个comparison slot及两个probe slot逐一对应，公开记录无stairs/neighbor/hold/hot阶段。B1失败后仍执行的B2原本就在事前四轮组内，不是额外补成功；两个B100并未全部通过，后续探索、邻近、最高档和hot门控未开放。不能以B2或之后单独QA通过回写本组为通过。

## cards指标与可支持的收益

cards计数范围为预热前到wrapper收尾后，包含warm、稳定及后台工作；不是精确稳态窗口。B1预窗物理L1条目640、B2为0，条目数本身不证明这些条目尚未过期或有效命中，不能据此简单归因两轮差异。两轮都保留正常预热、原TTL与自然过期成本。

| B端点counter差 | B1 | B2 |
| --- | --- | --- |
| cards requests | 6038 | 18251 |
| L1 hits / Redis hits / miss items | 101004 / 18038 / 1718 | 310620 / 52395 / 2005 |
| flight started / joins | 151 / 96 | 437 / 0 |
| 实际内容SQL批次 / 行 | 151 / 1192 | 437 / 2005 |
| 实际内容UTF8字节 | 334567 | 562772 |

两轮均有`L1 hits＋Redis hits＋miss items＝cards requests×20`，分别120760/365020项；flight_started＝loads_started＝内容SQL批次，所有保存counter差与原始端点相符。load_errors、busy_rejected、redis_errors、invalid_payloads、oversize_uncached均0。最终L1为640条/227000编码payload字节，inflight/active/waiting均0，符合上限；端点归零不否定负载中排队。

A组件计数确实缺省/null，不能转成0内容SQL或据此算正文查询节省百分比。B2 joins0只说明这轮未观察到同批重叠；受控8 HTTP的1flight/7join/1SQL2行是不同证据范围，不能当作持续100RPS收益。已有冷HTTP1批2行、热0内容SQL体现功能接入与加载替代，热内容加载0仍不代表接口0DB；这些受控真实行为的独立执行由主核验报告另记。

全实例两项重叠SQL差分为A1 74667/74544、B1 24182/24137、A2 74482/74359、B2 75034/74911。B1早停与其它完整窗口不能直接比总数；B2全实例次数也未低于A。查询次数又不等于正文读取字节，因此本批不能证明总SQL量降低、HTTP提速或容量提高。只能据各自范围讲实际公共片段缓存/批量回源/同批合并机制，不能写已经获得一致性能提升。

## 资源与因果边界

| 观测 | A1 | B1 | A2 | B2 |
| --- | --- | --- | --- | --- |
| 完整资源/有效CPU区间 | 37/36 | 12/11 | 37/36 | 37/36 |
| host有效CPU最大% | 43.1 | 39.6 | 53.8 | 36.7 |
| host内存最大% | 82.1 | 82.6 | 83.3 | 83.5 |
| API RSS最大MiB | 36.94 | 37.42 | 37.69 | 37.65 |
| k6 RSS最大MiB | 172.65 | 162.17 | 169.86 | 173.02 |

记录均完整且sampler退出0，无资源stop/强制终止；有效CPU排除初次priming值。5秒样本不能证明间隔内没有暂停或瞬时峰。

B1最后resource外层时刻`11:20:33.790596Z`、诊断body时刻`11:20:34.845713Z`。当时SQL InUse20、累计WaitCount160/19.7865206秒，Redis76/2.7787627秒，cards inflight25/active8/waiting37，均未超64/8/1024固定上限。预热前到收尾后端点差为SQL351/115.2335135秒、Redis1055/14.2800203秒；不能把较早last sample当最终累计。

A2同范围也有SQL80/13.7685399秒、Redis42/2.597011秒，A1/B2差均0。池等待与内容回源并发同窗成立，但不能因此确定是新缓存、MySQL、Redis或宿主的单一触发。累计等待是并发累计，不能当一次请求时延；达到loader active8只证明上限在工作，不证明它是根因或应扩大池。原1.137秒事故与本次B1 1.45秒P99均未由这批数据归因。

## 字节、构建、CI及收尾

raw-manifest的213份公开原始文件共1430913字节，与冻结证据提交`980d01556951d9ae3da60dc9589b83d018cd62b8`的Git blob、保存长度和SHA256逐一相同，差异0。该范围不包含所有未来文档，也不重建逐响应body/分布。

两只实际exe重算SHA256分别为A `fe5d594762cc7d1485fd283412b00d87c7c918ef3ba83ad02f0c8da9e38e7ee3`、B `2522ce89f53b15a27a0b8a5db8d3f40e6dd9ca42ebda2c4558668761bbad8ec8`，与构建/启动元数据相符；源为f3d99ee/30b6571、构建前后clean及VCS modified=false记录一致。六stage的metadata原字节、API PID、fixture manifest、实测脚本hash均核对通过；未在本子审查重新构建或运行这些exe。

归档CI精确headSha `30b6571227b5508d0f04bc8c4cfc1bcfbc313275`、run41046300775，四job成功；CI支持对应源码验证，不能升级为本机容量通过。本子审查核的是已归档CI与日志引用，没有重新查询远端或把669文档提交当同次CI。

cleanup与fixture finish均正常注销32会话，Redis会话32→0；两个不可变CID及挂载与environment一致，exit0/OOMfalse、卷保留。6份read私有env及受控环境单一test env回收记录匹配；最后API14328按身份停止，exit15且未称Go优雅退出。四个API进程各自停止/切换资料保留，未把3次切换说成4次切换。这里复核记录，不声称本子审查现场停止容器或重做已经销毁凭据的逐值扫描。

工具可移植性有明确限制：原`cap07-audit.py`的CLI绑定固定cohort目录、记录的绝对metadata路径及本机exe；另一个checkout/机器直接运行不保证可用。本次独立复算按已保存basename核本checkout原始字节，并另核可读取的原exe，不依赖该CLI退出0来判断真实性。这是审计执行范围，不影响上述原件一致性，也不能作为放宽hash或给缺失产物补成功的理由。

当前可交结论为“功能材料另验、A/B完整失败留证、容量与一致收益未证”。无依据追加成功轮、提高QPS口径或把新缓存收益覆盖原失败均不成立；本子审查没有拓展测试、负载、功能或新中间件。
