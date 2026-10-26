# GO-030 / 受理 → Stream 成单与本人订单交付

2026-10-26，状态：**自测通过待独立核验**。开始 SHA `e410c71d9d8dfeaf654e203e439fa3f871c9afd4`，依赖 GO-021；用户已将此前批次交其他 agent 核验并授权继续。本票交付异步建单及本人页面，不将支付、取消或恢复全流程记为完成。

## 分功能提交

| 子功能 | 完整交付 SHA | 可观察行为 / 详细证据 |
| --- | --- | --- |
| GO-030a 准入与 ID | `f86a97fc563d526dba5c74c9e62f76d6167d3da6` | 八键 Lua 原子预占、稳定受理 ID、下架后找回；[准入报告](admission-report.md) |
| GO-030b 持久结果与本人 API | `0e5f5913d2c548f96fc7f2858c6e7f1bc008cba5` | 库存/订单/审计/outbox 同事务，本人详情和分页、持久拒绝；[落单报告](order-report.md) |
| GO-030c Stream 消费与生命周期 | `871773efe0693ec6c05ae29ee84736146beab82c` | 持久结果才 ACK，未知结果留 PEL，公平读取/接管及安全关池；[消费报告](stream-report.md) |
| GO-030d 用户页面与真实联调 | `a7e45fd6ab9d990dfb47defe8c28e6431daffa53` | 抢券响应丢失后恢复、订单详情/本人分页/越权错误；[页面报告](ui-report.md) |
| GO-030e 真实集成 CI | `c87cd195a40710e948fe8eff0a294414b0ec8735` | 新增三个订单包的真实依赖/race步骤；本地仅静态解析，远端未运行；[CI说明](ci-report.md) |

各子功能自测后分别提交；最后另行提交本报告、进度与环境收尾证据。期间核验 agent 的 GO-020/021 报告、GO-021-QA 脚本/CI修正及合并提交进入共享 main，已保留其历史；没有将其归入本票实现或使用旧核验代替 GO-030 核验。`git diff 871773e a7e45fd -- backend-go` 为空，页面交付未改变已测 Go 源码。

## 行为与验收范围

三个本人路由为 `POST /voucher-order/seckill/{id}`、`GET /voucher-order/{id}`、`GET /voucher-order?current=1&pageSize=10`。身份取服务端登录态；受理返回字符串 ID，详情区分 PROCESSING / CREATED / FAILED，只有持久订单才能展示金额、门店及期限。PROCESSING/FAILED 的业务状态显式 null；列表为本人 DB 订单、字符串 total、稳定时间/ID倒序，列表无核销 token。

新订单使用 DB 时间和售价/门店/核销期快照，`pay_deadline_at=min(DB NOW()+GO_ORDER_PAY_TIMEOUT, redeem_end_time)`；配置默认 15m，仅允许 1s—24h 的整数秒，配置变动不重算旧单。扣库存、订单、USER 创建审计和 ORDER_CHANGED 同提交。重复消息读当前状态，不将 2/3/4/6 改回 1；生成列只读。固定连接 GET_LOCK 保护跨订单/拒绝表的 ID，锁释放不确定时丢弃物理连接并返回未知，不 ACK。

明确拒绝先持久化唯一 RESERVATION_REJECTED，才可返回 FAILED 并 ACK；**补偿事件仍 PENDING，本票不回补 Redis、不删除资格或 accepted 映射**。ORDER_CHANGED 的交易投影同样待后续票处理，活动 relay 不认领它们。没有 DB 持久依据的 Redis FAILED/CREATED 都不能证明成单、失败或允许释放。

| 验收映射 | 本票实际边界 |
| --- | --- |
| MIG-GO-03 | 订单字符串 ID/金额、上海时间、null/省略、数组/total、分页；不包含 SSE |
| MIG-GO-04 / P1-7 | 建单事务回滚、未知结果保留、生成列及当前锁定读；不包含补偿去重/库存重建 |
| P2-1 | 真实同账号20请求与200用户抢100库存，按唯一ID对账；多个独立活动和两时区轮次 |
| P2-2 | 真实提交前和提交后 ACK 前的受控 context 取消/新 consumer 接管子集；未做 OS kill 或生产60秒等待 |
| P2-3 | 独立已关闭DB池连续8次失败仍保留PEL，健康实例恢复；未做物理断库/完整review重放 |
| P2-7 | 真实Lua所有依赖错类型、坏参数、缺键等拒绝且无部分业务写入，修复后可受理 |

P1-2—5 的支付/关单/核销/退款、P2-4 的 Redis 重启、P2-5 的交易限流及 P2-6 的持续负载未由本票实现或核验；不能据子集结论称整个交易验收通过。

## 实际命令与结果

隔离 MySQL 3308 / Redis 6380，Go API 8082 / Vite 5174；原项目 MySQL3307/Redis6379未修改。工具为 Go1.27.0 windows/amd64、Node24.15.0、系统Chrome；镜像声明及各源码 SHA256、运行二进制 SHA256 见 [运行来源](runtime-source.json)。二进制 revision 是 `f86a97f-go030-worktree`，表示当时工作区构建，不冒充干净 f86a97f 源码；其生产 Go 代码与 GO-030c 一致。以下凭据由环境提供，不写入证据。

在 `backend-go/`：

```powershell
go mod verify
gofmt -l .
go vet ./...
go test -count=1 ./...
# 显式隔离 MYSQL_* / REDIS_* / GO_ENV=development / GIN_MODE=release
$env:GO_INTEGRATION='1'
go test -count=1 -v ./internal/orderadmission
go test -count=1 -v ./internal/order -run TestIntegrationOrder
go test -count=1 -v ./internal/orderstream
```

真实 order 测试会写自己活动的共享 stream.orders 条目，必须先停 API 消费者，不能边运行应用边执行；admission / orderstream 测试使用私有 Stream。所有夹具按精确 ID 清理，不回拨真实序列。

| 验证 | 实际结果 / 原始证据 |
| --- | --- |
| 普通 Go / 模块 / 格式 / vet | 全部 PASS；[最终日志](go-validation-final.txt) |
| 真实准入 | 9准入、4 ID、1恢复主组 PASS；[原始日志](admission-real.txt) |
| 真实落单/API | 12主组、accepted竞争另4子组 PASS；[最终日志](order-real-final.txt) |
| 真实消费 | 8主组及2普通单测 PASS；[最终窗口时间线](stream-window-final.txt) |
| 前端类型/构建/格式 | PASS；[构建](frontend-build-final.txt)、[格式](frontend-format-final.txt) |
| 真实Vue→Go→Stream→MySQL | UTC/上海各30项 PASS，cleanup=true；[UTC](ui-utc-final.json)、[上海](ui-shanghai-final.json) |
| 商铺浏览回归 | 诊断副本保留原14项断言，14 PASS；[结果与边界](shop-diagnostic-report.md) |
| 启动/关闭回归 | 6场景 PASS；正常退出0、未强杀、端口可重绑；[原始结果](bootstrap-final.json) |
| 新订单 CI 配置 | PyYAML解析 PASS；Linux race/远端未运行；[静态记录](ci-yaml-check.txt) |

根目录的页面与关闭命令：

```powershell
node --check scripts/go-order-ui-smoke.mjs
# 同上隔离依赖，另提供 PLAYWRIGHT_MODULE / CHROME_PATH
$env:BROWSER_TIMEZONE='UTC' # 另一轮 Asia/Shanghai
$env:UI_EVIDENCE='docs/evidence/2026-10-26-e410c71/GO-030/ui-utc-final.json'
node scripts/go-order-ui-smoke.mjs
python scripts/go-bootstrap-smoke.py --binary backend-go/bin/api-go030.exe --output docs/evidence/2026-10-26-e410c71/GO-030/bootstrap-final.json
```

`frontend/` 执行 `npm run build` / `npm run format:check`。UI脚本需先启动本票 API/Vite，关闭脚本使用独立临时 API 端口。页面每轮的20同用户请求只有1个新ID，200用户得到100唯一受理/100售罄并最终100单，另1张券验证真实受理响应丢失及下架后找回，共102条worker消息；清理前逐条XPENDING为空。分页12条SQL单和PROCESSING查询夹具单独标记，不算worker成单。

## 保留的失败与限制

- [Get两次读取间提交](order-get-race-initial.txt) 曾将刚提交的本人单错判404；修复为读取当前完整记录并校验owner。[accepted恢复/重用旁路](order-accepted-race-initial.txt) 四例曾返回错误绑定ID；修复两种入口的DB/拒绝/预约绑定检查。真实最终回归全部通过。
- 初轮UI行为通过但按钮溢出卡片，保留旧截图；修复换行后重新完成两时区30项及最终截图检查。另保留[Vite失联](ui-vite-unavailable.json)及[商铺初次分类等待超时](shop-regression-utc.json)，重启后未复现；缺少同期诊断，不能证明二者因果，也没有把旧FAIL覆盖为PASS。
- 故障窗口采用测试私有屏障和context取消，新consumer在测试里claimIdle=0；生产为60秒。没有OS强杀、物理DB中断、Redis重启/灾难丢失、Linux race、远端CI或持续负载成绩。200用户为短突发正确性，不能换算容量或提升比例。
- reservation24小时、accepted7天；只有索引而没有持久事实/预约绑定时，详情可能不可见。这不表示失败或允许释放，完整review/对账归GO-033。Redis序列丢失、Java旧gate接管与正式双后端收敛仍须GO-033/071策略。

## 收尾与下一票

[DB清理检查](cleanup-db.txt)为本票用户/商家、Stream夹具券及PENDING/PROCESSING outbox均0；只读Redis检查共享Stream长度0、group pending0、私有测试Stream无残留。API/Vite无监听；独立Go MySQL/Redis使用60秒预算停止，均Exited0、OOM=false，卷保留；原项目两容器仍healthy。[收尾记录](cleanup-final.json)。

下一票 GO-031 按模拟支付、主动取消、超时扫描和相应页面继续拆小提交。必须沿用旧单DB期限、当前状态条件和版本递增，审计/outbox同事务；新建路径按券→活动库存→有效订单加锁，新增生命周期事务要统一锁序，避免订单/库存锁反转。完整取消/拒绝补偿、review和库存重建归GO-033，未核验前仍不开放重建入口。本票没有新DDL或依赖，不改Java；回退须停止订单消费者并保留未完成PEL/outbox，再按依赖倒序可追踪revert，不能删卷代替回退。
