# CAP-08B 最小观测与受控接线

先按 [08A](failure-window.md) 固定缺口，再新增默认关闭的固定阶段与在途年龄。没有改业务SQL/RR、池、TTL、deadline、鉴权、数据来源、共享加载限额或取消语义，也没有认定B1根因。

- 原HTTP/direct完成计数 `requests` 保持，`shared_tasks` 单列；app-owned任务使用原life+2s context的独立 `cards.shared` trace，不绑定首caller。全部scope仍共用最多32条慢记录。
- HTTP卡片等待/完整事实回退及shared槽排队、二查、内容SQL、回填分别计时。每阶段1024个固定起始时钟，短锁只保护内存；active/oldest一起采样，溢出显式列untracked/oldest_complete=false。关闭时无recorder/时钟分配。
- [一般六包真实回归和vet](observation-related-checks.json)通过，含正常真实HTTP冷热：auth/facts/cards/counts各2次、shared完成1、正文SQL1，阶段最终收敛；旧FC及计数失效回归保留。专用受控场景在该一般回归中明确SKIP，随后单独按预留slot运行，不混计。

## 场景1：释放前可区分SQL阶段与活动槽等待

[事前预测](controlled-wiring-prediction.json)、[预留](slot-controlled-1.json)在执行前固定。真实MySQL/Redis、两个自有Ref，第一个任务在GORM Row到驱动之前屏障暂停，测试仅将active limit置1（生产仍8），第二任务实际入槽队列。首caller取消后，两shared仍活跃；确认两种在途年龄均超过100ms再调用真实loopback collector。

[实际测试PASS](controlled-wiring-1-test.txt)，[核对结果](wiring-result.json)：collector在释放前抓到1份10604字节、未截断goroutine profile，触发理由同时包含 `cards.cold_sql`、`cards.slot_wait`（年龄约337/336ms）。测试在collector调用前后及profile handler前后都确认屏障未释放/阶段仍活跃；之后释放、第二caller成功、所有活动数归零。采证不依赖已完成慢trace才触发。

这是观测接线证明，屏障不等于MySQL服务端执行慢；不算容量或业务修复，不消除原B1未知风险。使用场景1名额，场景2留给现场出现的具体候选。受控模式只作一次抓取，不声称180秒采样覆盖。

## 采证边界

collector只访问已核loopback/PID，禁止redirect；正式诊断最多1Hz、3次≤1MiB栈、相邻捕获完成后至少15s。成功/失败抓取均记录起止，诊断首尾/最大采样间隔按事前2.5s判覆盖，年龄缺失不补0。最多保留看到的duration最高32条不同慢记录；timeline/window仅存阶段/池等原值和visible sequence引用，全文payload只在slow-records.json的保留集。原响应SHA与parsed subset口径分列，不冒称完整响应落盘、逐请求分位重建或零开销。

组件/root实现和collector均经内部非作者静态审查，工具审查发现的采证口径问题在本场景前修正。独立外部核验、精确源码CI及现场诊断结果后续分别记录；本报告不提前标08C/E通过。
