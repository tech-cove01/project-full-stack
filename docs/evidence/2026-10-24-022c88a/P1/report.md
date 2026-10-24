# P1 交易与商家闭环验收报告

日期：2026-10-24 21:05 +08:00

## 提交与环境

- 基线：`c99e0b1`（P0）；P1 代码提交：`022c88abe090403034b9e4648d7e1cfaf1c56a35`。
- 环境：Windows 11 / i9-13900H / 32 GB；JDK 24.0.1（目标 21）、Maven 3.9.12、Node 24.15.0、Python 3.13.0。
- MySQL 8.4.13（3307）、Redis 7.4.11（6379）、后端 8081、前端 5173；Docker Engine 29.3.1 / Compose 5.1.0。
- 迁移：`infra/mysql/migrations/003-trade.sql` 由 `scripts/migrate.mjs` 应用；`tb_schema_migration` 记录版本与 SHA-256。
- 本报告中的测试活动均为独立测试数据，不把课程原始活动 `1001/1002` 当作并发结果。

## 迁移验证

| 场景 | 结果 | 证据 |
| --- | --- | --- |
| 基线库升级 | PASS | 003 在已有 2 笔订单的基线上应用；`shop_id/pay_amount/pay_deadline_at` 回填，`total_stock` 按“剩余库存 + 状态 1/2/3 订单数”计算，`active_flag` 生成列与唯一索引创建 |
| 迁移重复执行 | PASS | 再次运行输出 `skip 003-trade / database is up to date`；未重复 ALTER |
| 全新空库 | PASS | 临时 Compose 项目 `pfs-p1check`（3308）空卷初始化 001/002，再应用 003：16 张表（14 基线 + outbox + migration），`active_flag`、`publish_status` 存在；随后删除临时卷 |
| 回滚边界 | 记录 | MySQL DDL 隐式提交；若迁移中途失败，版本行不会写入，需按结构检查恢复。当前未演练“迁移中途 kill” |

## P1 验收结果

| 编号 | 结果 | 实际证据 |
| --- | --- | --- |
| P1-1 发布与幂等 | PASS | 店主通过真实页面发布活动 `1013`；同请求键同内容返回同一 ID，不同内容返回 409；STAFF 发布返回 403；下架 `1006` 后 gate 为 `CLOSED/2`，重放旧 `ACTIVITY_OPEN`（v1）后 gate 仍为 `CLOSED/2` |
| P1-2 三种终态 | PASS | `1009` 订单 `A` 已支付（status=2）；`1011` 订单 `A` 已核销（status=3）；`1011` 订单 `B` 已退款（status=6）；金额 1000 分、操作人类型 USER/MERCHANT 正确 |
| P1-3 取消重购/退款不可重购 | PASS | `1009` 用户 25 取消订单 `652319843369877520` 后成功创建新单 `652319843369877521`；`1011` 用户 28 退款后再次抢购未产生新订单，旧单保持 status=6，Redis 资格标记保留 |
| P1-4 超时关单 | PASS | `1010` 订单 `652319856254779410` 的 `pay_deadline_at` 改为过去后，DB 定时扫描将其改为 4，库存回补；再支付返回 409。ZSet 未作为唯一依据 |
| P1-5 并发核销 | PASS | `1014` 订单 `652321784695095319` 的 20 个并发核销请求：11 个成功（其中 10 个 `alreadyRedeemed=true`）、9 个 409、0 个 5xx；`ORDER_REDEEMED` 审计仅 1 条；订单最终 status=3 |
| P1-6 权限矩阵 | PASS | STAFF 退款 403；普通用户核销 403；用 STAFF 对错误门店 `2` 核销门店 1 的 token 返回 404；无效 token 返回 404；店主可退款，STAFF 只可核销授权门店 |
| P1-7 补偿幂等/缺键 | PASS | 取消事件重复投递后 Redis 库存保持 3（未二次增加），`comp:done` 去重键存在；删除库存键后重投同一事件，outbox 进入 `NEEDS_REVIEW` 且没有创建库存键；随后人工恢复库存键与事件状态 |

### 补充：退款与核销并发（2026-10-24 23:20）

3 笔已支付订单分别同时发起“店员核销”和“店主退款”：

- 3/3 场景退款先成功（status=6），核销返回 409；没有出现同时核销和退款。
- 审计：每笔只有 `ORDER_PAID` + `ORDER_REFUNDED`，无 `ORDER_REDEEMED` 记录。
- 库存：活动 `1023` 初始 10，三笔退款后 DB/Redis 均为 10。

这补充了 P1-5 的“退款与核销互斥”证据；原 20 并发核销证据仍然有效。

## 对账快照

| 活动 | DB 剩余 | 初始 | 有效订单（1/2/3） | 等式 | Redis 剩余 |
| --- | --- | --- | --- | --- | --- |
| 1009 | 3 | 5 | 2 | 3 + 2 = 5 | 3 |
| 1010 | 2 | 2 | 0 | 2 + 0 = 2 | 2 |
| 1011 | 4 | 5 | 1 | 4 + 1 = 5 | 4 |
| 1013 | 99 | 100 | 1 | 99 + 1 = 100 | 99 |
| 1014 | 2 | 3 | 1 | 2 + 1 = 3 | 2 |

- 同一 `user_id + voucher_id` 的有效订单重复查询为空。
- outbox 39 条全部 `DONE`，无 `NEEDS_REVIEW`；Stream pending 0、死信 0。
- 审计动作：`ACTIVITY_CREATE 10 / ACTIVITY_ACTIVE 10 / ACTIVITY_OFFLINE 1 / ORDER_PAID 7 / ORDER_REDEEMED 3 / ORDER_REFUNDED 1 / ORDER_CANCELLED(USER) 2 / ORDER_CANCELLED(SYSTEM) 5`。

## 页面演示

- [店主发布活动](p1-owner-published.png)
- [用户支付后展示核销码](p1-user-paid.png)
- [店员核销与订单列表](p1-staff-redeemed.png)

浏览器流程：店主在 `/merchant/activities` 发布 → 用户在店铺页登录、抢购、模拟支付 → 店员在 `/merchant/orders` 粘贴 64 位 token 核销。最终用户订单 `orderStatus=3`，页面无 JS 异常。

## 实现中修复的缺陷

1. `OrderCreationService` 未填充新增的 `shop_id/pay_amount/pay_deadline_at` 非空快照，导致订单落库失败并进入重试；已按活动与券快照补齐。
2. `JdbcTemplate` 条件更新绕过了 MyBatis 一级缓存，支付后立即查询返回旧的 status=1；在更新后清理本地缓存，并改为重新读取。
3. 取消后的 Redis 资格释放是异步的，用户马上重购会拿到旧受理 ID；在事务提交后增加一次 best-effort 同步补偿，outbox 仍作为失败兜底。
4. 活动列表 SQL 误用 `sv.shop_id`（门店归属在 `tb_voucher`），已改为 `v.shop_id`。
5. 种子数据缺少 `SET NAMES utf8mb4` 的 P0 问题已在 P0 批次修复。

## 未执行 / 限制

- P1-D AI 助手（`ai-design.md` / `ai-acceptance.md`）是交接期间新增的规范，本次未实现；需要先确认是否纳入本轮范围与模型 Key。
- P2 的 k6 多账号负载、消费者重启/DB 故障演练、限流 429 尚未在此报告完成。
- 测试数据未清理；活动 `1005/1007/1008/1013/1014` 等仍是本地测试数据，P2 前会准备独立活动与账号池。
- 页面角色切换使用本地缓存的店主/店员 Token，未重复验证两者的登录 UI；登录 UI 在 P0 浏览器场景已验证。
- 本结论只适用于本机单实例、无并发负载场景。
