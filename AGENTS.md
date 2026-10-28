# 项目协作约定

## 共享工作区

- 与 project-crm 同处 project/code，但保持独立 Git 与任务状态。存在 ../WORKSPACE.md 时读取共享目录约定；独立克隆不依赖父层文件。
- 本仓通过 .agents/skills 的本地目录联接共用 workspace-delivery、typescript-pro、sql-pro、playwright-expert。按任务需要加载，不将 React/Pi 或 CRM 简历规则套到 Go/Vue，也不因 Skill 增加无关测试或更改现有技术路线。

## 当前路线

- 用户已确定全面迁移 Go，后续不再新增 Java 功能。保留 Vue 3/TS、MySQL 8.4、Redis 7.4、SQL/Lua 和外部业务契约。
- 先读 STATUS.md 和 docs/implementation-plan.md，再按 docs/agent-handoff.md 执行。STATUS.md 是唯一任务状态入口；docs/README.md 保留产品与历史交付详情。
- Java 参考基线为 java-baseline-20261026，不等于全部验收通过。Go 实际完成前不得沿用 Java 测试/吞吐量作为 Go 成绩。
- 订单保留 Redis Stream / 交易 outbox，社区独立 outbox → Kafka。不新增 PostgreSQL/RabbitMQ；2026-10-28用户确认补公开笔记ES全文搜索（GO-076），独立于正在实施的GO-072，不扩Canal/ELK/集群或压缩计数。
- 临时 backend-go/ 完成等价验收后收敛至 backend/；不长期开发双后端，不同时消费同一 Stream/group/outbox。

## 任务、自验与提交

- 每个 GO-* 小任务交付可验证行为，列依赖、验收ID、命令、证据与限制。自测通过后独立本地 commit，用户已授权，无需逐次确认。
- 用户于2026-10-28再次明确：一个独立问题一次commit。只包含该问题必要的代码、测试和说明；多个独立缺陷分别提交，不把整轮核验修复攒成一笔；新发现追加修复提交，保留已提交历史。
- 暂存明确文件/hunk，不批量收走其他 agent 修改；提交带任务ID并交完整SHA。自测通过仅代表待核验。
- 修复追加提交，保留失败证据；不 amend/rebase 他人历史。
- 2026-10-26 用户持续授权本核验流程：核验发现问题时一并修复、完成适用验证、分步提交并正常推送至 origin/main，无需再次确认；本次此前已提交内容也已获推送授权。此授权不包含强推、覆盖他人修改或部署/发布；实现 agent 的自测仍不等于独立核验通过。
- gofmt/vet/test 与真实依赖/Stub/真实模型/负载结果分开。文档/格式改动无需形式测试。
- 人工重点审查事务tx、Lua预校验、ACK/review、门店权限、字符串ID/金额、goroutine/context和全部AI终态。
- 不用 AutoMigrate 替代版本 SQL，不删除数据卷或未知文件，不改无关服务；回退说明迁移兼容性。
- README 描述真实产品能力，未实现与历史证据明确标注。来源和许可证保留在 THIRD_PARTY_NOTICES.md。
