# CAP-08A：B1失败窗口与最小观测缺口

2026-10-30，起点 `1de173517a54c3be46748779437b592f7bcdbc58`。本票只读已有CAP-07原生结果、访问/资源/端点和当前应用源码，未启动服务、执行SQL/API、发负载或补探针；不占用CAP-08受控场景、诊断或修复名额。原B仍为 `30b6571227b5508d0f04bc8c4cfc1bcfbc313275`，当前交接与其backend无业务差量。

[抽取JSON](failure-window.json)保留12份原始输入的字节/散列、B1与A2各4个选定样本、原生计数和完整前后端点。它是旧证据整理，不是新的容量或现场观测。

## 已知事实与时间轴

以下UTC时刻加8小时即上海时间。`elapsed`取5秒资源采样器自身的单调时钟；资源行起始时刻、capacity handler内部时刻和卡片/SQL/Redis字段读取并不原子。原生stable开始分别为B1 `11:19:37.815Z`、A2 `11:22:08.260Z`，不能把sampler elapsed当成与stable完全相同的起点。

| B1已有记录 | SQL open / in_use；累计wait次数 / 秒 | Redis总连接 / idle；累计wait次数 / 秒 | cards状态 |
| --- | --- | --- | --- |
| elapsed40.008；行起始11:20:18.769428，capacity11:20:18.8187613 | 7 / 1；0 / 0 | 4 / 4；0 / 0 | 0flight / 0active / 0waiting；累计内容SQL64批 |
| elapsed45.009；行起始11:20:23.770354，capacity11:20:23.8200943 | 7 / 2；0 / 0 | 4 / 4；0 / 0 | 0 / 0 / 0；累计内容SQL64批 |
| elapsed50.010；行起始11:20:28.771064，capacity11:20:28.8189562 | 7 / 1；0 / 0 | 4 / 3；0 / 0 | 0 / 0 / 0；累计内容SQL87批 |
| elapsed55.029；行起始11:20:33.790596，capacity11:20:34.845713 | 20 / 20；160 / 19.7865206 | 10 / 6；76 / 2.7787627 | 25flight / 8active / 37waiting；178个flight started、10joins、161内容SQL批、1670行 |
| 预热前11:19:24.4699605 → 收尾11:20:37.9114909的端点差 | +351 / +115.2335135 | +1055 / +14.2800203 | 183−32=151内容SQL批、1832−640=1192行；最后flight/active/waiting归0 |

来源：[B1资源](../2026-10-30-cap07/b1-multi100-control/resources.ndjson)、[前端点](../2026-10-30-cap07/b1-multi100-capacity-before.json)、[后端点](../2026-10-30-cap07/b1-multi100-capacity-after.json)。最迟在capacity内部时刻11:20:34.845713已观测到排队；前一个内部时刻11:20:28.8189562仍未累计pool wait，不能据此保证两个采样点之间没有更短的等待或停顿。此差异窗约6.027秒，不是一个被定位的6秒请求。

B1原生stable命令于11:20:37.213结束，墙钟59.398秒，exit99；5788发起/5787完成并成功/unknown1、dropped0，P95/P99为40.82359/1449.571716ms，max2054.2401ms。原预算仍按计划180秒分母，不能以32.15成功/计划秒解释活跃区间持续吞吐。[原生结果](../2026-10-30-cap07/b1-multi100-load/result.json)、[summary](../2026-10-30-cap07/b1-multi100-load/stable-summary.json)、[control](../2026-10-30-cap07/b1-multi100-control/exit-status.json)表明无外部资源stop、无强制终止、12/12完整资源样本；这是原生负载失败，不是采样器失败。

卡片内容的load_errors、busy_rejected、redis_errors均未增长，固定64flight/8active/1024miss-caller界未越界。8个active占满和其余flight排队只证明组件当时存在等待，不能从状态数得知槽内正在二查Redis、借SQL/执行SQL，还是回填。表中的多项累计数不能相加为单请求总时间。

## A2对照与无法排除的范围

A2未接公共内容缓存（cards为null），但资源行elapsed25.010、起始11:22:34.257807、capacity内部11:22:34.6999227已出现SQL累计80次/13.7685399秒、Redis42次/2.597011秒；该点SQL14开/4用，可能已经错过更高的在途数。前一elapsed20秒样本等待仍为0，后续累计未再增长，最终端点与此一致。A2的P95/P99为29.7205/45.3673ms、max789.2475ms，18001全部成功。来源：[A2资源](../2026-10-30-cap07/a2-multi100-control/resources.ndjson)、[前](../2026-10-30-cap07/a2-multi100-capacity-before.json)/[后](../2026-10-30-cap07/a2-multi100-capacity-after.json)、[原生结果](../2026-10-30-cap07/a2-multi100-load/result.json)。

因此，池等待并非只在有公共卡片缓存的版本出现；这不证明A2与B1具有相同原因，也不否定新组件在B1窗口内的放大作用。A2与B1不同窗/不同运行时长/不同缓存初始状态，不能当成一项严格因果排除实验。

两轮的whole-instance prepared Execute事件分别24136次/累计8.440818174秒与74360次/累计28.393276729秒，来源各自access-delta。这些是整实例服务端事件总和，没有逐条SQL的分布、调用者或同窗最大值；binary prepared SQL在该digest视图缺失。不能把平均值、较低采样CPU或累计执行时间当作“数据库没有慢过”，也不能用它们推算客户端纯池/网络等待。

B1最后样本行起始到capacity内部时刻差约1.055秒，A2排队样本差约0.442秒；该差包含采样准备和capacity请求等顺序成本，没有单独的collector发出/收回边界，不能当作接口RTT或某个存储耗时。B1该5秒段GC PauseTotal约增加97.9342ms；这是累计GC暂停观测，不是单次暂停分布，更不能凭较小GC fraction排除调度、依赖或宿主停顿。

## 源码支持的等待位置与可区分预测

| 候选位置（均未证实） | 已有支持与边界 | 下一次不同的预测 |
| --- | --- | --- |
| 事实RR取得/持有SQL连接、事实语句或提交 | SQL20/20且出现pool wait；A2也有共同现象。已核验目录事务结束后才等cards/counts，不能重新假定旧嵌套借池缺陷仍在 | 如果先出现长龄`feed.facts`/`sql.begin_acquire_roundtrip`或`sql.transaction_hold`，而shared SQL/回填不老，应先看事实链路；若shared SQL先老、事实随后借池排队，则方向不同 |
| shared loader活动槽内的内容SQL（含借池/执行/扫描） | 8 active、25flight、37waiting，内容miss与回源增长同窗；尚无shared阶段 | `cards.cold_sql`在途年龄先长、queue随后增，shared二查Redis/回填年龄短；SQL pool异常可与此对齐，但仅此不能分解纯借池/服务端执行 |
| shared二查Redis或同步回填占活动槽 | 当前active槽覆盖二查、SQL、序列化/L1和Redis pipeline；回填原有250ms子期限，没有证据不能先改成无界异步 | 如`cards.recheck`或`cards.fill`（嵌套`redis.pipeline_total`）先变老、内容SQL较短，queue随槽占用增长，才支持该方向；若SQL阶段已长则不能归因回填 |
| 公共计数装配/分片publication/Redis往返 | A2保留该路径且也出现等待；B1 cards变化不排除共同依赖影响计数 | 如果请求`feed.counts`及既有counts子阶段先老、shared仍快，应先看计数链路；保留分片失效次序，不因命中仍取门就直接删门 |
| 多依赖/驱动往返、运行时或宿主停顿 | pool/card积压可能是暂停后的结果；5秒CPU/内存未触线不排除瞬时停顿 | 如多个互不相同阶段同时变老、collector发出/收到间隔也突增，且栈停在不同往返/等待，支持共同停顿候选；仍需区分实际采证窗，不能用恢复后栈排除先前阻塞 |

`[A,B]`与`[B,C]`等不同缺失集合允许各一批回源，已核验设计不承诺逐key全局去重。TTL自然过期导致更多partial batch是需计量的正常成本，不因本次计数增加就引入新微批/缓存架构，也不延TTL避开它。

## CAP-08B最小选择

起点1de1735的[请求Feed](../../../backend/internal/social/feed.go)分出facts/counts，但没有公共卡片等待span。[shared loader](../../../backend/internal/communitycards/cards.go)从应用life派生2秒context，正确地不继承首caller context，因此只在HTTP上启用[起点recorder](../../../backend/internal/platform/taildiag/recorder.go)时，无法观察该worker自己的SQL/Redis阶段；起点有active总数但没有阶段最老年龄。slow trace只在结束时发布也不能可靠在释放前触发采证。这是本票选中的缺口，不是业务修复结论。

后续应只补固定阶段：请求鉴权/事实RR/卡片等待/计数，独立app-owned shared trace的slot queue/二查Redis/内容SQL/回填/结束；给在途阶段数量及最老年龄和collector实际发出/收到/失败时刻，继续32条慢记录上限。请求等待与shared工作可重叠，不能简单相加；worker保持应用context、原2秒deadline和原64/8/1024限额，不为串trace绑定首caller。

第一个受控场景只验证该观测接线：在已进入的shared阶段设barrier，并令另一个独立batch进入已确认的slot等待，释放前应能区分目标阶段与排队、取得oldest age并触发有界采证；调用者取消仍独立。barrier进入/释放和超时必须可证，原业务deadline不放宽，采证缺失如实记。它不提前证明B1根因，不发新的容量组；第二场景应留给现场已有证据支持的候选，不轮流注入全部假设。

CAP-08C首次现场诊断再按这些互斥预测观察；没有证据不改业务、不扩池/TTL/期限、不停止其它服务。各阶段是否有采证与是否可进入后续由根STATUS及本轮协议决定，本票不自行预占或追加名额。

## 拟定观测协议名对齐

上述候选采用08B实际固定阶段名：请求侧`auth.resolve`、`feed.facts`、`feed.cards`、`feed.card_fallback`、`feed.counts`，共享任务为独立`cards.shared` trace，内部`cards.slot_wait`、`cards.recheck`（二查包含L1/Redis）、`cards.cold_sql`、`cards.fill`（编码/L1及Redis pipeline）。已有`sql.begin_acquire_roundtrip`、`sql.transaction_hold`及Redis read/pipeline hooks会在相应request/shared context记录；阶段相互嵌套，不按互斥耗时相加。本文不使用未实现的`cards.load.*`名称。

在途年龄注册按每阶段1024个固定时钟槽有界；active仍计全部在途，超限时单列tracked/untracked、overflow与oldest_complete=false，不能把缺失年龄伪填为完整零等待。32条慢记录为全局共享ring，不为每scope扩成32条；它仍只保留已完成慢记录，释放前触发依赖新active/oldest而非期待已有慢记录。以上为观测契约对齐，非旧B1已经采到这些阶段的声称，也不是新增受控场景。
