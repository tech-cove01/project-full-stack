# GO-075e 核心交易与AI配置隔离

新增可交接入口`scripts/go-ai-isolation-smoke.py`，用专用依赖和预编译API逐个启动三种配置：AI关闭、请求真实Provider但缺Key、配置完整但兼容地址不可达。每个实例实际发布活动→Redis准入→异步成单→模拟支付→取得本人核销码→商家核销→审计查询，共9项断言；分别检查capabilities开关/ready/远端unchecked，再正常关闭API、退出0且端口可重新绑定。无付费请求，不代表同时运行的AI请求持续故障压测。

本票保留所有轮次：

- 同等逻辑本机编排的初始轮：[isolation-final.json](isolation-final.json)及同目录API日志/交易结果，各9项通过。
- 发布脚本首次复跑：[isolation-script-final/isolation-final.json](isolation-script-final/isolation-final.json)，三种配置各9项通过。
- 发现旧演示脚本遗留session后追加独立GO-075-QA1修复，最终复跑：[isolation-script-session-fixed/isolation-final.json](isolation-script-session-fixed/isolation-final.json)，仍各9项通过，自有演示session0，见[清理问题](session-cleanup-report.md)。前两轮业务通过不代表夹具清理完整。

运行示例：在仓库根目录设置专用MYSQL/REDIS、`GO_AI_ISOLATION_BINARY=<绝对路径API>`、`GO_AI_ISOLATION_EVIDENCE=<新的空目录>`，执行`python scripts/go-ai-isolation-smoke.py`。脚本只用固定合成Key和本机不可达地址，不读取真实Provider凭据。交易脚本只删自身SQL；所有模式的API都在finally中关闭，强制清理或非零退出会使结果失败。Redis中交易Stream/派生键属于专用演练运行痕迹，未对共享服务做flush或重置。

独立审查provider_adapter发现中断证据覆盖风险：只检查final.json不足以保护尚未生成final的旧日志。提交前已改为拒绝任何非空目录，并由ai_eval复核、实测拒绝保留原文件hash：[isolation-evidence-guard.json](isolation-evidence-guard.json)。前述审查文件[lifecycle-review.md](lifecycle-review.md)的“待修”是当时快照，此报告记录最终关闭。

Node语法/Python AST通过。API构建revision为`go075-worktree`（未伪称精确Git提交），普通全套构建/测试与最终二进制启动结果另见总报告。本票没有前端源码改动，没有用这些交易HTTP结果代替GO-074页面或GO-073持续负载。
