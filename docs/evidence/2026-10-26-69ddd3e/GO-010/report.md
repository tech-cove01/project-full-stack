# GO-010 身份链路自测

日期：2026-10-26（Asia/Shanghai）。状态：自测通过待核验。依赖 GO-001；开始 SHA `69ddd3ee001251b5a16ee252fbacf51130643630`，交付 SHA `cc0c6f920897b3ed65a4f080997a6c538fe0c2fa`。最终冻结二进制 SHA256：`CB2E9A7F74B5D1E551EF884B3577D964A29BFCA15586B188888C46879586A244`。

范围为四个 user API、验证码/用户/session 持久行为、已有 Vue 登录/恢复/退出与失效拦截。不新增页面、表结构、独立 refresh 路由或业务 worker。沿用原 Java 的正常手机号号段，修正旧 `[0-3,5-9]` 误收逗号；验证码 TTL120秒/节流60秒、session36000分钟、原始/Bearer authorization 与三字段 hash 兼容。Go 写入与续期 Lua 原子化，缺 TTL 的异常凭证拒绝授权/消费，不自动修复。

环境同 GO-001：Go1.27.0/Windows、Node24.15.0、真实独立 MySQL3308/Redis6380，Go8082、Vite5174、Playwright+本机 Chrome。使用 `backend-go/.env.example` 显式进程配置，无模型调用。

| 验收/命令 | 结果 | 原始证据与边界 |
| --- | --- | --- |
| gofmt / `go vet ./...` / `go test -count=1 ./...` | PASS | [单测输出](tests-unit.txt)，默认跳过 opt-in 真实集成 |
| `GO_INTEGRATION=1 go test -count=1 -v ./internal/auth -run TestIntegrationAuth` | PASS，7主组、wrongtype4子组 | [真实依赖输出](tests-integration-initial.txt) |
| `node scripts/go-auth-ui-smoke.mjs` | PASS，12项，初稿和最终冻结二进制分别运行 | [最终JSON](ui-final.json)、[最终截图](ui-final.png)、[初轮JSON](ui-initial.json) |
| MIG-GO-02 / MIG-GO-03（身份/序列化子集） | PASS | 真实 Vue 与服务身份、401、字符串大ID、省略null/保留空串/空数组/字符串total；业务金额/时间/SSE未执行 |

真实集成包括：验证码两个 TTL、错码不消费、12个同码并发仅一成功与重放拒绝、旧号复用；16并发 repository 首次建号仅一行；session 字段/25天TTL/续期、原始与 Bearer、退出只撤销当前session；`9007199254740993` 精确字符串、坏ID/缺字段/过期/随机session拒绝；production不返回或写入code；无TTL凭证拒绝且不改动；Redis错误类型无部分写；8读者×30次刷新与logout竞争后session保持删除。并发建号直接验证真实 repository 与唯一约束，其余主要经 httptest HTTP 观察，并精确清理测试自有夹具，无 flush/truncate。

浏览器脚本通过真实 Vite `/api/actuator/health` 与直接8082比对 Go 身份；登录、刷新昵称、退出清本地与撤销旧token；第二个session被服务端撤销后页面刷新触发401，跳回登录并清理 token / 最近订单。脚本不Mock API，也不记录真实格式凭证；创建的随机演示用户保留在隔离库。浏览器当时首页商铺仍待 GO-011，商铺404不计入本票已迁移范围。

首次 agent 全套单测受 sandbox Windows socket 权限限制，日志保留在 [失败证据](tests-agent-sandbox-failure.txt)；root同版完整单测与真实依赖实际通过。独立只读审查者复查源码/单测/夹具/脚本，独立执行mod verify/vet/fulltest/gofmt和真实7组集成均PASS，无阻断；浏览器由root执行，审查者静态核对脚本与JSON。logout并发不是hook命中的精确窗口，结合refresh Lua仅EXPIRE、没有HSET重建的静态审查说明其边界。

Linux race和远端CI尚未执行；新增CI配置会初始化隔离001-schema并以race运行auth真实集成。没有以历史Java或Stub成绩替代Go。业务表/Stream/outbox和后续角色/商铺/交易/AI不在本次验收内。回退可revert本票；无数据库结构变更，正常Redis登录态格式与Java兼容，异常永久凭证需人工检查，不能靠删除卷回退。

复现命令见 [运行文档](../../../development.md#go-迁移环境与启动)。UI环境需运行Go8082/Vite5174并指定Playwright模块，例如本机 `PLAYWRIGHT_MODULE=file:///C:/Users/19083/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright/index.mjs`、`CHROME_PATH=C:/Program Files/Google/Chrome/Application/chrome.exe`。
