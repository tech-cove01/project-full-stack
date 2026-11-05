# CAP-10 点赞状态变更正式容量报告

2026-11-05。本批固定配置已通过候选及两轮正式验证，稳定成功变更吞吐取两正式较低值为 **3008.3278次/秒**。这是点赞与取消点赞合计的状态变更吞吐，来自1996组独立用户/笔记关系。本次固定配置速率3010，未探索更高档位或集中热点模型。不再追加负载轮次，证据归档和精确清理仍在进行，尚不宣称服务已停或ECS已关闭。

## 原始结果与计量

结果来自已审抽取器对原command、stage、native summary、collector及匹配ReportId/真实EndTime的provider审计直接抽取，不重采样分位数、不拼接阶段。[脱敏原摘要](cloud-like-S29-H3-H4-summary.json)保留原字节，SHA256=`5eb52fb30c1016336737bab98069519a6dc866add4c7dd8eadcfee6722da928b`；其中`twoPassedSameFreezeFormals=true`、`stable3000TargetProven=true`。

| 窗口 | 身份 | 完整主窗 | 成功状态变更 | 实际变更/秒 | all与mutation P95/P99 | native结束后恢复 |
| --- | --- | --- | --- | --- | --- | --- |
| S29 | 候选 | 60秒 | 180299 | 3004.983333 | 36/72ms | 1.637856秒 |
| H3 | 第一正式 | 180秒 | 541499 | 3008.327778 | 62/102ms | 1.654332秒 |
| H4 | 第二正式 | 180秒 | 541499 | 3008.327778 | 55/94ms | 1.875416秒 |

两正式使用同一freeze（SHA256=`b9ad14447b94cc903b427f51b24e9db991cf046e8501d93545e267d35cfe70f1`）、API进程/部署、资源、fixture、packet/admission、observer和工具身份。稳定值为`min(541499/180, 541499/180)`，完整180秒均作为分母；配置3010不直接写成成绩。warm与每phase1996次准备noop另计，均不进入主窗成功变更数。两轮P95/P99分别保留，不平均分位数。

三个窗口的attempted/completed/successful/mutation计数相等，主窗noop、reject、unknown、effect/classification unknown、drop、timing、content/HTTP/system errors均0，`unknownIsExact=true`且计数与DB匹配。HTTP成功须关系变更与Outbox同事务实际提交；后续消费者收据与下游收敛单独校验，不能把HTTP全成功等同于全部验证通过。

## 实际环境与通过范围

单机ECS为16vCPU/32GiB。MySQL容器配额8CPU/8GiB，buffer4GiB、redo requested/effective均2GiB且resize状态OK；双1、group commit delay0、doublewrite及全局RR保持，既有事务隔离策略不变。API GOMAXPROCS8、SQL池128/128、relay8×32、consumer32；Kafka为2CPU/2GiB、MemorySwap3GiB，显式profile为`mysql8-kafka2-v1`。MySQL容器配额不写成整机规格；数据库和API在同一主机，共享硬件。

业务源码为`c464c1fa4d153806fb44ac746312552655ebda63`，API二进制SHA256=`4565c2422d24ac5ea34ac180890ec80ff767e8084d765f4eb9a809d9078516d1`；observer SHA256=`9502ae36bc13e094d39c3d21924a5deae88670024a5e7f5ff86661159d8bd26d`，工具源/产物独立绑定，不借API CI。负载为1996组独立关系、4个PTS引擎/2000VU，交替点赞与取消；不代表集中争用单一热门笔记、Feed读取、下单或其它业务吞吐。

两正式均通过原API 200/500ms、50ms调度、零unknown/drop、完整事实/派生、下游、资源、采集和恢复判据，`childrenConverged=true`、`batchFatal=false`、工具字节稳定。下游正式判据使用完整180秒中**最后60秒的保守采样窗口**：H3最大增长为收据19，Kafka lag增长0；H4最大增长为Outbox30，收据增长0、Kafka lag最大29，分别低于1655.45/1655.63预算。正式摘要的`tail15=null`，不将其改称额外末15秒已测；S29另有末15秒检查通过。native结束后的两次恢复均小于原60秒门槛。

H4之后又完成当前1996关系的新鲜完整只读终审，facts/derived/downstream通过；原reconcile SHA256=`0d9da46732727abd2c33c45edc66f5810b5f00e893114bc9c32971215bdfcbb9`。这份清理前对账不替代正式窗口原终审，也不生成新成绩。

## 优化与瓶颈证据如何解释

| 工作 | 已核实的变化 | 结论边界 |
| --- | --- | --- |
| Like事务 | 现有关系的锁读与更新分别合并，业务SQL6→4；BEGIN/COMMIT/隔离SET仍各1，保version fence、Outbox与提交后fresh读 | 有限同程序计数与真实MySQL正确性成立，不等于QPS或延迟改善33% |
| Done微批 | N≥8点驱动，满批32条事务内完成语句32→3，保lease/attempt fence、fresh guard和一次COMMIT；小批保原路径 | 与Claim认领UPDATE32→1是不同组件，不混为Done32→1或COMMIT32→1 |
| 生成器与observer | 同线程准备noop与持久屏障、endpoint缓存、整数快路；manifest顺序摘要修复 | 整数有限分配约261–262B/op→52B/op仅为组件证据，不能证明旧抖动由GC造成；工具修复不冒称业务收益 |
| 资源配置 | MySQL buffer4GiB/redo2GiB、Kafka1→2GiB并保额外1GiB swap allowance，本boot重新验证运行时配置 | Kafka旧H2触发85%保护线但没有OOM；新配额针对明确保护，不能追认旧失败或声称已证明GC根因 |

S26的完整60秒诊断为3004.983333变更/秒、P95/P99=216/333.02ms，但API与下游失败。其native开始约27秒后才启动1Hz设备观察，只有后33个完整区间：IOPS平均6787.386、最大7193.924，写入144.889MiB/s、await3.377ms、util80.48%、队列深度22.91。这支持I/O压力线索，不能凭局部设备完成计数确诊云盘throttle或纯MySQL瓶颈。更多机制和原件边界见[优化档案](offline-improvements.md#11-05同机redo容量试验收益待验证)。

redo100MiB→2GiB可能缓解检查点/刷新压力，但每次成功仍须同步持久化，没有禁双1或异步攒请求提交。S26诊断开启，S27关闭诊断并换fixture，S28修复observer后候选P95/P99为13/52ms，S29又换boot、Kafka配额及工具后为36/72ms。因此这些窗口不能构成纯redo或SQL优化倍率，更不能把各组件收益相乘解释正式3008。1000us group commit组件研究未采纳，delay保持0。

旧H2正式FAIL及其中独立4条调度违规、S25中止/未知边界、S27工具失败、旧H1失败、CAP-09未过容量与B1未知全部保留。本批只证明当前明确配置和业务模型达到稳定3000目标，不将旧结论改成全部PASS；Feed保留原已测结论，不增加新轮次。

## 成本与交付边界

匹配真实EndTime的provider VUM：S29为1080+2623=3703，H3为1228+6571=7799，H4为927+6669=7596；本批19098，加此前已核29295，累计48393。已购60000相减11607是未独立查询账户的计划余量，不是账户余额；包含准备、warm及平台生命周期成本，不按主窗时长猜账单。主机费用另计。

本批不再发压。原始报告、journal、配置/二进制/CI、资源/boot和失败闭合证明的归档、下载逐SHA核验及精确清理仍在进行；本报告不提前宣称备份完成、服务停止、凭据清理或ECS关机。原主库、卷、历史FAIL与用户原keys按既有归属规则保留。

可用的单条简历表述：围绕点赞状态变更后的Outbox与消费积压，通过活跃索引、连接复用和有界微批减少数据库往返，保留事务、租约围栏与持久化确认；在单机16vCPU/32GiB及明确容器配额下，两轮各180秒正式验证稳定约3008次点赞/取消变更每秒，P95/P99分别62/102ms、55/94ms，并通过完整事实、下游稳态与恢复校验。
