# Feed作者索引：隔离迁移与直接回归

在所有read负载暂停期间，仅恢复本票已知 `project-full-stack-cap-opt-tests` MySQL3452/Redis6552，保持read夹具、API与其它服务原样。新增013只增加非唯一作者前缀索引，保留007及全部SQL权限/排序/游标谓词；Go文件只澄清旧索引计划注释，没有更改查询字符串或执行逻辑。

从仓库根执行 `node scripts/migrate.mjs`，[首次记录](migrations.txt)应用013成功、exit0；[原样重跑](migrations-rerun.txt)核对校验和后全部skip、exit0，没有AutoMigrate或直接改业务事实。然后在 `GO_INTEGRATION=1`、development、MySQL127.0.0.1:3452、Redis127.0.0.1:6552/0、GOMAXPROCS=4、Kafka/ES/AI关闭条件，从backend执行 `go test -count=1 -p 1 -v ./internal/social`。

[真实日志](social-test.txt)和[退出记录](social-test-exit.json)均保留：五个真实依赖入口与五个普通入口通过，无skip。包括并发首次关注、同秒复合游标/无重漏/取消关注、期望状态点赞、多个作者的投影与DB回退、公开状态/当前关系变化、本人状态与事实优先。没有追加镜像业务逻辑的索引存在性测试。

测试完成后已正常停止该测试项目两容器，保留原卷，未与正式read压测并发。非作者静态审查由cap_trade完成，SQL字段/现有索引、非cover与多作者排序限制、写维护/DDL成本及前向DROP回退见[设计](../feed-index-design.md)。本记录验证迁移兼容和直接正确性；真实6400篇夹具的after EXPLAIN及同档HTTP收益另由CAP-03实跑，不提前记为通过。
