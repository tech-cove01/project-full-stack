# d74f7a2 独立核验

日期：2026-10-26，Asia/Shanghai。核验提交 `d74f7a2908903e067d4b7336fa6dee8f959365cf`，对比 `f6c96ac`；开始时工作区干净。没有修改应用代码、停止服务或调用远端模型。运行实例未提供构建 SHA，HTTP 结论绑定本机实例；源码与构建结论绑定上述提交。

## 结论

R1、R3 主要修复可以认可；R2 锁定库存读取已改，但未决查询仍读旧快照，**本轮已端到端复现**。R4 原子绑定通过代码核验；R5 截止/租约有改进，还需统一所有终态分支。社区、Kafka、Feed 没有新增实现。

| 项 | 本轮结果与状态 |
| --- | --- |
| 后端 verify | PASS：60 tests，0 failure/error/skipped |
| R1 PROCESSING 阻断 | 独立实测 PASS：活动 1050，返回409，DB版本保持1，合成补偿仍PROCESSING，清理成功 |
| R2 当前库存 | 代码已改为从同一 FOR UPDATE 行读取并传入 Lua；不再普通 selectById 重读库存 |
| R2 未决查询 | **FAIL：锁等待后提交的新 PROCESSING 补偿被漏检，接口200、版本1→3** |
| R3 终态与投递认领 | 代码先条件认领再 XADD；成功/明确拒绝后 resolve。T1 新 raw 为成功重放且 RESOLVED；本轮只读 DB 再确认同订单仍RESOLVED |
| R4 bindShop | 代码通过：EXISTS+HSET 已在同一 Lua，已指出的 hash 复活路径已关闭；未另做删除竞态端到端场景 |
| R5 生命周期 | 部分：deadline从排队前开始，SSE/锁分别turn+10/30s，主Chat成功路径加保护；其他分支仍绕过保护 |
| AI 基础回归 | Stub下敏感输入4种400且调用计数不变；占锁409后同消息重试answer/done；高日预算计数429且不调用provider，全部PASS |
| 前端 | 本提交无前端变更，沿用上轮构建/格式证据，本轮不重复跑 |

原始结果：[PROCESSING阻断](reconcile-processing-d74f7a2.json)、[并发快照复现](reconcile-snapshot-d74f7a2.json)、[AI基础回归](ai-boundaries-d74f7a2.json)、[DB只读观察](db-observations-d74f7a2.json)。T1 故障时间线为实现者的 [t1-result.json](t1-result.json)，本轮未再次停库。

## 下一轮只修两个具体问题

### H0-R2 / P1：未决查询仍使用恢复事务的旧快照

位置：[MerchantActivityService.java:325](../../../backend/src/main/java/com/fullstack/platform/activity/MerchantActivityService.java#L325)、[MerchantActivityService.java:337](../../../backend/src/main/java/com/fullstack/platform/activity/MerchantActivityService.java#L337)。

业务/权限普通查询先建立 RR 快照。FOR UPDATE 取得当前库存，不会刷新普通一致性快照。`openReviews` 与 `pendingCompensations` 后续 COUNT 仍可能看不到在首次普通读后提交的事件。

本轮可控复现：

1. 创建独立活动1051，版本1；单独DB连接持有该活动行锁。
2. 发起真实 reconcile；通过 processlist 确认其 FOR UPDATE 在等待。
3. 锁持有者插入属于该活动、未来租约的PROCESSING补偿，并提交。
4. reconcile 取得锁后仍返回200并更新到版本3；补偿行仍PROCESSING。预期是409，保持关闭。

这证明新查询仍漏未决项。本测试没有执行合成补偿；实际取消/退款若已回补DB而Redis待补偿，漏检后重建再补偿会重复回补。活动已下架，合成行已清理，没有触碰其他活动。

最小修复：让恢复核对读取当前已提交状态（选择适合此恢复事务的隔离级别或当前读），保留活动锁与Redis版本屏障。继续使用本次可控窗口测试，修复后应409，不再补一个只在请求前插入事件的替代测试。

### H0-R5 / P2：终态输出与缓存写入缺少统一有效性保护

位置：[AiAssistantService.java:160](../../../backend/src/main/java/com/fullstack/platform/ai/AiAssistantService.java#L160)、[AiAssistantService.java:179](../../../backend/src/main/java/com/fullstack/platform/ai/AiAssistantService.java#L179)、[AiAssistantService.java:244](../../../backend/src/main/java/com/fullstack/platform/ai/AiAssistantService.java#L244)。

新guard只覆盖主要Chat成功路径。工具结果、无命中、缓存回放等没有统一检查deadline、锁归属和会话存在；超时分支也在guard前发送answer。请求期间删除/超时后，仍可能发出终态业务答案并完成去重缓存。原子append保护已存在，因此这里不再声称它会复活会话hash。

最小修复：所有终态答案和DONE缓存写入前走同一有效性检查；失效任务清理去重并结束，超时使用专门错误，不再输出业务answer。用可控慢Stub补“生成/检索期间删除”“超过截止”两个场景。远端请求无法取消、已发生调用可能计费可以作为明确MVP限制，无需换框架。

本项是代码核验，未在本轮制造慢调用/删除窗口；不能用日预算和锁重试PASS代替。

## 任务与提交

本轮开始执行用户要求：一项细粒度任务自测完成后独立本地commit，交SHA待核验；核验失败追加修复提交，不重写其他agent历史。现有d74f7a2包含多项修复与文档，保留原样，不倒拆。

当前任务：H0-R1已验收；H0-R3主要路径已验收；H0-R4代码核验通过；H0-R2、H0-R5待修。之后按原H1—H5做笔记/点赞/关注闭环、Kafka投影、位图/Lua、Feed缓存与持续负载，不再重做已通过的H0正常流程。

原版Matt skills没有安装；项目已采用适配后的交接规约，实现agent直接读取即可，无安装前置。

## 复现与证据留存

```powershell
mvn -B -ntp -f backend/pom.xml '-DargLine=-javaagent:D:\develop\apache-maven-3.9.12\mvn_repo\org\mockito\mockito-core\5.17.0\mockito-core-5.17.0.jar' verify
$env:MYSQL_PORT = '3307'
python docs/reviews/2026-10-26/scripts/verify-reconcile-processing.py --output docs/reviews/2026-10-26/reconcile-processing-d74f7a2.json
python docs/reviews/2026-10-26/scripts/verify-reconcile-snapshot.py --output docs/reviews/2026-10-26/reconcile-snapshot-d74f7a2.json
python docs/reviews/2026-10-26/scripts/verify-ai-boundaries.py --output docs/reviews/2026-10-26/ai-boundaries-d74f7a2.json
```

本轮给两个既有核验脚本加 `--output`，默认生成新时间戳文件，后续不覆盖旧版本失败结果；新快照脚本必须指定输出。命令中的输出路径用于重现本次结果，后续提交应换用新SHA/运行时间路径。

报告/脚本/原始结果独立提交，应用修复留给实现agent。没有执行Git回退、推送或部署。
