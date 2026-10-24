# project-full-stack

面向本地生活服务的全栈交易与商家运营平台，连接用户消费、商家经营与平台运营。

项目围绕「活动发布 → 用户抢购 → 异步生成订单 → 到店核销 → 经营分析」构建业务流程，关注突发流量下的订单正确性、清晰的前端状态反馈，以及商家与门店的数据权限。

## 功能进度

| 业务 | 当前实现 |
| --- | --- |
| 用户账号 | 验证码登录、登录态刷新、退出；开发环境提供模拟验证码 |
| 商铺浏览 | 商铺列表、详情、分类与附近商铺接口；前端已接入列表和详情 |
| 优惠活动 | 优惠券展示、活动时间校验、Lua 库存预扣与防重复领取 |
| 异步订单 | Redis Stream 消费、事务落库、幂等约束、本人订单状态查询、有限重试与异常消息留存 |
| 前端交互 | 抢购防重复提交、订单状态轮询、停止查询与刷新恢复 |
| 商家工作台 | 已登录用户的商家角色、授权门店查询；店主与店员访问范围区分 |
| 后续业务 | 商家活动发布、核销、模拟支付、取消与退款、操作审计、经营统计 |

当前版本是可继续开发的基础版本。商家活动写入、核销和售后流程仍在建设中；社区内容写入暂未开放；未发布并发容量或吞吐量结论。

## 技术栈

| 层次 | 选型 |
| --- | --- |
| 前端 | Vue 3、TypeScript、Vite、Vue Router、Pinia、Element Plus |
| 后端 | Java 21、Spring Boot 3.5、MyBatis-Plus、Maven |
| 数据与缓存 | MySQL 8.4、Redis 7.4、Caffeine、Redisson |
| 异步处理 | Redis Stream、Lua、数据库事务与业务唯一约束 |
| 验证与运行 | JUnit、Mockito、H2、Docker Compose、GitHub Actions；提供 HTTP 冒烟与 k6 负载脚本 |

采用模块化单体和单个前端工程，用户区与商家工作台共享基础设施。技术决策、改造顺序和验收标准见[开发方案](docs/implementation-plan.md)。

## 快速开始

需要 JDK 21 或兼容版本、Maven、Node.js 24，以及已启动的 Docker。

在仓库根目录启动 MySQL 和 Redis，首次创建数据库卷时会导入表结构与演示数据：

```sh
docker compose up -d mysql redis
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
mvn -f backend/pom.xml verify
npm --prefix frontend run build
python scripts/smoke.py
```

HTTP 冒烟脚本需要已运行的开发环境，会验证商铺、登录、商家门店范围及异步订单查询，并创建一笔演示订单。k6 脚本及结果记录方式见[运行与验证](docs/development.md)。

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
