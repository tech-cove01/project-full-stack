# GO-076c 物理 ES 停机与受控恢复计划

本脚本只允许本票专用项目 `project-full-stack-go076`，固定 MySQL3332 / Redis6404 / Kafka9099 / ES9202 / API8086 / Vue5176。执行前要求 root 确认其它页面/夹具已结束；不能与本票其它写入演练并行。使用 playwright-expert 的角色定位、Page Object、实际等待与匿名 trace；因物理停机共享本票 ES，刻意串行，不能按 Skill 示例并行破坏同一依赖。

`scripts/go-note-search-fault.mjs` 实际执行以下链路，不 mock 成功数据：

1. 独立合成用户/商家/门店，真实开发登录及发布两篇笔记；确认真实 Vue 搜索和三组已提交 offset。
2. 检查 Docker Compose 项目/服务标签，只停止 `project-full-stack-go076-elasticsearch-1`，API/Vue/MySQL/Redis/Kafka 保持运行；真实浏览器观察 503 与友好重试空态。
3. 再真实发布两篇笔记。确认 MySQL 四篇公开事实、社区 outbox `delivery_status='DONE'`、follow/feed 各四条 receipt，search 原两条 receipt 加两条持久 PARKED。确认三组 Kafka lag 为零，明确称为“两条停车恢复积压”，不能说尚未提交的 Kafka 消费积压。
4. ES 停机期间运行既有真实交易 trade 演示九项检查。
5. 启动同一个 ES 容器，确认旧 generation 和前两篇文档仍在；仅对自有失败坐标执行恢复 CLI，确认 RESOLVED、四个唯一 ES 文档，浏览器原重试按钮获得实际成功响应。
6. 确认三组已提交 offset 到 log end 后，精确清理自有 ES 文档、SQL facts/outbox/receipt/搜索派生状态/失败坐标与会话/验证码键。finally 总会尝试恢复原 ES 容器；清理未完成会保留安全 fixture ID 并判失败。

必须显式配置 `API_BASE/WEB_BASE/MYSQL_*/REDIS_*/ES_URL/ES_INDEX/ES_CONTAINER/KAFKA_CONTAINER/KAFKA_TOPIC/GO_SEARCH_BINARY/PLAYWRIGHT_MODULE/CHROME_PATH/FAULT_EVIDENCE_DIR`。证据目录必须为空；保留失败轮、匿名页面截图和 trace。浏览器中没有登录或后台准备请求，不保存手机号、验证码、Token、核销凭据。固定 Playwright-core 1.56.1 复用本票既有工具目录，不增装 SDK 或改变应用依赖。

执行命令为 `node scripts/go-note-search-fault.mjs`；真实结果写入每轮新目录 `fault-result.json` 和截图/trace/交易证据。本计划创建时未执行停机；最终报告另存 `fault-report.md`，按实际结果填写，不能拿 loopback:1 的连接故障测试代替本次物理停机。
