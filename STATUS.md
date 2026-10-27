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
| GO-040 FAQ/索引版本 | 自测通过待核验 | `2a0cc97`、`21e843d`；真实 MySQL 发布待核验 |
| GO-041 检索/只读工具/SSE | 自测通过待核验 | `85a5ef7`、`536fe09`、`ec89df7`；真实回合待核验 |
| GO-042 去重/预算/终态 | 自测通过待核验 | `eacd5a3`、`c638912`、`515973d`；真实 MySQL/Redis 回合 PASS |
| GO-050 笔记发布/详情 | 自测通过待核验 | `147c70a`、`f12a04d`；迁移 006，真实 MySQL 发布 PASS |
| GO-051 关注与 DB Feed | 自测通过待核验 | `e2c1474`、`76abdd7`；幂等/游标 PASS，`EXPLAIN` 走 `idx_blog_feed` |
| GO-052 社区 outbox → Kafka | 自测通过待核验 | `db77e58`、`2f920ca`；真实 broker 投递/重试 PASS |
| GO-053 期望状态点赞与位图 | 进行中 | 期望状态点赞、位图计数与重建 |
| GO-060—062 缓存/计数/多投影 | 待做 | 批量装配、singleflight、热点/位图与重建 |
| GO-070 / 071 证据与正式切换 | 待做 | 持续负载、三流程演示、目录/CI/启动收敛到 Go |

## 门槛（未满足前不开放对应能力）

- GO-033 通过前：不开放库存重建写入口。
- GO-042 通过前：AI 只在 stub 环境验证或保持关闭。
- GO-062 通过前：不把位图计数当作对账依据。

## 横切未完成项

- 商铺详情未接 Redis/L1 缓存；`GET /shop/of/type` 附近坐标查询仍是明确 400。
- 交易/商家/AI/社区的大量 Java 旧路由在 Go 下仍是 404，旧写入口保持 403。
- 前端：商家订单/核销/退款/审计页、AI 助手页、社区页面仍在接 Go。
- Linux `-race` 与远端 CI 至今未实际执行。
- Java 基线已修的并发窗口（review 快照、锁定行、AI 终态）需要在 Go 对应票里重新取证。

## 复跑命令

```bash
# 后端
cd backend-go && gofmt -l . && go vet ./... && go test -count=1 ./... && go build ./...

# 前端
npm --prefix frontend run build && npm --prefix frontend run format:check

# 真实依赖（本机隔离实例）
$env:MYSQL_PORT='3307'; $env:GO_INTEGRATION='1'
$env:KAFKA_BROKERS='127.0.0.1:9092'
docker compose -f compose.go.yaml --profile kafka up -d kafka
go test -count=1 -run 'TestIntegration' ./...
```

## 已核验票的证据

独立核验报告在 `docs/reviews/`，本文件只给状态与链接；实现者自测记录仍在 `docs/README.md` 各票小节，不覆盖历史失败。
