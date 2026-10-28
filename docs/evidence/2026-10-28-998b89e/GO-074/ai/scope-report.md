# GO-074b-QA2：来源类型标签遵守公开契约

独立问题：Go检索来源使用`PUBLIC/SHOP_PUBLIC`，两个Vue页面却按`PLATFORM`判断平台类型；公开详情DTO还没有scope字段，详情页无法判断。该问题独立于QA1 href路径，不合并提交。

必要变更：公开详情`PublicView`和SQL选择/返回补充已有`d.scope`；不改公开状态、版本或权限条件。AssistantView与PublicKnowledgeView按`PUBLIC`显示平台类型，其余既有SHOP_PUBLIC显示门店规则。无需迁移、额外模型调用或新的平台管理接口。

在既有知识权限/版本集成中新增独立子组，精确核验两种公开scope的实际JSON。修复前：[scope-initial.txt](scope-initial.txt) FAIL（scope缺失）；修复后完整原测试七子组：[scope-final.txt](scope-final.txt) PASS，包含原权限撤销/私密来源/发布失败/版本停用行为。环境为专用MySQL3381/Redis6451与stub，不付费；只调整并恢复该测试的自有文档scope，精确清理。

普通知识模块测试、vet、gofmt、前端类型检查/构建、全前端format:check均通过，命令结果摘要：[scope-checks.json](scope-checks.json)。未将本次构建当作浏览器已验收，真实PUBLIC/SHOP_PUBLIC标签与来源点击另由GO-074b矩阵执行。根agent非作者代码审查通过后独立提交，最终SHA由根交接记录。
