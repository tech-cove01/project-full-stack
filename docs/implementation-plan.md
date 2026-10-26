# Go 迁移与分阶段交付方案

日期：2026-10-26。用户已明确：项目全面转 Go，后续不再开发 Java 功能；保留完整交易、B 端、社区和业务 AI。原 Java 源码参考基线为 9f8c2b10839ef74556de0f55d19afc3ac215f9da，保留在 Git 标签 java-baseline-20261026。

本文件是当前唯一总方案。此前 Java 阶段计划由 Git 历史保留；[交易设计](transaction-design.md)、[AI 设计](ai-design.md)、[亮点需求](backend-highlights-plan.md) 的业务规则继续有效，其 Java 框架实现描述仅作历史参考。实际状态统一见 [开发入口](README.md)，交接见 [agent-handoff.md](agent-handoff.md)。

## 1. 产品与边界

本地生活交易与商家运营平台，配套探店社区和业务助手，保留 Vue 3/TypeScript 前端。交付三条真实流程：

1. 店主发布活动 → 用户抢券/模拟支付 → 店员核销 → 店主审计；取消、超时、退款、故障恢复可验证。
2. 用户发布探店笔记 → 他人关注/点赞 → 稳定翻页的关注动态；计数、缓存和异步投影有自己的实验。
3. 用户问公开规则/查本人订单 → 来源或实时结果卡片；商家修改/停用知识后旧规则失效。

不改成短视频平台，不做真实支付、商家入驻审核、大 V 推荐、复杂评论/收藏、MCP/多 Agent 平台。Canal、ES、PostgreSQL/pgvector、自定义二进制压缩计数、分钟热榜、微服务/集群均非本轮前置。

## 2. 技术选择

| 部分 | Go 目标 | 约束 |
| --- | --- | --- |
| HTTP | Go + Gin | 模块化单体；一个正式 API；身份放不可变请求上下文 |
| 数据访问 | GORM + MySQL 驱动；必要 SQL 显式书写 | 同一事务统一使用 tx，复杂条件更新/锁定查询不强行 ORM 化 |
| 数据库 | MySQL 8.4 / InnoDB | 沿用表、生成列、约束及版本 SQL，不切 PostgreSQL |
| Redis | go-redis/v9 + Redis 7.4 + 现有 Lua | 缺键和错误分开；脚本参数/返回码/TTL 语义保持 |
| 本地缓存 | 有界 TTL/LRU，例如 hashicorp/golang-lru/v2/expirable | 不照搬 Caffeine 类结构，不自造缓存框架 |
| 回源合并 | golang.org/x/sync/singleflight | 单进程同 key 合并；不宣称跨实例全局去重 |
| 异步 | 订单 Redis Stream；交易 outbox；社区 Kafka | 不引入 RabbitMQ；社区 outbox 不认领交易事件 |
| Kafka 客户端 | kafka-go（实现时锁定兼容版本） | 至少一次投递，消费效果确认后提交 offset，业务版本防重 |
| AI | Go net/http + context + 兼容 Chat/Embedding Provider | 小规模精确向量检索，MySQL 保存知识/向量；规则路由只读工具 |
| 前端 | Vue 3 / TS / Vite / Pinia / Element Plus | 复用页面和请求层，补基础产品交互与社区页面 |
| 验证 | go test / httptest、真实 MySQL/Redis/Kafka、k6、CI | Linux CI 跑 race；Windows 普通构建/测试可先完成，不为 race 先折腾本地 C 工具链 |

本机 Go 命令已可运行，观察版本为 go1.27.0 windows/amd64；不代表新后端已构建。GO-001 锁定 go.mod/go.sum、工具链与 CI，后续不临时追新依赖。不为 RAG 名称强加新框架或 PG；现有规模的规则检索先使用明确过滤和版本约束。

换 Go 不保证 QPS 增加。语言/运行时、SQL、网络往返、连接池、GC、数据分布、缓存命中和一致性工作共同影响结果；Java 历史指标不能写成 Go 成绩。

## 3. 复用与重写

直接保留：Vue 页面和 API 契约、infra/mysql/ 的版本 SQL、8 份 Lua、Compose MySQL/Redis、业务场景与多数 HTTP/Python/k6 脚本。脚本的 Java 路径、端口和依赖假设需要参数化。

重写：Gin 路由/中间件、GORM repository/事务、Stream/outbox worker、角色门店范围、缓存、AI Provider/知识/会话/SSE、Go 自动化测试。Java 单测可提取行为断言，不能把原测试数量移作 Go 通过记录。不逐类翻译旧教程模块，不迁移未承诺的写接口。

原 SQL 迁移继续使用显式版本入口；不以 GORM AutoMigrate 替代表结构管理。生成列 active_flag 只读；同用户同活动的有效订单唯一约束、NULL 语义、token/事件唯一约束和扫描索引都保留。

## 4. 外部与数据契约

- 接口路径、方法、authorization 请求头、Idempotency-Key、HTTP 状态和 Result 的 success/errorMsg/data/total 保持兼容，以 frontend/src/api.ts、types.ts 和现有验收为参照。
- ID 必须是十进制字符串；金额按分传字符串；不得把 int64 JSON 数字直接交给浏览器。null/省略/空数组行为有契约测试。
- 明确 Asia/Shanghai，时间格式与旧接口一致，时区不靠机器默认值。游标使用发布时间与 ID 的确定顺序。
- 用户身份来自服务端登录态；OWNER/MANAGER/STAFF、跨商家混合身份和门店范围逐资源验证，前端按钮按行所属门店判断。
- 订单准入保留 Redis Lua 预占→Stream→MySQL 事务落单；DB 库存/订单/审计/outbox 同事务，事务内部不混用全局 db。DB 变更引发的后续缓存/投影/补偿在提交后执行，由持久 outbox 兜底；不把准入预占挪到 DB 提交后。
- 保留取消后可重买、退款后不可重买、一单一券、支付截止、核销/退款互斥与幂等规则。
- Stream 没有明确持久结果就不 ACK；unknown outcome 保留 pending 或持久 review。review 的 OPEN/REQUEUED/RESOLVED 和受控重放不能缩水。
- 受控重建：先关准入且旧 OPEN 无法重开，再按当前视图核对 Stream、review、PENDING/PROCESSING/NEEDS_REVIEW 补偿。恢复事务显式 READ COMMITTED，并直接使用锁定行库存；回归并发快照窗口。
- Lua 完整预校验后写；错误不自动回滚。沿用 KEYS/ARGV 顺序、返回码、命名与生命周期，缺库存保持关闭。
- Go HTTP 任务传播 context 超时/取消；订单 worker 使用应用生命周期 context，不能随受理 HTTP 结束而消失。获取资源后安排 defer，锁释放核对 token；后台 goroutine 不持有 Gin Context。
- AI 维持 meta/status/sources/tool_result/answer/done/error 事件。所有终态、工具、无命中和缓存重放统一核对身份、会话存活、deadline、锁归属及知识依赖；真实 token 流必须实际实现才宣传。

## 5. 目录与切换

最终仅一个 Go 后端：

~~~text
backend/
  go.mod / go.sum
  cmd/api/                 组装、启动与关闭
  internal/
    auth/ shop/ merchant/ activity/ order/ ai/ community/
    platform/              配置、MySQL/Redis、HTTP通用层
  internal/platform/redis/lua/   可 go:embed 的原脚本
frontend/
infra/mysql/
scripts/
docs/
~~~

迁移期间可临时使用 backend-go/，不持续开发两套业务。Java 冻结在标签，不再加功能；Go 完成等价验收后将正式目录收敛到 backend/，移除主分支跟踪的 Java 源码/pom 与 Java CI，历史由 Git 保存。迁移与删除先检查文件归属，不递归删除未知文件或数据卷。

隔离开发默认可使用 Go API 8082、独立 Compose 项目/测试依赖 3308/6380；现有 Java 8081、MySQL3307/Redis6379 保留参考。实现者选择并记录隔离方式，测试脚本不得硬编码旧端口。两个后端禁止同时消费同一 Stream/group/outbox。

GO-001/010 就将前端代理目标参数化，并记录 Go 服务身份与实际目标，避免页面仍请求 Java 8081。兼容矩阵覆盖字符串 ID/金额、时间、空数组/null、401 事件、分页 total 与 SSE。

正式切换前停止旧 Java 生产者与 worker、核对 pending/outbox/review，再启动 Go 使用正式端点并验证页面。若接管旧数据，验证 SQL 迁移校验和、用户/订单/审计与原支付期限保留、ID 不碰撞、Redis 键/序列化、Stream 字段/group/pending、outbox/review 状态兼容。未决任务须有排空、接管或持久复核去向，不重新初始化库存掩盖差异；切换失败可停 Go 回退基线，新增迁移需保持兼容并说明恢复。Go 未完成前不得声称已迁移、已验收或沿用旧容量数据。

## 6. 里程碑与细粒度任务

G0—G7 是里程碑，不是每个阶段一个大提交。以下任务逐个补交付行为、依赖、验收 ID、命令、原始证据和 SHA；自测后独立 commit，交核验者后再标已验收。

| 里程碑 | 小任务 | 依赖 / 交付与门槛 |
| --- | --- | --- |
| G0 基线与工程 | GO-000 规范与Java冻结；GO-001 Go配置/健康/依赖/关闭/CI基础 | 无；GO-000本轮整理，Go代码尚无。GO-001用隔离真实依赖启动，缺配置明确失败，关闭无泄漏；形成首个可运行Go提交 |
| G1 身份与浏览 | GO-010 登录/刷新/退出；GO-011 商铺列表详情/搜索分类 | G0；真实Vue→Go；字符串ID、登录失效/退出、权限拒绝、分页/空态/重试通过 |
| G2 商家与活动 | GO-020 工作台/门店范围；GO-021 发布/下架/交易outbox | G1；混合角色正确；重复发布不重置库存，关闭后旧事件不重开，审计与业务同事务 |
| G3 交易与恢复 | GO-030 受理→Stream成单；GO-031 支付/取消/超时；GO-032 核销/退款/审计；GO-033 pending/review/补偿/重建 | G2；每票独立业务流程。真实约束、幂等/竞争、缺键/错类型、重放终态、RC并发窗口通过；补三个列表分页 |
| G4 业务AI | GO-040 FAQ/索引版本；GO-041 RAG与本人只读工具/SSE；GO-042 会话/去重/预算/取消与终态保护 | G3；Stub先验证生成中停用/删除/租约/超时/凭据，真实模型小集单独评测，不扩Agent |
| G5 社区与可靠事件 | GO-050 笔记发布详情；GO-051 关注与DB基线Feed；GO-052 社区outbox→Kafka首投影；GO-053 期望状态点赞 | G3；每票均含必要页面/接口/数据与验证。Broker断开可积压补发，回滚无事件；关系版本与幂等有效 |
| G6 缓存、计数、多投影 | GO-060 批量装配/页卡片计数缓存/singleflight；GO-061 TTL抖动/轻量hotkey；GO-062 Lua位图计数/重建与第二投影 | G5；同毫秒翻页无重漏、跨用户不串状态、冷miss回源可测、重复/乱序收敛、失败恢复可重放 |
| G7 验收切换 | GO-070 持续负载/故障/三流程演示；GO-071 目录CI启动与正式端点收敛 | G4+G6及交易回归；干净环境可复现、Go默认且无Java生产依赖、README与四条简历亮点符合实测 |

细依赖至少为：GO-010→001，011→010，020→010，021→020，030→021，031→030，032→031，033→030/031/032；040→033，041→040，042→041；050→033，051→050，052→051，053→050/052，060→051/053，061→060，062→052/053/060；070→042/061/062，071→070。每票开始时补独立交付和验收，必要时拆子票。

GO-033 通过前不开放库存重建写入口；GO-042 通过前 AI 只在隔离 Stub 环境验证或保持关闭。并行只做依赖满足且写入归属不冲突的任务；G4/G5 可在 G3 稳定后分别推进。不要一口气生成所有 Go 代码再联调。

## 7. Feed 与截图亮点如何落地

- 业务数据分成页ID列表、公开笔记卡片、独立计数；当前用户 liked/角色不进入共享卡片。存储上使用有界本地缓存 + Redis，不把数据分类误称为三个物理存储层。
- 收集一页全部ID，批量读缓存，聚合缺失ID后用有限次批量DB查询装配，保留原顺序，不逐条回源。
- singleflight 合并同进程同key加载；相同页及不同用户页面共享卡片都测回源次数，错误可重试，不长期锁死。
- 按 (publishedAt,id) 复合游标翻页；覆盖同毫秒跨三页、并发插入、取关后重置版本/游标。
- 短窗口热点识别、有上限的TTL延长及抖动；不是分布式预测系统。分钟ZSet热榜是可选排行业务，不代替hotkey保护。
- 点赞事实在MySQL，Redis位图/计数为可重建投影；关系变更+outbox同事务，经Kafka至少一次消费并按版本应用，不因截图换成没有可靠投递协议的Redis-first方案。

## 8. 验收、量化与人工审查

复用 [交易验收](acceptance.md)、[AI验收](ai-acceptance.md)、[亮点验收](backend-highlights-acceptance.md) 的业务条件，Java特定命令/类/测试框架由Go适配。本项目不重建第二套规范或初始化OpenSpec。

Go任务至少执行适用的 gofmt、go vet、go test；race在具备环境的CI执行。真实MySQL/Redis/Kafka联调、Stub协议、真实模型质量、持续负载分别标结果，不能互相代替。

人工重点看：事务句柄/提交边界、RowsAffected/生成列、Lua参数、Stream ACK、权限数据范围、JSON大ID、goroutine/context与锁生命周期、所有AI终态校验。关键缺陷先复现失败再修；低影响文档/格式改动不加无意义测试。

量化选择稳定几分钟的RPS/P95/P99与DB查询量、同key冷miss实际加载次数、事件投影延迟/积压追平和库存计数对账。记录硬件/版本/数据量/命中率/负载时长。Java 20VU/200账号短突发、29ms混合响应、822req/s均不得复制成Go容量或提升比例。

当前Java30—50小时估计已经失效。GO-010/011首次真实贯通后重新评估移植剩余量，Go尚未运行时不承诺固定QPS或工期。

## 9. 依据

- [Gin官方开始](https://gin-gonic.com/en/docs/quickstart/)
- [GORM事务](https://gorm.io/docs/transactions.html)：同一业务事务必须使用回调中的tx。
- [go-redis官方指南](https://redis.io/docs/latest/develop/clients/go/)
- [singleflight](https://pkg.go.dev/golang.org/x/sync/singleflight)
- [本地LRU库](https://github.com/hashicorp/golang-lru)
- [Go数据库取消](https://go.dev/doc/database/cancel-operations)
