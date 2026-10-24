# project-full-stack

面向本地生活服务的全栈交易与商家运营平台，连接用户消费、商家经营与平台运营。

项目围绕「活动发布 → 用户抢购 → 异步生成订单 → 到店核销 → 经营分析」构建业务流程，关注突发流量下的订单正确性、清晰的前端状态反馈，以及商家与门店的数据权限。

## 功能进度

| 业务 | 当前实现 |
| --- | --- |
| 用户账号 | 验证码登录、登录态刷新、退出；开发环境提供模拟验证码 |
| 商铺浏览 | 商铺列表、详情、分类与附近商铺接口；前端已接入列表和详情 |
| 优惠活动 | 店主发布/下架、Redis 准入门控与库存预热、活动时间校验 |
| 异步订单 | Redis Stream 消费、事务落库、幂等约束、模拟支付、取消、超时关单、核销、模拟退款与 outbox 补偿 |
| 前端交互 | 抢购防重复提交、订单状态轮询、支付/取消、核销码展示、订单列表与刷新恢复 |
| 商家工作台 | 活动管理、门店订单、核销、模拟退款、操作审计；店主与店员访问范围区分 |
| 并发与恢复 | k6 多账号抢券、同账号重复请求、限流 429、消费者重启与 pending 接管、补偿去重 |
| AI 助手（计划中） | 平台/门店规则问答与来源引用、实时活动与本人订单查询、商家公开 FAQ 维护 |
| 后续业务 | ES 搜索、经营统计；社区内容写入暂未开放 |

当前版本已形成可演示的交易与商家履约闭环：发布活动 → 抢购 → 异步建单 → 模拟支付 → 到店核销，并覆盖取消、超时、退款、权限与审计。AI 助手仍为规划能力；未发布并发容量或吞吐量结论。

## 技术栈

| 层次 | 选型 |
| --- | --- |
| 前端 | Vue 3、TypeScript、Vite、Vue Router、Pinia、Element Plus |
| 后端 | Java 21、Spring Boot 3.5、MyBatis-Plus、Maven |
| 数据与缓存 | MySQL 8.4、Redis 7.4、Caffeine、Redisson |
| 异步处理 | Redis Stream、Lua、数据库事务与业务唯一约束 |
| 验证与运行 | JUnit、Mockito、H2、Docker Compose、GitHub Actions；提供 HTTP 冒烟与 k6 负载脚本 |

采用模块化单体和单个前端工程，用户区与商家工作台共享基础设施。技术决策、改造顺序和验收标准见[开发方案](docs/implementation-plan.md)。

实现任务见[交接说明](docs/agent-handoff.md)，业务规则见[交易设计](docs/transaction-design.md)，完成条件见[验收标准](docs/acceptance.md)。

AI 助手的实现范围与评测方式见 [AI 方案](docs/ai-design.md) 和 [AI 验收](docs/ai-acceptance.md)，当前仍为规划能力。

## 快速开始

需要 JDK 21 或兼容版本、Maven、Node.js 24，以及已启动的 Docker。

Windows 的完整启动步骤见 [Windows 开发说明](docs/windows-development.md)。本机原有 MySQL 占用 3306，运行以下命令前，在启动 Compose 和后端的各个 PowerShell 窗口中设置 `$env:MYSQL_PORT = '3307'`。

在仓库根目录启动 MySQL 和 Redis，首次创建数据库卷时会导入表结构与演示数据：

```sh
docker compose up -d mysql redis
```

应用版本化迁移（已有数据卷也必须执行）：

```sh
$env:MYSQL_PORT = '3307'
node scripts/migrate.mjs
```

启动后端：

```sh
mvn -f backend/pom.xml spring-boot:run
```

启动前端：

```sh
npm --prefix frontend ci
npm --prefix frontend run dev
```

前端地址以终端输出为准，默认为 `http://localhost:5173`；后端默认监听 `127.0.0.1:8081`，由前端开发服务器代理 API 请求。

默认 `dev` 环境会返回随机演示验证码，登录页可直接填入。演示店主手机号为 `13800000000`，可见两家门店；演示店员手机号为 `13900000000`，仅可见一家授权门店。验证码接口每个手机号有 60 秒发送间隔。

`.env.example` 列出本地数据库配置。Docker Compose 会读取根目录的 `.env`；自行修改连接参数时，也需要为后端设置对应环境变量。共享部署应显式使用 `prod` 环境，配置自己的认证与短信服务；当前模拟验证码仅用于本地开发。

## 验证

```sh
node scripts/migrate.mjs
mvn -f backend/pom.xml verify
npm --prefix frontend run build
python scripts/smoke.py
```

HTTP 冒烟脚本需要已运行的开发环境，会验证商铺、登录、商家门店范围及异步订单查询，并创建一笔演示订单。P0/P1/P2 的真实联调、页面演示、并发与恢复证据见 [docs/evidence](docs/evidence)。k6 脚本及结果记录方式见[运行与验证](docs/development.md)。

本机开发对比结果：200 个不同账号争抢 100 库存得到 100 笔订单、0 超卖、0 重复；同账号 20 并发只产生 1 笔订单。该数字来自单机短突发压测，不是生产容量承诺。

## 目录

```text
backend/                 后端服务与测试
frontend/                用户端与商家工作台
infra/mysql/             表结构与演示数据
scripts/                 HTTP 冒烟与负载脚本
docs/                    改造方案、接口约定与运行说明
compose.yaml             本地依赖服务
```

## 许可证

项目原创内容采用 [MIT License](LICENSE)。复用来源及适用范围见[第三方来源说明](THIRD_PARTY_NOTICES.md)。
