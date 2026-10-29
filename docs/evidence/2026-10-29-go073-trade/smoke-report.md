# GO-073 交易脚本短验证

这是脚本与对账路径的短验证，不是持续负载结果。代码基线 `40fd575ec850b5d41315460e0f75f05c6326f8a5`，构建出的 Go 二进制版本字符串为 `go073-trade-40fd575ec850b5d41315460e0f75f05c6326f8a5`；编译时工作区包含未提交的测量脚本/证据，`vcs.modified=true`。正式测量须在最终功能修复提交后重新构建并重新执行。

专用 Compose 项目 `project-full-stack-go073-trade`；MySQL 3420、Redis 6520、API 8220 均绑定本机。独立空卷初始化后 003–012 迁移成功，重跑见 [migration-rerun.txt](migration-rerun.txt)。Kafka/ES/AI 在此交易短测中关闭，其他项目服务未使用。MySQL 最大连接 20、空闲连接 10，Redis 池 10；服务使用默认 15 分钟支付期限。

第一轮 [smoke-1/result.json](smoke-1/result.json) 未发负载：脚本活动时间带非零毫秒，而活动接口要求时间精确到秒；按源码校验修正夹具后保留原结果。第二轮 [smoke-2/result.json](smoke-2/result.json) 以 2 次/秒、预热 2 秒和稳定 3 秒运行：预热 4、稳定 7 次均以独立账号受理，11 单逐 ID 和用户核对一致，MySQL 与 Redis 库存均从 174 降至 163，受理映射 11、review/rejection 0、PEL 0、group lag 0、k6 丢弃迭代 0。稳定阶段比名义 6 次多一次 k6 端点边界迭代，不能用 7/3 当稳定容量；有效稳定窗口中的观测次数另记。

[requests.json](smoke-2/requests.json) 为脱敏逐请求、预期用户和订单；[polls.json](smoke-2/polls.json) 为 SQL 查询前后时间，[settle.json](smoke-2/settle.json) 为 PEL/lag 收敛快照。每秒查询已提交订单：首次观察减 HTTP 响应完成的差值受到实际轮询间隔、SQL 查询耗时与调度影响，不是精确事务提交延迟。此轮轮询间隔 P95 1009ms，查询耗时 P95 171ms；负值单独计数并同时保留原始值和截零值。窗口从首个稳定请求时间近似开始，仅统计稳定批次 ID。

正常退出后两轮的临时 token 文件及其专用临时目录均不存在；临时文件位于当前用户系统临时目录，Windows 的 mode 设置不保证 ACL，异常强杀可能使其残留。`fixture-progress.json` 保存了准确临时路径供人工检查。测试数据、审计、已确认 Stream 历史与卷为审计保留，未清空或删除；后续每轮先检查用户前缀无碰撞，新建活动并只对该活动对账。短测 API 收到中断退出码 1，端口不再监听，不将其声称为优雅退出。
