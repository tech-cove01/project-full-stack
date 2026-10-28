# GO-076：公开笔记搜索页面

起点 `290cd60358e0467f43caef4ddd647704bdbcfac6`，实现者自测记录，尚不代表独立核验。依赖真实搜索 API 与独立 Kafka 消费组；本改动仅包含搜索页面、API/DTO、路由/主导航和专项浏览器脚本，不改变原有 Feed、笔记编辑或商铺浏览。

## 可观察行为

- 未登录用户可从主导航“搜索笔记”进入 `/notes/search`；静态路径位于动态 `/notes/:id` 之前。
- 按标题或正文检索公开笔记，可选门店 ID，支持“相关度”“最新发布”和每页10条的继续加载。门店及笔记 ID 始终为字符串；不展示未经定义的精确总数。
- `q/shopId/sort` 保存在 URL；刷新、复制链接、浏览器返回恢复同一查询条件并从第一页开始。输入变化立即取消旧请求、清空旧结果与游标；空关键词、超过100个 Unicode 字符、非规范门店 ID 均有明确提示。
- 标题、正文用 Vue 文本插值显示，不使用 `v-html`；结果可打开真实笔记详情和关联门店。加载更多失败保留已有结果并重试原游标；即使当前公开状态过滤后短页或空页，只要还有下一游标就能继续。
- 不可用统一显示“笔记搜索暂时不可用，请稍后重试”，提供实际重试；正常界面不出现 ES/Kafka/offset 等实现术语，也不把其它查询静默替代为全文搜索。
- 翻页返回400表示该次分页条件已不可用，清空旧结果/游标并提示“分页已失效，请重新搜索”；保留提交条件，按钮从第一页创建新的查询，不循环重试已失效的游标。浏览器脚本只关闭本人查询的真实 PIT，再验证400与重新搜索200，不伪造响应。

## 已执行

```text
npx --prefix frontend prettier --write frontend/src/views/NoteSearchView.vue frontend/src/router.ts frontend/src/api.ts frontend/src/types.ts frontend/src/App.vue
npm --prefix frontend run build
npm --prefix frontend run format:check
node --check scripts/go-note-search-ui-smoke.mjs
```

构建（含 `vue-tsc --noEmit`）、前端格式检查及脚本语法检查均通过；最终原始输出见[构建](ui-build-final.txt)、[格式](ui-format-final.txt)。

## 真实浏览器结果与边界

`scripts/go-note-search-ui-smoke.mjs` 需要显式 API/WEB/MySQL/Redis/ES 环境；通过真实开发登录、13次真实笔记发布、真实 outbox/Kafka 消费后等待公开搜索可见，再运行浏览器。SQL 夹具使用本人合成用户、商家、两家门店；用户/门店 ID 大于2^53。所有成功响应来自真实 Go API；只注入网络 abort 和请求暂停，不伪造成功 JSON。

上海与 UTC 两个独立新夹具均 **25 项 PASS**，覆盖匿名入口、两页与独立 SQL 排序对账、门店过滤、URL刷新/返回、中文正文命中、转义和详情导航、空态、非法输入清空、旧请求取消、实际 PIT 失效400与重新搜索200、首次/翻页网络失败与真实重试、无未捕获异常。实际笔记 ID 也大于2^53，字符串 href 与独立 SQL 排序逐项一致；截图与不含登录凭据的匿名页面 trace 分开保存。

| 轮次 | 结果 | 原始证据 |
| --- | --- | --- |
| 上海首次 | 11项后脚本失败；原夹具保留，未判产品失败 | [初次记录](ui-shanghai-initial.json)、[截图](ui-shanghai-initial-failure.png)、[trace](ui-shanghai-initial-failure-trace.zip) |
| 原夹具受控清理 | 三组 offset 门槛通过，精确清理成功；没有新增浏览器验收 | [清理记录](ui-initial-controlled-cleanup.json) |
| 上海修正后 | 25项 PASS，cleanup=true，三组各检查分区0，共3条记录 caughtUp=true | [结果](ui-shanghai-final.json)、[截图](ui-shanghai-final-results.png)、[trace](ui-shanghai-final-trace.zip) |
| UTC | 25项 PASS，cleanup=true，三组各检查分区0，共3条记录 caughtUp=true | [结果](ui-utc-final.json)、[截图](ui-utc-final-results.png)、[trace](ui-utc-final-trace.zip) |

首次失败保留了两个测试脚本问题：详情断言仅等待与结果卡片同名的 heading，可能在 Vue 路由提交前就匹配旧卡片，实际截图已正确进入笔记详情；追加 `waitForURL` 后复跑。首次清理误把社区 outbox 的完成字段写成 `status/SENT`，实际契约为 `delivery_status/DONE`，SQL 明确失败并保留了13条自有笔记；修正字段后，用原失败文件的精确 ID 执行独立受控清理，原失败 JSON 不覆盖。这两项未改动产品行为，也未把失败记录改成 PASS。

清理前等待本人 outbox 全部 `delivery_status=DONE`、三个消费组的本人 eventId 收据，再通过显式专用 `KAFKA_CONTAINER` 中的只读 Kafka CLI 核对 follow/feed/search 三个组对 `KAFKA_TOPIC` 每个分区的 committed offset 与 log end 均明确且相等、lag=0。CLI 使用独立 INTERNAL 监听器 `localhost:9094`（可显式覆盖 `KAFKA_INTERNAL_PORT`），避免容器内误连宿主机 advertised 地址。只在该门槛通过后清理本人笔记文档、投影/收据/失败和业务行，以及精确登录 session/code/throttle；门槛失败则保留夹具 ID 并报告清理失败。ES 清理使用明确 alias 下的本人 documentId，不删除索引、共享 topic/group 或全库数据。可显式设置 `UI_CLEANUP_FROM` 指向本脚本的失败结果文件，重新校验精确夹具 ID 后只执行受控清理。

实跑环境：专用 API `127.0.0.1:8086`、Vite `5176`、MySQL `3332`、Redis `6404`、ES `9202`、Kafka 容器 `project-full-stack-go076-kafka-1`；明确 alias `community-notes-v1` 已由 CLI 初次重建，Kafka 三组正常运行。Playwright Core 固定1.56.1，仅装在忽略的 `backend/bin/go076-tools`；使用本机 Chrome，不新增前端依赖。执行时显式设置 `API_BASE/WEB_BASE/MYSQL_*/REDIS_*/ES_URL/ES_INDEX/KAFKA_CONTAINER/KAFKA_TOPIC/KAFKA_INTERNAL_PORT/PLAYWRIGHT_MODULE/CHROME_PATH`，不把密码、登录 Token 或完整手机号写入证据。

```text
UI_TIMEZONE=Asia/Shanghai UI_EVIDENCE=<本目录>/ui-shanghai-initial.json node scripts/go-note-search-ui-smoke.mjs
UI_CLEANUP_FROM=<本目录>/ui-shanghai-initial.json UI_EVIDENCE=<本目录>/ui-initial-controlled-cleanup.json node scripts/go-note-search-ui-smoke.mjs
UI_TIMEZONE=Asia/Shanghai UI_EVIDENCE=<本目录>/ui-shanghai-final.json node scripts/go-note-search-ui-smoke.mjs
UI_TIMEZONE=UTC UI_EVIDENCE=<本目录>/ui-utc-final.json node scripts/go-note-search-ui-smoke.mjs
```

两个时区完成后已通知主实现者，本 agent 不再占用浏览器或准备新夹具；API/Vite 和专用容器由主实现者统一收尾。这里的传输失败由实际网络 abort 注入，PIT 失效只关闭本人查询快照；真实 ES 物理停机及恢复由本票专项故障证据另列。没有声称完成持续负载、其他浏览器兼容性、相关性通用质量或远端 CI。

提交前非作者审查：根agent检查页面/API/DTO/路由和完整浏览器脚本，并实际查看上海截图，布局与转义文本正常。审查发现原“翻页400后重试原游标”无法恢复，提交前已改为清空失效游标并从第一页重新搜索；两时区均用真实关闭本人PIT复验400→200。该修复属于本次页面的必要恢复行为，未用Mock成功响应通过。
