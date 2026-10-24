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
