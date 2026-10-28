# GO-075d：真实 HTTP 生命周期独立审查

2026-10-28，独立审查 agent `/root/provider_adapter`。只读核对测试代码、执行计划、原始结果及隔离 smoke helper；没有再次付费调用，也未改写原始记录。独立重跑 `go test -count=1 ./internal/aichat` 通过（0.206s，付费测试保持 opt-in 跳过）；Python helper 的 AST 语法解析通过。

依据：[执行前计划](lifecycle-plan.md)、[原始 JSON](lifecycle-real-initial.json)、[原始测试输出](lifecycle-real-initial.txt)。原始 JSON SHA-256：`e20341bc40a1af46e50c6b0b23c5238218cedb4799485ec7c6c076b3fb71ff7d`。审查的 `provider_lifecycle_integration_test.go` SHA-256：`691cbfe868585b5e5e9ad567cfa45b57119554f3fffbbb449b8fe7ecc9353854`。

## 生命周期结果

真实 MySQL/Redis、实际 loopback Gin HTTP 服务器、`http.Client` 鉴权 POST SSE、真实兼容 Provider。不是直接调用 Emit 的30题质量集，也不是预造成功 JSON。9 次实际 HTTP 观察：准备 Embedding 1 次，四个进入模型的场景各 Embedding+Chat 2 次，最后预算拒绝 0 次。已知 usage 合计 415 tokens；取消/超时两次结算未知，不计为免费。

| 场景 | 独立判定 | 代码断言与实际记录 |
| --- | --- | --- |
| client_abort | PASS | Chat 调用入口发信号，100ms 后取消实际客户端 context；若模型此前已完成测试会失败。实际观察 cancelled（100.5547ms），客户端中断；无 answer/done，assistant 历史0，lease/dedupe删除，逻辑分钟/每日预算均0。服务器 capture 可记录断开后尝试发送的 error，不宣称断开的客户端实际收到该 error。 |
| provider_deadline | PASS | 仅测试包装器在实际 Chat 调用前设置20ms context deadline；实际 HTTP 观察 timeout（20.5164ms）。客户端连接正常收取 error，无 answer/done/assistant，lease/dedupe删除，预算0。它证明受控远端请求期限传播，不证明已等待生产45s整体超时。 |
| session_revoked | PASS | 使用真实 HTTP 登出当前Token；Provider Chat 正常成功（152输入+12输出tokens），最终身份守护丢弃结果。HTTP仅 error，无 answer/done/assistant，lease/dedupe删除，预算0。不是用模型失败代替撤权验证，也不宣称登出即时停止了上游生成或计费。 |
| recovery | PASS | 同一用户预先准备的另一有效登录Token，在同一聊天会话继续请求；Provider成功，唯一 answer/done、唯一assistant历史，lease释放，成功dedupe保留，逻辑分钟/每日预算均1。 |
| budget_reject | PASS | 同用户新消息触发测试的1/min、1/day限额，仅 error；新增模型尝试0、assistant仍1，失败dedupe删除，lease释放，预算维持1。这里只证明配置成1的逻辑预算，不是生产全局费用熔断。 |

断言在 handler 完成后查询真实 Redis 与历史，失败回合不能靠“无异常”通过。测试共用用户与会话，因此前三个失败后的0条assistant及预算0，再到恢复后的1条/预算1，能证明占位释放及成功只记一次。最后核对双方订单仍为初始待支付；只覆盖这些合成订单的无写副作用。HTTP观察确认实际发起了客户端 Provider 请求，入口信号不等于供应商确认已开始模型计算；取消后远端计算是否继续及账单仍未知。

## 隔离 smoke helper

`scripts/go-ai-isolation-smoke.py` 显式要求专用 MySQL/Redis配置，用合成 Key 和 loopback:1 的 Provider端点，串行运行 AI关闭、缺Key、端点不可达配置三种实际API启动与交易演示；不继承真实Key去发付费请求。检查 readiness、能力状态、交易脚本真实结果，以及正常退出/无强制清理/端口可复用；固定输出不含env/原始异常，未发现 P1。

[既有隔离实跑](isolation-final.json) 三种模式各9项交易检查、exit0、无强制清理且端口可复用。该记录来自主 agent 先前的等价本地 helper；公开脚本的最终实跑与版本对应由主报告记录。本独立审查仅运行语法解析，不借用这份结果宣称已重新执行最终脚本。

初审指出：输出目录保护只检查 `isolation-final.json`，未写final的中断轮次仍可能被覆盖，建议最终 helper 对非空目录直接拒绝。该点及修复复核在最终主报告/提交中记录。端点不可达模式未同时发起付费模型请求；结论限定“该配置下核心交易启动/运行仍可用”，不冒充远端中断时并发AI与交易压测。

无新增 P1。此处不覆盖真实429、生产45s总期限、浏览器取消、OS强杀、持续并发或提供商结算；质量/来源结论另见 [30题逐项复核](eval-review.md)。
