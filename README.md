# project-full-stack

面向本地生活服务的全栈平台，连接用户消费、商家经营与探店分享。围绕活动发布、抢券、模拟支付、到店核销和审计构建交易流程，配套商家工作台与带来源的业务 AI 助手。

后端使用 Go，前端使用 Vue 3/TypeScript。用户、商家和内容社区围绕同一套门店业务协作：完成消费与核销，分享探店笔记，并查询公开规则和本人实时订单。具体交付与核验范围见 [工作流与状态](STATUS.md)。

## 产品能力

| 模块 | 目标体验与当前基线 |
| --- | --- |
| 用户与商铺 | 登录、身份恢复与退出，商铺搜索、分类、分页、详情和公开优惠 |
| 活动与订单 | 幂等发布与下架，异步抢券成单、模拟支付、取消、超时关闭、核销和退款 |
| 商家工作台 | OWNER/MANAGER/STAFF 逐门店权限，活动与订单管理、核销、退款和审计查询 |
| 业务 AI | 公开规则检索与来源、本人订单只读查询、隔离会话、FAQ 版本维护和 SSE；支持兼容 Chat/Embedding，有限集证据与协议已按范围核验，真实 AI 页面待补，默认关闭 |
| 探店社区 | 笔记发布、期望状态点赞、关注关系与复合游标 Feed；公开笔记全文搜索、门店筛选和分页，搜索新增范围自测通过待核验 |
| 可靠性与性能 | 事务 outbox、补偿与受控重建，社区 Kafka 投递与关注/Feed目录消费投影、持久失败重放，批量计数缓存与单进程回源合并；持续容量仍待测量 |

模拟支付与退款不接真实资金通道。AI 没有交易写工具，实时库存和订单由服务端按当前身份查询。

## 目标技术栈

| 层次 | 选型 |
| --- | --- |
| 前端 | Vue 3、TypeScript、Vite、Vue Router、Pinia、Element Plus |
| 后端 | Go、Gin、GORM、go-redis/v9 |
| 数据与缓存 | MySQL 8.4、Redis 7.4、有界本地 TTL 缓存、并发回源合并、Lua |
| 异步 | 订单 Redis Stream、事务 outbox；社区 Kafka 至少一次投递与独立关注/Feed目录/搜索消费组 |
| 搜索 | Elasticsearch 8.19.23、内置 CJK、PIT 分页；MySQL 当前公开事实复核、版本同步与持久失败重放 |
| AI | Go net/http 兼容 Chat/Embedding 与本地 stub、小规模精确向量检索、MySQL 知识持久化、向量空间指纹与版本失效 |
| 验证与运行 | go test/httptest、k6、Docker Compose、GitHub Actions |

采用模块化单体，MySQL 为业务事实源。公开笔记搜索 GO-076 已自测通过，待独立核验；Provider 实现/协议及原始有限集证据已按记录范围核验，真实 AI 页面与持续容量仍待完成。具体提交、证据和限制见 [STATUS](STATUS.md)。

## 开发与验证

当前可复用前端、数据库迁移、Lua 和 HTTP 验收脚本。[Go 启动步骤](docs/development.md#go-迁移环境与启动) 使用隔离 Compose、API 8082 / MySQL 3308 / Redis 6380；前端代理默认指向 Go。请勿将 Java 历史测试或吞吐数字当成 Go 结果。

- [迁移与分阶段方案](docs/implementation-plan.md)：Go 任务、契约、隔离与切换。
- [实现交接](docs/agent-handoff.md)：小任务、自验、独立 commit 与审查。
- [Windows 开发说明](docs/windows-development.md)：当前路线与历史环境记录。
- [运行与接口](docs/development.md)：复用接口及历史 Java 启动方法。
- [交易验收](docs/acceptance.md)、[社区亮点验收](docs/backend-highlights-acceptance.md)、[AI 验收](docs/ai-acceptance.md)。

Java 参考源码保存在 Git 标签 `java-baseline-20261026`，历史证据位于 [docs/evidence](docs/evidence) 与 [docs/reviews](docs/reviews)。Go 迁移后重新验证权限、事务、故障恢复与持续负载，测量条件和限制随结果记录。

## 目录

~~~text
archive/backend-java-20261026/ 冻结的 Java 基线源码（标签 java-baseline-20261026），不再参与构建
backend/              Go 服务；目录收敛后占用 backend/
frontend/                用户端与商家工作台
infra/mysql/             表结构、演示数据与版本迁移
scripts/                 HTTP 冒烟、恢复与负载脚本
docs/                    设计、任务、验收与证据
compose.yaml             本地依赖服务
compose.go.yaml          Go 迁移隔离依赖（独立项目与数据卷）
~~~

## 许可证

项目原创内容采用 [MIT License](LICENSE)。复用来源及适用范围见 [第三方来源说明](THIRD_PARTY_NOTICES.md)。
