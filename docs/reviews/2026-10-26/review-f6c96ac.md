# f6c96ac 独立核验与剩余交付

日期：2026-10-26，Asia/Shanghai。核验源码 `f6c96ac729b383fa7fe619fc7345af96f02c8ba7`；开始时工作区干净。对比上轮 `e8a292c`，实质进展集中于 H0 修复，前端差异为格式化；H1 社区、Kafka、Feed 尚未开始。

本轮只新增核验文档/脚本及结果，没有修改应用代码、重启后端、停止数据库或调用外部模型。真实 API 针对现有 localhost 实例，该实例没有构建 SHA 接口；构建/源码结论绑定上述 SHA，HTTP 结论绑定当前运行实例，两者不冒充同一构建证明。

## 已执行与认可的进展

| 项目 | 本轮独立结果 | 边界 |
| --- | --- | --- |
| 后端 Maven verify | PASS：60 tests，0 failure/error/skipped | 本机显式 Mockito javaagent；单测不是所有真实故障证明 |
| 前端类型/生产构建、格式检查 | PASS | 没有重做全部浏览器场景 |
| 真实交易回归 | PASS | 发布幂等、STAFF 发布拒绝、异步成单、跨用户拒绝、取消重买、支付、核销重试、退款重试与退款后不重买 |
| 最终库存 | PASS：DB=2、Redis=2 | 活动 1044 初始 3，三单为取消/核销/退款；活动已下架 |
| 补偿 Lua | PASS：4 场景 | 非数字库存、错误资格类型、正常补偿/重复、超过总库存；临时键精确清理 |
| AI 敏感输入 | PASS | 四种输入均 400，Stub embed/chat 计数 43/9 前后不变 |
| AI 抢锁失败重试 | PASS | 人工占有本测试会话锁时 409，NEW 去重已清理；释放后同消息 ID 返回 answer/done，无 error |
| AI 日请求预算 | PASS | 仅将新测试账号日计数临时置高，429 且 Stub 计数 44/10 前后不变，随后恢复；不等于验证实际 token 计费/跨日重置 |
| PROCESSING 补偿阻止重建 | **FAIL，已复现** | 活动 1045 有未过期租约的 PROCESSING 行，reconcile 仍 200 并从版本 1 升为 3；合成行已删除、活动已下架 |

原始结果：[交易与 Lua](runtime-f6c96ac.json)、[AI 边界](ai-boundaries-f6c96ac.json)、[重建漏洞](reconcile-processing-f6c96ac.json)、[只读 DB 观察](db-observations-f6c96ac.json)。

实现者 [T1 原始结果](t1-result.json) 现在支持“DB 不可用到第 5 次仍 pending → 恢复写持久 review → CLI 重放成单”的合法分支，本轮认可该证据，不再沿用上轮 FAIL 判断。本轮没有再次中断数据库。先关闸 N+1、防旧 OPEN 重开、N+2 重建的版本屏障已加入；这部分无需重做。

AI 原子 append、全来源失效分支清理、验证码“是/为”过滤已加入。现有 [AI 原始结果](ai-result.json) 已将预算/deadline/锁释放分开标记，表述比上轮准确；本轮另外独立补了日预算与锁失败重试。

## 仍需修改的具体问题

### R1 / P1：重建漏掉 PROCESSING 补偿（真实接口已复现）

位置：[MerchantActivityService.java:328](../../../backend/src/main/java/com/fullstack/platform/activity/MerchantActivityService.java#L328)、[OutboxRepository.java:63](../../../backend/src/main/java/com/fullstack/platform/outbox/OutboxRepository.java#L63)。

阻断查询写 `IN_PROGRESS`，relay 认领后实际写 `PROCESSING`。新建独立活动及一条未来租约的合成 PROCESSING 补偿，调用 reconcile 返回 200，版本 1→3，补偿行依然 PROCESSING；预期应返回 409 保持关闭。该测试证明漏检，没有故意执行重复回补破坏真实订单。

影响：DB 取消/退款库存已回补、Redis 补偿尚未应用时重建，再应用补偿会重复加 Redis 库存；总库存上限不能阻止所有中间数量失真。

收尾：使用真实投递状态，统一状态常量；补 PROCESSING、PENDING、NEEDS_REVIEW 下的阻断与完成后可恢复验证。脚本：[verify-reconcile-processing.py](scripts/verify-reconcile-processing.py)，只清本次合成事件。

### R2 / P1：锁定行没有包含实际用于重建的库存

位置：[MerchantActivityService.java:265](../../../backend/src/main/java/com/fullstack/platform/activity/MerchantActivityService.java#L265)、[ActivityPublicationService.java:120](../../../backend/src/main/java/com/fullstack/platform/activity/ActivityPublicationService.java#L120)。

reconcile 先执行普通业务/权限查询，FOR UPDATE 只取 publish_status/version；重建又通过普通 `selectById` 取库存。项目 DB 只读观察为 REPEATABLE-READ，应用未另配隔离级别。在先前一致性快照形成后、活动锁获得前有订单提交的窗口，普通读取仍可能拿旧库存，即使 Stream 此刻已无在途。

收尾：在锁定行直接读取库存与重建必要字段并传入 Lua，不再普通查询旧快照；未决查询也应按恢复协议取得当前状态。补“首次普通读后、锁获得前订单提交”的可控并发窗口证据。本轮为代码/隔离级别核验，没有端到端制造该窗口。

### R3 / P1：review 成功终态与重放认领仍不完整

位置：[OrderReviewRepository.java:36](../../../backend/src/main/java/com/fullstack/platform/order/OrderReviewRepository.java#L36)、[review-order.mjs:63](../../../scripts/review-order.mjs#L63)、[MerchantActivityService.java:320](../../../backend/src/main/java/com/fullstack/platform/activity/MerchantActivityService.java#L320)。

1. 新代码能把已 REQUEUED 且再次失败的记录改回 OPEN；但成单/明确终态后没有置 RESOLVED 路径。最新 T1 已成功订单 `653018492815015973` 的实际记录仍为 REQUEUED；重建把它视为未决，会持续 409。
2. CLI 先 XADD 后改 REQUEUED。如果消费者在两条命令间已再次失败并记录 OPEN，CLI 后写会覆盖成 REQUEUED；消息已 ACK 而 CLI 又只允许 OPEN，重放失去入口。

收尾：重放先进行带版本/尝试标识的受控认领，投递确认与消费结果不互相覆盖；成功/确定终态持久结束，失败可回到可重放状态。验证一次成功、重放再次失败再恢复、消费先于 CLI 标记这三个小场景，不扩建工单平台。

### R4 / P2：AI 绑定门店仍有删除竞态

位置：[AiConversationService.java:77](../../../backend/src/main/java/com/fullstack/platform/ai/AiConversationService.java#L77)。

append 已改原子 Lua，可以认可；bindShop 仍是 EXISTS 后分别 HSET，删除落在两者之间会重建 hash。收尾为同样的“存在才绑定”原子操作；删除后的旧任务不能再次使会话可见。无需增加框架。

### R5 / P2：整轮截止、SSE 与锁租期未统一

位置：[AiAssistantService.java:122](../../../backend/src/main/java/com/fullstack/platform/ai/AiAssistantService.java#L122)、[AiLockService.java:26](../../../backend/src/main/java/com/fullstack/platform/ai/AiLockService.java#L26)。

SSE 固定 60s，锁 90s，普通 executor.execute 不持有可取消任务；deadline 在任务开始后设置并只在阻塞调用前后检查。上游慢调用或排队时，旧任务尚未退出而新请求已可能取得过期锁。报告已经说明上游无法取消，但这不能代替任务截止和写入归属控制。

收尾：统一队列等待、任务截止和锁租约；超时/删除/断开后禁止旧任务发送或持久化结果，释放本地资源。上游不能取消就如实保留计费限制。用可暂停 Stub 覆盖，不需要付费模型。

## 后续按三批推进

| 顺序 | 必做内容 | 完成条件 |
| --- | --- | --- |
| 当前修复 | R1—R5，已通过项保留 | 对应小场景通过，复核状态有终点；本批形成稳定 SHA |
| 社区与基础产品 | H1 笔记/点赞/关注/Feed 基线、新 DTO/迁移；outbox/Kafka 基础；列表分页、分类、按门店按钮 | 前后端真实闭环；不会只展示前 50 条；不开放旧不安全写接口 |
| 亮点与交付 | H2—H4 位图/Lua、Feed 分层缓存/热点、Kafka 两类投影；H5 持续负载与演示/材料 | 重复/乱序/重建可验证，数字来自自己的持续测量，README/简历与实际能力一致 |

不增加 Canal、SDS 压缩、ES、微服务等必做项，不取消已授权社区/Kafka/Feed。当前进度是交易/B端/AI主干已成形、H0 明显推进，但三个新增后端亮点的主体尚未开发，不能说整个项目接近全部验收完成。

## 本轮复现命令与限制

```powershell
mvn -B -ntp -f backend/pom.xml '-DargLine=-javaagent:D:\develop\apache-maven-3.9.12\mvn_repo\org\mockito\mockito-core\5.17.0\mockito-core-5.17.0.jar' verify
npm.cmd --prefix frontend run build
npm.cmd --prefix frontend run format:check
$env:MYSQL_PORT = '3307'
python docs/reviews/2026-10-26/scripts/verify-reconcile-processing.py
python docs/reviews/2026-10-26/scripts/verify-ai-boundaries.py
```

交易/Lua 独立复核直接复用了 `verify-local.py` 的函数，但没有执行其覆盖旧 `runtime-check.json` 的主程序；结果独立保存为 `runtime-f6c96ac.json`。AI 脚本必须先确认 stub 才生成答案，临时预算只作用新测试账号。首次 AI 脚本因本机 socket 权限未执行请求；获得本机访问权限后修正 SSE 元组解析并重跑完整通过。没有把脚本自身失败当应用缺陷。

保留测试账号/订单历史，活动 1044/1045 已下架；合成 outbox、临时 Redis 故障键和测试会话已清理。API 活动与账号写入仅本机 dev。实际模型质量、生成中停用、并发重建快照、SSE 慢调用/断开与持续负载均未在本轮执行，不能沿用构建 PASS 代替。
