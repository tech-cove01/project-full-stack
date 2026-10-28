# GO-074b-QA1：来源链接与真实 Vue 路由一致

源码问题：`Assistant.respond` 生成 `/knowledge/:id?version=...`，Vue只有 `/ai/knowledge/:id`，真实点击会落到首页兜底。此独立缺陷不与浏览器脚本或来源类型标签合并提交。

在既有 `TestIntegrationAssistantTurn` 采集真实 sources，新增精确 `href=/ai/knowledge/<documentId>?version=1` 断言。修复前真实 MySQL3381/Redis6451+stub 测试失败：[href-initial.txt](href-initial.txt)；只修改后端来源路径前缀后，同一测试通过：[href-final.txt](href-final.txt)。真实依赖测试精确清理其自有 FAQ/会话/dedupe，未运行 API worker 或付费Provider。

```powershell
# backend/；显式专用3381/6451环境、GO_INTEGRATION=1
go test -count=1 -timeout 1m ./internal/aichat -run '^TestIntegrationAssistantTurn$' -v
```

此修复保留字符串文档ID与发布版本；不扩大来源权限或修改知识内容。真实Vue来源点击复验由GO-074b专项脚本在root准备的真实Provider环境执行，不能用本次stub通过冒称页面已经通过。父SHA及最终交付SHA由根审查提交后记录。
