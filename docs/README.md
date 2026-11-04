# 文档导航

产品介绍与运行概览见[项目首页](../README.md)。当前任务、进度和性能状态只在[根STATUS](../STATUS.md)维护；各票历史记录已归档，不从旧“下一步”恢复执行。

## 产品与实现

| 阅读目的 | 入口 |
| --- | --- |
| Go/Vue产品范围、架构与外部契约 | [总方案](implementation-plan.md) |
| 交易、库存、门店权限与恢复 | [交易设计](transaction-design.md)、[验收场景](acceptance.md) |
| 社区、缓存、事件与搜索 | [后端需求](backend-highlights-plan.md)、[验收场景](backend-highlights-acceptance.md) |
| 只读业务AI与知识范围 | [AI设计](ai-design.md)、[AI验收](ai-acceptance.md) |
| 实际模块与前后端调用链 | [源码讲解路线](source-walkthrough.md) |
| 本地运行、迁移与配置 | [开发说明](development.md)、[Windows补充](windows-development.md) |
| 用户、商家与业务AI演示 | [录屏指南](demo-recording.md) |

后端为Go模块化单体，前端保留Vue/TypeScript。交易使用Redis Lua/Stream与MySQL，社区使用独立Outbox/Kafka和可恢复查询投影；支付为模拟流程，AI工具只读。具体功能与边界以当前源码和各项验收为准。

## 协作与证据

| 阅读目的 | 入口 |
| --- | --- |
| 当前唯一状态 | [STATUS](../STATUS.md) |
| 具体授权与压缩后接续 | [协作记录](collaboration-agreements.md) |
| 实施、小提交与独立核验 | [交接规则](agent-handoff.md) |
| 保留日期/SHA/语言的原始证据 | [evidence](evidence/)、[reviews](reviews/) |
| 历史Go票据与交付细节 | [功能交付快照](../archive/docs/go-delivery-history-20261104.md) |
| 旧执行过程与计划 | [状态快照](../archive/docs/status-history-20261104.md)、[交接快照](../archive/docs/agent-handoff-history-20261104.md) |
| 冻结Java源码 | [archive/backend-java-20261026](../archive/backend-java-20261026/) |

历史快照保留当时的限制、失败和独立核验范围，不将旧Java或组件指标改名为当前Go吞吐成绩。当前CAP-10证据仍在原目录，归档不改变工具ROOT、原件SHA或旧窗口判定。
