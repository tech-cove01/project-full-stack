# H0 修复记录：T1—T4、A1—A3

日期：2026-10-26。源码基线：`1af11be`（工作区含本轮未提交改动）。
对应规范：[backend-highlights-plan.md](../../backend-highlights-plan.md) 的 H0、[review.md](review.md) 第 4 节。

## 1. 改动

| 项 | 位置 | 改法 |
| --- | --- | --- |
| T1 | `VoucherOrderServiceImpl`、`OrderReviewRepository`、`005-order-review.sql` | 超过重试阈值后先写 `tb_order_review`（DB），成功才写死信并 ACK；写不进去就保持 pending 并指数退避（1s→30s）。受控重放：`scripts/review-order.mjs list/replay` |
| T2 | `order-restore.lua`、`OrderCompensationService` | 写前校验 done 类型、stock 存在/类型/可解析、`stock+1 <= total_stock`、资格集合与映射类型；全部通过才 `SET done`、改库存、释放资格。去重键不再过期，生命周期由 `tb_outbox` 的 DONE 记录承担 |
| T3 | `DevDataInitializer`、`ActivityPublicationService`、`activity-reconcile.lua`、`MerchantActivityController` | 启动时 ACTIVE 缺 stock/gate 只写 `seckill:reconcile:<id>` 差异标记并把 gate 置 CLOSED，不再从 DB 库存重建；`prepareAndOpen` 对 ACTIVE 只允许开已存在的同版本 gate；新增 `POST /merchant/activities/{id}/reconcile?confirm=true`，要求 Stream 无 pending/lag 才重建 |
| T4 | `ShopAccessService.managedShopIds`、`MerchantOrderService.audits` | 审计范围只取 OWNER/MANAGER 门店，STAFF 授权门店不再并入 |
| A1 | `AiAssistantService` | 生成后再次核对来源 `documentId:version:epoch`；缓存保存依赖，重放前校验，失效则删除缓存并返回 `KNOWLEDGE_CHANGED`；对话记忆跳过引用已失效的旧答复 |
| A2 | `AiDedupeService`、`AiBudgetService`、`AiAssistantService`、`AiConversationService` | 去重改 Lua 原子操作，摘要含有效门店；抢锁/并发失败清理 NEW 记录；每用户每分钟限频与日预算；整轮 deadline；会话删除后 append 不再重建会话 |
| A3 | `AiAssistantService` | 敏感过滤覆盖 32 位十六进制登录 token 与带标签 6 位验证码，保留订单号/金额 |

配置新增：`ai.daily-budget`（默认 200）、`ai.turn-timeout-seconds`（默认 90）。
迁移新增：`infra/mysql/migrations/005-order-review.sql`。

`scripts/migrate.mjs` 修复 Windows 下 CRLF 导致的校验和误报（之前 003 会被判 mismatch）。

## 2. 执行与结果

环境：Windows、JDK 24.0.1、MySQL 8.4（3307）、Redis 7.4（6379）、后端 8081（`AI_ENABLED=true`、`AI_PROVIDER=stub`）。

| 项 | 命令 | 结果 |
| --- | --- | --- |
| 后端 | `mvn ... verify` | PASS，60 tests（原 59 + 1） |
| 交易回归 + Lua | `python docs/reviews/2026-10-26/scripts/verify-local.py` | PASS；活动 1028，库存 2/2；Lua 四场景 FIXED |
| T1 | `python docs/reviews/2026-10-26/scripts/verify-t1.py` | 核心不变量成立，但脚本记录为 FAIL（见下） |
| T3 | `python .../verify-reconcile.py prepare` + 重启 + `check 1031` | 缺键保持关闭、显式重建后成单 `652996579891871775`；`reconcile` 的停写窗口尚不完整（见限制） |
| T4 | `python .../verify-mixed-role.py` | 混合账号审计只含 shop3；shop1 订单查询仍返回 366 条 |
| A1/A3 | `python .../verify-ai.py` | FIXED（停用后缓存重放被拒、新问题不再引用；两类凭据 400） |
| A2 | 同上 | 只覆盖每分钟限频（第 17 次 429）；日预算、锁失败清理、整轮 deadline 未单独验证 |

T1 时间线（订单 `652997653633695778`）：

```text
16:44:57 attempt 1  ... 16:47:12 attempt 5   MySQL 停止，create 全部失败
XPENDING g1 = 1，reservation state=PROCESSING，无 ACK
MySQL 恢复后下一轮：review 行写入成功 -> ACK；订单未创建
node scripts/review-order.mjs replay ... --confirm -> 成单 status=1，review=REQUEUED，pending=0，DB/Redis 库存均 1
```

脚本本身以 HTTP 404（取消订单时本人 token 已不可用）结束，因此 [t1-result.json](t1-result.json) 记录为 FAIL；业务结果以随后的 SQL/Redis 查询与 `review-order.mjs` 输出为准。更早一次运行（活动 1037）走的是另一条分支：DB 恢复后消息直接建单，`XPENDING` 归零，没有产生 review 行。两条分支都证明「只有持久结果才 ACK」，但脚本还没把「review → 重放」识别为合法恢复，见限制。

Lua 四场景（`runtime-check.json`）：非整数库存、资格键类型错误均返回 -2 且 done/库存/映射零变化；正常补偿一次生效、重复返回 0；超过 total_stock 返回 -3 且零变化。

AI 结果：首轮命中测试文档并返回 2 条来源；同 `clientMessageId` 重放 `cached=true`；停用后同 ID 重放返回 `KNOWLEDGE_CHANGED` 且无 answer；新问题只引用平台 FAQ（不含被停用文档）；32 位 token 与「验证码 123456」均 400；第 17 次连续请求 429。

原始结果：[runtime-check.json](runtime-check.json)、[t1-result.json](t1-result.json)、[ai-result.json](ai-result.json)。

## 3. 限制

- T1 的「超过阈值」窗口：第 5 次失败时 DB 仍不可用，消息保持 pending；恢复后的下一次尝试写成了 review 行并 ACK，订单由受控重放补回。没有做「DB 永久不可用后手工核对」的演练。
- AI 验证全部在 stub provider 下完成，只证明协议、权限与失效边界；回答质量不能引用本结果，真实模型证据仍是 `docs/evidence/2026-10-24-cbeef75/ai/`。
- 未执行：k6 负载、Kafka、社区功能（H1 起）。前端格式检查已修复，`format:check` 与 `build` 通过。
- 本轮启动的本地实例使用 stub provider，真实模型需要另外配置 `AI_ENABLED/AI_API_KEY` 后重启。
- `reconcile` 仍以「Stream pending/lag 为 0」作为重建前提，没有先关闭准入、核对未决 review 与待补偿 outbox，也没有阻止旧 OPEN 事件重开，见 [docs/README.md](../../README.md) 第 3 节。
- `OrderReviewRepository` 刷新行时不把 `REQUEUED` 改回 `OPEN`，CLI 又只允许 `OPEN`，同一订单第二次重放失败后无法再次受控恢复。
- 会话删除与写消息仍不是原子操作；来源全部失效的提前返回分支没有清理 dedupe 的 PROCESSING 记录；无法取消的远端请求尚未记录为已知限制。
- 敏感输入只覆盖了「32 位 token」和「验证码 123456」，未覆盖「验证码是123456」「验证码为：123456」等变体。
