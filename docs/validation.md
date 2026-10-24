# 首批基线验证记录

日期：2026-10-24。

| 验证 | 结果 | 覆盖与边界 |
| --- | --- | --- |
| 后端 `mvn test package` | 59 项通过，0 失败、0 错误 | 鉴权、编码/矩阵路径绕过、控制器写入兜底、缓存、订单幂等/事务路径、H2 商家门店范围 |
| 前端 `npm run build` | 通过 | 包含 vue-tsc 类型检查和 Vite 生产构建 |
| 前端 `npm run format:check` | 通过 | 源文件与配置格式 |
| Chrome + Mock API | 9 项场景通过 | 浏览、开发验证码、登录跳转、单次抢购提交、异步完成、大整数 ID、刷新恢复、商家门店、401；不等于真实后端联调 |
| Docker Compose 配置检查 | 通过 | `docker compose config --quiet`；未实际创建服务 |
| 文档链接和迁移环境检查 | 通过 | 本地链接有效；未导入原课程环境地址和凭证；构建产物/本地环境文件已忽略 |
| GitHub Actions 干净环境构建 | 前后端通过 | Java 21 / Node.js 24；[首批基线运行记录](https://github.com/tech-cove01/project-full-stack/actions/runs/40091125874) |
| MySQL + Redis HTTP 联调 | 未执行 | 记录生成时 Docker 引擎不可用；这是历史原因，不代表当前状态。提供 `scripts/smoke.py` 供真实服务就绪后运行 |
| k6 / 并发与故障实验 | 未执行 | 已提供店铺查询负载脚本，尚无性能结果 |

本地构建使用 JDK 24.0.1 编译到 Java 21，Node.js 24.15.0。GitHub Actions 配置使用 Java 21 和 Node.js 24，远端结果以仓库 Actions 记录为准。

下一步优先完成真实依赖环境联调，再实现商家活动发布、核销和售后闭环，最后基于同一环境做负载与故障实验。

## 方案交接时的环境复核

2026-10-24 编写交接规范时，只读确认 Docker Desktop 4.66.1 / Engine 29.3.1 的 Linux 引擎可访问，使用 WSL 2 内核；`docker compose ps` 未发现本项目容器。本机 3306 已由原有 MySQL 占用，项目建议改用宿主端口 3307。

本轮没有拉取镜像、启动服务、执行 HTTP 冒烟或重新运行上表测试，真实联调、并发和故障验收仍为未执行。环境与启动步骤见 [Windows 开发说明](windows-development.md)；分阶段交付以 [改造方案](implementation-plan.md) 和 [验收标准](acceptance.md) 为准。

## P0 真实环境联调（2026-10-24 20:32）

上表中“MySQL + Redis HTTP 联调：未执行”的历史记录已被本节取代；历史测试与 GitHub Actions 结果保留。原始报告见 [P0 报告](evidence/2026-10-24-c99e0b1/P0/report.md)。

- 环境：JDK 24.0.1 / Maven 3.9.12 / Node.js 24.15.0 / Docker Engine 29.3.1 / Compose 5.1.0；MySQL 8.4.13（3307）、Redis 7.4.11（6379）；后端 8081、前端 5173。
- P0-1 PASS：真实 MySQL/Redis/后端/前端启动；`/actuator/health` 为 `UP`；容器 healthy。
- P0-2 PASS（修复后）：临时空卷项目 `pfs-initcheck` 初始化出 14 张表且中文正确；主库重建后同样正确。发现并修复 `002-demo-data.sql` 缺少 `SET NAMES utf8mb4` 导致的二次编码；未删除主数据卷。
- P0-3 PASS：真实订单 `652313753106251777`（smoke）与 `652313774581088258`（浏览器）均落库为 `status=1`；DB/Redis 库存 100→98；预约状态 `CREATED`；Stream pending 0、lag 0、死信 0。
- P0-4 PASS：`python scripts/smoke.py --base-url http://127.0.0.1:8081` 输出 `PASS`。
- P0-5 PASS：浏览器走真实后端完成登录、浏览、抢券、轮询、刷新恢复；他人查单被拒绝，匿名请求 401；截图见 [real-order.png](evidence/2026-10-24-c99e0b1/P0/real-order.png)。
- P0-6 PASS：本节即为记录。
- 未执行：P1 的 003 迁移与旧卷升级、P2 的 k6/并发/故障演练；浏览器仅 Chrome 桌面。

## P1 交易与商家闭环（2026-10-24 21:05）

P1 代码提交 `022c88a`；完整原始报告见 [P1 报告](evidence/2026-10-24-022c88a/P1/report.md)。

- 迁移：`scripts/migrate.mjs` 应用 003；已有基线库升级通过，重复执行跳过；临时空卷项目 `pfs-p1check` 初始化 001/002 后应用 003，16 张表、`active_flag` 与 `publish_status` 存在。未演练迁移中途失败。
- P1-1 PASS：店主在真实页面发布活动，重复请求键幂等、不同内容 409、STAFF 403；下架后的旧 `ACTIVITY_OPEN` 重放未重新开闸。
- P1-2 PASS：已支付、已核销、已退款三种终态；金额快照 1000 分，操作人类型正确。
- P1-3 PASS：未支付取消后可重购（新订单 ID）；退款后再次抢购不产生新订单，资格标记保留。
- P1-4 PASS：DB 到期扫描关闭订单；过期支付返回 409。
- P1-5 PASS：20 个并发核销请求只有 1 次核销效果（10 个幂等成功、9 个 409、0 个 5xx），`ORDER_REDEEMED` 审计仅 1 条。
- P1-6 PASS：STAFF 退款 403、普通用户核销 403、跨门店核销 404。
- P1-7 PASS：取消补偿事件重投不重复加库存；库存键缺失时 outbox 进入 `NEEDS_REVIEW` 且不创建库存键。
- 对账：活动 1009/1010/1011/1013/1014 的 `DB剩余 + 有效订单 = 初始库存` 与 Redis 剩余量均成立；outbox 全部 DONE，Stream pending/死信 0。
- 页面：店主发布、用户支付取码、店员核销三段真实前端流程通过；截图见 P1 报告。
- P0 的两笔未支付演示订单已被 P1 的到期扫描关闭（status=4、库存回补），因此后续库存快照与 P0 当时记录不同，属预期行为。
- 未执行：P1-D AI 助手（新增规范，需真实模型 Key）、P2 的 k6/故障/限流、AI 验收。

## P2 并发与恢复（2026-10-24 21:15）

P2 代码提交 `cf65fd2`；完整报告见 [P2 报告](evidence/2026-10-24-cf65fd2/P2/report.md)。

- 多账号：20 VU × 10 轮共 200 个不同账号争抢活动 `1021` 的 100 库存；100 个受理、100 个售罄、0 个系统错误；DB 100 单/100 用户、DB/Redis 库存 0、pending 0、死信 0；受理 P95 29.38 ms（本机短突发，不作容量）。
- 重复请求：活动 `1019` 同账号 20 并发，5 次业务成功均为同一订单 ID 的幂等返回，15 次 429；最终只有 1 笔订单、库存 99。
- 消费者重启：活动 `1020` 暂停消费者后受理 20 单（lag 20、DB 0），kill 后端并重启后 20 单全部创建，库存 0、pending 0。
- pending 接管：活动 `1022` 将 1 条消息投递给不 ACK 的假消费者（pending 1、DB 0），重启后新消费者 claim 并创建订单，库存 2、pending 0。
- 负载模型发现：200 个 VU 同时建连时 100 个请求在 TCP 层被拒绝，未作为业务结果；后续改用 20 VU × 10 轮。具体限制层未定位。
- 未执行：MySQL 短暂不可用、DB 提交结果未知、死信自动重放/对账页面、系统 CPU/内存/连接池指标、多实例、AI 验收。

## P1-D AI 助手 MVP（Stub，2026-10-24 21:40）

AI 后端提交 `803b9f6`、`6a59da6`，前端 `cbeef75`；完整报告见 [AI 证据](evidence/2026-10-24-cbeef75/ai/report.md)。

- AI-01 PASS：`AI_ENABLED=false` 启动，capabilities 返回 `enabled=false/mode=disabled`；真实 MySQL/Redis 交易冒烟 PASS，AI 未阻塞交易。
- AI-03/04 PASS（stub）：有据问题返回正确来源与引用；无据问题拒答且不发送 sources；A1/A2 门店规则按服务端门店过滤。
- AI-05/06 PASS（部分）：本人订单工具返回实时状态且不含 token；手机号输入 400，他人会话 404，不存在订单不泄露信息。
- AI-07 PASS（部分）：同会话并发 409；同消息 ID 同内容返回 cached、不同内容 409；TTL/预算未用可控时钟验证。
- AI-08/09/10 PASS（有限）：草稿不可检索、发布后可检索、停用后不可检索；STAFF 只读已发布、跨店 403；注入未导致泄露或写操作。
- AI-12 PASS（stub 页面）：商家创建→发布→助手检索来源→打开公开来源；助手“我的订单”显示实时卡片；截图见 AI 报告。
- 真实 provider（AI-11/13/14）部分执行：SiliconFlow Chat `Qwen/Qwen2.5-7B-Instruct` + Embedding `Qwen/Qwen3-Embedding-0.6B`（1024 维）可用；真实 UI 通过；31 题固定集知识 11/12、无据拒答 6/6、工具 7/7、多轮 6/6，K07 出现一次有证据但模型误拒。未测黄金 chunk Recall@K、真实 usage/费用、429/abort/预算耗尽。Key 只在进程环境使用，未写入仓库；因曾在聊天中出现，建议轮换。
- 与设计的已知差距：未引入 LangChain4j（自实现 OpenAI-compatible provider）、未使用 `InMemoryEmbeddingStore`（进程内精确扫描）、发布为请求内同步嵌入而非后台 worker。
- 嵌入模型隔离：`49db35c` 记录每个 provider 的 embedding model，索引加载时跳过不同模型/维度；切换 provider 时种子文档自动重建。该分支尚未用真实 provider 演练。
