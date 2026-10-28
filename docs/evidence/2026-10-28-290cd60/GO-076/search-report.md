# GO-076a：公开笔记 ES 查询与当前事实复核

起点 `290cd60`（完整开始 SHA、最终提交由主报告补齐）。依赖现有公开笔记事实与固定社区事件契约。此文件是实现者自测，独立审查/最终核验状态由主报告和根 STATUS 记录；本 agent 未提交。

## 交付

`backend/internal/notesearch/client.go` 使用 Go 标准库 HTTP；不新增 SDK。ES 服务版本由根 Compose 锁定 8.19.23。端点仅 HTTPS 或 loopback HTTP 根路径，无凭据/query/fragment；固定公开字段 `id/shopId/userId/title/content/publishedAt/version`，dynamic=strict，标题正文使用内置 cjk analyzer，无插件。字符串 ID 保持 uint64 规范十进制，映射 keyword；时间为 date_nanos。每个请求最多读取 2 MiB 上游 JSON，超时/重定向/HTTP失败返回固定错误，不回显原始 ES 正文。

- 只允许配置 alias 和其 `-build-<受限后缀>` generation。写 alias 必须 `require_alias=true`，防止误创建同名物理索引；写入使用业务 `external_gte` version，验证成功响应的 ID/version/result。明确版本冲突返回 `ErrVersionConflict`，不假装持久效果已确认。
- Generation 创建、refresh、alias 切换由恢复/重建调用方使用。切换查询旧 alias 归属，只操作本命名空间 generations，不删除旧 generation。一个 `_aliases` action list 同时 remove/add，remove.must_exist=true；acknowledged 外还核对 errors/action_results，部分失败不能冒充切换成功。跨 MySQL/ES 事务边界和全量重建写入限制另见同步模块报告。
- `GET /notes/search?q=&shopId=&cursor=&limit=&sort=relevance|recent` 匿名查询公开笔记；query 最多100字符、limit 1—20，默认为10。AND multi_match 查询 `title^3/content`，不启用 query-string 脚本或任意 DSL。shopId keyword filter；排序固定为相关度降序→发布时间降序→ID关键字降序，或发布时间降序→ID降序，PIT追加稳定 _shard_doc。ID的同时间排序是字符串字典序，不宣称按数值序排序。
- PIT+search_after 页游标使用 Service 随机32字节 HMAC key，绑定 query/shop/sort/limit 和一分钟有效期；篡改、跨查询复用、超时和进程重启旧游标都需重新搜索。ES PIT未命中返回明确400；部分 shard失败/timed_out、ES不可达返回503，不用 SQL LIKE 冒充同等搜索。
- ES `_source` 仅取候选 id/version。一次 SQL IN + LEFT JOIN shop 查询当前 PUBLISHED事实，核对版本和当前门店再返回 SQL 的正文/标题/图片/时间；ES文本不会成为页面正文。未公开、已删除、版本落后或门店变更的候选被过滤。
- SQL过滤可能短页甚至空页，不报精确总数。nextCursor 根据最后一个原始 ES 命中推进，保证用户仍可继续翻页。最后页/失败关闭 PIT，取消后使用单独最多2秒清理期限；用户放弃翻页的 PIT 由一分钟 keep_alive 过期。运行中状态撤销的检查点是本次 SQL读取，不承诺请求返回瞬间的全局停写屏障。

## 已跑验证

Windows Go 1.27；普通9主组：[search-unit-final.txt](search-unit-final.txt)。覆盖配置/路径白名单、mapping/请求JSON、alias外部版本与原子动作、409/假成功/坏版本/上游故障、不拥有的alias、部分shard/超时/过期PIT/大小限制、取消/redirect、公开HTTP400/关闭503、游标签名/绑定/一分钟失效。`gofmt -l`、`go vet ./internal/notesearch`、`go build ./internal/notesearch` 通过，无格式输出。

真实 ES8.19.23 + MySQL8.4/Redis7.4 隔离实例（3332/6404/9202），无 API workers，持有 `GET_LOCK('go076_integration',30)`。只创建唯一 `go076-search-<纳秒>` alias/generation 与自有商家/门店/用户/笔记，测试后删除精确自有对象，不删除共享索引或数据库卷。

```powershell
# 先由主 agent 初始化并迁移独立实例003—012，再显式注入 MySQL/Redis 配置
cd backend
$env:GO_INTEGRATION='1'; $env:ES_URL='http://127.0.0.1:9202'
go test -count=1 -timeout 2m ./internal/notesearch -run '^TestIntegrationPublicNoteSearch$' -v
```

[search-real-initial.txt](search-real-initial.txt) 首轮通过，函数实测1.79s，三个子组：

1. 实际中文“咖啡”命中、门店过滤、AND无命中、超JS安全整数的字符串ID、只返回当前SQL正文/图片；草稿和旧版本索引结果均被过滤。
2. 最近排序第一原始页两条都不可见，items空但cursor继续；打开PIT后新增ES笔记不挤入后页，同时后续SQL检查隐藏刚下架的既有候选；最后页无重复并关闭PIT。
3. ES外部版本3写入/重复收敛，随后旧版本1返回明确冲突，不覆盖新文档。

清理没有错误，fixture锁与自有资源释放后已通知主 agent 允许下一模块测试。结果是小规模中文样例及确定行为，不是中文检索准确率、延迟容量或持续性能基准。

## 保留失败与修复

主 agent 独立代码审查指出 ES `_aliases` 可 `acknowledged=true` 但部分 action失败。新增反例先失败：[search-alias-initial.txt](search-alias-initial.txt)，再补 `errors`/逐 action status/error 核验及 remove.must_exist=true，反例与最终全普通组通过：[search-alias-final.txt](search-alias-final.txt)。此前普通自测保留 [search-unit-initial.txt](search-unit-initial.txt)，未覆盖此新反例，不能替代修复后结果。

## 官方依据与边界

- [Elasticsearch8.19 search_after/PIT](https://www.elastic.co/guide/en/elasticsearch/reference/8.19/paginate-search-results.html)：稳定快照、最新PIT ID、_shard_doc及显式关闭。
- [内置CJK分析器](https://www.elastic.co/docs/reference/text-analysis/analysis-lang-analyzer#cjk-analyzer)：采用内置中日韩分析，不声称等同外部中文分词插件质量。
- [Elasticsearch8.19 alias API](https://www.elastic.co/guide/en/elasticsearch/reference/8.19/indices-aliases.html) 与 [部分 action结果](https://www.elastic.co/guide/en/elasticsearch/reference/8.19/aliases.html#multiple-actions)：must_exist与errors/action_results须显式处理。

此模块未执行浏览器页面、Kafka同步/停车重放、ES停机积压恢复、重建增量交错、Linux race或远端CI；由相应子任务提供独立证据。ES不存在或关闭时搜索明确不可用，交易/普通浏览接线及新页面由主 agent 交付，不据当前模块测试扩大结论。
