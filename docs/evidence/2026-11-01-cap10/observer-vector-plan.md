# CAP-10：事实版本增量观测与完整审计

2026-11-03。任务状态见根[STATUS](../../../STATUS.md)，失败与收益见[改动档案](offline-improvements.md)。本票解决大历史数据让观测器反复扫描的问题，不改变点赞业务、持久化、ACK或容量判定。纯记账、有界SQL、逐窗绑定、严格完整审计和阶段去重已交，真实N2互通与组件验证通过；完整云端阶段和容量尚未验证，按[接续清单](offline-resume-checklist.md)执行，不自动恢复实例。

## 依据与边界

原观察器每5秒扫描窗口内不断增长的Outbox，并独立扫描三组收据。独立本机1225500事件/每组1225500收据的全史与900000后缀probe均未过原预算；另增至2GiB缓存的全史30秒也失败。保留全部原件，不把超时空结果补为0，也不把此结果当Go API或云机容量上限。

全局Outbox最大ID不能作为精确发现游标：自增ID分配顺序不等于事务提交顺序，较小ID延后提交可能漏掉。新方案使用已固定的最多4096个(note,user)事实版本，同一业务事务同时提交版本与唯一LIKE事件。它依赖已审查的生产者、DONE和收据不可逆合同，不声称能即时发现任意人工SQL污染；窗口前后仍须独立完整审计，孤儿事件或不可解释差异必须阻断成绩。

## 采样方式

1. 窗口前完整审计所有相关历史、旧范围积压、事实/计数/Redis及收据，保留原始文件和SHA。以同一RR快照的准确事实版本与控制行指纹创建基线，新就绪读必须匹配该基线。
2. 每次RR只读事务读取选定笔记的**全部**点赞事实行，拒绝多余用户、重复行、事实消失、版本倒退及active奇偶失配。按上次已发布版本到本次版本枚举所有新唯一事件ID，同时加入所有尚未退休事件。
3. 分块按唯一事件键读取源和三组收据，验证源身份、canonical payload/hash、状态及严格字节等值。未完成事件每次重新读取，不因曾经见过就忽略。只有DONE且三组准确IGNORED才退休至累计计数；已见DONE或收据不得倒退、消失。
4. SQL事务实际提交且整个SQL/Kafka样本成功后才发布新状态。取消、读失败、语义错误或超出事件/内存上限不推进版本，不丢pending，不将失败样本事后改成通过。保留4秒总、3秒读、5秒节奏及原缺样判据。
5. 结束后按原native stableEndedAt核60秒恢复，另做独立完整审计。完整审计失败、缺原生终态、unknown或计数不一致均不能宣称稳定成功QPS。

新模式必须显式`like-vector-v1`；缺失保持`full-history-v1`。固定完整snapshot审计预算分别120000/30000ms，scope仅`full-snapshot-audit-only`，不接受任意超时环境变量。120秒仅容纳窗口外完整审计，不能称为SQL优化收益，不移动主窗或恢复截止时间。

## 小票与验收

| 小票 | 行为与直接验证 | 当前 |
| --- | --- | --- |
| OBS-V1 | 纯版本记账，延后提交、完整pending刷新、身份/hash、receipt/DONE不可逆、caps、epoch及双gate；生产Decode产生独立golden | `374ccbc`，普通16顶层/18子例、race/vet、Linux单文件构建、独审通过；未接运行路径 |
| OBS-V2 | 有界同RR SQL适配，索引计划、真实延后事务/别名/取消及失败不推进 | `d6ac1e5`，6顶层真实回归通过；32768事件完整读取1.349935秒/135查询，首轮4秒FAIL保留，未改原预算 |
| AUDIT-V1a | 固定模式与完整审计预算，默认兼容，非法值拒绝 | `f65d62b`，Node39与runner71检查、独审通过 |
| OBS-POLICY / OBS-LAUNCH | Python共用固定合同；启动元数据与实际进程环境绑定mode/预算，历史缺字段仅旧模式兼容 | 纯合同`3f3b601`已交；启动绑定10项检查及独审通过，不代表SQL/main或窗口接线完成 |
| OBS-BIND | 预约、命令、API/active/formal freeze及ready须匹配同一固定方法/预算；vector不得借旧缺字段元数据 | 89项策略、71项runner检查及独审通过；旧observer在vector ready门前拒，仍未启用新SQL/main |
| AUDIT-V1b | 实际完整SQL与严格源/hash/未知projection校验 | 实际32/12/4组与3计划通过，完整1225500源审计106.077秒<120；造数hash无效且缺事实，非业务对账通过；[小票](full-audit-plan.md)，旧FAIL保留 |
| OBS-PY-AUDIT | 本窗原admission/reconcile SHA→CLI/ready/summary→最终完整审计E+delta绑定，保留原恢复门槛 | `44f05c0`，9项文件绑定/89策略/71runner检查及独审通过；[规格](observer-audit-binding-plan.md)，无native互通或容量声明 |
| OBS-SAMPLE-DIGEST | 同RR精确事实/控制摘要仅在整个样本Accept成功后发布，供阶段轻读绑定 | `5c6c8e5`，7新顶层/11子例、含原检查的36顶层/89子例race/vet/Linux构建及三方独审、精确CI通过，零新SQL；[小票](observer-sample-digest-plan.md) |
| PHASE-AUDIT-DEDUP | 原逐窗完整基线+中间轻量事实/控制+同一接受样本绑定，原生恢复后最多一次完整终审 | `e53534f`，10阶段/10完整审计/39原Node、9文件绑定/71runner检查及独审通过；[小票](phase-audit-plan.md)，完整真实阶段仍待云准入 |
| OBS-WIRE | Go显式模式、逐窗原审计/事实/控制fresh RR绑定，完整SQL/Kafka样本成功才推进，监督关闭 | Go29顶层/78子例纯检查、race/vet/Linux构建及独审通过；真实N2 SQL/Redis→Go factory互通通过，NULL/非NULL控制行、present v0及stale拒绝后精确恢复；尚未运行真实observer main或PTS |

基线和终审SHA、事实向量摘要是逐窗引用；mode、工具、固定预算属于整个freeze。工具SQL/query/事件/逻辑字节与真实进程资源分别报告，逻辑上限不是RSS。没有上述接线和真实验证时，不把纯引擎、交叉编译或本机SQLprobe记成PTS或业务容量通过。

## OBS-WIRE真实互通范围

新模式CLI必须带本窗原完整reconcile路径/SHA；先严格读原夹具/方法/控制/计数，再以同一实际提交的RR快照核事实与控制均未变化，历史E明确来自绑定的Node完整审计，timeline仅记录本窗delta。默认缺失模式保持旧方法；固定采样4秒总/3秒读/5秒节奏及native恢复60秒不变，单样本限额与逻辑字节/真实RSS分别报告。

真实本机独立IT使用N2/E2：一个inactive v2配对有两条生产Decode规范源与三组各两条准确收据，另一配对明确present v0；实际Node提取原snapshot/factErrors/derived/reconcile函数并读真实Redis生产Mark Lua结果，完整对账valid/factsValid/converged、errors均通过。Go精确原源读取该原件，fresh RR factory接受，受控改变v0事实后拒绝旧基线，再精确恢复。NULL与非NULL DATETIME(3)控制值按六位小数互通，未改数据库时区或持久化。

该用例没有调用固定端口的Node main、Go observer main、Kafka、API或PTS；不能把局部真实工厂互通称为完整容量接线已通过。两条源、六条收据、笔记/事实、三个原控制值及四个最初不存在的Redis键均已精确清理/恢复，自有MySQL/Redis正常停止、卷与其它服务保留。accepted-sample摘要与阶段扫描去重已另票交付，原4/3/5秒和setup/恢复60秒保持；不因组件通过跳过真实云准入。
