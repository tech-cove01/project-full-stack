# GO-030 独立核验与修复

结论：**GO-030本票范围核验通过，发现的3处问题均已修复并复验，最终代码Linux CI通过**。核验基线：`0072c867ab1f34d7219e815d04779c651c55d9d0`。仅验收受理 → Stream → 持久订单/拒绝 → 本人查询与页面；支付、取消、超时、核销、退款、完整补偿/review/库存重建归后续票。另一个实现现场继续 GO-031，本轮在独立工作树与独立 MySQL/Redis 运行，不以其未提交内容作为本票成绩。

## 发现与独立修复

1. **P2 / 销售结束边界**：原代码在 NextID 与 SQL 查询前采样时间，Lua 继续用旧值；实际执行已越过销售结束仍可能扣库存并发消息，时钟超前时也可能提前受理。新增真实 Redis 用例先证明失败，然后改用 Lua 内 Redis TIME 对新受理作最终 `[begin,end)` 判断，createdAt 记录同次服务器时间。已有 accepted ID 仍优先恢复，结束/下架不会使原受理失联；所有预校验仍在首笔写入前。
2. **P2 / 恢复请求跨账号**：同源两个标签页中，A 的已受理响应丢失后，另一页登录 B，原页面恢复按钮会用当前 B Token 再次抢券。修复后恢复记录包含原 userId，每次确认当前主体并将同一已验证 session 绑定到 POST；同账号换新 Token 仍可找回原 ID。旧成功响应不能记住/跳转原订单，旧 401 不能删除新会话，当前会话的 401 仍是明确拒绝而非结果未知。
3. **P2 / 迟到退出响应**：真实 A 退出已经到后端，但响应到达前另一页切 B，原 finally 无条件清理会话并导航。修复为只清理原会话；出现不同的新 Token 时保留新登录、页面及提示状态，普通退出与真实失效退出仍可完成。

| 小任务 | 完整提交 SHA |
| --- | --- |
| GO-030-QA1 / Redis 执行时刻裁决 | `1ce0eb401f922d53ae3ebf491a28d1df2ea2c2a9` |
| GO-030-QA2 / 抢券恢复绑定身份 | `72645faf659b7496afb66b6879d8fe4b801df462` |
| GO-030-QA3 / 退出响应隔离 | `08d59533a5a76765824b22d57d061634ef0f72c5` |

没有新增 DDL、依赖或业务接口；保留失败原始结果，未 amend/rebase 实现者提交。最终源码与二进制散列见 [来源记录](source-manifest.json)。

## 验证

- Go 模块校验、gofmt、vet、全套普通测试与专用二进制构建通过；[普通测试](go-unit.jsonl)、[模块](go-mod.txt)、[vet](go-vet.txt)。
- 全新独立数据库执行版本 SQL，再校验重跑通过：[首次迁移](migrate-fresh.txt)、[重跑](migrate-repeat.txt)。
- 显式真实依赖，`go test -count=1 -p 1 -json` 串行执行 auth/shop/merchant/activity/activitypub/outbox/orderadmission/order/orderstream 九个包，无 fail/skip。交易部分为准入及ID/恢复原14组 + 新5组时钟边界、落单12组、消费8组；其余身份、门店、活动与outbox为回归。[完整真实结果](integration.jsonl)。
- 前端类型、构建、格式与脚本语法通过：[类型](frontend-build.txt)、[构建](frontend-vite-build.txt)、[格式](frontend-format.txt)。
- 最终源码真实 Vue → Go → Redis Stream → MySQL：UTC/上海各原30项订单页面回归通过，包含20同账号请求、200用户抢100库存、精确ID/金额/上海时间、真实丢响应恢复、本人分页和越权错误。[UTC](orders-complete-utc.json)、[上海](orders-complete-shanghai.json)。
- 最终身份专项 UTC/上海各9项通过：跨账号恢复、同账号新session、迟到成功、旧401、迟到退出、明确401及普通退出；使用真实登录/发布/受理/落单/退出，仅延迟或丢弃指定响应。[UTC](identity-complete-utc.json)、[上海](identity-complete-shanghai.json)。
- 六个启动/关闭场景通过，Go正常退出、未强杀；[bootstrap](bootstrap.json)。

真实消费核验包括：提交前/提交后ACK前的受控context取消、新consumer测试接管、已关闭独立DB池连续失败后健康实例恢复、非法消息/未知结果留PEL、公平轮转及真实状态幂等。独立审查确认RC事务、跨表ID命名锁与物理连接丢弃、库存/订单/审计/outbox同提交、明确持久结果才ACK、后台worker先退出再关池；未发现额外阻断。

## 失败证据与限制

- [销售期修复前](admission-boundary-initial.jsonl)：3个新边界失败；[旧createdAt夹具中间失败](admission-boundary-fixture-failure.jsonl)保留；[包级修复后](admission-boundary-final.jsonl)与本轮九包最终真实结果通过。结束边界用例证明执行时已到或超过截止被拒，不声称恰好采中同一毫秒。
- [跨账号修复前](identity-initial-utc.json)：实际观察到1次以B发出的POST；[退出修复前](logout-initial-utc.json)：新登录被清且页面被旧退出改写，均已精确清理。identity-final/identity-refined 为中间迭代记录，最终以 identity-complete 为准；refined 的UTC为6项，上海为新增迟到成功后的7项，不冒充同版最终证据。
- 200用户为短突发正确性，不是持续QPS/容量或提升比例。未执行OS强杀、真实COMMIT回包丢失、物理断库、生产60秒接管等待、Redis灾难恢复、完整补偿/review/重建；这些不能由当前PASS推导。
- 核验使用独立 compose 项目 `project-full-stack-review-go030`，MySQL3320/Redis6392、API8094/Vite5186。页面每轮精确清理；最终DB/Stream/PEL对账见 [清理核对](cleanup-audit.json)。序列不回拨、卷保留，不触碰GO-031或旧Java服务。先前 [清理快照](cleanup-before-logout-fix.json) 与 source-before-logout-fix 为中间审查时点记录，不是最终源码验收。
- Redis7.4支持在写脚本内使用TIME；效果复制/AOF传递实际写入，不在副本重新作时间裁决。[官方脚本复制说明](https://redis.io/docs/latest/develop/programmability/eval-intro/#script-replication)。

## CI 与并行交接

冻结0072c86已经实际完成Linux race和订单真实集成，三job成功，不再沿用交付时“远端未执行”的状态：[基线CI](https://github.com/tech-cove01/project-full-stack/actions/runs/40349194001)、[结构结果](ci-baseline.json)、[真实入口核对](ci-baseline-real-tests.json)。QA2代码72645fa的Linux CI同样通过：[修复CI](https://github.com/tech-cove01/project-full-stack/actions/runs/40352081306)。QA3最终代码08d5953三个job全部成功，包含Linux race及全部真实集成入口：[最终代码CI](https://github.com/tech-cove01/project-full-stack/actions/runs/40352995705)、[结构结果](ci-final-code.json)、[真实入口核对](ci-final-code-real-tests.json)。

GO-031仍是独立待验收范围。合入本轮修复时必须保留其新增支付/取消/超时与页面，不以本轮旧文件覆盖同路径；同步API session可选参数和原smoke新增身份分支。若主工作区同文件仍在修改，先保留隔离修复分支和清晰合入记录，不stash/reset其开发内容。

实际交接：修复与全部证据已推送 `origin/review/go030-0072c86`。准备整合时，main已到 `43eb901e6bc63033ea291691f253559b69e3857a`，实现者仍在修改 `frontend/src/api.ts`、`frontend/src/types.ts`、`frontend/src/views/OrderView.vue`、`scripts/go-order-ui-smoke.mjs`，因此保留其工作区，不强行合入main。已在隔离分支 `integrate/go030-review` 准备候选 `9949b1dd3022651359abc00d4812f6cdf462b251`：仅进度表冲突需合并已核验行与GO-031进行中行，源码自动合并；候选全套普通Go测试及vet通过。这是整合兼容检查，不是GO-031业务独立验收。GO-031提交上述同路径修改后，再在隔离候选合并该提交并验证差量，主工作区无重叠未提交修改时安全快进；保留原修复SHA，勿用整文件覆盖。
