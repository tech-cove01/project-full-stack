# P0 真实环境联调报告

日期：2026-10-24 20:32 +08:00

## 提交与环境

- 基线提交：`c99e0b17536441034fcf628a762fda6196f1da18`
- 工作区：规范文档与 `infra/mysql/002-demo-data.sql` 有未提交改动；本报告同时记录 diff 范围，提交号在 P0 批次提交后补记。
- 机器：Windows 11，Intel i9-13900H，33,960,505,344 bytes 内存。
- 工具：JDK 24.0.1（编译目标 21）、Maven 3.9.12、Node.js 24.15.0、npm 11.12.1、Python 3.13.0。
- Docker：Desktop 4.66.1 / Engine 29.3.1 / Compose 5.1.0（Linux containers）。
- MySQL：`mysql:8.4`，实际 8.4.13，digest `sha256:76f0358884cb92d9a086d24e5671c38048d3fef40f2588703d73eb55392d6b69`；宿主 3307 → 容器 3306。
- Redis：`redis:7.4-alpine`，实际 7.4.11，digest `sha256:57e4255cc971b1c8d6f8a6ca283ac01bc3ecdd914273ef6bfaede87aaecd6541`；宿主 6379。
- 后端：127.0.0.1:8081，`dev` profile；前端：127.0.0.1:5173。
- 本地 `.env` 仅含 `MYSQL_PORT=3307`，被 Git 忽略。

## 复现命令

```powershell
Set-Location -LiteralPath 'D:\桌面\The ALL\agent\project\code\project-full-stack'
docker compose up -d mysql redis
docker compose ps
# 另一个终端
$env:MYSQL_PORT='3307'; $env:SPRING_PROFILES_ACTIVE='dev'
mvn -f .\backend\pom.xml spring-boot:run
# 另一个终端
npm.cmd --prefix .\frontend run dev -- --port 5173 --strictPort
# 验证
python .\scripts\smoke.py --base-url 'http://127.0.0.1:8081'
cd .\frontend; node .qa\real-smoke.mjs
```

## 结果

| 编号 | 结果 | 期望 | 实际 | 证据 |
| --- | --- | --- | --- | --- |
| P0-1 | PASS | 真实依赖、后端、前端启动，浏览器请求进入后端 | `/actuator/health` 返回 `{"status":"UP"}`；Vite 5173 正常；容器 healthy | 本报告 + 后端日志 |
| P0-2 | PASS（修复后） | 空库初始化表结构、约束、种子数据正确 | 临时 Compose 项目 `pfs-initcheck`（3308）空卷初始化：14 张表，门店中文正确；主库手动重建后同样正确 | 见“发现与修复” |
| P0-3 | PASS | 登录、浏览、抢券、轮询、刷新恢复、越权拒绝 | smoke PASS；浏览器 PASS；订单 `652313774581088258` 最终 `CREATED/1` | [real-order.png](real-order.png) |
| P0-4 | PASS | `scripts/smoke.py` 通过 | `PASS: shops, login, owner/staff shop scope, async order, cross-user and anonymous access checks.` | 命令输出 |
| P0-5 | PASS | 浏览器真实后端主链路 | `{"pass":true,"phone":"13745034291","orderId":"652313774581088258","otherUserRejected":true,"anonymousStatus":401}` | [real-order.png](real-order.png)、[real-smoke.mjs](real-smoke.mjs) |
| P0-6 | PASS | 更新验证记录 | 已追加到 `docs/validation.md` | 本文档 |

## 业务与存储快照

订单（真实 MySQL）：

```text
id                  user_id  phone        voucher_id  status  create_time
652313753106251777        3  15871688736        1001       1  2026-10-24 20:30:31  # smoke
652313774581088258        4  13745034291        1001       1  2026-10-24 20:30:36  # 浏览器
```

- DB 库存：`1001=98`、`1002=50`（初始 100/50，两笔受理各扣 1）。
- Redis 库存：`seckill:stock:1001=98`。
- 购买资格标记：`SISMEMBER seckill:order:1001` → 用户 3、4。
- 预约：`order:reservation:652313753106251777`、`order:reservation:652313774581088258`，state 均为 `CREATED`。
- Stream：消费组 `g1`，pending 0，lag 0；死信流长度 0。
- 幂等性：两笔订单分别属于两个不同用户；无重复订单。

## 发现与修复

1. **种子数据二次编码（已修复）**：`002-demo-data.sql` 缺少 `SET NAMES utf8mb4`。Docker 入口脚本会为每个初始化文件启动独立的 mysql 客户端；容器 locale 为 POSIX，客户端按 latin1 读取 UTF-8 文本，导致中文被存成 `è¡—è§’å’–å•¡`。`001-schema.sql` 自己的 `SET NAMES` 不会延续到 `002`。
   - 修复：在 `002-demo-data.sql` 顶部加入 `SET NAMES utf8mb4;` 和说明。
   - 验证：临时项目 `pfs-initcheck` 在空卷上重新初始化，14 张表、门店名称正确；随后 `down -v` 只清理该临时项目卷。
   - 主库：用 root 重建 `project_full_stack`（utf8mb4/utf8mb4_0900_ai_ci），按顺序导入 001、002；未删除主数据卷。
2. **首次空卷校验误判**：MySQL 初始化期间会先启动临时 server，健康检查可能短暂变 healthy；第一次查询落在临时 server 关闭窗口，出现 `ERROR 2013`。第二次等待稳定 healthy 后通过。这属于校验脚本时序问题，不是初始化失败。
3. **PowerShell 显示问题**：`Invoke-RestMethod` 在控制台把 UTF-8 显示成乱码，但原始响应字节以 `E8 A1 97...` 开头，按 UTF-8 解码正确；浏览器页面中文正常。不是服务端编码错误。

## 未执行 / 限制

- 已有基线库升级到 P1 迁移：尚未编写 003，未执行。
- k6、并发、消费者重启、补偿重试、Redis/MySQL 故障演练：属于 P2，未执行。
- 浏览器仅覆盖 Chrome 桌面 1280×900；未在本轮重跑移动端截图。
- 结论仅适用于本机单实例、单 MySQL/Redis、无负载场景，不能外推生产容量。
