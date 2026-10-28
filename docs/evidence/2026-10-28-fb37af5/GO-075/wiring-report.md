# GO-075b：可配置的真实 Provider 接入

- 起点：`fb37af549d5e8d0d3e5e3ed66a542fb7e99601eb`。依赖 GO-075a 兼容适配器与已核验的 GO-040—042 协议范围；本记录为实现者自测，不是独立验收。
- 对应 AI-D01、AI-01/02/03/11 的配置与接入部分。真实模型质量、真实存储与付费调用由本票其它证据单列；此处不以 httptest 或本地配置检查代替真实 Provider 证据。

## 行为

`AI_ENABLED` 默认 false，应用不再无条件启用 stub。显式启用后可选 `stub` 或 `openai-compatible`，索引与问答共用同一 Provider。无 Key、无模型、无地址、参数越界或适配器拒绝配置，只令 AI 不可用，仍完成核心 API 组装，不因可选远端模型中断登录、浏览和交易。

配置解析只返回字段名或固定原因，不把值、地址、Key 写入诊断。应用给知识服务传入非 nil 的 unavailable Provider，避免其测试用的 nil→stub 默认值悄悄启用 AI。知识编辑与已发布原文仍可用；需要模型的发布/重建/检索会明确失败。助手的已登录请求在访问会话或 nil Assistant 前返回 503，未登录仍先返回 401。

`/ai/capabilities` 区分 enabled（请求启用）、ready（本地配置可构造）、mode、Chat 模型、Embedding 模型与维度；真实 Provider 的 `providerHealth=unchecked`，说明远端可用性待请求确认。启动不请求付费模型，也不以配置正确宣称远端健康或真实质量通过。所有能力 DTO 均不含 Key、Base URL。默认禁用、配置不完整与 stub 均有明确原因。

`AI_TOP_K`、`AI_MIN_SCORE` 同时作用于助手检索及 `/ai/search` 默认值；请求仍不能改变门店/版本权限或降低阈值。`AI_DISABLE_THINKING=true` 仅在指定兼容提供方支持时关闭 thinking，默认 false 不发送该厂商字段。配置包含向量维度、最大输出 tokens 和 provider timeout；配置值不会从请求正文读取。

既有的 Chat HTTP 整体 45s、知识 HTTP 10s、Assistant 60s 和连接关闭取消行为保留；实际截止时间取各层最早者，`AI_CHAT_TIMEOUT_SECONDS=60` 不代表 HTTP 请求能持续 60s。Provider 的池在 Run 退出、HTTP 已完成或强制关闭后释放空闲连接；既有关闭测试覆盖强制关闭取消 handler context。

## 自测

在 `backend/` 执行：

```text
gofmt -l <本次 11 个已修改/新增 Go 文件>
go test -count=1 ./internal/platform/config ./internal/platform/app ./internal/aichat ./internal/aiknowledge
go vet ./internal/platform/config ./internal/platform/app ./internal/aichat ./internal/aiknowledge
go build ./internal/platform/app ./cmd/api
```

对应 [格式](wiring-gofmt.txt)、[测试](wiring-test-final.txt)、[vet](wiring-vet.txt)、[构建](wiring-build.txt) 均通过。

新增断言覆盖默认关闭、显式 stub 无凭据、真实配置参数、四类缺失必需项、无效开关/Provider/维度/输出/TopK/阈值（含 NaN/Inf）/超时与安全诊断；能力 DTO 分别检查禁用/stub/未配置真实/已配置真实状态与 Chat/Embedding 名称分离；禁用 guard 在会话访问前终止；本地构造真实适配器不调用网络。复用的关闭测试通过，包含已有请求 drain、强制关闭取消 handler、依赖池释放顺序。

最初一次 app 命令误在仓库根执行，Go 明确报告无根模块；改在 `backend/` 执行即通过，无产品修复。未据此记为功能失败或覆盖其它失败证据。

## 限制

这里的 tests 不调用远端模型、不验证真实 Key/配额/网络，也不验证已登录后的完整 API 交易隔离。真实评测与隔离集成结果见总报告对应原始证据；新代码远端 CI/Linux race 由交付者据实际执行单列。`ready` 仅本地配置就绪，不是整库索引完整性或远端健康探针；不兼容已发布向量的处理为另一个独立修复。
# 提交前独立审查补充

ai_eval只读检查config/app/HTTP与检索配置接入，确认默认关闭、缺Key和无效AI配置不影响核心配置加载，未就绪不会回退stub；独立重跑config/app/aichat/aiknowledge普通测试通过。根agent复核组装与HTTP守卫。真实API与交易隔离验证另列，不用此处普通测试替代。
