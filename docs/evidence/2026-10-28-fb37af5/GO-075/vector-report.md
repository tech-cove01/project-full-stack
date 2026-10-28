# GO-075-vector：拒绝向量中的 JSON null

起点 `77aa810`。独立问题：Go `encoding/json` 将 `[1,null,3]` 解码至 `[]float32` 时把 `null` 静默转换为数字 0。提供商响应与 MySQL 已存向量均可能因此被当作合法数据，错误的向量仍可用于检索。

先增加提供商 `[1,null,3]` 与已存 `[1,null]` 两条边界回归；两个包分别复现失败，见 [vector-null-initial.txt](vector-null-initial.txt)。测试调用本机 HTTP 夹具，没有付费 Provider 请求。

解码先保留每个组件的可空性，再复制成业务使用的 `[]float32`；任一 `null` 立即拒绝。维度、有限数和整条向量非零检查继续生效。新增成功对照 `[1,0,-3]`，证明合法数字 0 不被误拒。

适用验证：

```powershell
cd backend
gofmt -w internal/ai/compatible.go internal/ai/compatible_test.go internal/aiknowledge/search.go internal/aiknowledge/search_test.go
go test -count=1 ./internal/ai ./internal/aiknowledge
go vet ./internal/ai ./internal/aiknowledge
```

两个包测试与 vet 通过，[vector-null-final.txt](vector-null-final.txt) 保留测试输出。未做额外付费模型、真实依赖、前端或负载测试：这是一项 JSON 边界校验修复，不改变正常数值向量的业务行为。
