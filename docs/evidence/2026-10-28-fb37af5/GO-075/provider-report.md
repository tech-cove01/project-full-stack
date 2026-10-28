# GO-075a：真实 Provider HTTP 适配器

起点：`fb37af549d5e8d0d3e5e3ed66a542fb7e99601eb`。本节仅为实现者自测；提交由主 agent 在独立审查后完成。对应 AI-01/02/03/11/13/14 的 Provider 边界，不将本地 HTTP 夹具称为真实模型质量。

## 交付行为

- `backend/internal/ai/compatible.go` 用标准库 `net/http` 请求文本 Chat Completions 和 float Embeddings；保留现有 `Provider` 接口，区分 Chat 与 embedding 模型名。启动只做本地配置校验，无付费探测；适配器不自动重试，不跟随任何 HTTP redirect。
- HTTPS 端点无 userinfo/query/fragment；测试允许 loopback HTTP。Key/模型/维度/token/timeout 均有边界。`DisableThinking=true` 才发送 DashScope `enable_thinking:false`，其它兼容 API 默认不发送该扩展。
- 每实例最多 3 个并发操作，等待许可受同一 context/timeout 约束；连接池也最多 3 条。Embedding 总输入最多 64 文本，按 10 条顺序分批，后一批失败不返回部分向量。每条最多 32768 bytes、总计 512 KiB；系统与用户提示合计最多 128 KiB。Chat 响应最多 512 KiB、完整答案最多 64 KiB；embedding 响应每批最多 8 MiB。
- 严格拒绝非完整 `stop` 结果、非 assistant/空回答/工具调用/多模态内容、未知行号/重复行号/缺行/错误维度/非有限数/零向量。按响应 index 恢复原输入顺序。解析后再次核对取消状态，不输出半成品。
- `ProviderError` 只有固定 code 和 HTTP status；网络及上游错误正文、URL、Key、输入不进入 error。Context 取消/超时仍可用 `errors.Is` 区分。
- `WithObserver` 按实际 HTTP 尝试记录 operation/model/outcome/duration/usage；无 HTTP 的本地拒绝或排队取消不计付费尝试。Usage 缺失/异常/失败为未知，不等于零费用；完整响应里可核对的 usage 不因内容被拒绝而丢失。取消成功只能证明客户端停止等待和连接被取消，不能保证供应商不计费。
- `ai.IndexModel` 使用可选接口保留旧 Provider 兼容性。真实适配器以规范化 endpoint、embedding model、dimension 的 SHA-256 形成 63 字符向量身份，适配现有 `embedding_model VARCHAR(64)`；Key 轮换、Chat 模型或 timeout 更改不改变向量身份，端点/向量模型/维度切换会改变。调用方持久化/搜索集成另见主报告。

## 实测

Windows Go 1.27.0；标准库 HTTP 服务器夹具，不需要 MySQL/Redis 或真实模型，不新增依赖。

```powershell
cd backend
gofmt -l internal/ai
go vet ./internal/ai
go build ./internal/ai
go test -count=1 -v -timeout 45s ./internal/ai
```

均通过。原始普通测试输出：[provider-final.txt](provider-final.txt)。适配器 10 个主测试，含配置/真实 HTTP 请求 JSON 与认证/23 文本分批乱序回组/响应坏值/无 usage/本地输入边界/301、307、400、401、404、429、500、503/两种 oversize/上游 context 取消与超时/恢复/向量身份/3 槽位并发与排队取消；已有输入/stub 测试一并通过。HTTP 429 夹具验证一次调用不重试，未声称打到真实供应商 429。

保留的失败：

1. [provider-cancel-fixture-initial.txt](provider-cancel-fixture-initial.txt)：取消用服务器未读取请求正文，HTTP/1 handler 等待 Context 而未触发后台读，测试在 cleanup 等待超时。夹具先消费其已知小正文，再等待断开；产品取消代码未更改。取消、父 deadline、Provider timeout 三项通过，并验证下一次请求可用。
2. [provider-duration-fixture-initial.txt](provider-duration-fixture-initial.txt)：Windows 快速请求记录 `Duration=0`，断言要求大于零错误。改为允许非负观测值，不捏造最小延迟；其它场景当轮已通过。

未执行：本子任务未调用真实供应商、未测真实费用/引用质量、未运行 Linux race 或远端 CI。根交付应记录实际模型、Knowledge/index 版本、真实评测和未知费用，不用本地成功响应代替质量验收。

## 官方接口依据

提交前独立只读审查：provider_wiring检查适配器的context、HTTP结果、usage、错误脱敏和指纹；未发现阻断项，并独立重跑`go test -count=1 ./internal/ai`通过。根agent复核了完整实现。此审查不替代整票独立验收。

- [Alibaba Cloud OpenAI-compatible Embeddings](https://help.aliyun.com/en/model-studio/embedding-interfaces-compatible-with-openai)：`/embeddings`、float encoding、维度、index/usage 与 `text-embedding-v4` 每请求 10 行边界。
- [Alibaba Cloud OpenAI-compatible Chat](https://help.aliyun.com/en/model-studio/qwen-api-via-openai-chat-completions)：`/chat/completions`、system/user 文本、buffered response、token 限额、thinking 参数、choices/finish reason/usage。

以上仅作为 HTTP 协议依据；实际账号地域、模型可用性和价格由主任务沿用已配置 Provider 并实测，不据文档推断账号可用或零费用。
