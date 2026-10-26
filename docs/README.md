# 当前进度与开发入口

更新：2026-10-26。用户已确定全面改用 Go 后端，前端保留 Vue 3/TypeScript，数据库保留 MySQL。唯一总路线见 [Go 迁移方案](implementation-plan.md)，实现者先读 [交接说明](agent-handoff.md)。

## 当前事实

- Java 参考基线为 `9f8c2b10839ef74556de0f55d19afc3ac215f9da`，由标签 `java-baseline-20261026` 保留；冻结后不再新增 Java 功能。
- GO-001 已实现 `backend-go/` 配置、真实依赖探测、Go 身份健康接口、HTTP 排空与连接池关闭；隔离启动/失败/关闭六场景自测通过，见 [证据](evidence/2026-10-26-434679c/GO-001/report.md)。业务尚未迁移，不能称为全面迁移完成。
- Java 基线已有交易/B端/业务 AI，社区/Kafka/Feed 尚未实现。这些是 Go 的移植范围与新增范围，不是 Go 已完成功能。
- 最新 Java 修复提交 `e3d938d`、`9568206`、`9f8c2b1` 包含快照/AI 终态修复及窗口脚本，尚未在本聊天独立复核。标签是源码参考点，不是“全部验收通过”标签。
- 本机已观察到 `go1.27.0 windows/amd64` 可调用。数据库、Redis、Kafka、Go 依赖及 CI 在实施时重新验证，不能沿用旧环境记录推断当前健康。

## 产品与交付范围

本地生活交易 + 商家运营 + 探店社区 + 业务 AI，一个后端和一个前端贯通三条流程：

1. 商家发布活动 → 用户抢券/模拟支付 → 店员核销 → 店主审计；取消、超时、退款与恢复可验证。
2. 用户发探店笔记 → 他人关注/点赞 → 稳定翻页的关注动态；事件、缓存、计数可以恢复和测量。
3. 用户问公开规则/查本人订单 → 带出处或实时结果的答复；商家维护 FAQ，停用后旧知识失效。

前后端基础完整性包括登录/退出、商铺搜索分类、列表分页/加载更多、空态/错误/重试、按门店控制管理按钮。保留社区/Kafka/Feed 主要能力；Canal、ES、PostgreSQL/pgvector、压缩计数、分钟热榜、微服务集群不作为成型前置。

## 任务状态（唯一维护入口）

小任务的范围、依赖和门槛见 [方案第 6 节](implementation-plan.md#6-里程碑与细粒度任务)。下表记录状态；未开始的任务不另建重复票据。新任务展开时在此补依赖、交付行为、验收、命令/证据和完整 SHA。

| 任务 | 当前状态 | 下一步 |
| --- | --- | --- |
| GO-000 规范与 Java 冻结 | 已交付规范；13份文档静态核对及独立设计审查通过，随本任务提交 | 本地标签已核对；不改应用或运行中的服务 |
| GO-001 Go 工程、配置、健康、关闭与 CI | 自测通过待核验 | 真实隔离依赖六场景通过；Linux CI/race 配置已添加，远端未执行 |
| GO-010 / 011 登录与商铺 | 待做 | Vue→Go 首条真实链路、响应兼容测试 |
| GO-020 / 021 商家权限与活动 | 待做 | 逐门店范围、幂等发布/关闸、交易 outbox |
| GO-030—033 交易与恢复 | 待做 | 成单/履约/补偿/review/重建；真实窗口回归 |
| GO-040—042 业务 AI | 待做 | FAQ/检索/只读工具/SSE/生命周期；Stub 与真实质量分开 |
| GO-050—053 社区与 Kafka | 待做 | 笔记、关注、DB Feed、可靠事件、期望状态点赞 |
| GO-060—062 缓存/计数/多投影 | 待做 | 批量装配、singleflight、复合游标、热点/位图与重建 |
| GO-070 / 071 证据与正式切换 | 待做 | 持续负载、三流程演示、目录/CI/启动收敛到 Go |

依赖满足且文件归属独立时可并行；G3 稳定后 G4/G5 可分别推进。每个可验收小任务自测后独立 commit，交完整 SHA；核验通过才写“已验收”。核验失败追加修复提交，不改写其他 agent 的历史。

### GO-001 / 工程、配置、健康与关闭

- 依赖：GO-000；开始 SHA `434679cf86aaa125f1f9a35fa695cc7050eed907`。交付 SHA 随本地 commit 在交付记录中列出。
- 交付行为：隔离 MySQL/Redis 启动 Go API；缺配置、依赖不可达或端口冲突明确失败；健康区分 live/ready 并返回 Go 身份；所有 HTTP 退出路径有界排空后关池。前端代理目标可配置，默认 Go 8082。
- 验收：MIG-GO-01；MIG-GO-02 的代理配置部分（业务与真实页面归 GO-010/011）。不涉及业务/SQL 变更、Lua、Stream/outbox 消费。
- 命令与证据：`go mod verify`、`gofmt -l .`、`go vet ./...`、`go test -count=1 ./...`、构建和 `go-bootstrap-smoke.py`；前端 build / Vite 配置 prettier。详见 [报告](evidence/2026-10-26-434679c/GO-001/report.md)。
- 限制：Linux race 和远端 CI 未执行；启动后真实依赖中断恢复尚未执行，ready 失效/超时由局部测试覆盖。独立静态审查不替代完整业务验收。

原 Java 增量开发的 30—50 小时估计已失效，不能用于 Go 重写。GO-010/011 首次真实贯通后按实际移植速度、剩余交易/AI边界与联调量重新估算。不要把模型生成代码时间当成可投递项目交付时间。

## 文档与目录

`project-full-stack/` 是正式仓库，旁边 `references/` 是参考资料，不进入构建。当前 `backend/` 仍是 Java；迁移期间可临时使用 `backend-go/`，验收后正式收敛到 `backend/`，最终不保留两套活跃后端。前端、SQL、Lua 和 HTTP 验收尽量复用。

| 要做什么 | 入口 |
| --- | --- |
| 技术栈、迁移任务、隔离切换、截图能力落点 | [implementation-plan.md](implementation-plan.md) |
| 交给实现 agent、单任务提交/复核规则 | [agent-handoff.md](agent-handoff.md) |
| 交易状态、权限、库存与恢复契约 | [transaction-design.md](transaction-design.md)、[acceptance.md](acceptance.md) |
| 社区/缓存/事件业务要求与验收 ID | [backend-highlights-plan.md](backend-highlights-plan.md)、[backend-highlights-acceptance.md](backend-highlights-acceptance.md) |
| AI 业务范围与验收 | [ai-design.md](ai-design.md)、[ai-acceptance.md](ai-acceptance.md) |
| Windows 环境、接口表和历史启动命令 | [windows-development.md](windows-development.md)、[development.md](development.md) |
| 历史 Java 实测与独立核验 | [evidence](evidence/)、[reviews](reviews/) |

旧报告保留原日期/SHA/语言，不能批量改成 Go。H0—H6 是业务需求/历史报告引用编号，当前执行顺序统一使用 G0—G7 / GO-*，不同时维护两条路线。已有已通过的业务场景转为 Go 回归，不继续按旧计划补 Java。

## 四条亮点与量化边界

本项目定位是一页简历里的一段后端/全栈项目，配合 RAG 实习和另一个 Agent 项目。目标是三条后端 + 一条 AI，以下是完成验收后的写作方向，不是当前完成声明：

1. 高并发交易：Redis Lua 准入、Stream 异步落单、MySQL 条件更新/唯一约束、事务 outbox、持久复核与库存恢复。
2. Feed 查询与缓存：页 ID/公开卡片/计数拆分，批量读取和缺失项批量回源，本地有界缓存 + Redis，singleflight、复合游标、TTL 抖动与轻量热点保护。
3. 可靠社区事件：关系变更与 outbox 同事务，Kafka 至少一次投递，关系版本/幂等消费、两类业务投影、积压恢复与重建。
4. 业务 AI：公开规则 RAG 带来源，本人订单只读查询，知识版本失效、隔离会话、原子去重与有界预算。

Java 历史 20 VU × 10 次、200 账号抢 100 库存得到 100 单、无重复/超卖，是短突发正确性证据。约 0.243 秒的 821.97 req/s 和混合成功/售罄 P95 29.39ms 不是持续容量，更不是 Go 成绩。截图里的 QPS、内存节省与延迟降幅不能沿用。

Go 重新记录稳定负载 RPS/P95/P99、DB 查询数、cold miss 加载次数、成功受理至成单耗时、投影延迟及故障恢复时间，报告硬件/版本/数据量/分布/时长。语言与运行时会影响性能，不能简单认为换 Go 就提高 QPS。

完成标准是三流程可复现、关键权限/竞争/故障通过、真实负载证据和干净环境启动，README 与实物一致。达到后以项目讲解、算法和面试准备为主，不继续为技术栈名单叠组件。
