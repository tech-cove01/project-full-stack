# 运行与验证

Windows 本机的具体端口、环境检查和三个 PowerShell 终端启动步骤见 [Windows 开发说明](windows-development.md)。后续改造任务见 [分阶段方案](implementation-plan.md)，交接入口见 [agent-handoff.md](agent-handoff.md)，目标验收见 [acceptance.md](acceptance.md)。本文接口表描述当前基线，不代表规划接口已经实现。

## 环境与启动

- JDK 21 是编译目标；兼容的较新 JDK 也可以通过 `--release 21` 构建。
- Node.js 24；前端依赖以 `frontend/package-lock.json` 为准。
- Maven 3.6.3+；Docker Compose v2。

先运行 `docker compose up -d mysql redis`，等 `docker compose ps` 显示两个服务 healthy 后，再运行 `mvn -f backend/pom.xml spring-boot:run`。

本机原有 MySQL 已占用 3306；在启动 Compose 和后端的各个 PowerShell 窗口中都先执行 `$env:MYSQL_PORT = '3307'`，使项目容器映射与后端连接保持一致。仓库默认值仍是 3306；这个环境变量是本机端口覆盖，不会更改原有 MySQL 服务。

首次初始化将读取 `infra/mysql/001-schema.sql` 和 `002-demo-data.sql`。已有数据库卷不会再次执行这些脚本；新增表结构通过 `infra/mysql/migrations/` 下的版本化 SQL 和 `scripts/migrate.mjs` 显式应用。迁移脚本会在 `tb_schema_migration` 记录版本与校验和，重复执行会跳过已应用版本。

```sh
$env:MYSQL_PORT = '3307'
node scripts/migrate.mjs
```

默认数据库名为 `project_full_stack`，用户 `fullstack`，密码为 `.env.example` 中的本地开发值。

AI 助手默认关闭；关闭时交易不受影响。配置 `AI_ENABLED=true` 和 `AI_PROVIDER=stub` 可用确定性组件验证知识发布、检索、工具和 SSE；配置 `AI_PROVIDER=openai-compatible` 与 `AI_API_KEY/AI_BASE_URL/AI_CHAT_MODEL/AI_EMBEDDING_MODEL/AI_EMBEDDING_DIMENSIONS` 才调用真实模型。Key 只放在后端环境，不写入前端或仓库。后端使用 `MYSQL_HOST/PORT/DATABASE/USER/PASSWORD`、`REDIS_HOST/PORT/PASSWORD/DATABASE` 环境变量。根目录 `.env` 仅由 Compose 自动读取，后端进程不会自动读取它。

应用默认使用 `dev`：返回模拟验证码并从演示数据库预热 Redis 活动库存与 GEO。预热使用 SETNX，不覆盖已有预约库存。部署时显式设置 `SPRING_PROFILES_ACTIVE=prod`、自己的连接参数和服务地址；真实短信服务尚未接入。

Docker 未运行时，可以进行后端单元测试和前端构建，真实 API 联调需要 MySQL、Redis 就绪。

## 关键接口

接口直接由后端提供，前端开发服务器将 `/api` 前缀去掉后代理到后端。

| 方法 | 后端路径 | 说明 |
| --- | --- | --- |
| POST | `/user/code?phone=...` | dev 下返回 `{code, mode:"development"}` |
| POST | `/user/login` | JSON `{phone,code}`，返回 Token |
| POST | `/user/logout` | 撤销当前 Token |
| GET | `/shop/of/name?current=1` | 分页店铺列表 |
| GET | `/shop/{id}` | 店铺详情 |
| GET | `/voucher/list/{shopId}` | 门店活动 |
| POST | `/voucher-order/seckill/{id}` | 受理抢购，返回字符串订单 ID；同用户重试返回原 ID |
| GET | `/voucher-order/{id}` | 本人订单详情，含 `orderStatus`、金额、期限；已支付单返回核销 token |
| GET | `/voucher-order?current=1` | 本人订单分页 |
| POST | `/voucher-order/{id}/pay` | 本人模拟支付；幂等 |
| POST | `/voucher-order/{id}/cancel` | 本人取消未支付单；取消后可重购 |
| GET | `/merchant/workspace` | 当前用户商家角色与授权门店 |
| POST | `/merchant/activities` | 店主发布活动，需 `Idempotency-Key` |
| GET | `/merchant/activities` | 授权范围活动分页与准备状态 |
| POST | `/merchant/activities/{id}/retry-publication` | 重试 Redis 准备/开闸 |
| POST | `/merchant/activities/{id}/offline` | 同步关闸并下架 |
| GET | `/merchant/orders` | 授权门店订单分页，不返回核销 token |
| POST | `/merchant/orders/redeem` | 店员/店主凭 `{shopId, token}` 核销 |
| POST | `/merchant/orders/{id}/refund` | 店主/管理者模拟退款 |
| GET | `/merchant/audits` | 店主/管理者查看操作审计 |
| GET | `/ai/capabilities` | AI 开关、provider 模式与就绪原因，不返回 Key |
| POST | `/ai/conversations` | 新建本人会话 |
| GET/DELETE | `/ai/conversations/{id}` | 本人历史/删除；他人 404 |
| POST | `/ai/conversations/{id}/messages` | 携带 authorization 的 POST，返回 SSE 阶段事件 |
| GET | `/ai/knowledge/{id}?version=` | 当前已发布公开来源 |
| GET/POST | `/merchant/knowledge?shopId=` | 门店公开 FAQ 列表/新建草稿 |
| PUT/POST | `/merchant/knowledge/{id}` 及 publish/disable/rebuild | 店主/管理者维护公开知识 |
| GET | `/actuator/health` | 健康状态 |

统一响应为 `{success,errorMsg?,data?,total?}`。`authorization` 请求头携带登录 Token；业务失败应读取 `success`，不能只判断 HTTP 200。订单 `state` 取 PROCESSING、CREATED 或 FAILED，业务订单状态另由 `orderStatus` 表示；数据库物理列仍为 `status`。

## 自动化验证

```sh
mvn -B -ntp -f backend/pom.xml verify
npm --prefix frontend ci
npm --prefix frontend run build
```

测试不依赖外部数据库或 Redis：Mockito 验证订单/缓存/鉴权路径，H2 验证商家和门店的真实查询范围。它们不验证 Redis Lua 在真实服务上的行为或 MySQL 的全部特性。

开发服务运行后：

```sh
python scripts/smoke.py
```

此脚本只适用于 dev，使用本地模拟验证码，创建一个随机演示用户和一笔订单，并检查店主/店员范围以及跨用户查单限制。固定演示账号每次取码需间隔 60 秒。

## 负载实验

安装 k6 后，在服务运行时执行：

```sh
k6 run scripts/load/shops.js
```

可用 `BASE_URL`、`VUS`、`DURATION` 和 `SLO_P95_MS` 调整实验。默认场景是小规模热点店铺查询，仅用于建立测量流程；脚本阈值是实验目标，不是已达成指标。

抢购负载需下一阶段准备多账号登录态、独立活动与初始库存，区分“多个用户争抢”和“一个用户重复请求”。实验后检查订单唯一性与最终库存，不能只看请求数。

每份报告至少记录：提交号、CPU/内存、JDK/Redis/MySQL 版本、数据量、并发模型、持续时间、吞吐、P95/P99、错误率、最终订单/库存、积压和故障恢复结果。

## 当前可靠性边界

- Caffeine 只在本实例更新后失效，其他实例最多保留短 TTL 旧值；跨实例广播和读写竞态仍需要单独验证。
- Redis AOF everysec 不是零丢失保证。当前 Lua 使用单实例键设计，未适配 Redis Cluster 哈希槽。
- 订单消费者在 5 次失败后保留死信证据；未知事务结果保留 PROCESSING，自动对账/人工重放页面待实现。
- 模拟登录和演示数据用于本地开发；支付、核销、退款和经营报表尚未形成完整业务闭环。
