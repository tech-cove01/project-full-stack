# GO-040—042 独立核验（AI lane）

基线 `358381843d0fe5efb98dfb75cac34c089f0777b8`。本 lane 只修改 `backend/internal/ai`、`aiknowledge`、`aichat`、`aitool` 及其测试；根 agent 负责最终独立复核、分步提交、前端、CI 与状态。所有模型调用均为确定性 stub/可控测试 provider，没有读取真实模型凭据或发起付费调用。

## 已复现并修复

- 原回答只看会话/租约/本地时间，sources 后停用仍可把旧文本发给 Chat，Chat 期间停用仍可输出并保存旧答案。同 clientMessageId 的缓存重放绕过所有 guard；下一回合还会把旧 assistant 答案送进 provider。真实 MySQL/Redis 四个故障窗口均先 FAIL，见 `ai-lifecycle-initial.jsonl`。
- 原输入中的手机号、验证码和长十六进制凭据在校验前进入存储；新增用例先证明三类输入触碰了后续存储，现均在任何存储/provider 前明确拒绝。
- 修复后的每个 SSE 事件、写入/缓存/最终回答边界都检查 context、会话、租约及 HTTP 原 session；规则分支同时检查当前公开知识 fingerprint 和精确 chunk/version/epoch。送模型前再次校验；缓存重放同样检查，并重新执行实时只读工具；历史只携带短用户回合，不把旧 assistant/tool 文本作为证据。知识重建导致 chunk 变化也会改变 fingerprint。
- 原 Rebuild 在鉴权前向 Embed 发送正文；现在先锁定当前文档并核验实际门店管理身份、未停用和当前发布版本，计算后再次以锁定当前读核验。发布/停用/重建审计与业务改动同事务；外部 Embed 失败保留旧发布版本，计算中撤权不能提交。
- 公共读取原来接受任何旧 READY 版本，检索使用 `scope<>SHOP_PUBLIC` 会意外包含其他 scope；现公共来源仅 `PUBLIC/SHOP_PUBLIC`，显式旧版本不再公开，检索校验当前 epoch、READY 和模型配置，超过2000个有效chunks明确拒绝，不静默截断。
- 活动工具原读不存在的 `tb_seckill_voucher.pay_value` 列；现读取 `tb_voucher.pay_value`，保留分金额字符串，非 ACTIVE 活动不通过公共工具暴露。本人订单查询始终由服务端 userID 过滤，DTO无完整凭证。
- capabilities 缺 `ready` 导致 Vue 不显示助手，会话 GET 返回数组而非对象，sources 缺 chunkId/href，工具卡片缺 type/items；本轮补齐现有前端类型所需字段，来源只构造站内路径，大ID使用字符串。capabilities 明示 `mode=stub` 和当前限制。
- 原预算先于锁/去重，每次缓存重试也计入预算；现仅新逻辑回合占位，失败释放所持精确key的逻辑计数。此处不承诺真实提供商退款或零费用。
- 页面追加核验还复现了 manager 列表不含正文导致编辑清空、scoped STAFF 明示只读却被403、原样保存因RowsAffected=0误报503，以及修改草稿标题后未发布标题出现在公共来源。`ai-list-initial.jsonl` 与 `ai-title-initial.jsonl`保留原始FAIL；修复管理列表携带草稿正文，店员仅按实际scope读取当前公开READY版本，不见草稿或停用内容，无变化保存为明确no-op。
- 新增显式版本迁移 `008-ai-version-title.sql`，将标题与正文一起作为不可变版本保存，公共接口/检索/店员只读都取版本标题；发布二阶段也比较标题，拒绝生成期间被改标题的候选。迁移在现有AI独立库首次升级与重复校验通过，见 `ai-migrate-008.txt`、`ai-migrate-repeat.txt`。追加后的完整四包实测见 `ai-008-final.jsonl`。

## 实际核验

独立环境：MySQL `127.0.0.1:3321/project_full_stack_review_ai`；Redis `127.0.0.1:6393` DB2。根 agent 初始化结构；本 lane 不启动API/容器，不碰开发库。测试只用自有文档、商家、会话、Token与精确key，清理补充了旧测试遗漏的自身dedupe/audit行。

- `go test -count=1 -timeout 150s -p 1 -json ./internal/aiknowledge ./internal/aichat ./internal/aitool ./internal/ai` 真实依赖通过，`go vet` 同四包通过；见 `ai-final.jsonl`。
- 六类知识用例包括：越权/停用重建provider零调用、发布失败保留旧版本、计算中撤权拒绝提交、公开旧版本隐藏、同事务审计、非公开scope不进入公共来源；后续追加明确scope用例见 `ai-knowledge-final.jsonl`。
- HTTP真实Redis登录态撤销发生在Chat期间，旧回答不输出、不保存，并以error终止；capabilities、创建/读取会话对象DTO同时核对。四个知识失效窗口修复后均通过。
- lease丢失、会话删除、身份撤销、context取消均不输出answer/done或保存assistant文本；预算用例证明缓存工具重试不多扣、provider失败后同ID可重新执行；见 `ai-guards-budget-final.jsonl`。取消用例不是实际等待45/60秒期限到期，期限传播由代码审查核对。
- 真实活动工具读取大于2^53的精确分金额并拒绝OFFLINE活动，见 `ai-tools-final.jsonl`。
- `gofmt` 与 `git diff --check` 通过。根 agent 另行做跨lane整合、页面与远端Linux race，不能拿本lane普通Windows结果替代。

## 保留的边界

- 当前生产装配只有 stub，不存在兼容真实 Chat/Embedding 的 provider 实现、真实质量评测、usage/费用和远端429等证据。这些不能由本轮 guard PASS 宣称完成；GO-075及真实provider路径保持待做。
- 有用户级频次/逻辑回合预算，但全局模型并发池/付费token成本预算尚未实现；stub不证明外部模型容量。
- 超出2000 chunks的检索会显式拒绝，发布前也检查容量；同时发布不同文档的全局容量预检不是一个分布式严格配额，本轮不作该声明。
- 公开知识与Redis身份跨存储不能构成同一原子事务；本轮证明的是已命中的请求/模型/发射边界复核，不能泛化为任意跨系统时刻的线性一致性。
- 没有重新评测历史30问集，不沿用Java指标为Go成绩。规则路由仍是固定只读意图，不是通用function calling/Agent。
- 008回填只能保存迁移当时文档表已知标题，无法恢复此前从未存储的历史标题。列允许NULL以保持旧写入SQL兼容，但新读端对之后缺失标题快照的版本明确拒绝；回退到旧程序再发布后，恢复新程序需重新发布完整版本，不得删除008字段或改写已应用迁移校验和。
