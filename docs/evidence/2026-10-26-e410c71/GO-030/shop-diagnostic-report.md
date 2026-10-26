# 商铺 UTC 浏览回归诊断

2026-10-26。此次仅诊断已有 `scripts/go-shop-ui-smoke.mjs` 在分类错误重试段的超时，未修改产品源码、正式脚本或 README，未提交代码。

原始 [shop-regression-utc.json](shop-regression-utc.json) 保留：前 9 项检查通过，此后 `locator.waitFor` 在 30 秒后超时，cleanup=true。该脚本的 catch 只保存异常第一行，未记录失败 selector、阶段或截图，因此原始文件不能确定三个分类等待中的哪一个失败，也不能仅据此认定 Go/Vue 业务回归。

主 agent 另行观察到 Vite 5174 进程已经以 exit 1 退出；API 8082 仍在线，后续一次页面检查在最初 fetch 即失败。主 agent 重启 Vite 并确认 ready 后，允许此次诊断重跑。这是环境异常的已报告观察；Vite 在 reload 期间失联可以解释未渲染出分类重试 UI，但缺少初次超时的同期服务日志/截图，**仍属相容推断，不是已证明的初始根因**。

诊断使用正式脚本的临时副本，保留原 14 项断言、真实 Go API 与精确 SQL 夹具，只增加安全诊断：分类重试各阶段的时间戳、请求失败的 URL pathname/错误类型、分类响应状态，以及失败时的 selector 数量和截图。没有打印请求头、Cookie、token、数据库密码或 SQL 诊断。仅注入原脚本已有的失败请求；成功重试仍命中真实 Go/MySQL。

运行参数：UTC 浏览器，WEB `http://127.0.0.1:5174`，API `http://127.0.0.1:8082`，隔离 MySQL `127.0.0.1:3308/project_full_stack_go`。Playwright 从绝对 runtime 模块加载，Chrome 使用系统安装；凭据通过环境提供。

结果见 [诊断 JSON](shop-diagnostic-utc.json) 和 [最终截图](shop-diagnostic-utc.png)：**14 项全部通过，cleanup=true**。分类段按序到达并通过 `wait-retry-button`、`wait-shop-card`、`wait-fixture-category`，随后优惠重试及 UTC 时间状态也通过；记录到的 abort/offline 请求与原脚本主动注入的失败相符。本次没有复现产品异常。诊断脚本未执行任何 Go Stream 集成测试，也未写订单 Stream。

建议主 agent 的最小修正是补足正式脚本诊断能力：每个错误注入/重试等待前记录明确 stage；失败时保留当前 stage、截图及直连 API/前端服务的安全健康快照。暂不据单次环境失联修改产品行为。服务退出的真正原因应由 Vite 同期 stdout/stderr 另查，不能把延长 selector 超时当作业务修复。

临时副本 `scripts/.go-shop-ui-diagnostic.mjs` 已在本次调查收尾时按精确路径删除。原始 FAIL、此次 PASS 和截图均保留，主 agent 可独立决定是否修改正式脚本及如何提交。
