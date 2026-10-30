# Feed 冷缓存二次借连接：确定性复现与回归

2026-10-30，正式HTTP负载完全暂停后，复用已核对身份的本票`project-full-stack-cap-opt-tests`（MySQL3452/Redis6552，原013版本/自有卷）。仅正常start，测试后正常stop，卷保留；没有和正式读压测并行。

同一个`TestIntegrationFeedColdCountsWithOneConnection`通过公开Service API建立真实关注/两篇公开笔记，启用projection和真实counts Cache，清理仅本次笔记计数键，SQL池限1。该限值用于确定性暴露嵌套依赖，不是压力环境的池设置；正式API仍为20。

- **旧实现失败**：[原3db源码字节](before-feed.go.txt)、[来源/hash](before-source.json)、[Go overlay](before-overlay.json)。`go test -overlay <before-overlay.json> -count=1 -run '^TestIntegrationFeedColdCountsWithOneConnection$' -v ./internal/social`只替换旧Feed实现，测试和其余源码完全相同。[日志](before-test.txt)显示3.20秒后事务已结束/回滚且pool wait增加1；[退出记录](before-exit.json)为预期exit1。
- **新实现通过**：同命令去掉overlay，[日志](after-test.txt)单用例0.27秒、[退出](after-test-exit.json)为0。测试还验证SQL无新增wait/in-use0、cold loader确实填入Redis、已有cache值覆盖公共计数、本人点赞不被cache改变、同秒游标、取消关注后页面不因缓存复活。
- **直接真实回归**：`GO_INTEGRATION=1 go test -count=1 -p 1 -v ./internal/social ./internal/communitycache`，[日志](social-cache-test.txt)/[退出](social-cache-test-exit.json)为PASS/0：social六个真实入口及五个普通入口通过（包含投影/回退/当前关系/公开状态/快照子场景），cache八个真实入口通过；原`TestGO073CacheMeasurement`因其专用测量开关未开而SKIP，不称已复跑旧函数级量化实验。两包分别3.128/1.339秒。
- `go vet ./internal/social ./internal/communitycache`，[日志](vet.txt)/[退出](vet-exit.json)为0。

环境显式development、MySQL127.0.0.1:3452/本地合成库、Redis127.0.0.1:6552/0、GOMAXPROCS4；Kafka/ES/AI关闭。未改数据库池、预算、缓存TTL/epoch或权限谓词。修复让原计数缓存装配在事实快照事务返回之后进行，事务连接先归还，不把tx绑入共享cache或用旧快照回填cache。

非作者`cap_trade`审读实现、测试和[设计](../feed-pool-design.md)无阻断；root复查并统一执行上述实测。内部审读/回归不是外部验收。源码和单连接实测证明条件性池饥饿依赖；不据此断言它是此前每次dropped的唯一原因。新固定API的HTTP阶梯与180秒重复另交，旧3db所有失败保留。
