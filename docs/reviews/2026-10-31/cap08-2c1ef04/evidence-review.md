# CAP-08 独立原始证据复核

2026-10-31（Asia/Shanghai）。交付 `2c1ef04b7b1d1ae10d98767e18e6c12ff697166c`，原测量发生于2026-10-30。本子审查只读固定checkout、Git历史、原生工件和保存的exe字节，未启动服务、发HTTP/SQL、运行负载、重编译应用或调用付费模型，未改原证据。

**结论：所核数值、来源、停止及清理记录一致，未发现需修的P1/P2证据问题；正式第2轮确实失败，本组容量未通过。** 原生unknown/drop失败与访问计数时基拒绝分别成立，不能将整轮降格为只有后处理脚本失败。诊断未重现、正式1通过和正式2失败不能相互覆盖。

## 独立复算方法

读取[总报告](../../../evidence/2026-10-30-cap08/report.md)、[失败审读](../../../evidence/2026-10-30-cap08/failure-final.md)、诊断/受控报告、事前协议、三stage的native summary/result/control/端点、timeline/manifest、构建/CI、closure和cleanup。源码与真实回归由同批其他独立审查另记，不计成本子审查实跑。

本次使用一次性PowerShell here-string经`python -X utf8 -`自行复算，没有依赖作者audit计分函数或只凭audit退出0作结论：

- 将三轮native的started、HTTP、terminal、success、三种已分类错误及dropped逐项读取；存在原result.result时与其匹配，不存在时保持缺失，仅单独记录native事实。全部/成功avg、median、P95、P99、max逐项核对，不重新构造没有保存的逐请求分布。
- 由原计划100/s×180秒、native计数、退出、unknown/drop和延迟预算重算判据，分别与原布尔结论匹配；未将早停墙钟改为计分分母。
- 从MySQL前后快照自行重算全部global delta、statement event/digest和Redis command增量，以及状态采样中点、Uptime误差和旧helper容差，不将通用failure文案当根因。
- 逐行核诊断182次poll、29个stage的在途字段与完整性，核首末覆盖、触发/overflow/slow及request/shared分离；再核正式关闭额外观测、原5秒资源完整性、原生停止原因和cards/池端点差。
- 重新计算fixture/metadata字节hash，并用`git show 516d534...:scripts/<name>`核三轮共30个运行时工具声明，全部与历史源码相符。当前post-group guard不同不替代历史来源。
- 以`git cat-file --batch`读取字节审计所指的不可变提交，对132份实际文件逐项核字节、长度、SHA256。另核旧failure-window的CRLF/LF补正、实际exe hash、归档CI、slot/closure及cleanup。

没有运行会写原证据的审计输出命令，也没有为复算补采原本缺失的现场。已保存audit的三阶段errors为空、capacityProtocolPassed=false与本次独立结论相符。

## 三阶段原生结果

| 阶段 | started / HTTP / terminal / success | unknown / dropped | 全部与成功P95/P99 ms | 已记录max ms | stable exit | 资源完整/有效CPU区间 |
| --- | --- | --- | --- | --- | --- | --- |
| diagnostic-1 | 各18001 | 0 / 0 | 19.3259 / 26.6919 | 81.2575 | 0 | 37 / 36 |
| formal-1-multi100 | 各18001 | 0 / 0 | 17.3377 / 23.9781 | 63.5345 | 0 | 37 / 36 |
| formal-2-multi100 | 16133 / 15933 / 15933 / 15933 | 200 / 57 | 24.18684 / 84.048128 | 3656.4646 | 99 | 33 / 32 |

已分类system/HTTP/content错误三轮均0。formal2按原名义窗口重算：

- 发起达成16133/18000＝89.6278%，低于99%。
- 成功/发起15933/16133＝98.7603%，低于99%；unknown200、dropped57也分别不符零预算。
- 成功/计划180秒＝88.5167/s，不是活跃区间容量上限。
- P95/P99符合其已记录完成样本预算，不能说本轮由P99超限停止；native console记录的是dropped阈值早停。

200个unknown的最终客户端结果和具体时延没有原始分布可恢复。低P99只描述已记录的15933次完成观察，不能代表这200次也很快或成功。57个dropped为未开始的计划迭代，与unknown不同，不能合并编造成257个已经发送的失败响应；剩余未完成的计划量也不能一律称丢请求。该接口是只读Feed，不能描述成200笔丢单。3条status0警告不是完整分类，也不能替代unknown200。

诊断带额外collector，不列正式成绩。formal1虽然完整通过，formal2失败后这组重复条件不成立；不能补轮或取其它历史成功凑成重复通过。

## 缺失result与访问时基

formal2原result.json确实没有正常`result`字段，保留`meetsBudget=false`及原failure。原生计数与延迟来自另一份未修改的stable-summary，未倒填缺字段或把缺失当0。

| 原始事实/独立计算 | 值 |
| --- | --- |
| before GLOBAL STATUS窗口（UTC） | 14:00:10.678—14:00:10.801 |
| after GLOBAL STATUS窗口（UTC） | 14:02:54.772—14:02:54.910 |
| MySQL Uptime | 2230→2391，增161秒 |
| Windows窗口中点间隔 | 164101.5ms |
| 绝对差 | 3101.5ms |
| 原helper不确定度 | 2000＋123＋138＝2261ms |

3101.5＞2261，因此`uptimeConsistent=false`可由原始值直接重现。所有global、statement event、digest及Redis command delta均未发现负值，loss/missing相关记录没有给出其它重置证据。复合`counterResets=true`在本轮可由uptime分支解释，不证明实际reset、重启或具体宿主故障。

正向Com_select/Com_stmt_execute原始值可用于解释guard，但本轮访问一致性已被拒绝，不能将它们升级为有效访问量、每HTTP查询数或缓存收益；二者本来也有重叠。前后快照没有Redis uptime/server时钟，不能用MySQL时基替代。restartCount0/OOMfalse只支持保存的容器状态，不排除宿主/VM暂停或时钟变化，也不证明其确是原因。

原生unknown/drop和比例失败不依赖这项后处理，所以即使未来改测量工具，仍不能把本轮修成容量通过；本次没有修改容差或重跑。

## 诊断覆盖与新增active字段

实际timeline182条，manifest成功poll182/失败0。对29×182个stage记录核`active=tracked_active+untracked_active`、年龄非负、无untracked/overflow且oldest_complete=true，未发现不一致；每次PID44828、tail enabled，trigger/errors/missed interval均0，latest slow sequence和visible sequence保持空。

首poll距stable进程创建42.938ms，末poll至done427.571ms；采样起点跨度181.000568秒，monotonic最大间隔1000.8774ms。独立按wall-clock再算约1000.8889ms，两种时钟口径不混写。最大fetch27.0237ms，低于既定覆盖上限；没有达到100ms年龄或pool正增触发，因此capture/保留slow均0，不是触发后丢失。

| stage | 采样所见最大active / oldest ms |
| --- | --- |
| auth.resolve | 1 / 1.8483 |
| feed.facts | 2 / 27.0744 |
| feed.cards | 1 / 4.2871 |
| feed.counts | 1 / 7.0161 |
| sql.begin_acquire_roundtrip | 1 / 12.1013 |
| sql.transaction_hold | 2 / 25.4423 |
| cards.shared | 1 / 3.2322 |
| cards.slot_wait | 0 / 0 |
| cards.recheck / cold_sql / fill | 各1 / 0.5466、2.1745、0.5215 |

slot_wait完成次数实际增575，所以采样active0不能说从未经历排队边界。诊断端点request增18251＝250warm＋18001stable；shared_tasks和shared/slot/recheck/cold_sql/fill各增575，与575个flight/内容SQL吻合，未混进HTTP请求计数。内容SQL实际1920行/538920字节；这些窗口含预热与收尾，不当精确稳态逐请求归因。

受控接线原文件的7个源码hash与后续46ddefc应用/516工具一致；执行时worktree modified已披露，没有冒称控制测试在干净46提交执行。受控capture10604字节、SQL与活动槽年龄约337/336ms、释放前barrier声明和最终归零有记录；测试活动上限1、生产8的差别明确。它证明GORM调用前barrier的接线和共享取消行为，不是MySQL服务器阻塞或旧B1根因。

两个正式阶段均没有extra collector目录，资源中tail disabled；不能用诊断轮字段替代正式2未采的分段/在途栈，也不能用1Hz/5秒样本排除所有瞬时停顿。

## 正式2末窗与风险

formal2最后样本outer14:02:51.989240、capacity14:02:52.0422603；SQL开/用16/16、wait累计仍0，Redis10连接/0idle、wait9/24.7611607秒、stale1。该capacity时点早于stable命令结束约2.540秒，不能把它当全轮最终状态。

预热前到wrapper收尾后独立端点差为SQL wait328次/180.0832286秒、Redis368次/258.5982841秒。它们是并发累计，不能当一次数百秒请求或纯服务器执行时间。cards内容SQL从elapsed130.019到160.199约30秒保持856批，flight/active/waiting采样均0；最后没有观察到回源队列，不代表所有时刻无等待，也不支持直接归因于卡片loader。正式2内容端点差408批/1920行/538920字节，joins及loader/Redis/坏值/拒绝错误均0，不能抵消unknown和原生失败。

33/33资源样本完整，无resource stop、forced或sampler错误；sampler exit0、harness/wrapper1。有效host CPU峰68.4%、内存79.5%、API RSS38.8438MiB、k6 RSS163.5508MiB，与报告相符，未触原资源预算。低采样值、容器未重启、组件最终归零和原生完成样本低P99都不足以给出唯一原因。

## 字节、来源与停止

131份raw共3463134字节，加manifest共132份3490228字节，与`a9ea133bffab8c34263d255656d0e474b02478a6`的Git blob、保存SHA256/长度逐一相同，差异0。原failure-window在9b提交的35122字节LF，与保留36147字节CRLF原件在规范换行后逐字节一致且JSON语义相同；原件SHA为`322f3e22bf1cd95da36dffa92ade37ddf1c33c97af4ac136d57f2fac9c10e774`，旧blob为`607f784cf7816fc8022c2c0478e75a8280cdf65da308754d5f490111f87a278a`。追加补正没有改JSON语义、原生工件或manifest，不是隐藏重跑。

三轮共30份工具hash均匹配历史516d534，共10个不同工具blob；未借当前终态guard的字节替代运行时来源。同一实际exe重新算SHA为`e65decd1f87a2667d3391954aa20de3b9ae3fcec3e99f41b2812a29d43da0ce2`，与46ddefc干净构建/metadata相符。归档CI精确46ddefc、run41065266414、四job success；本子审查没有重新查询远端或将专用controlled SKIP算作CI实际启用。

slots恰为controlled1、diagnostic1、formal1/2，closure同样记受控1、诊断1、业务修复0、正式2，hot/stair/neighbor/hold均0。保守13:10 UTC起点至正式gate13:54:19约44分19秒，closure仍在4小时内，不需要扣除CI并行或负载等待来满足上界。正式2失败后没有已交的补轮/升档/新组；剩余名额不构成继续本批的授权。

cleanup/fixture finish均为正常logout32、会话32→0；两完整CID及挂载与environment一致，exit0/OOMfalse、卷保留，4份登记env精确删除。API66608按身份停止、Windows terminate退出15，不称Go优雅关闭。本子审查仅核原记录，没有执行清理或重新取得销毁凭据。

目前可支持“功能沿既有核验成立、观测接线另验、本次正式容量组失败且原因仍未知”。不能写重复稳定100/s、根因已修、缓存提速比例或项目性能收尾完成；没有据此补测试、开启新功能、CAP-09或新的诊断轮。
