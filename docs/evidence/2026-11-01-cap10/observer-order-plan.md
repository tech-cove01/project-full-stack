# CAP10-OBSERVER-ORDER：保留夹具原序的向量摘要

数字笔记 ID 跨越位数边界时，夹具原序可能与字符串字典序不同，例如 `9998,9999,10000,10001`。ready 审计按 manifest 原序编码；此前 live 摘要却使用引擎内部已排序的 pairs，导致同一组未变化的 v0 事实出现不同 SHA。该失败属于观察器身份核验错误，不能据此判断 API 性能或数据不一致。

引擎现在分别复制两份 pairs：原 `pairs` 继续按字典序排序，供原有 SQL 遍历与向量算法使用；新增不可由调用者切片修改的 `digestPairs` 保留 manifest 原序，仅供 live 摘要编码。`like-vector-digest-v1` 的字段、JSON 字节规则及成功 SQL commit、整样本 Accept 后才发布摘要的顺序保持不变，不新增查询。

验收覆盖跨四/五位的非字典序输入、全 v0、混合 v2，以及版本总量和 active 总量相同但关系位置不同的向量。独立字节金样校验 live 摘要；SQL 返回事实行倒序，构造后修改调用者切片，同时断言内部字典序与事件计数保持原值。后三项避免通过排序整个协议、依赖 SQL 行序或共享切片来掩盖问题。

同一新增回归在旧源码上真实失败：1 个顶层测试、3 个子例均为摘要不匹配，原日志保留。修复后，与现有向量、SQL adapter、factory 和摘要测试一起执行的脚本级 `-race` 检查通过：37 个顶层测试、92 个子例，包耗时 2.012 秒；同范围 vet 通过。这里的 SQL driver 和 Kafka reader 是内存夹具，没有连接真实 MySQL/Kafka。未重跑业务全套，现有 backend CI 也不自动包含这些仓库外层脚本测试，不能将本地检查称为新 CI 或云端互通通过。

从 `backend/` 可复现相关检查：

```sh
GO_INTEGRATION=0 go test -mod=readonly -race -count=1 -p 1 -timeout 2m -v \
  ../scripts/cap10-downstream.go ../scripts/cap10-downstream_test.go \
  ../scripts/cap10-downstream-vector_test.go ../scripts/cap10-downstream-vector-sql_test.go \
  ../scripts/cap10-downstream-vector-wire_test.go ../scripts/cap10-downstream-vector-digest_test.go \
  ../scripts/cap10-downstream-vector-order_test.go
```

本次仅修工具并另构建 Linux/amd64 observer，原 observer 产物与失败证据保留；容量 API 的 `c464c1fa4d153806fb44ac746312552655ebda63` 源码及二进制不变。新工具仍须重新绑定部署、空载和后续窗口证据；本次纯测试与构建不构成稳定 QPS、正式组或云端恢复成功证据。
