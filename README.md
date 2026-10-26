# project-full-stack

面向本地生活服务的全栈平台，连接用户消费、商家经营与探店分享。围绕活动发布、抢券、模拟支付、到店核销和审计构建交易流程，配套商家工作台与带来源的业务 AI 助手。

**开发状态：后端正在迁移到 Go，前端保留 Vue 3/TypeScript。** 当前源码仍为 Java 基线，Go 服务尚未实现；交易、B 端和 AI 作为迁移契约保留，社区/Feed/Kafka 属于新增范围。具体任务与已验证状态见 [开发入口](docs/README.md)。

## 产品能力

| 模块 | 目标体验与当前基线 |
| --- | --- |
| 用户与商铺 | 验证码登录、商铺列表/详情；补齐搜索、分类与分页交互 |
| 活动与订单 | 商家发布/下架、抢券异步成单、模拟支付、取消/超时、核销、模拟退款；Java 已有流程，Go 待移植 |
| 商家工作台 | OWNER/MANAGER/STAFF 门店范围、活动与订单管理、操作审计；Go 待移植并回归混合角色 |
| 业务 AI | 公开规则检索与来源、本人订单等只读查询、隔离会话、FAQ 发布/停用与 SSE；Java 有 MVP，Go 待移植 |
| 探店社区 | 笔记发布、关注/点赞、复合游标 Feed；待实现 |
| 可靠性与性能 | 交易幂等/补偿/review、社区 outbox/Kafka、批量缓存/singleflight；Go 需重新联调和测量 |

模拟支付与退款不接真实资金通道。AI 没有交易写工具，实时库存和订单由服务端按当前身份查询。

## 目标技术栈

| 层次 | 选型 |
| --- | --- |
| 前端 | Vue 3、TypeScript、Vite、Vue Router、Pinia、Element Plus |
| 后端 | Go、Gin、GORM、go-redis/v9 |
| 数据与缓存 | MySQL 8.4、Redis 7.4、有界本地 TTL/LRU、singleflight、Lua |
| 异步 | 订单 Redis Stream、事务 outbox；社区 Kafka |
| AI | 兼容 Chat/Embedding API、小规模精确向量检索、MySQL 知识持久化 |
| 验证与运行 | go test/httptest、k6、Docker Compose、GitHub Actions |

采用模块化单体。数据库保留 MySQL；PostgreSQL、ES、Canal 不作为当前交付前置。依赖和客户端兼容版本在相应 Go 实现中锁定。

## 开发与验证

当前可复用前端、数据库迁移、Lua 和 HTTP 验收脚本。Go 工程启动命令将在首个运行任务完成后补充；请勿将 Java 历史测试或吞吐数字当成 Go 结果。

- [迁移与分阶段方案](docs/implementation-plan.md)：Go 任务、契约、隔离与切换。
- [实现交接](docs/agent-handoff.md)：小任务、自验、独立 commit 与审查。
- [Windows 开发说明](docs/windows-development.md)：当前路线与历史环境记录。
- [运行与接口](docs/development.md)：复用接口及历史 Java 启动方法。
- [交易验收](docs/acceptance.md)、[社区亮点验收](docs/backend-highlights-acceptance.md)、[AI 验收](docs/ai-acceptance.md)。

Java 参考源码保存在 Git 标签 `java-baseline-20261026`，历史证据位于 [docs/evidence](docs/evidence) 与 [docs/reviews](docs/reviews)。Go 迁移后重新验证权限、事务、故障恢复与持续负载，测量条件和限制随结果记录。

## 目录

~~~text
backend/                 当前 Java；Go 验收后收敛到此目录
backend-go/              迁移期间的临时 Go 路径（尚未创建）
frontend/                用户端与商家工作台
infra/mysql/             表结构、演示数据与版本迁移
scripts/                 HTTP 冒烟、恢复与负载脚本
docs/                    设计、任务、验收与证据
compose.yaml             本地依赖服务
~~~

## 许可证

项目原创内容采用 [MIT License](LICENSE)。复用来源及适用范围见 [第三方来源说明](THIRD_PARTY_NOTICES.md)。
