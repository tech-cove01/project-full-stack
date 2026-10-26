# 项目协作约定

## 当前路线

- 用户已确定全面迁移 Go，后续不再新增 Java 功能。保留 Vue 3/TS、MySQL 8.4、Redis 7.4、SQL/Lua 和外部业务契约。
- 先读 docs/README.md 和 docs/implementation-plan.md，再按 docs/agent-handoff.md 执行。docs/README.md 是唯一任务状态入口。
- Java 参考基线为 java-baseline-20261026，不等于全部验收通过。Go 实际完成前不得沿用 Java 测试/吞吐量作为 Go 成绩。
- 订单保留 Redis Stream / 交易 outbox，社区独立 outbox → Kafka。不新增 PostgreSQL/RabbitMQ；Canal/ES/压缩计数非必需。
- 临时 backend-go/ 完成等价验收后收敛至 backend/；不长期开发双后端，不同时消费同一 Stream/group/outbox。

## 任务、自验与提交

- 每个 GO-* 小任务交付可验证行为，列依赖、验收ID、命令、证据与限制。自测通过后独立本地 commit，用户已授权，无需逐次确认。
- 暂存明确文件/hunk，不批量收走其他 agent 修改；提交带任务ID并交完整SHA。自测通过仅代表待核验。
- 修复追加提交，保留失败证据；不 amend/rebase 他人历史。推送/发布另依会话授权。
- gofmt/vet/test 与真实依赖/Stub/真实模型/负载结果分开。文档/格式改动无需形式测试。
- 人工重点审查事务tx、Lua预校验、ACK/review、门店权限、字符串ID/金额、goroutine/context和全部AI终态。
- 不用 AutoMigrate 替代版本 SQL，不删除数据卷或未知文件，不改无关服务；回退说明迁移兼容性。
- README 描述真实产品能力，未实现与历史证据明确标注。来源和许可证保留在 THIRD_PARTY_NOTICES.md。
