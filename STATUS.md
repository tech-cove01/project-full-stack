# 工作流与任务状态

更新：2026-10-28。本文件是**唯一状态入口**：任务状态、依赖、门槛、提交与核验流程都记在这里；`docs/README.md` 只保留产品范围与各票的详细记录。

## 本轮确认的四条主线与完成边界（2026-10-28）

用户已确认补齐以下四条主线，形成可投递的完整项目；这不是四条能力已经全部完成的声明。具体设计和验收见 [总方案第9节](docs/implementation-plan.md#9-简历版本范围与结束条件2026-10-28)。

| 主线 | 本轮交付边界 | 当前差距 |
| --- | --- | --- |
| 高并发交易 | Lua准入、Stream异步落单、MySQL事务与幂等，取消/退款、补偿和受控恢复 | 已有正确性/故障证据；GO-073补持续负载和成单对账，不把快速售罄响应算成订单吞吐 |
| 热点查询优化 | 批量装配、计数缓存、有界L1、singleflight、复合游标 | GO-073补冷/热对照、真实DB加载次数与延迟；不要求补页ID/卡片缓存、动态hotkey或分钟热榜 |
| 可靠事件与搜索 | Outbox → Kafka消费投影，加公开笔记ES全文搜索与页面 | 保留GO-072现有follow/feed范围；新增GO-076独立消费组与索引同步，当前均不算已验收 |
| 业务AI | 公开知识检索附来源、本人只读工具、权限与版本失效、SSE/取消 | GO-075实现真实Chat/Embedding适配并完成小集评测；stub通过不等于真实模型质量通过 |

前后端完整性继续覆盖登录、商铺浏览、活动、订单、商家工作台/核销/退款/审计、社区与AI页面。B端逐门店授权和旧Token撤权作为业务与权限证据，不额外拆一套产品。

**功能结束条件**：GO-072/075/076的上述范围实现并独立核验；GO-073在最终功能提交上完成持续负载、故障恢复与数据对账；GO-074补齐关键B端角色页面和搜索/真实AI流程；干净环境初始化/迁移/启动可复现，README、限制和四条亮点均有对应源码及证据。通过后冻结本轮功能，转向源码讲解、八股、算法和简历；只修影响既定行为的问题，不再因技术名词继续扩展范围。

完成不要求固定QPS数字、生产部署、旧Java目录删除、复杂推荐、微服务、Canal/ELK或更换数据库。Go实际指标据实记录。八股和算法现在即可并行开始，无需等功能冻结。

## 工作流

1. 一个小任务自测通过 → 独立 commit → 把完整 SHA 交给核验方；核验失败追加修复提交，不改写其他 agent 的历史。
2. 一个独立问题一次 commit（用户2026-10-28再次明确），只带该问题必要的代码、测试和说明；不同缺陷、无关格式化与报告分别提交，不攒整轮修复。
3. 交付前至少跑：`gofmt -l`、`go vet ./...`、`go test -count=1 ./...`、`go build ./...`；前端改动再加 `npm --prefix frontend run build` 与 `run format:check`。
4. 真实依赖相关的票必须给出实跑证据；不能实跑的写进“限制”，不能标“已验收”。
5. 只有核验方给出独立报告后，状态才从“自测通过待核验”改为“已核验”。

## 任务状态

小任务的范围与门槛见 [docs/implementation-plan.md](docs/implementation-plan.md) 第 6 节。未开始的任务不另建重复票据。

| 任务 | 当前状态 | 说明 |
| --- | --- | --- |
| GO-000 规范与 Java 冻结 | 已交付 | 13 份文档静态核对与独立设计审查通过；标签 `java-baseline-20261026` |
| GO-001 Go 工程、配置、健康、关闭与 CI | 已核验（本票运行范围） | 独立重跑启动/关闭 6 场景；Linux race/远端 CI 未执行 |
| GO-010 登录、身份恢复与退出 | 已核验（身份子集） | 独立真实集成与 Vue 12 项通过 |
| GO-011 商铺列表详情/搜索分类 | 已核验（浏览子集） | 独立真实集成、UTC/上海页面各 14 项通过 |
| GO-011-QA1 UI 夹具会话时区 | 已核验（夹具时区范围） | UTC 会话负对照与修复通过 |
| GO-020 门店权限/工作台 | 已核验（只读范围） | 真实 11 组、Vue 28 项；[报告](docs/reviews/2026-10-26/go020-bb5d985/report.md) |
| GO-021 活动发布/下架 | 已核验（本票范围） | 真实 25 组、两时区各 29 项；[报告](docs/reviews/2026-10-26/go021-e410c71/report.md) |
| GO-030 受理→Stream 成单 | 已核验（含 QA 修复） | 真实集成、两时区各 30+9 项；[报告](docs/reviews/2026-10-26/go030-0072c86/report.md) |
| GO-031 支付/取消/超时 | 已核验（当前交易范围） | 真实期限/竞争/扫描与两时区生命周期各26项通过 |
| GO-032 核销/退款/审计 | 已核验（含QA修复） | 事务内锁定授权、核销/退款并发和审计/outbox对账通过 |
| GO-033 补偿/review/重建 | 已核验（受控恢复范围） | 拒绝补偿、Lua预检/归属、review重放、重建部分成功重试及无group门槛通过；灾难数据丢失不在此结论内 |
| GO-040 FAQ/索引版本 | 已核验（含008迁移） | 公开标题/正文版本冻结、店员只读/管理编辑、审计与发布停用重建通过 |
| GO-041 检索/只读工具/SSE | 已核验（stub协议范围） | 来源/工具DTO、实时活动SQL、本人范围与页面通过；真实provider尚未实现 |
| GO-042 去重/预算/终态 | 已核验（stub协议范围） | 身份/知识失效/租约/会话/取消/重放及用户逻辑预算通过；非全局付费预算 |
| GO-050 笔记发布/详情 | 已核验（含QA修复） | 笔记与NOTE_PUBLISHED同事务，真实页面发布/详情通过 |
| GO-051 关注与 DB Feed | 已核验（基础范围） | 期望状态幂等、初次no-op、现有复合游标与批量装配通过；大数据游标压力仍未测 |
| GO-052 社区 outbox → Kafka | 已核验（投递侧） | 真实broker投递/重试、耗尽租约不堵队列；不含GO-072消费投影 |
| GO-053 期望状态点赞 | 已核验（含QA修复） | MySQL事实优先、幂等/并发/初次no-op及本人页面状态通过 |
| GO-060 页卡片批量装配与计数缓存 | 已核验（已实现子集） | 公共计数缓存/批量SQL、回源合并、旧加载隔离与独立取消；页ID/卡片缓存未实现 |
| GO-061 热点保护 | 已核验（固定L1子集） | 有界2秒本地缓存/TTL抖动通过；无动态hotkey检测或TTL伸缩 |
| GO-062 点赞计数投影 | 已核验（版本化集合子集） | 完整性与用户版本、零值、重建行锁和recount通过；非Kafka多投影 |
| GO-070 验收脚本与故障演练 | 已核验（演示/正确性范围） | 三流程22项、短突发9项、Redis停机6项、MySQL消费故障7项；非持续容量 |
| GO-071 目录/CI/启动收敛 | 已核验（当前目录与CI） | backend/为Go，真实依赖/Linux race/格式/启动通过；旧数据正式接管与Java删除未执行 |
| GO-072 Kafka 消费端投影 | 自测通过待独立核验 | follow/feed独立group、持久去重/版本/停车重放与查询回退已分步提交；真实Kafka6组、仓储9/恢复5、查询20子组和HTTP贯通通过 |
| GO-073 持续负载与量化 | 待做 | 最终功能提交上用k6测稳定负载RPS/P95/P99、有效成单率与延迟、冷miss实际加载次数、Kafka/ES延迟与积压追平、库存计数对账 |
| GO-074 页面级浏览器证据 | 部分完成 | 本轮两时区交易各26项、社区/FAQ/AI各10项；补关键B端核销退款/越权拒绝、GO-076搜索及GO-075真实AI流程，复用已有有效证据 |
| GO-075 真实Provider与小集评测 | 自测通过待独立核验 | 真实Chat/Embedding与配置接入、向量身份/完整性、百炼30题和HTTP取消/超时/撤权/恢复已分步提交；真实30题12/6/6/6逐题复核通过，交易隔离三种配置各9项；[报告](docs/evidence/2026-10-28-fb37af5/GO-075/report.md) |
| GO-076 公开笔记全文搜索 | 进行中 | 从290cd60继续：a ES公开搜索/API与页面、b独立Kafka组/持久恢复、c故障与重建分别自测审查提交；使用独立MySQL3332/Redis6404/Kafka9099/ES9202，保持GO-072/075待核验状态 |

### GO-053 交付记录

- 依赖 GO-050/052；提交：后端 `b7e9717ce8af381315087721dd174b5fb91e4aa8`；页面 `3e99ce98d4ceccb3a214260101335dbac4ac25f9`。
- 行为：`PUT /notes/:id/like` 为期望状态，先锁笔记行再改事实与计数，重复状态不改版本、不发事件；计数用 `GREATEST(0, ...)` 保底，每次真实变化写一条 `LIKE_CHANGED`。`GET /notes/:id/like` 返回本人状态、版本与公开计数；未发布与不存在同为 404。
- 验证：真实 MySQL 通过 —— like/unlike 幂等、10 并发点赞收敛为 1 条 active 与 1 条事件、未知笔记拒绝；前端构建与格式通过。
- 限制：点赞计数仍写在 `tb_blog.liked`，位图与重建归 GO-062；未做点赞列表。

### GO-060 交付记录

- 依赖 GO-051/053；提交：批量装配 `e67e28081973821a01c5cee0dba7fe15c5a4f419`；计数缓存 `f521831acfadb27cce19f65c7876c64816b0cd2f`。
- 行为：Feed 页一次 JOIN 取门店名、一次 IN 查本人点赞，卡片带 `shopId/shopName/likedByMe`；计数走 `community:counts:<id>` 缓存，页面用一次 MGET + 一次 IN 回源，详情用单键 singleflight，TTL 60s + 0–15% 抖动，点赞后删除键。缓存读写失败只降级，不影响请求。
- 验证：真实 Redis/MySQL 通过 —— 冷读回源、直接改库后仍命中旧值、失效后重建、20 并发读一致、批量读忽略未知笔记；Feed 集成测试断言相邻卡片不串门店/点赞字段。
- 限制：TTL 抖动已实现，热点保护与位图计数归 GO-061/062；缓存只覆盖 `liked/comments`，不含关注数。

### GO-061 / GO-062 交付记录

- 依赖 GO-060；提交：热点本地层 `031d97cca906267c55e561eefd966064f53ddc97`；位图与重建 `54fc5e8a1fef779e55f3c7701f508cbcf6d58be4`。
- 行为：计数缓存前加 2 秒进程内副本（上限 4096 条，失效时两层同时清除），热点笔记不再每次打 Redis；点赞位图按平台用户 id 置位，事务提交后更新、状态读取优先位图，超出 32 位偏移的 id 跳过；`cmd/recount <noteId>` 从 `tb_blog_like` 用一个流水线重建位图，计数与事实不符时以非零退出报错。
- 验证：真实 Redis/MySQL 通过 —— 删除 Redis 键后本地层仍能命中、过期后回源重建；位图幂等置位、取消、重建替换旧位、越界 id 跳过；点赞后位图与状态读取一致。
- 限制：位图无 TTL，靠 `recount` 修复而非过期；重建只支持单个笔记；`tb_blog.liked` 仍是并行计数列，未做统一对账入口。

### GO-070 交付记录

- 依赖 GO-053/062；提交：演示脚本 `93dea4a`；缺陷修复 `fix(GO-033)`（核销/退款事件补齐补偿字段）。
- 行为：`scripts/go-flow-demo.mjs <trade|community|ai|all>` 用真实 MySQL/Redis 跑通三条流程 —— 交易：发活动、等 gate ready、抢券、成单、支付、拿核销码、店员核销、审计可见 `ORDER_REDEEMED`；社区：发笔记、点赞、关注、关注动态出现该笔记且 `likedByMe` 正确；AI：建 FAQ 草稿、发布、开会话、SSE 依次给出 meta/sources/answer/done。脚本只删自己插入的行，从不 truncate。
- 验证：本机 22 项检查全过、清理无残留；证据 `docs/evidence/2026-10-27-go070/go-flow-demo-result.json`。
- 缺陷：写脚本时发现核销/退款事件缺 `voucherId/userId`，补偿 worker 无法解析会停在 `NEEDS_REVIEW`（退款将无法回补 Redis 库存），已修复并加回归测试。
- 追加：并发抢券脚本 `go-load-seckill.mjs`（库存 5、并发 30 → 受理 5、拒绝 25，数据库与 Redis 库存一致）和 Redis 故障脚本 `go-fault-redis.mjs`（停容器 → 请求 503 且无订单写入 → 恢复后重试被受理并恰好一单）。隔离环境下分别 9 项、6 项检查全过，证据在 `docs/evidence/`。
- 环境结论：之前偶发的“活动不就绪”不是 Go 缺陷，而是 Java 基线实例与 Go API 共用同一 MySQL/Redis，两套 worker 互相消费 outbox 事件，Java 写入的 gate 缺少 `phase` 字段被 Go 脚本拒绝；改用隔离环境后不再出现。
- 限制：浏览器页面证据未包含；MySQL 停机下的受理/复核/重放窗口未做（Redis 停机已覆盖）；持续压测与容量结论未做。

## 门槛（未满足前不开放对应能力）

- GO-033 通过前：不开放库存重建写入口。
- GO-042 通过前：AI 只在 stub 环境验证或保持关闭。
- GO-062 通过前：不把位图计数当作对账依据。

2026-10-28 核验补充：GO-033受控恢复、GO-042 stub协议与GO-062版本集合已通过上述实际范围；MySQL事实仍是计数依据。外部模型/跨实例一致性/数据灾难恢复不得据此开放或宣传。详细修复、命令、失败证据、迁移与限制见[本轮报告](docs/reviews/2026-10-28/batch-3583818/report.md)。下方旧交付记录是实现时自测快照，不覆盖本次结论。

## 横切未完成项

- 商铺详情未接 Redis/L1 缓存；`GET /shop/of/type` 附近坐标查询仍是明确 400。
- 交易/商家/AI/社区的大量 Java 旧路由在 Go 下仍是 404，旧写入口保持 403；已逐个核对前端 13 个视图都有对应 Go 路由，没有悬空入口。
- 前端已接 Go：商家订单/核销/退款/审计、商家活动、商家知识库（含重建）、AI 助手、笔记/关注/Feed/点赞；仍缺页面级浏览器证据与运维入口。
- Linux `-race` 与远端 CI：已在 GitHub Actions 执行（2026-10-28 首次全绿，含 `-race`、真实 Kafka、真实 MySQL/Redis 集成）。
- Java 基线已修的并发窗口（review 快照、锁定行、AI 终态）需要在 Go 对应票里重新取证。

## 复跑命令

```bash
# 后端
cd backend && gofmt -l . && go vet ./... && go test -count=1 ./... && go build ./...

# 前端
npm --prefix frontend run build && npm --prefix frontend run format:check

# 真实依赖（本机隔离实例）
$env:MYSQL_PORT='3308'; $env:GO_INTEGRATION='1' # 使用下方Go专用完整配置，先停API消费者
$env:KAFKA_BROKERS='127.0.0.1:9092'
docker compose -f compose.go.yaml --profile kafka up -d kafka
go test -count=1 -p 1 -run 'TestIntegration' ./...
```

### GO-071 交付记录

- 依赖 GO-070；提交：CI 覆盖 `ci(GO-071)`；Java 归档与文档 `refactor(GO-071)`；目录收敛 `refactor(GO-071)`。
- 行为：CI 新增社区/AI/知识库/计数缓存与商家订单/补偿的真实依赖测试，并新增带单节点 KRaft broker 的 `community-kafka` 作业，社区事件投递不再只靠核验轮；Java 源码以 rename 方式移到 `archive/backend-java-20261026/`，Java CI 只守护归档构建；Go 服务目录由 `backend-go/` 收敛为 `backend/`，CI、脚本与文档路径同步更新（历史核验报告与证据文件保留当时的路径原样）。
- 验证：改名后 `gofmt/vet/go test/go build` 与前端构建、格式检查全部通过，三流程演示脚本复跑 22 项全过；CI YAML 解析通过。
- 注意：核验方请改用 `backend/` 路径；`docs/reviews/` 与 `docs/evidence/` 中的 `backend-go` 字样是历史记录，未做改写。
### 远端 CI（2026-10-28 首次全绿）

- `backend-go`：编译、`gofmt`、`go vet`、`go test -race ./...`、真实 MySQL/Redis 集成（认证、商铺、商家、活动、订单、社区、AI、知识库、计数缓存）、启动冒烟 —— 全绿。
- `community-kafka`：新增作业，启动单节点 KRaft broker，社区事件投递集成测试通过。
- `backend`：归档后的 Java 基线守护构建通过；`frontend`：构建与格式检查通过。
- 本轮在 CI 上暴露并修掉三个真实缺陷：目录改名导致两个 job 同名使工作流被 GitHub 拒绝；发布笔记后跨连接读 `LAST_INSERT_ID()` 导致详情 404；雪花 user id 无法作为位图偏移使点赞投影始终为空。

## 隔离环境（Go 专用，务必用它复跑）

```bash
docker compose -f compose.go.yaml up -d mysql redis
$env:MYSQL_HOST='127.0.0.1'; $env:MYSQL_PORT='3308'
$env:MYSQL_DATABASE='project_full_stack_go'
$env:MYSQL_USER='fullstack'; $env:MYSQL_PASSWORD='fullstack_go_dev_only'
$env:REDIS_HOST='127.0.0.1'; $env:REDIS_PORT='6380'
node scripts/migrate.mjs

cd backend && $env:GO_INTEGRATION='1' && go test -count=1 -run 'TestIntegration' ./...

node scripts/go-flow-demo.mjs all      # 交易 / 社区 / AI 三流程
node scripts/go-load-seckill.mjs 5 30   # 并发抢券不超卖
node scripts/go-fault-redis.mjs         # Redis 停机 → 干净拒绝 → 恢复
```

Kafka 需要时：`docker compose -f compose.go.yaml --profile kafka up -d kafka`，并设置 `KAFKA_BROKERS=127.0.0.1:9092`。

> 混跑提醒：Java 基线实例（8081）与 Go API（8082）若共用同一个 MySQL/Redis，两套 worker 会互相消费对方的 outbox 事件。Java 的活动脚本写入不带 `phase` 的 gate，Go 的 gate 脚本按“缺少 phase 即拒绝”处理，表现为活动迟迟不就绪。这不是 Go 的缺陷，但会让演练结果失真，因此复跑请始终使用上面的隔离环境。
### 2026-10-28 剩余执行顺序（按本轮确认范围）

1. **GO-072**follow/feed消费投影已自测并小提交，交独立核验；不插入ES改写既有提交，核验发现问题继续一问题一提交。
2. **GO-075**真实Provider、小集评测和HTTP生命周期已自测并小提交，交独立核验。用户授权复用CRM本机已验证的加密百炼凭据，仅内存使用；79次实际尝试、7673已知tokens、2次取消/超时usage未知，费用边界见报告，不以stub替代真实结论。
3. **GO-076**在GO-072事件契约稳定后，交付公开笔记搜索与Kafka→ES同步；只增加这一类搜索资源，不扩商铺搜索、向量检索、推荐或新消息中间件。
4. **GO-074**随功能补页面证据，覆盖核心B端授权、搜索与真实AI；既有两时区页面证据按源码变化做差量复核。
5. **GO-073**在上述功能固定后完成持续压测、恢复和对账。先分析瓶颈，再做有对照的必要优化；不为凑漂亮数字继续扩功能或照搬Java的822 req/s、29 ms。
6. **冻结与交接**：各票按实际范围独立核验，README/限制/启动说明与四条主线一致，记录最终SHA和证据。旧Java源码删除、接管旧生产数据均不作为本轮完成前置；继续保留归档与回退依据。

GO-031—GO-071的15票已按顶部范围核验；GO-060/061/062只代表实际子集。页ID/卡片缓存、动态hotkey等早期扩展转为可选后续，不阻塞本轮结束。

## 已核验票的证据

### GO-072 / 交付与待核验范围

- 起点 `8c3fb0e0fe1169ad98e19faaf7ae49f907362530`，依赖已核验GO-050/051/052；状态仅自测通过待独立核验。[总报告/命令/失败与限制](docs/evidence/2026-10-28-8c3fb0e/GO-072/report.md)。未覆盖上一轮已核验范围或运行其它项目测试。
- 小提交：a持久投影 `c90c3e57b9ec59eadc80c3e1fbd7f3b5181a1af1`；b停车/重放/重建 `942034a0c43a058486bc34a07a58746cb2b4d338`；c查询 `b070bb94b64b8fb64d13cddf5a4e668436f03115`；d消费者/组装 `d4c81ec56a02cb98e0dd71ec54fc02eb9f83e5a9`；e CI `c0f282dc5501ad4ca3b67cf2a18e53ebde4f2c29`；f贯通 `acd26af3b1b0f8c2053d868993ad7fa75157302c`。文档收尾另提交，不改写历史。
- 两类有实际用途的投影：关注正反关系索引、公开笔记Feed候选目录。采用作者目录+实时查询，不建立每用户fan-out队列，不宣称已实现fan-out恢复；本人关系与公开状态继续由MySQL事实守护，历史无事件/部分投影回退数据库。
- 消费效果与eventId去重同事务，旧version不能覆盖新状态；无持久结果不提交offset。失败持久可见并支持精确重放，不删除共享topic/group、不做付费模型或持续负载。
- 真实仓储9/恢复5/Kafka6主组、查询1入口20子组、HTTP→outbox→Kafka→两组→实际查询及启动关闭6场景通过；全套普通Go/gofmt/vet/build通过。迁移009—011首次与重复校验通过，初始并发死锁/hash缺陷与夹具/沙箱失败均保留。新代码未跑远端CI/Linux race、OS强杀、物理broker故障或持续负载；上一轮CI全绿不替代本票。
- 专用MySQL3330/Redis6402/Kafka9098已停、卷保留、原开发与CRM服务未停止；Kafka正常stop后观察exit143，非OOM。自有fixture/未完成事件/停车0；[收尾](docs/evidence/2026-10-28-8c3fb0e/GO-072/cleanup-final.json)。

独立核验报告在 `docs/reviews/`，本文件只给状态与链接；实现者自测记录仍在 `docs/README.md` 各票小节，不覆盖历史失败。

### GO-075 / 交付与待核验范围

- 从`fb37af549d5e8d0d3e5e3ed66a542fb7e99601eb`继续新功能；a适配器`129526ddc763f14c9c14bf33bdaed8be55fd077a`，b配置接入`49abf3af8ba1f92413c0a31f15cc2737f9cd5bb0`，索引守护`77aa81041e33175e92abfadcde6d82dd240943ba`，null向量独立修复`33bdb4d91d7bb83cc73af45cc36849a6bfc2b448`，c真实30题`09663f059363f6e858d514014792f8734963f907`，QA1演示session清理`31fb563214739dbc7f010f6e773bad16b1aab7d8`，d真实HTTP终态`62c3f36679e7122f897969f44a9439efc11f4247`，e交易隔离`8286a707a9808598f95130e34cc2a1bce2037461`。
- 真实百炼`qwen3.8-flash`/`text-embedding-v4`1536维；固定集有据12/12、无据6/6、工具6/6、多轮6/6，黄金Recall@5/Hit@5=1，只代表该小集。逐题对照公开原文和版本，不是模型自评；两项关键词候选误报保留。重启不重新嵌入文档，显式重建会付费。
- 实际HTTP客户端取消、受控20ms Provider期限、生成中退出登录、恢复和预算5场景通过；失败不写assistant、无answer/done、lease/dedupe清理、逻辑预算退款，成功后保留1。取消/超时结算未知；不是生产45s超时、真实429或Vue浏览器证据。
- 普通Go全套/gofmt/vet/build、知识与会话真实MySQL/Redis回归、三种AI配置的交易各9项、最终二进制启动关闭6场景通过。缺失/不兼容/部分向量明确不可用，匿名搜索在慢embedding后复核撤权；无自动付费重试/重建。
- 无新增SQL迁移，无前端源码改动；本票远端CI/Linux race、GO-074真实模型浏览器页面和GO-073持续量化未执行。实现自测与提交前审查不替代票级独立核验，GO-072状态不因此提升。
- 专用MySQL3331/Redis6403已正常停止并保留卷；SQL自有用户/商家/知识/订单/outbox/review均0，登录session/AI键0，交易Stream/派生运行痕迹未flush。CRM仅只读取得已授权凭据，未改代码/配置/数据或停止其容器。下一开发票GO-076，GO-074随后差量页面，GO-073最终量化。
