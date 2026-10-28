# GO-072e / 新投影真实集成进入CI

在现有社区真实MySQL/Redis步骤加入 `./internal/communityprojection`，版本迁移先于测试；真实Kafka job在既有relay测试后串行执行 `./internal/communityconsumer`。保留 `GO_INTEGRATION=1`、Linux `-race`、显式依赖和 `-p 1`，避免并行扫描/relay认领互相影响。新增查询测试由既有 `./internal/social` 步骤覆盖。

整份YAML本地解析通过，[静态记录](ci-yaml-check.txt)。本次新代码尚未运行远端Actions/Linux race，不能拿10-28上一轮绿色CI当GO-072的结果。普通Go与专用MySQL/Redis/Kafka的本地真实验证见总报告。
