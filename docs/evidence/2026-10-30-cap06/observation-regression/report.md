# CAP-06A 观测接线与直接回归

本次只增加默认关闭的分段观测，未修复缓存门、调整池/TTL或声称长尾已解决。固定阶段、32条慢记录与每条256个span的上限及不可拆分范围见[观测说明](../../../../backend/internal/platform/taildiag/README.md)。非作者源码审读覆盖配置、完整Feed路由/鉴权、ORM事务接口、Redis透传、计数门和有界性，未发现阻断。

正式负载尚未启动时，在本轮新自有MySQL3451/Redis6551与001–013迁移上运行`GO_INTEGRATION=1 go test -count=1 -p 1 -v ./internal/platform/taildiag ./internal/social ./internal/communitycache`。[真实日志](real-tests.txt)/[退出记录](real-tests-exit.json)均保留，exit0：

- 新`TestIntegrationObservedStorageTransactions`实际包装安全池，执行真实只读RR提交/受控回滚、归还连接、Redis PING/取消及原ORM.DB接口核对，耗时0.02秒；不修改业务事实/依赖配置。
- 既有social六个真实入口及普通用例通过，保留单连接冷计数、投影/数据库回退、当前关注/公开状态/个人点赞及同秒游标回归；这些Service构造默认不启观测，不能冒称所有既有用例都在包装下运行。
- communitycache八个真实入口通过；原专用`TestGO073CacheMeasurement`因开关未开而SKIP，不称复跑旧量化实验。

config/diagnostics/app/auth四包[普通测试](related-tests.txt)通过；相关七包`go vet`通过，见[退出记录](related-checks-exit.json)。普通driver/并发测试验证界限、取消及不读参数，不替代上述真实兼容性回归。随后同一干净构建的观测开/关短探针与最多三轮正式长尾诊断另行落证；这里没有新的容量或因果结论。
