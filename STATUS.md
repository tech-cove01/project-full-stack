# 工作流与任务状态

更新：2026-10-27。本文件是**唯一状态入口**：任务状态、依赖、门槛、提交与核验流程都记在这里；`docs/README.md` 只保留产品范围与各票的详细记录。

## 工作流

1. 一个小任务自测通过 → 独立 commit → 把完整 SHA 交给核验方；核验失败追加修复提交，不改写其他 agent 的历史。
2. 一个 commit 只含一个可验收的小功能；无关修改（格式化、文档、脚本）分开提交。
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
| GO-031 支付/取消/超时 | 自测通过待核验 | 4 个功能提交 + CI 提交；真实支付 8/取消 11/扫描 6+3 |
| GO-032 核销/退款/审计 | 自测通过待核验 | `8d5b655`、`b5439fd`、`1b29269`、`f0d9c55`；真实 MySQL 并发待核验 |
| GO-033 补偿/review/重建 | 自测通过待核验 | `5fc0879`、`ac67d41`、`9b00b9a`、`c279cf8`；真实 Redis 窗口待核验 |
| GO-040 FAQ/索引版本 | 自测通过待核验 | `2a0cc97`、`21e843d`、`bed4092`（补齐前端重建入口）；真实 MySQL 发布/重建待核验 |
| GO-041 检索/只读工具/SSE | 自测通过待核验 | `85a5ef7`、`536fe09`、`ec89df7`；真实回合待核验 |
| GO-042 去重/预算/终态 | 自测通过待核验 | `eacd5a3`、`c638912`、`515973d`；真实 MySQL/Redis 回合 PASS |
| GO-050 笔记发布/详情 | 自测通过待核验 | `147c70a`、`f12a04d`；迁移 006，真实 MySQL 发布 PASS |
| GO-051 关注与 DB Feed | 自测通过待核验 | `e2c1474`、`76abdd7`；幂等/游标 PASS，`EXPLAIN` 走 `idx_blog_feed` |
| GO-052 社区 outbox → Kafka | 自测通过待核验 | `db77e58`、`2f920ca`；真实 broker 投递/重试 PASS |
| GO-053 期望状态点赞 | 自测通过待核验 | `b7e9717`、`3e99ce9`；真实 MySQL 幂等/10 并发收敛 PASS；位图计数与重建归 GO-062 |
| GO-060 页卡片批量装配与计数缓存 | 自测通过待核验 | `e67e280`、`f521831`；真实 Redis/MySQL 缓存与并发读 PASS |
| GO-061 热点保护 | 自测通过待核验 | `031d97c`；进程内短 TTL 吸收热点，真实 Redis 验证 |
| GO-062 位图计数与重建 | 自测通过待核验 | `54fc5e8`；位图投影 + `cmd/recount`，真实 Redis/MySQL PASS |
| GO-070 验收脚本与故障演练 | 自测通过待核验 | `93dea4a`、负载与 Redis 故障脚本；隔离环境下三流程 22 项、并发抢券 9 项、Redis 停机 6 项全过 |
| GO-071 目录/CI/启动收敛 | 自测通过待核验 | CI 覆盖社区/AI/Kafka；Java 基线归档到 archive/；Go 服务目录已收敛为 backend/；收敛后三流程复跑通过 |

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

## 横切未完成项

- 商铺详情未接 Redis/L1 缓存；`GET /shop/of/type` 附近坐标查询仍是明确 400。
- 交易/商家/AI/社区的大量 Java 旧路由在 Go 下仍是 404，旧写入口保持 403。
- 前端已接 Go：商家订单/核销/退款/审计、商家活动、商家知识库（含重建）、AI 助手、笔记/关注/Feed/点赞；仍缺页面级浏览器证据与运维入口。
- Linux `-race` 与远端 CI 至今未实际执行。
- Java 基线已修的并发窗口（review 快照、锁定行、AI 终态）需要在 Go 对应票里重新取证。

## 复跑命令

```bash
# 后端
cd backend && gofmt -l . && go vet ./... && go test -count=1 ./... && go build ./...

# 前端
npm --prefix frontend run build && npm --prefix frontend run format:check

# 真实依赖（本机隔离实例）
$env:MYSQL_PORT='3307'; $env:GO_INTEGRATION='1'
$env:KAFKA_BROKERS='127.0.0.1:9092'
docker compose -f compose.go.yaml --profile kafka up -d kafka
go test -count=1 -run 'TestIntegration' ./...
```

### GO-071 交付记录

- 依赖 GO-070；提交：CI 覆盖 `ci(GO-071)`；Java 归档与文档 `refactor(GO-071)`；目录收敛 `refactor(GO-071)`。
- 行为：CI 新增社区/AI/知识库/计数缓存与商家订单/补偿的真实依赖测试，并新增带单节点 KRaft broker 的 `community-kafka` 作业，社区事件投递不再只靠核验轮；Java 源码以 rename 方式移到 `archive/backend-java-20261026/`，Java CI 只守护归档构建；Go 服务目录由 `backend-go/` 收敛为 `backend/`，CI、脚本与文档路径同步更新（历史核验报告与证据文件保留当时的路径原样）。
- 验证：改名后 `gofmt/vet/go test/go build` 与前端构建、格式检查全部通过，三流程演示脚本复跑 22 项全过；CI YAML 解析通过。
- 注意：核验方请改用 `backend/` 路径；`docs/reviews/` 与 `docs/evidence/` 中的 `backend-go` 字样是历史记录，未做改写。
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
## 已核验票的证据

独立核验报告在 `docs/reviews/`，本文件只给状态与链接；实现者自测记录仍在 `docs/README.md` 各票小节，不覆盖历史失败。
