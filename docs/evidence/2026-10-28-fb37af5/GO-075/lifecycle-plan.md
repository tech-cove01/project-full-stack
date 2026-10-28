# GO-075d：真实 Provider 的 HTTP/SSE 生命周期诊断

本计划在执行前落盘，实际结果另存 `GO_AI_LIFECYCLE_OUTPUT`，不能用本计划宣称通过。需要显式 `GO_INTEGRATION=1`、`GO_REAL_AI=1`、真实配置和独立 MySQL/Redis；普通测试跳过付费请求。与真实30题评测共享 `GET_LOCK('go075_integration',30)`，禁止同时运行。

## 预算与样例

总期限4分钟；单 Provider 调用35s，Chat最大输出300 tokens；最多10次调用预留，计划9次实际 HTTP 尝试、已观察 token 总量不超过20000，并在下一调用前预留4096 token余量。取消与超时的结算可能未知，本诊断明确预留这两次尝试，并继续受总调用上限约束，不把未知算作免费。不请求真实429，不做付费并发负载。

| 步骤 | 计划模型请求 | 验证 |
| --- | --- | --- |
| 准备一条本人合成门店公开停车规则 | Embedding 1 | 真实发布与持久向量 |
| HTTP客户端取消 | 查询Embedding + Chat 2 | 实际loopback服务器的POST鉴权SSE；进入真实Chat 100ms后取消客户端context，上游调用观察为cancelled，无answer/done、无assistant记忆，lease/dedupe清除，逻辑预算退款为0 |
| Provider deadline | 查询Embedding + Chat 2 | 仅测试包装器给真实Chat调用20ms deadline，实际net/http尝试必须有timeout观察；SSE明确error，旧成功不得替代此项 |
| 真实生成时退出登录 | 查询Embedding + Chat 2 | 同一鉴权Token通过真实HTTP退出，远端Chat正常结束但最终身份复核丢弃答案；无answer/done/assistant记忆，所有占位退款 |
| 同一用户另一个有效会话重试 | 查询Embedding + Chat 2 | 成功后只有一条assistant历史、一次answer/done，逻辑分钟/每日预算均保留1 |
| 再发新消息触发1/min、1/day上限 | 0 | SSE error，模型新增尝试0，不伪造完成 |

全部步骤共用一个真实Redis会话和限额1/min、1/day，每个失败后查询持久lease、精确clientMessageId去重键、预算计数和历史。准备两个同一用户的真实登录Token供撤权与恢复；第二次登录只重置本测试手机号的throttle，是明确夹具动作，不是产品鉴权限流结论。Token、验证码、完整手机号不输出或写证据。

进入Chat的通道与实际HTTP观察分别记录。若提供方在100ms中断窗口之前已经完成，标记本次条件未命中并失败，保留原始记录；不在模型完成后人为延迟返回来假装上游取消。deadline使用20ms受控调用期限，不宣称已等到生产45s整体超时。超时/取消后的远端是否计费未知，成功usage也只是提供方报告，费用需另行核账。

真实Gin HTTP SSE通过实际loopback `httptest.Server` 和 `http.Client`，HTTP鉴权、退出和开发登录用真实MySQL/Redis，不返回mock成功JSON。公开业务订单在诊断前后保持未支付状态；模型无写工具。只删除自己fixture行、会话、session/code/throttle、精确dedupe与本人budget键，不flush/truncate，不动共享业务数据。

## 命令（执行方填写专用依赖与Provider环境）

```powershell
cd backend
$env:GO_INTEGRATION='1'; $env:GO_REAL_AI='1'
$env:GO_AI_LIFECYCLE_OUTPUT='<独立原始结果文件>'
go test -count=1 -timeout 5m ./internal/aichat -run '^TestIntegrationRealProviderHTTPLifecycle$' -v
```

需要分别记录首次失败与追加成功文件；成功仅为上述HTTP/终态/持久不变式的实际范围，不能代替真实模型30题引用支持度评测、真实429或生产故障。
