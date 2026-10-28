# 工作流与任务状态

更新：2026-10-28。本文件是**唯一状态入口**：任务状态、依赖、门槛、提交与核验流程都记在这里；`docs/README.md` 只保留产品范围与各票的详细记录。

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
| GO-072 Kafka 消费端投影 | 待做 | 消费 `community.events`，按 `eventId` 去重、按 version 应用，补第二投影读模型 |
| GO-073 持续负载与量化 | 待做 | k6 稳定几分钟的 RPS/P95/P99、同 key 冷 miss 加载次数、投影延迟与积压追平、库存计数对账 |
| GO-074 页面级浏览器证据 | 部分完成 | 本轮两时区交易各26项、社区/FAQ/AI各10项；B端核销退款完整页面与更多角色矩阵仍可补 |
| GO-075 真实Provider与小集评测 | 待做 | 当前Go只有stub；先实现真实Chat/Embedding适配，再以真实provider验证质量/引用/取消与费用，不只缺凭据 |

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
### 2026-10-28 收工盘点（剩余工作与建议顺序）

- 结论：交易闭环、社区基础和stub AI已成形，核验结论以顶部实际范围为准。剩余既有量化/页面验证，也有Kafka消费投影、部分缓存规划和真实Provider代码，不能概括成“只差验收与量化”。
- 建议顺序：
  1. **GO-072 Kafka 消费端投影**（唯一还缺的功能代码）：消费 `community.events`，按 `eventId` 去重、按 `version` 应用，补第二投影读模型；plan 第 7 节明确要求“经 Kafka 至少一次消费并按版本应用”。当前只有投递侧。
  2. **GO-073 持续负载与量化**：稳定几分钟的 RPS/P95/P99、同 key 冷 miss 的实际加载次数、事件投影延迟与积压追平、库存计数对账。不得引用 Java 的 822 req/s 或 29 ms 混合响应作为 Go 成绩；已有的只是短时正确性突发。
  3. **GO-074 页面级浏览器证据**：Playwright 跑交易/社区/AI 三条流程的页面，UTC 与上海各一轮，产出截图与断言；目前只有 HTTP 层证据。
  4. **GO-075 真实模型小集评测**：有 provider 凭据则评测回答质量与引用正确性；没有则明确标注“仅 stub 协议已验证”，不宣称质量。
  5. **独立核验（原盘点，已更新）**：GO-031—GO-071 的15票已完成本轮按范围核验与修复，结论以顶部表格及10-28独立报告为准；GO-060/061/062只验收实际子集，不把规划亮点算完成。
- 收尾动作（核验通过后）：删除 Java 基线（当前在 `archive/backend-java-20261026/`），删除前确认没有页面或脚本依赖其 8081 端点。

## 已核验票的证据

独立核验报告在 `docs/reviews/`，本文件只给状态与链接；实现者自测记录仍在 `docs/README.md` 各票小节，不覆盖历史失败。
