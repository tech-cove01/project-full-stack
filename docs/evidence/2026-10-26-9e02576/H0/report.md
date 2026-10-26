# H0 核验与修复记录（2026-10-26）

提交：`9e02576`（H0 代码与核验材料；后续证据提交单独记录）
范围：T1—T4；A1—A3 未在本批实施；H1—H5 未开始。

## 本轮命令

```powershell
mvn -B -ntp -f backend/pom.xml test
python docs/reviews/2026-10-26/scripts/verify-local.py
python docs/reviews/2026-10-26/scripts/verify-reconcile.py prepare
# 重启后端
python docs/reviews/2026-10-26/scripts/verify-reconcile.py check 1035
node frontend/.qa/t4-mixed-role.mjs
```

## 结果

| 项 | 结果 | 证据/边界 |
| --- | --- | --- |
| 后端单测 | PASS：60 tests | 0 failures / 0 errors |
| verify-local 交易 | PASS | 发布幂等、STAFF 发布拒绝、取消重买、支付、核销重试、退款后不可重买；最终 DB/Redis 库存 2 |
| verify-local Lua 故障 | PASS：4/4 FIXED | 非数字库存、错误资格类型、重复恢复、超过 total_stock 均无部分写入；原始结果 [runtime-check.json](../../reviews/2026-10-26/runtime-check.json) |
| T3 缺库存/门控 | PASS | 重启后 gate CLOSED + reconcile marker，库存键保持缺失；确认无在途后 reconcile 到新版本并开闸，测试订单成单；[verify-reconcile.json](verify-reconcile.json) |
| T4 混合角色 | PASS | OWNER-A + STAFF-B 账号：A 审计 1 条，B 审计 0 条；[t4-mixed-role.json](t4-mixed-role.json) |
| T1 DB 不可用 | **部分** | 代码在复核记录无法持久化时保留 pending 并退避；单测覆盖 `messageStaysPending`；真实 MySQL 中断超过阈值后恢复未完成（一次演练在停库前已消费成单，不能作为证据） |
| T2 去重生命周期 | **部分** | 异常输入零部分变化、正常重投不二次回补已验证；done 标记长期保留与 outbox 生命周期的边界未做长时间故障演练 |
| A1—A3 | **未执行** | 生成中停用/缓存版本校验、去重包含门店与删除 fence、真实格式凭据遮蔽/预算/限频尚未实现 |
| H1—H5 | **未开始** | 社区/Kafka/位图/Feed/多投影未实现 |

## 修复内容

- T1：新增 `tb_order_review`（迁移 005）、`OrderReviewRepository` 和 `scripts/review-order.mjs`；无法持久记录时不再 ACK，保留 pending 并指数退避。
- T2：`order-restore.lua` 先校验库存、资格集合、受理映射类型和 `maxStock`，再写业务效果；非法输入零部分变化。
- T3：ACTIVE 启动只关闭准入、不按 DB 库存重建；新增显式 reconcile（`activity-reconcile.lua`），拒绝存在 Stream pending/lag 的活动。
- T4：审计查询改用 `managedShopIds`，排除混合账号的 STAFF 门店。
- 迁移 runner 兼容已应用脚本的换行校验；新增 `verify-reconcile.py`、`review-order.mjs`。

## 限制

- H0 尚未完成：A1—A3 没有实现，不能把 BASE-04 标为通过。
- BASE-01 的真实数据库中断恢复未取得有效证据；不能用单元测试替代。
- 测试活动 1030/1032/1033 等是本轮演练数据，未删除业务历史；演示前应下线或清理。
