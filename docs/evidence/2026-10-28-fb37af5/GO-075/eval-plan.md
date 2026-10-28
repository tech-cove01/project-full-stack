# GO-075c 真实 Provider 固定小集

依赖 GO-075a/b 和已核验 GO-040—042 的实际范围。只把公开、合成规则发送到真实 Provider；MySQL/Redis 使用本票专用实例。此计划不把旧 Java 评测或 stub 回归当作 Go 模型质量证据。

- 输入集为同目录 `eval-cases.json`，固定 12 个短文档、30 题：有据 12、无据 6、本人只读工具 6、多轮 6（每题两轮）。黄金文档及允许事实写在调用前，不以实际回答生成预期。
- 六条平台 FAQ 由受控合成种子发布；六条门店 FAQ 经真实 `CreateDraft → Publish` 生成不可变版本和向量。草稿 canary 只留在数据库；每次付费边界再次检查不能含该 canary。
- 固定三个公开门店；同一合成商家便于准备，商家之间的权限隔离不是本质量集结论。本人/其他用户各有一笔订单；工具只按真实身份读取，其他人订单与不存在一致。测试准备动作显式修改本人订单状态，并检查对照订单不变。
- 每轮通过实际 Assistant/Redis 会话/SQL 检索与工具运行，捕获与 SSE 共用的真实事件 emitter；这里不启动外部 HTTP 服务器，也不把本测试称为浏览器或网络 SSE 验收。真实网络取消另有 GO-075 生命周期证据。
- 限制：单线程、总截止时间 10 分钟、最多 90 次 Provider 方法调用、每次 Chat 最多 400 输出 tokens、观察到的总 usage 最多 150000 tokens；下一次调用预留 14000 tokens，usage 未知则停止新增付费调用。没有自动重试。实际费用按真实 usage 和当日官方价格另算；取消结算未知不得填 0。
- 显式 `rebuild` 是付费重索引；“重启复用”检查重新构建 Service 不重新嵌入文档，随后查询仍需一次 query embedding。两种操作分开记录。
- 知识更新、停用在多轮集里通过真实业务 Service 执行。新版本问题只能引用新版本，停用后不能继续复述此前规则。缓存重放、生成中撤权、真实取消/超时由另一个生命周期测试覆盖，不能仅凭本集宣称全部通过。

执行方式（环境由本地私有配置加载，命令不含 Key）：

```powershell
$env:GO_INTEGRATION='1'
$env:GO_REAL_AI='1'
$env:GO_AI_EVAL_MAX_CALLS='90'
$env:GO_AI_EVAL_MAX_TOKENS='150000'
$env:GO_AI_EVAL_OUTPUT='<绝对证据目录>/eval-real-initial.json'
cd backend
go test -count=1 -timeout 12m -run '^TestIntegrationRealProviderEvaluation$' ./internal/aichat
```

测试还要求显式 `MYSQL_*`/`REDIS_*`、真实 `AI_API_KEY/AI_BASE_URL/AI_CHAT_MODEL/AI_EMBEDDING_MODEL/AI_EMBEDDING_DIMENSIONS`；`AI_MIN_SCORE` 默认 0.5，topK 为 5。未同时设置两项开关或缺少 Key 会 SKIP，表示未执行。要求数据库没有同配置已有向量以免改变固定黄金集合；不会删除别人的索引来通过此检查。

结果保存原始问题模板、选择门店序号、合法来源 seedKey/版本/摘录/分数、回答、事件顺序、首阶段/首回答/总耗时、工具类型/状态/金额及逐调用 usage。凭据、手机号、会话、真实 Token 不写结果；合成订单号替换为占位符。首回答可见时间不称为上游 TTFT。

每个黄金文档只有一个短 chunk，故本集 12 题的宏 Recall@5 与 Hit@5 数值相同。词语检查只生成 `lexicalCandidate` 候选，可能漏掉同义表达，也不能判断否定与语义支持；最终“事实正确且来源支持”必须由人逐题对照固定原文复核，并写明失败理由。无据、工具、多轮各保留固定分母，真实超时和 429 单列，不能移除失败项提高通过率。

测试失败会保留输出；重跑写新文件，不覆盖失败轮。清理只按自己创建的 ID 删除 fixture，不 truncate、不清 Redis 数据库、不重置自增。
