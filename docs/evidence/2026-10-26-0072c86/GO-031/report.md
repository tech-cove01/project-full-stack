# GO-031 / 模拟支付、取消与超时关单交付

2026-10-27收尾，运行证据执行于2026-10-26（Asia/Shanghai）。**自测通过待独立核验**，开始 SHA `0072c867ab1f34d7219e815d04779c651c55d9d0`，依赖 GO-030。用户授权继续下一票；未把前一票或本票的实现者自测改成独立验收通过。

## 按小功能提交

| 子任务 | 完整 SHA | 行为与证据 |
| --- | --- | --- |
| GO-031a 模拟支付 | `a5125d6ef8c7189e024a58dc2dd0fbc49cc78c47` | 本人、状态/旧期限条件、随机凭证、审计/事件同事务；[报告](pay-report.md) |
| GO-031b 取消与过期事务 | `6701e62a5d53d4132c4e9b5f81debdd82c6e2ae4` | 主动取消/SYSTEM关单，DB库存精确回补一次、版本与审计/outbox；[报告](cancel-report.md) |
| GO-031c DB扫描与应用组装 | `8f82fa51f22926225d836d3cb8838aff66cfee02` | 公平批量扫描、真实生命周期联调、进程越期重启；[报告](timeout-report.md) |
| GO-031d 页面与延迟确认修复 | `a9662cfd393cf434bec5b4b9b6b252845e843b2f` | 支付/取消确认、本人凭证、超时观察、不确定写后查询；[报告](ui-report.md) |
| GO-031e CI覆盖 | `43eb901e6bc63033ea291691f253559b69e3857a` | 新超时包加入真实依赖/race步骤，远端仍未执行；[说明](ci-report.md) |

功能分别自测后提交；本报告、进度与收尾另作文档提交。没有新DDL、依赖或Java改动，不改写此前历史。

## 业务结果与验收映射

`POST /voucher-order/{id}/pay` 和 `/cancel` 均从登录态取得本人身份。未登录401、外人/缺失404、非法参数400、状态/期限冲突409、未知依赖/事务结果503。本人状态2重复支付、状态4重复取消返回当前结果；不同动作的冲突不会伪装为幂等成功。

支付只允许状态1且写入时DB `NOW(3)<pay_deadline_at`，不重算旧金额或期限；状态改2、pay_time、256位随机十六进制token、version+1、ORDER_PAID审计和ORDER_CHANGED同事务。凭证仅本人当前已支付详情/支付结果可见，列表、终态、审计和outbox不含凭证。历史已支付非空token不轮换，不能据新生成强度宣称旧数据全部修复。

取消/超时只将状态1改4，写时间/原因并清token；DB库存必须条件 `stock>=0 AND stock<total_stock` 精确加1，否则连同状态/版本/审计/事件回滚。主动取消actor为本人，超时actor为SYSTEM/0。**Redis库存、资格和accepted映射尚未释放；对应恢复事件仍PENDING，完整补偿归GO-033。** 不能承诺取消后立刻可重新抢券。

写事务显式READ COMMITTED，统一券FOR SHARE→活动库存FOR UPDATE→订单FOR UPDATE，再核对当前owner、状态、版本及DB期限。事务内不混用全局db，等待锁之后重新用DB时间判断，不拿事务开始或进程时钟当支付依据。扫描只发现候选，真实关单事务再次复核；未知结果留在DB待重扫。

| 验收 | 本票证明的子集 |
| --- | --- |
| MIG-GO-03 | 新支付/取消DTO、字符串ID/金额、上海时间、本人凭证隔离、null/省略；无SSE |
| MIG-GO-04 / P1-7 | 状态/DB库存/审计/outbox原子、错误回滚、当前读与条件更新；不含Redis补偿/重建 |
| P1-2 / P1-3 | 支付与主动取消的DB分支，取消后的生成列资格释放；完整重买、核销和退款尚未证明 |
| P1-4 | DB截止边界、支付/取消与支付/超时竞争、无提醒扫描、停机越期后恢复，旧期限不受新配置影响 |
| P1-5 | 支付/取消同动作并发幂等子集；核销/退款互斥属于GO-032 |

## 已执行结果

环境：Go1.27.0 windows/amd64、Node24.15.0、系统Chrome；隔离MySQL3308/Redis6380、API8082/Vite5174。源码/二进制SHA256与配置来源见 [runtime-source.json](runtime-source.json)。运行revision为 `6701e62-go031-worktree`，是含最终超时组装的工作区构建，不冒充干净6701e62；后续页面/CI提交未改变backend-go生产代码。

| 验证 | 结果及原始文件 |
| --- | --- |
| 模块/格式/vet/普通Go全套 | PASS；[最终日志](go-validation-final.txt) |
| 真实支付 | 8主组PASS，含20并发、锁等待跨期限、回滚/碰撞/权限；[最终日志](pay-real-final.txt) |
| 真实取消/过期事务 | 11主组PASS，含20取消、12轮支付/取消及8轮支付/过期；[最终日志](cancel-real-final.txt) |
| DB扫描发现/调度 | 6真实SQL主组及7普通主组PASS；SQL processor不冒充真实业务事务；[日志](timeout-real-final.txt) |
| 扫描→真实CancelExpired | 3主组PASS：库存/审计/事件、双实例一次转换、持久故障回滚后续进展及重扫；[最终日志](timeout-lifecycle-real-final2.txt) |
| 真实停止/越期/重启 | 18检查PASS，两进程exit0、未强杀、清理成功；[进程与DB时间证据](restart-initial.json) |
| 启动/关闭 | 6场景PASS；[原始结果](bootstrap-final.json) |
| 真实Vue生命周期 | UTC/上海各25 PASS、每轮7真实成单消息ACK、pending0、清理0；[UTC](ui-utc-final.json)、[上海](ui-shanghai-final.json) |
| GO-030差量回归 | 原真实落单12/Stream8主组PASS；[日志](go030-real-regression.txt)。原页面30项PASS，含200人抢100库存；[页面](order-regression-utc.json) |
| 前端类型/构建/格式与脚本语法 | PASS；[构建](frontend-build-final.txt)、[格式](frontend-format-final.txt)；两JS `node --check`、Python AST解析通过 |
| CI声明 | YAML解析PASS；[静态日志](ci-yaml-check.txt)。Linux race/远端CI未执行 |

在backend-go目录执行（先通过环境注入隔离依赖；API消费者须停止）：

```powershell
go mod verify
gofmt -l .
go vet ./...
go test -count=1 ./...
$env:GO_INTEGRATION='1'
go test -count=1 -v ./internal/order -run TestIntegrationPay
go test -count=1 -v ./internal/order -run TestIntegrationCancel
go test -count=1 -v ./internal/ordertimeout
go test -count=1 -p 1 -v ./internal/order ./internal/orderstream -run 'TestIntegrationOrder$|TestIntegrationOrderStream$'
```

根目录以同一隔离环境运行 `python scripts/go-order-timeout-restart-smoke.py --binary backend-go/bin/api-go031.exe --output <独立证据文件>`，另运行bootstrap脚本。API/Vite启动、默认新单期限15m时，提供Playwright/Chrome路径，以 `BROWSER_TIMEZONE=UTC` / `Asia/Shanghai` 和不同UI_EVIDENCE运行 `node scripts/go-order-lifecycle-ui-smoke.mjs`；原订单页面回归为 `go-order-ui-smoke.mjs`。前端目录执行 `npm run build` / `npm run format:check`。具体隔离变量格式沿用 `.env.example`，证据不记录密码、手机号、登录码或完整核销凭证。

## 失败窗口、夹具与限制

- [UI初始FAIL](ui-utc-initial.json)前23项通过：真实行锁延迟支付、浏览器响应已丢、首次真实GET仍1，放锁后真实POST提交2；后续GET已确认2，旧动作错误仍遮住凭证。[遮罩失败图](ui-utc-initial-failure-masked.png)保留。修复为每次有效本人GET统一处理pendingAction；相同用例在两个时区各25项通过，没有自动重发POST或伪造成功响应。
- scanner真实生命周期首轮一组失败为测试夹具同秒写入相同deadline、RowsAffected0；只将未来对照从3600改1800秒后通过。后续补owned guard防测试碰未知行；initial/final/final2全部保留，未把夹具误报写成应用缺陷。
- 重启实验用真实API发布/抢券/Stream成单；完整退出后的DB时刻严格早于原10秒deadline，随后离线跨期限，1h配置重启仍关旧单。正常退出与进程重启已测，OS强杀、物理断库、Redis重启/灾难恢复未测。
- UI仅对owned旧单SQL调整截止作为过期时钟夹具，另用真实库存行锁延迟支付；不使用SQL伪造支付/取消状态。原GO-030分页12单和PROCESSING仍为查询夹具，200用户为短突发正确性，不换算持续容量。
- 生产scanner每批100、每轮后5秒，scan2s/item5s/poll10s。游标推进到已尝试行，失败留DB并可回访；测试验证前100失败不饿死第101条。当前没有ZSet读写依赖，不删除猜测的key冒充提醒丢失实验。持续积压容量、Linux race、远端CI未执行。
- GO-032的核销、退款、商家订单/审计页面，以及GO-033完整补偿、review、受控重放/重建尚未实现；本票不开放重建入口，不宣称交易闭环全部完成。

## 收尾与下一票

API/Vite已停止；独立Go MySQL/Redis以60秒预算正常停止，均Exited0、OOM=false、卷保留，原MySQL3307/Redis6379仍healthy。[清理DB计数](cleanup-db.txt)与[收尾](cleanup-final.json)记录订单/PENDING事件/指定夹具0、共享Stream长度及pending0。删除的是自有实验数据，不能把清理后的事件0称为Redis relay已经补偿完成。

下一票GO-032按商家订单查询、门店核销、退款及审计/页面分小功能提交。继续使用实时逐门店授权及当前锁定读、统一券→库存→订单锁序；核销/退款以DB核销期和状态互斥，退款保留资格，审计/outbox与版本同事务。代码回退须先停超时worker，再按依赖可追踪revert；已提交订单状态/库存/outbox不会随Git回退撤销，不删卷或直接改回状态代替恢复。
