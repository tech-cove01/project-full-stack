# GO-072 / GO-075 独立核验

日期：2026-10-28（Asia/Shanghai）。结论：**GO-072双消费组/持久投影与受控恢复、GO-075真实兼容适配/请求生命周期通过本轮范围内核验；原始真实模型固定集逐题复核通过，本轮没有重新发起付费模型调用。** AI继续保留为项目产品能力；求职表达仍以Go后端、前后端交付和B端为主，CRM承担Agent深度。

基线`290cd60358e0467f43caef4ddd647704bdbcfac6`。隔离分支`review/go072-go075-290cd60`，最终本地代码`170629973facf8133f06bf77698f5c7716255764`。没有修改GO-076实现者的未提交文件、使用其测试库或停止CRM/开发服务。GO-076在主工作区继续开发，不纳入本次业务验收。

## 修复与提交

| 内容 | 隔离提交 / 接入main | 结论 |
| --- | --- | --- |
| GO-072-QA1 配置拒绝保留主题名 | `91dace63f1652ecea86305856898bfb80051891b` / `c5ed739730d15920766078c73981484f36713f1c` | 配置曾接受`.`和`..`，运行时与Kafka却无法使用；先补4项失败用例，再拒绝两名称，另4项合法含点名称保持可用 |
| GO-075独立兼容HTTP回归 | `170629973facf8133f06bf77698f5c7716255764` / `1b4ebe8b2bbd0494f0d8f9b68bdecd28f4a20592` | 实际Compatible适配器→本地受控模型HTTP→Gin SSE→真实SQL/Redis，证明客户端取消、同ID重试与缓存重放，无付费调用 |

配置缺陷是P3，不是交易数据损坏。没有发现其他确定的P1/P2，不以未承诺的生产容量、全局金额预算或完整Agent框架为缺陷。修复和回归分别提交，不改写实现者历史。[配置修复前](topic-initial.txt)、[修复后](topic-final.txt)保留原样；原始日志的末尾空行不为格式检查而改写。

## 独立源码审查

三名独立审查者分别核对GO-072存储/查询、消费/CLI/应用生命周期、GO-075 Provider/配置/知识服务，根核验者审查AI回合/HTTP终态并复跑整合验证。

- **投影与恢复**：控制行共享/排他锁隔离Apply与Rebuild；收据、业务版本和投影效果同事务；Replay效果与RESOLVED整体提交；重建保留收据与失败记录。同eventId异义、旧版本、失败原文和bootstrap认领均有约束。
- **查询**：完整性检查和实际读取处于同一RR快照，历史缺事件、部分目录、可选查询错误回退DB；公开状态/当前关注/本人字段仍由SQL事实守护。作者目录+实时pull，不是每用户fan-out。
- **消费**：Fetch→持久Apply或Park→同步Commit，未持久不越过当前消息，提交失败只重试提交；组与资源关闭顺序明确。持久停车是待处理结果，不是业务效果已经成功。
- **Provider与知识**：整次调用的3槽并发/期限、体积和响应校验、无应用层付费自动重试、拒绝重定向；向量身份绑定端点/Embedding模型/维度。检索前验证完整性，慢Embedding后复核Snapshot/Validate，发布/重建计算前后都授权并核对版本。
- **回合**：真实适配与规则只读工具保持权限边界；去重、lease、取消/超时、登录与知识失效、最终发出/缓存重放按原业务协议检查。SSE是阶段反馈和完整答案，不是逐token直传。

## 本轮独立执行

专用Compose项目`project-full-stack-review7275`，MySQL3340、Redis6410、Kafka9104，新卷；未复用GO-076的3332/6404/9099/9202、开发3308/6380或CRM数据库。命令与退出码见[commands.jsonl](commands.jsonl)，原始结果各轮单独保留。

| 验证 | 结果与范围 |
| --- | --- |
| 普通Go最终检查 | mod verify、gofmt、vet、全套普通测试和build通过；[普通测试](unit-final.txt)、[格式](format-final.txt)、[vet](vet-final.txt)、[构建](build-final.txt) |
| 真实依赖最终检查 | `-count=1 -p 1 -json`跑communityprojection/communityconsumer/social/aiknowledge/aichat五包，20个TestIntegration入口、67个子组通过；2个付费测试明确SKIP。入口/子组不相加成业务场景数；[原始结果](integration-final.txt)、[粒度汇总](test-summary.json) |
| Kafka实际位点与贯通 | 真实broker、repository/recovery、双消费组、HTTP→outbox→Kafka→实际查询测试均包含在上述五包复跑中；不是只依靠mock消费者 |
| 新增HTTP取消/重试/重放 | 活跃Chat期间客户端abort，上游取消且生产Observation为`cancelled`；无assistant/lease/dedupe残留、逻辑预算退款；同消息重试成功后重放不再Chat且只保留1条assistant；[最终单测记录](compatible-http-reviewed-final.txt)，最终整合也通过 |
| 核心业务隔离 | AI关闭、缺Key、远端不可达三种配置，真实交易各9项通过，进程正常退出0、端口可重用；[汇总](ai-isolation-final/isolation-final.json) |
| 启动关闭 | 带Kafka配置的核验二进制6场景通过，正常退出/端口回收，无强制清理；[JSON](bootstrap-final.json) |
| 版本迁移 | 本轮新库应用及重复校验跳过通过，包含009—011；[首次](migrate-initial.txt)、[重复](migrate-repeat.txt)，没有新增或改写SQL迁移 |
| 远端CI | main代码`1b4ebe8b2bbd0494f0d8f9b68bdecd28f4a20592`四作业成功，含Linux普通race、真实MySQL/Redis/Kafka与前端检查；[Actions](https://github.com/tech-cove01/project-full-stack/actions/runs/40672913380)、[run](ci-run.json)、[jobs](ci-jobs.json) |

本地被核验生产代码与main接入修复一致；上述CI提交还包含实现者已提交的GO-076部分代码。CI绿色可证明该提交的构建/测试结果，不能升级为本轮已审查ES业务。后续GO-076新增提交需单独核验，旧绿色不自动覆盖新HEAD。Linux普通race不等于所有真实集成都以race执行。

## 原始真实模型证据复核

独立逐题读`eval-cases.json`、`eval-real-initial.json`及原文/来源，核对30题36轮：有据12、无据6、工具6、多轮6；无串门店/明显无来源支撑答案。两项lexical候选误报保留：K07数字与中文数字等价；M01第二轮只问免费时长，无需重复价格。M05正确使用4小时v2，M06停用后拒答。

- 70次原始Observation＝42次Embedding+28次Chat，7258已知tokens，unknown0；12个单黄金chunk均在top5。只代表短合成小集，不外推通用召回/准确率。
- 原始生命周期5场景＝准备1次Embedding+4场景各2次+预算拒绝0次，共9尝试；2次取消/超时usage未知，其余7次415tokens。失败三项无answer/done/assistant且清理lease/dedupe/逻辑预算；恢复产生1条assistant，预算拒绝无新付费请求。
- 两类原证据合计79尝试、7673已知tokens，2次usage未知；原费用估算不是账单，未知结算不能写0。本轮只读复核与受控本地端点回归，未使用CRM凭据、改模型配置或重新付费。
- M04直接SQL改订单状态只验证只读工具刷新，不是允许已支付订单取消；重建Service不等于整机重启。质量集直接运行Assistant，原生命周期才走HTTP；Vue真实AI页面仍属GO-074。

固定原始证据SHA-256及最终关键源码/二进制哈希、真实VCS构建标记见[source-manifest.json](source-manifest.json)。原真实JSON仍留在实现报告目录，不复制密钥、会话或模型输入到新私有配置文件。

## 失败与环境收尾

新增本地HTTP回归先后出现两轮夹具失败，均保留：

1. [初轮](compatible-http-initial.txt)漏给合成session设置TTL，认证按设计拒绝，未进入Chat；补有效TTL及早期HTTP状态检查。
2. [第二轮](compatible-http-fixed-fixture.txt)受控Chat服务未消费请求体便等待context，HTTP/1读取侧没有进入观察断连阶段，最终测试超时；测试服务先读完请求体，并以全局ctx兜底收尾。无生产修改，不将其包装成适配器故障。随后[可用轮](compatible-http-final.txt)与独立审查加强后的最终轮通过，最终整合亦通过；取消前确认未结束并要求Observation=`cancelled`，排除自然超时误判。

推送曾因Git旧代理端口7897触发TLS EOF，当前系统代理7890可用；仅本次命令覆盖代理后正常推送，未改用户全局Git配置。Redis审计辅助脚本初次把JSON对象当平铺数组解析而失败，修正只读解析后完成，不涉及产品或数据写入。

SQL自有AI用户/商家/FAQ、演示用户及未完成outbox/社区停车均0：[DB对账](cleanup-db-final.txt)。AI键、登录session/code均0，`stream.orders`仍保留3条已消费历史，g1 pending0：[Redis](cleanup-redis.json)。不flush缓存、删除共享topic或宣称整个数据库为空。

本轮二进制启动脚本均已结束；仅本轮MySQL/Redis/Kafka正常停止，卷保留，退出0/0/143且无OOM：[退出记录](containers-stopped.json)。Kafka143来自正常SIGTERM，不改写为0。GO-076与CRM服务未停止。

## 当前还要做什么

- GO-076：实现者正在开发公开笔记全文搜索，完成后独立核验；本轮不审在途ES文件。
- GO-074：补搜索、真实AI和关键B端浏览器差量矩阵，复用旧有效证据。
- GO-073：最终功能固定后持续k6负载、成功成单率与P95/P99、冷/热回源次数、Kafka/ES积压追平和事实对账。目前没有Go稳态QPS可写。
- 匿名`/ai/search`不使用会话用户预算、2000分块预查不是跨文档并发硬额度、金额预算/真实429/生产容量/长期稳定/灾难恢复不在本轮结论内。

AI保留，但不再增加第二套Agent平台。原始实现报告不改写，任务状态以根STATUS本轮条目为准；求职材料当前仍是其固定快照，需要新证据时再更新，不能自动把历史资料升级为本轮结果。
