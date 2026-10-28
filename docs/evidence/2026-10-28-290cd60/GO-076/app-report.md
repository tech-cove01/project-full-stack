# GO-076a2 实际API和第三消费组接入

应用组装搜索Client/Service并注册匿名GET `/notes/search`，与现有`/notes/:id`共存。只有本地ES配置就绪且配置Kafka时才创建第三个`community.search-v1`组；原follow/feed协议和MySQL事实不变。启动不访问ES、不自动建库/重建索引，ES关闭或不可达不阻断核心readiness；搜索请求明确失败。

启动前API/消费者停止，实际执行一次CLI rebuild成功：[首次重建](rebuild-bootstrap.txt)。重建后专用API8086/Vite5176用于上海/UTC页面，两时区各25项通过；真实登录→POST笔记→outbox→Kafka→三组收据/实际ES查询及实际offset门槛见页面报告。二进制revision为`go076-worktree`，最终源码/hash在票级收尾记录，不伪称对应单一Git提交。

本地组装纯测试和普通Go全套/vet通过。独立只读审查provider_adapter确认local-only construction、条件组装和关闭顺序，无阻断问题，并重跑app/config Search测试通过。正常关闭先排空HTTP，再取消并等待worker Reader关闭，最后释放SQL/Redis；ES空闲连接在Run退出时释放。最终实际关闭记录另在总报告。

此接入不替代独立验收；尚未声称本票远端CI/race、真实ES停机、浏览器之外的平台或持续容量已通过。ES资源、查询、同步和页面各自分步提交。
