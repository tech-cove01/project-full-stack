# GO-030 用户抢券、订单查询与本人列表页面

日期：2026-10-26。本批前端基线 `e410c71d9d8dfeaf654e203e439fa3f871c9afd4`；本报告记录实现者自测，状态为自测通过待独立核验。最终交付 SHA 和完整运行环境见整票报告；不将此前 Java 成绩记为 Go 成绩。

## 页面交付范围

- 商铺页通过真实登录态提交抢券请求，收到字符串受理 ID 后跳转订单详情；受理不被描述为数据库订单已存在。网络失败、超时或 HTTP 5xx 保留本次活动 ID，独立“重试本次抢券”入口在售罄、下架、活动结束后仍可调用同一接口找回已有结果。前端不依赖错误文案识别不确定结果，使用 `ApiError.outcomeUncertain`。成功后清理恢复状态；页面离开取消在途请求。恢复状态仅保存在当前页面生命周期内。
- 订单详情初始显示“正在查询订单”；只有本人查询成功后才记录最近订单。`PROCESSING` 与 `CREATED` 分开呈现；处理中不展示金额、门店或支付期限。数据库订单显示当前业务状态、精确字符串金额及北京时间。404/403 或其他查询错误清除旧详情，避免继续显示上一订单金额。轮询采用有界退避，路由 ID 切换及卸载清理 timer、AbortController，并用代次屏障拒绝迟到响应。
- 本人订单列表通过服务端 `current/pageSize` 分页，支持 10/20/50 条；总数保留十进制字符串，以 BigInt 判断是否还有下一页。列表错误清空旧数据，提供独立重试；空账号显示真实空态。排序以服务端 `create_time DESC,id DESC` 为准，不用前端补排序掩盖跨页遗漏。
- 支付、取消及超时关闭仍未开放。详情只展示禁用的“稍后开放”按钮和明确提示，不调用这些写接口，不展示核销凭证。未提前开启商家核销、退款或审计操作。

## 真实验证脚本与证据

入口为 `scripts/go-order-ui-smoke.mjs`，需显式提供隔离 MySQL/Redis 环境变量；默认 WEB `http://127.0.0.1:5174`、API `http://127.0.0.1:8082`，支持 `PLAYWRIGHT_MODULE`、`CHROME_PATH`、`UI_EVIDENCE` 与 `BROWSER_TIMEZONE`。脚本核对直连和前端代理的 Go 服务身份，不用 Stub 或 Mock 结果替代受理、Stream 或数据库成单。

同一真实会话 20 次并发请求返回同一受理 ID；最终只有一条数据库订单、一条 Stream 消息、一条创建审计和一条订单 outbox，DB/Redis 库存各减一次。另一活动由 200 个 SQL 准备、经真实验证码登录 API 获得会话的用户争抢 100 库存，验证 100 个唯一受理与 100 次库存业务拒绝，最终 100 条订单、无同用户有效单重复、DB/Redis 库存均为零，并逐个用本人会话查询 `CREATED/orderStatus=1`。账号准备及登录不计入抢券请求过程；Token、验证码、手机号不输出或写入报告。

浏览器通过真实 Vue 登录并抢一张大金额券，使用 `route.fetch()` 将请求交给真实 Go，再仅丢失响应。活动由真实商家下架后，恢复入口仍找回原 ID，且无二次扣库、二次成单或额外 Stream 消息。订单页刷新后金额 `9007199254740993` 分仍精确显示为 `90071992547409.93` 元，创建时间与显式 `+08:00` MySQL 会话的结果一致。列表两页、每页数量调整、错误清空与重试、越权 404、不污染最近订单、空态与离页停止轮询均有浏览器断言。

布局修订后的最终结果如下；两轮运行均核对同一 Go 服务标识 `f86a97f-go030-worktree`，每轮覆盖 30 项真实 API/浏览器检查。

| 浏览器时区 | 最终原始日志 | 结果与清理 | 页面截图 |
| --- | --- | --- | --- |
| UTC | [ui-utc-final.json](ui-utc-final.json) | 30 项 PASS；`forcedCleanup=false`；owned 消息 102 条，清理前 pending 为 0；owned DB/Redis/Stream 残留均为 0 | [详情](ui-utc-final-created.png)、[列表](ui-utc-final-list.png) |
| Asia/Shanghai | [ui-shanghai-final.json](ui-shanghai-final.json) | 30 项 PASS；`forcedCleanup=false`；owned 消息 102 条，清理前 pending 为 0；owned DB/Redis/Stream 残留均为 0 | [详情](ui-shanghai-final-created.png)、[列表](ui-shanghai-final-list.png) |

初轮 [UTC 日志](ui-utc-initial.json) 与 [上海日志](ui-shanghai-initial.json) 均保留。初轮 UTC 行为 30 项通过，但[详情截图](ui-utc-initial-created.png)发现操作按钮超出卡片边界；已在 `OrderView.vue` 局部修正换行和按钮间距。实现者已查看最终 UTC 详情截图，按钮现在在卡片内自然换行；初轮原图保留为视觉缺陷证据，未以旧图覆盖修订后的验证。未执行移动设备浏览器专项测试。

最终 `npm run build`（vue-tsc 与 Vite）和 `npm run format:check` 均 PASS，已查看[最终构建日志](frontend-build-final.txt)与[最终格式日志](frontend-format-final.txt)。差量 `git diff --check` 通过。浏览器截图不替代数据库、Redis 及消息检查。

另有两轮非通过记录保留，边界如下：

- [ui-vite-unavailable.json](ui-vite-unavailable.json) 在服务身份检查阶段 `fetch failed`，checks 为 0、`forcedCleanup=true`；没有完成业务验收检查，也尚未创建本脚本的新增夹具。已观察到 Vite 退出，重启并确认 ready 后完成上表最终两时区验证；该失败记录不写成业务通过。
- 商铺回归 [shop-regression-utc.json](shop-regression-utc.json) 前 9 项通过后，在分类相关等待中超时，cleanup=true；原记录没有精确 selector、阶段或截图，不能确认哪个等待失败。保留原 14 项断言、只增安全诊断的复跑 [shop-diagnostic-utc.json](shop-diagnostic-utc.json) 为 14 项 PASS、cleanup=true，未复现产品异常。详见[诊断报告](shop-diagnostic-report.md)。Vite 退出与首次分类超时的因果关系未被同期证据证实，不能把环境异常观察写成已确定根因。

## 夹具、清理与验证边界

- 12 条同创建时间、不同 ID 的订单由 SQL 直接插入，仅用于本人列表分页验证；对应券禁用，标题标记“只读分页夹具”。它们不是 worker 成单证据，也不用于证明库存对账。
- `PROCESSING` 查询使用一个仅有本人 Redis reservation、没有数据库订单及 Stream 消息的查询夹具。该场景证明 `orderStatus=null`、不伪造快照和页面轮询清理，不证明真实 worker 生成中窗口。正常快速成单允许首次查询直接返回 `CREATED`。
- 200 用户实验是短突发正确性实验。记录的耗时包含该场景观察范围，不是持续 RPS、P95/P99 或可承诺容量；不得将它换算为 Go 吞吐提升。
- 清理限定本次随机高 ID 的用户、商家、门店、活动、订单、审计、outbox、reservation 与准入键。成功轮先确认 owned source ID 已被 worker ACK，再清除持久生产者并等待行锁退出，从 XRANGE 按本次活动筛选精确 source ID 执行 XACK/XDEL，删除 owned Redis 键并验证 EXISTS 为零。共享 Stream/group、真实每日 ID 计数器及数据卷保留。失败轮的测试清理 ACK 不计为 worker 功能证据。
- 离页浏览器断言证明未再发出已计划轮询；请求取消的实现由 controller abort 与代次校验共同支撑。导航或整页刷新不持久保存尚未收到受理 ID 的恢复按钮状态；已创建订单可从本人列表找回。
- 支付、取消、自动关单、核销、退款、订单补偿及受控库存重建留后续任务；本报告不将这些能力列为已完成。Linux race、远端 CI 与持续负载的执行状态以整票报告为准，本页面报告不作已执行声明。
