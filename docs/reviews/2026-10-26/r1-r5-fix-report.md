# f6c96ac 复核后的 R1—R5 修复记录

日期：2026-10-26。基线 `f6c96ac`，对应 [review-f6c96ac.md](review-f6c96ac.md) 第 2 节。

## 1. 改动

| 项 | 位置 | 改法 |
| --- | --- | --- |
| R1 | `MerchantActivityService.pendingCompensations` | 阻断查询改用 relay 实际写入的状态：`PENDING`、`PROCESSING`、`NEEDS_REVIEW`；原来写的 `IN_PROGRESS` 不存在于表中，导致已租约的补偿漏检 |
| R2 | `MerchantActivityService.reconcile`、`ActivityPublicationService.prepareForReconcile` | `FOR UPDATE` 语句同时读取 `stock / begin_time / end_time`，重建直接用这些值传给 Lua，不再在别处 `selectById` 读取可能更旧的快照 |
| R3 | `OrderReviewRepository.resolve`、`VoucherOrderServiceImpl`、`scripts/review-order.mjs` | 建单成功或确定拒绝后把 record 置 `RESOLVED`（失败只记日志，不撤销 ACK）；CLI 改为先 `UPDATE ... WHERE status='OPEN'` 认领再 `XADD`，`XADD` 失败回滚为 `OPEN`，因此消费者记录的失败不会被 CLI 覆盖 |
| R4 | `AiConversationService.bindShop` | 会话存在性与 `HSET shopId/updatedAt` 放进同一 Lua 脚本，删除竞态下不会重建 hash |
| R5 | `AiAssistantService`、`AiLockService`、`AiConversationService.exists` | 截止时间在受理时确定（队列等待计入）；SSE 超时 = `turnTimeout+10s`，锁租约 = `turnTimeout+30s`；生成后校验锁归属与会话存在，失去租约或会话已删除的任务不发送、不落库 |

## 2. 验证（本机 dev，stub provider）

| 项 | 命令 | 结果 |
| --- | --- | --- |
| 后端 | `mvn ... verify` | PASS，60 tests |
| 交易回归 | `verify-local.py` | PASS；活动 1049，库存 2/2；Lua 四场景 FIXED |
| R1 | `verify-reconcile-processing.py`（核验方脚本） | **PASS**：活动 1046，合成 `PROCESSING` 补偿存在时 `reconcile` 返回 409，版本保持 1，补偿行仍 PROCESSING |
| R1 反向 | `verify-reconcile-blocked.py` | 未决 review 时 409；清除后重建 ready |
| R3 终态 | `verify-t1.py` | 活动 1048，订单 `653026782101897263`：DB 停机时 pending=1 不 ACK，恢复后写 review，重放成单，`reviewStatus=RESOLVED` |
| R3 认领 | 手工两次 `review-order.mjs replay` | 第一次认领并投递（exit 0）；第二次返回 `review record is REQUEUED, only OPEN can be replayed`（exit 1），不再覆盖消费结果 |
| R4/R5 | `verify-ai-boundaries.py`（核验方脚本） | 敏感输入 4 种 400 且计数不变；占锁时 409 且 NEW 记录被清理、重试 200+done；日预算 429 且计数不变 |

## 3. 限制

- R2 只做了代码与隔离级别层面的修正，没有端到端制造「首次普通读之后、锁获得之前有订单提交」的并发窗口。
- R4 的删除竞态和 R5 的慢调用/租约过期没有独立故障场景，只验证了正常锁重试与预算路径。
- `verify-t1.py` 的重放子进程需要 `MYSQL_PORT`（本机 3307）；未设置时脚本按 3307 兜底，换机器需显式传参。
- 远端模型请求仍不可取消：超时或断开后旧任务会停止发送与写入，但已发出的调用可能完成并计费。
