# CAP-06 独立证据复核

2026-10-30。核验交付 `a3c64b5361069ae4491b9de76654b5432ac9f9a8`。本子审查只读取源码、Git历史和原始文件，并运行离线Python复算及`go tool pprof`解析；未启动API、数据库或负载，未重新编译应用，未调用付费模型，未改原证据或STATUS。

**结论：所核记录一致，未发现确定的P1/P2统计或来源问题。** 三轮54002次完整分类成功、自然冷填充、39/0/0条本轮slow记录及唯一恢复时点抓栈都有对应原件。仅支持CAP-06A观测与CAP-06B有限诊断的实际交付范围；旧秒级长尾未复现，原因未确认，不升级为CAP-06D正式组、根因修复或CAP-07前置已满足。

## 范围与实际命令

读取[总报告](../../../evidence/2026-10-30-cap06/report.md)、执行报告、事前计划、候选分析、开关探针、三轮review、原生summary/result、额外timeline/manifest/触发窗、API/fixture元数据、工具历史、audit/Git字节清单及cleanup。生产观测实现正确性、触发工具和定向测试由同批其他独立审查另记，不将本子审查说成执行了那些测试。

从仓库根实际执行以下只读命令；原audit的JSON经管道提取摘要，未覆盖已交audit.json：

```powershell
python -X utf8 docs/evidence/2026-10-30-cap06/audit.py | python -X utf8 -c "import json,sys; x=json.load(sys.stdin); print(json.dumps({'checks':len(x['checks']),'failed':x['failed_checks'],'inputs':len(x['inputs']),'stages':x['stages'],'scriptSHA':x['script_sha256']},ensure_ascii=False))"
git diff bb03d3fe9172f3bb369cbbaa473a2ab041ec4db5 1bed127a532307f2d24b03e1ef07e4b667bdfd59 --stat -- backend
go tool pprof -top -nodecount=8 docs/evidence/2026-10-30-cap06/baseline-diag-1-diagnostic/goroutine-1.pprof
```

audit实际返回164项检查、0失败、58份输入声明。`audit.py`字节SHA256为`6f31434dee3b1cff96836b9b414f7171419d28e5696bc4a36de7fd1f0a37faa9`，与归档声明一致。bb03到1bed的backend差量为空，不能把工具CI当成所有后续报告提交均跑过CI。

另写一次性只读Python，经PowerShell here-string传给`python -X utf8 -`，独立完成下列复核；没有调用原audit内部函数，也没有写结果文件：

- 对audit列出的58份输入重新计算实际字节数和SHA256，逐一与声明及当前交付Git blob比较，差异0。再以`git cat-file --batch`读取Git字节清单记录的`aa59fed1d6c79dcfc87db5895da012660bae5667`，逐一比较189份文件的实际字节、SHA256及Git blob，差异0。清单不自哈希，也不将189解释为全部未来报告文件。
- 对三轮`result.json`的attempted/native HTTP/completed/successful/systemErrors/httpErrors/contentErrors/dropped，与各自原生`stable-summary.json`的八项counter逐一匹配；全部和成功响应的avg、median、P95、P99、max逐项一致。分位没有逐请求样本可重建，属于原生导出交叉核对。
- 流式/逐行读取三轮timeline，按实际stable命令起止过滤trace，再用sequence去重；同一sequence在不同poll的内容没有变化。对首末`tail.stages`的cold SQL批次/items/Redis write、SQL/Redis池计数作独立差分，并按采样实际时点定位冷填充窗口。
- 将三轮resource记录数与control中的完整样本数匹配，核API/诊断PID及生成器存在、观察错误为空；将collector manifest的成功poll数与实际timeline行数匹配。
- 比较触发样本内最新已完成trace的end_at与抓栈started_at，核捕获文件hash、触发时SQL InUse和抓栈前后stable存活声明；检查三个slot与三个diagnostic command的编号/名称对应。
- 独立匹配cleanup与environment的两个不可变容器ID及挂载、退出状态、会话finish和私有env记账。销毁前逐值秘密扫描只核归档结果，没有重新取得已删除token来执行同一扫描。

## 三轮数值与自然过期

| 轮次 | started/native/completed/successful | P95/P99 ms | HTTP最大ms | 完整资源样本 | collector成功/失败 | 本轮fresh slow/抓栈 |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | 各18001 | 26.4453 / 43.7021 | 242.1976 | 37/37 | 183/0 | 39/1 |
| 2 | 各18001 | 29.2595 / 40.4513 | 76.5284 | 37/37 | 183/0 | 0/0 |
| 3 | 各18000 | 31.516005 / 40.927278 | 73.7404 | 37/37 | 182/0 | 0/0 |

稳定期合计18001＋18001＋18000＝54002次；unknown、系统/HTTP/内容错误和dropped均0。每轮multi100/s、名义稳定180秒，预热另计。它们带额外1Hz观测，不计正式容量组；不将完整诊断窗口或其中任意两轮当作原事故已修的证明。

| 首末collector差分 | 第1轮 | 第2轮 | 第3轮 |
| --- | --- | --- | --- |
| cold SQL批次 | 462 | 435 | 419 |
| cold missing输入items | 1324 | 1377 | 1369 |
| Redis写入次数 | 1324 | 1377 | 1369 |
| SQL WaitCount / 累计WaitDuration ms | 10 / 154.9949 | 0 / 0 | 0 / 0 |
| Redis WaitCount / 累计WaitDuration ms | 0 / 0 | 0 / 0 | 0 / 0 |
| timeline采样起点首末跨度s | 182.023 | 182.043 | 181.016 |
| 最大诊断fetch ms | 27.951 | 28.856 | 27.964 |

cold首次增量分别约在stable命令开始后50.477/50.471/50.413秒，后续约110秒及173秒后再次出现；同一夹具未清缓存、原TTL/抖动保持，与自然到期后的回填路径一致。首轮按0–45、45–80、80–105、105–145、145–170、170–190秒分桶的cold批次为0/192/0/237/0/33，后两轮分别0/151/0/219/0/65、0/156/0/208/0/55。不能将未复现解释为整轮只有热命中。

这些是不同时间采样的首末增量和missing输入数量，不是去重笔记数、完整逐请求归因、CAP-07内容loader结果，或纯数据库服务器执行时间。后两轮零池等待只表示该观察区间的累计计数无增量。

## slow ring与实际抓栈时点

首轮跨poll可见sequence1–39；后两轮只反复出现sequence8–39，且latest_sequence始终39。按各轮start_at/end_at过滤后是39/0/0，不将ring保留的旧32条重复计为新慢请求。同sequence跨poll内容一致，无截断、错误或取消标志。

首轮样本169同时观察到SQL WaitCount增10、累计WaitDuration增154.9949ms和多条已完成的200ms以上trace。触发时SQL InUse已经回到2。唯一抓栈开始`10:03:41.912450Z`，结束`10:03:41.926214Z`，耗时约13.7785ms；6013字节文件的SHA256与manifest一致，前后stable PID43332存活声明均true。

抓栈比触发样本所含最新已完慢请求end_at晚415.137ms。离线pprof解析为285个goroutine，其中255条处于HTTP连接空闲读路径；它是负载内恢复时点，不能说捕到了原排队峰值或用当时无排队栈否定此前排队。

最大已捕获trace为sequence10，总长237.7452ms，其中feed.facts215.0336ms、SQL Begin46.1504ms、transaction hold168.8832ms。Begin与hold等是嵌套阶段，不能再与facts简单相加。39条已捕获慢trace没有cold SQL阶段，计数/门阶段没有可见长等待；很短的零时长样本不能解释为真实零延迟。本轮小脉冲主要处于事实SQL链路，没有证据将其解释为秒级publication阻塞。

这不排除旧事故中的跨key门放大、底层往返或宿主暂停。原P99约1.137秒和诊断迟到约1.889秒都未复现；新观测本身会改变执行和采样成本。off/on短探针只证明开关与接线，未固定宿主背景、自然缓存状态或足够样本，不能量化“无开销”或因果收益。

## 来源、限次及收尾

API构建记录为Go1.27.0、revision `bb03d3fe9172f3bb369cbbaa473a2ab041ec4db5`、`vcs.modified=false`；metadata固定二进制SHA256 `a8ef9aa640164b0eaad729139e348491bafbcd19220c6a62b7a97fcc152a6d79`，on API PID59564。这里核的是保存的构建/metadata和来源链，不声称本子审查重新编译或现场启动该二进制。每个command绑定metadata原字节hash，工具hash与1bed127/3904d7d的相应历史源码吻合。

公开记录恰有slot1/2/3以及对应baseline-diag-1/2/3，诊断命令恰3条；没有第四条诊断或额外新tag的已交记录。每轮capture数量1/0/0，均未超过事前3次上限。失败启动也占名额的实际工具约束由同批脚本审查另核，不能仅凭三个文件证明工具所有分支正确。

收尾记录显示正常logout32，精确会话计数32→0；fixture finish同样记logoutCount32。两个容器的不可变ID/挂载与environment相符，正常stop后exit0/OOMfalse、卷保留；私有env删除记账为4份，API Windows terminate退出15且未宣称Go信号优雅关闭。销毁前扫描的声明范围是188份公开文件、33个唯一私有值、匹配0，不扩大为后续所有报告文件或本审查重新逐值扫描。

本轮可以核验为“有界观测和有限诊断的证据交付成立”。原始HTTP响应体及逐请求延迟没有保存，离线不能重做在线分类或重建分位；全局池/阶段统计不是原子、逐请求因果记录；5秒资源样本不保证捕捉瞬时峰。原长尾原因仍未知，CAP-06C/D与CAP-07均不由本证据复核自动放行，也没有据此新增第四轮诊断、功能或中间件。
