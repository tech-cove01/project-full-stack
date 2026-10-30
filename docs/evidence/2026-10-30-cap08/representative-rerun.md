# CAP-08 独立核验的单代表档

本批正式第2轮失败，CAP-08已结束。核验者先审代码与现存原始材料；如果实际复跑，最多在**新QA目录、新自有卷和新会话**做一次multi100/s×180秒代表档，原判据不变。它不能补齐本批失败、覆盖旧B1或把一次通过写成重复稳定容量。本文不启动负载，不授权停止未知服务，不重新开放本批名额或CAP-09。

## 固定对象与复用边界

- 应用与新fixture来源均固定 **`46ddefc2a16d41e4bb01a3d20d08bdfcaeff465f`**，revision为`capacity-<完整SHA>`。从独立干净checkout构建，exe放checkout外，保存构建前后clean状态、Go版本/命令、`go version -m`的VCS匹配/modified=false和exe SHA256。不能复制本批fixture/token或旧CAP-07的B30b来源。
- 工具取最终交付checkout，另记完整工具SHA及逐文件字节hash；本批实际负载用516d534，结束后policy守卫另有小补丁，应用没有变化。不能把新工具hash倒写到旧command。
- 新证据例如`docs/reviews/<日期>/cap08-<nonce>`、私有runtime为所有Git仓库外的sibling `.runtime-fullstack-cap08-qa-<nonce>`，均须不存在；runtime核owner ACL、TEMP/TMP指向此处。新observer与合成会话凭据只在私有env，不能输出完整配置、token、进程环境或Docker Config.Env。
- 只复用[CAP-07代表档](../2026-10-30-cap07/representative-rerun.md)中的通用`prepare/run/finish`与精确生命周期流程，将其**固定应用/fixture B30b全部替换成上方46ddefc**，证据/runtime/tag换为本次新对象。不要照搬其中旧CID/fixture来源，也不要运行固定ROOT的`cap07-env.py`或`cap08-env.py/stage.py`。这些批次的slots和结果已封存，不能改ROOT/删slot/改guard绕过。

## 新自有依赖与单次负载

按[新卷身份流程](../2026-10-30-go073-cap/qa-handoff.md)核交接：本批停止的MySQL完整CID为`9e71ca549169ab9f3836669376ad12a0b9b8b964c0f36a1cf6eb2c431e3a719a`，Redis为`112cd2e69ba3d002aa5f7363e31c469947d5f353875755eef733f639375d330b`；project/service/停止状态、挂载、network/subnet与端口须现场再次核实，若对象已被别的任务替换就不能沿用。只对明确移交且停止的两容器force-recreate，使用新随机volume override挂`/var/lib/mysql`和`/data`，保留旧新卷。启动后保存新CID与资源/挂载，不能仅换project名假装新卷。

仍用loopback MySQL3451/Redis6551/API8251/diag8351，启动依赖前核四口空；API启动前依赖必须是已验身份且健康的新容器，另两口空。MySQL4CPU/2GiB、Redis2CPU/512MiB、持久化、migration001—013、API GOMAXPROCS4/GOMEMLIMIT1GiB、SQL20/Redis10/workers1，Kafka/ES/AI关闭。`GO_FEED_DIAGNOSTICS=false`，不加collector/抓栈。API启动隐藏窗口并记录exe/hash/PID/UTC创建时刻、health version、diagnostics PID和cards.coalescing=true。

使用通用`node scripts/go073-cap-read.mjs prepare`生成新6400篇/32作者/32读者/256B正文/页20/multi640key，保存新READY manifest SHA和source46/revision，不复用已注销token。`CAP_SOURCE_SHA`、`CAP_READ_FIXTURE_SOURCE_SHA`均为46，`CAP_READ_EXPECTED_REVISION=capacity-46...`。

只运行一次通用`capacity-stage.py --mode read --api-pid <新PID> --diagnostics http://127.0.0.1:8351/debug/capacity --interval 5 --max-seconds 360 --stop-grace-seconds 90 -- node scripts/go073-cap-read.mjs run`，control-dir/stop-file/输出load路径必须全新。负载环境明确：multi、rate100、seconds180、warm10/25、pre/max VUs256/512；前后取PID匹配的capacity快照。正式期间不并行编译、Go测试、其它容量任务或新探针，不加hot/升档/补轮。

保持原安全与内容判据，提前退出仍用计划180秒分母；逐响应验证完整20条ID/作者/门店/排序/计数/本人点赞。分别核native发起、HTTP、终态、成功、unknown、drop、内容/系统/HTTP错误、全/成功P95/P99及资源覆盖，不能只读wrapper exit。若`result.result`缺失，保留缺失和failure，单独核native summary，不伪造汇总。访问计数的Uptime一致性失败也必须原样披露，不通过改时钟容差换取有效成绩。

## 收尾与报告

cards counter窗口是预热前至wrapper收尾后，含预热/后台；gauge分列，不能作次数或保证峰值。全实例Com_select/Com_stmt_execute互有重叠，各自报告，不相加、不替代内容SQL；访问一致性无效时不写有效SQL/Redis成绩。分位是native汇总，无逐请求原始分布则不称重建。

监督/本轮子进程收敛后，API与依赖保持可用直至通用`node scripts/go073-cap-read.mjs finish`正常注销32会话并删除私有输入；核Redis session EXISTS0，删本轮observer并核0。按本轮保存的exe/hash/PID/创建身份停止API、按新完整CID停依赖，核exited/exit0/OOMfalse/挂载和旧新卷保留，再精确删除登记私有env；失败先保留progress与恢复输入。Windows terminate不称Go优雅退出，不使用down-v/prune/flush或名称全杀。

新QA报告单独给应用/工具/二进制/fixture来源、资源与背景差异、全部原生判据、访问一致性、loader口径和清理。它与本批开发自验、内部审查、精确CI分别记账；本批失败及旧长尾未知结论保留。
