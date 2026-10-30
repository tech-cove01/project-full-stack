# 源码讲解路线

依据功能冻结版本 `0f185a558acf70ffcf60d05882d66db5a6b946db` 整理。用于按业务约束解释实现和回答追问，不将仓库历史自动等同于个人职责。所有结论按 [STATUS](../STATUS.md) 的核验范围使用。本文本轮仅做源码校对，没有重新运行服务、压测或真实模型。

## 先用 90 秒讲清项目

> 这是 Go 和 Vue 的本地生活交易与商家运营平台。顾客购买优惠券，门店核销或退款，商家查看审计；社区发布笔记并支持关注动态和全文搜索，业务助手查询公开规则及本人数据。后端是模块化单体，MySQL 保存业务事实，Redis 承担登录态、交易准入和缓存。订单通过 Redis Stream 异步落库，社区通过独立 outbox 和 Kafka 形成关注、Feed 目录及 ES 搜索投影。AI 保持只读，不能代替用户下单或退款。

从 [app.Run](../backend/internal/platform/app/app.go) 打开代码：这里组装 HTTP、数据库/Redis、AI、搜索和各个 worker。`startBackground` 使用进程生命周期 context，异步订单不会随着受理 HTTP 请求结束而取消；`backgroundResources.Close` 先取消并等待后台工作，再关闭连接池。前端入口是 [router.ts](../frontend/src/router.ts)，用户订单与商家工作台访问同一 Go 服务。

接下来按“约束 → 调用链 → 异常窗口 → 证据”讲。优先讲清第一条交易链，再按岗位选择缓存或消息作为第二条；AI 可作为业务补充。

## 1. 抢券、落单与补偿

业务约束：库存不能超卖，同用户不能重复持有同活动的有效订单；响应丢失后能找回已受理 ID；数据库结果未知时不能删除待处理消息。

| 阅读顺序 | 入口与实际职责 |
| --- | --- |
| HTTP 与重试恢复 | [order/http.go](../backend/internal/order/http.go) 注册登录后路由；[Service.Seckill](../backend/internal/order/query.go) 先查本人有效订单和 Redis 已受理映射，再读取当前活动和版本 |
| 恢复与原子准入 | [Admission.Recover / Reserve](../backend/internal/orderadmission/admission.go)：Recover 用 [recover.lua](../backend/internal/orderadmission/lua/recover.lua) 读取并校验既有受理映射；Reserve 用 [reserve.lua](../backend/internal/orderadmission/lua/reserve.lua) 先校验参数、键类型、库存及 gate，再扣 Redis 库存、记录资格/稳定映射和 reservation，最后 XADD |
| 消息处理 | [orderstream.Worker.Poll / deliver](../backend/internal/orderstream/worker.go) 在新消息与待确认消息间轮转，调用 Creator；明确持久结果才 ACK，未知结果保持 pending，超过阈值先持久 review 再 ACK |
| MySQL 成单 | [Creator.Create / createLocked](../backend/internal/order/creator.go) 用连接级订单锁串行化同 ID，再在 RC 事务内锁券/库存，条件扣库存，写订单、审计与 outbox；明确拒绝也持久化，不把异常当业务拒绝 |
| 订单状态变化 | [Pay / Cancel / CancelExpired](../backend/internal/order/lifecycle.go) 校验当前状态和期限；取消/超时的数据库库存恢复、版本、审计、outbox 在同一事务中提交 |
| Redis 收敛 | [ordercomp.Worker.Poll / Apply](../backend/internal/ordercomp/worker.go) 消费交易 outbox；[restore.lua](../backend/internal/ordercomp/lua/restore.lua) 先验证再写，以事件标记防重复恢复，并核对 reservation 归属 |

先说清“受理”与“真实订单”的区别：Redis 准入成功只说明稳定 ID 和异步任务已建立，前端继续查询数据库落单结果。ID/金额按字符串返回，避免 JavaScript 大整数和金额精度问题。

Redis Lua 的原子执行不代表脚本报错能撤销此前写入，所以脚本把可能失败的参数、类型和数值校验放在写入之前。新准入还使用 Redis TIME 判断活动窗口；既有受理映射优先恢复，使丢失响应后的合法重试能找回原 ID。

MySQL 与 Redis 没有跨系统原子事务。DB commit 成功而 ACK 失败时，消息会再次出现，订单事实和拒绝事实的幂等检查防止重复扣减；DB commit 结果未知时保持待确认。后续取消重买后，旧补偿不能误删新订单的资格映射。取消后可重买，退款后不可重买，是不同的资格释放规则。

代码验证入口：[Stream 真实集成](../backend/internal/orderstream/integration_test.go)、[补偿校验](../backend/internal/ordercomp/restore_integration_test.go)、[受控重建](../backend/internal/activity/reconcile.go)。重建需先关准入并核对在途、review 和补偿，不是丢失全部数据后的自动灾难恢复。

常见追问：为什么不先 ACK；连接级锁释放失败为何不能直接归还物理连接；Lua 的哪一步开始写；谁负责已受理但数据库拒绝的预占；怎样区分取消和退款后的购买资格。

## 2. B 端逐门店权限

业务约束：同一用户在不同商家可以有不同角色；旧 Token 不能永久保留已撤销的门店权限；核销或退款写入前，授权必须仍然成立。

阅读 [auth.RequireLogin](../backend/internal/auth/http.go) 与 [Resolve](../backend/internal/auth/service.go)，再看 [merchant/mysql.go](../backend/internal/merchant/mysql.go) 的 `Workspace`、`MembershipForShop`、`RequireManager`、`VisibleShopIDs` 和 `ManagedShopIDs`。登录态提供当前用户身份；OWNER/MANAGER 只在本商家管理，STAFF 还要命中成员自己的门店 scope，跨商家的脏 scope 不能扩大权限。

在 [merchantorder/mysql.go](../backend/internal/merchantorder/mysql.go) 中对照列表与审计的范围：订单使用可见门店，审计使用管理门店；越权查询筛选可以返回空页，不能用按钮或筛选项判断资源存在性。写入看 [Redeem](../backend/internal/merchantorder/redeem.go)、[Refund](../backend/internal/merchantorder/refund.go) 和 [lockMutationMembership](../backend/internal/merchantorder/authorization.go)：事务外检查用于尽早拒绝，真正变更前在业务事务中锁定 shop、merchant、member 和必要 scope，再锁券、库存和订单。

成员停用或 scope 撤回后，原 Token 可能仍能恢复登录身份，但业务权限已缩减。事务内复核处理“检查后立刻撤权”的窗口。核销允许有具体门店权限的店员，退款要求当前管理身份；核销与退款的状态竞争也受同一事实锁保护。

证据入口：[商家交易集成](../backend/internal/merchantorder/batch_integration_test.go)、[GO-074 页面与权限报告](reviews/2026-10-28/go074-bca0ced/report.md)。常见追问：为什么登录有效但操作 403；为什么不能取用户最高角色全局放行；事务外权限检查为什么不够。

## 3. 批量查询、L1 与同键回源合并

业务约束：列表避免 N+1，共享缓存不混入当前用户的点赞状态；更新完成后的失效不能被旧回源重新覆盖。

先看 [social.Service.Feed / feedPage](../backend/internal/social/feed.go)：按 `(create_time,id)` 游标翻页，批量装配门店和当前用户字段。`likedByMe` 按调用者单独查询，不能放入共享公共计数。再看 [readSnapshot](../backend/internal/social/projection.go)：投影完整性与查询依赖使用同一个 RR 只读快照，不完整或可选投影不可用时回退数据库，最终 SQL 仍校验当前公开状态与关注事实。

[communitycache/counts.go](../backend/internal/communitycache/counts.go) 中有两条路径：

- `Get`：L1 → Redis → MySQL，用自有 `inflight/flight` 合并同 key 加载。等待者可以用自己的 context 取消等待；真正执行加载的请求仍携带它的 context。这里没有调用 `x/sync/singleflight`。
- `GetMany`：先取 L1，剩余一次 MGET，缺失项一次 SQL IN；这是列表的批量路径，不再为每个 key 建 flight。

`Invalidate` 清两层并推进 generation；`publish` 对比回源携带的 epoch，旧加载不会在已完成失效之后重新发布。L1 默认 2 秒、最多 4096 条，到边界清 map，未实现 LRU 或动态热点检测。Redis 计数 TTL 为 60 秒加正向抖动；Redis 读写故障降级，MySQL 仍是事实源。L1 在多实例之间没有广播失效能力，不能把本机设计讲成跨实例强一致。

[真实函数级实验](../backend/internal/communitycache/go073_measure_test.go) 和 [独立复跑报告](reviews/2026-10-29/go073-trade-399705a/report.md)对应实际回源次数：100 次冷/热为 100/0 次，20 轮各 32 并发同 key 为 20 次；32 key 的 20 轮批量为 20 次，逐条为 640 次。这不是完整 Feed HTTP 性能；热读计时为 0 只代表低于本次计时分辨率。

常见追问：旧回源与失效竞态怎么处理；为何不能缓存当前用户字段；批量路径为何不再逐 key 合并；请求取消由谁承担；两实例的 L1 一致性有什么边界。

## 4. Kafka 投影与 ES 搜索恢复

业务约束：业务事实成功后不能遗漏事件；重复与旧版本消息不能破坏新状态；外部索引不可用时仍要保留可重放的失败事实。

| 阅读顺序 | 入口与责任 |
| --- | --- |
| 事实与事件 | [note.Create](../backend/internal/note/note.go)、[SetLike](../backend/internal/social/like.go)、[SetFollow](../backend/internal/social/follow.go)：事实变化与 community outbox 同事务；期望状态重复 no-op 不涨版本、不多发事件 |
| 投递 | [communityrelay.Claim](../backend/internal/communityrelay/repository.go) 和 [Worker.Poll](../backend/internal/communityrelay/worker.go)：租约认领，aggregate ID 为 key，Kafka 确认后标 DONE；不确定投递可能重试 |
| 消费 | [communityconsumer.NewWorker / Poll](../backend/internal/communityconsumer/worker.go)：follow/feed/search 三个稳定独立组；Apply 最多三次后转 Park，效果或失败持久化后才同步提交位点，未确认时保留当前消息 |
| SQL 投影 | [Repository.Apply](../backend/internal/communityprojection/repository.go)：收据和投影效果同事务，eventId/hash 防冲突，业务 version 防旧覆盖；无关已知事件也留 IGNORED 收据 |
| 搜索投影 | [notesearch.Sync.Apply / Park / Replay](../backend/internal/notesearch/sync.go)：查当前公开事实，ES 按业务版本写入，SQL 保存状态/hash/收据，失败保存原消息及 topic/partition/offset 供精确重放 |
| 查询与重建 | [Service.Search](../backend/internal/notesearch/service.go) 使用 PIT/search_after 取候选，再由 SQL 复核公开事实及版本；[Rebuild](../backend/internal/notesearch/rebuild.go) 离线创建新代次并切换 alias |

这里要画清三个进度：“outbox 已投递”“Kafka 位置已确认”“业务索引可查询”。停车成功后 Kafka 位点可以前进，因此 lag=0 时仍可能有 PARKED；不能仅凭 HTTP 200 或 lag 宣称业务已收敛。搜索 SQL 复核可能过滤候选，页面因此变短；cursor 仍按原始 hit 推进，避免反复读同一页。

ES 和 SQL 不是同一事务。ES 写成功而 SQL 回滚后，同业务版本重放可以继续收敛；离线全量重建需停 API 和消费者，并显式设置 `GO_SEARCH_REBUILD_OFFLINE=1`，不宣称零停机。独立恢复报告中的 29.920 秒包含容器启动、十次人工精确重放及 refresh，不是自动追平 SLA。

最后讲本轮遇到的实际首启问题：`kafka.NewReader` 构造时就启动 group，如果先于主题创建完成空分配，可能持续不推进。修复先在后台查询不自动创建主题的 metadata，等配置 topic 和 leader 就绪后才构造 reader；不改变组名、不重置位点。[真实回归](../backend/internal/communityconsumer/fresh_topic_integration_test.go)覆盖该首次建主题路径，运行中删除并立即重建主题仍未验证。

证据：[事件独立复跑](reviews/2026-10-29/go073-399705a/events/report.md)。常见追问：为什么至少一次；ES 成功而 SQL 失败怎么恢复；收据为何与效果同事务；为什么搜索命中后还查 SQL；为什么 lag=0 还要看停车表。

## 5. 业务 AI 与终态保护

业务约束：只公开规则和本人数据可见，知识停用或身份失效后旧答复不能继续输出；进行中的同 ID 消息与短期完成重放不新增 Provider 调用，取消与失败要清理自己的资源。失败清理 claim 后允许同 ID 重试，可能产生新的调用；完成去重缓存目前为 10 分钟，不能当成永久或付费账单幂等保证。

从 [aichat/http.go](../backend/internal/aichat/http.go) 的消息接口读到 [Assistant.Ask](../backend/internal/aichat/assistant.go)：校验输入和会话归属、turn deadline、会话锁、`clientMessageId` 与内容 fingerprint。先尝试 [aitool.Route](../backend/internal/aitool/tool.go)，这是固定只读意图路由，订单 SQL 显式绑定 `user_id`，工具不会接受模型指定的另一个用户身份或执行交易写操作。活动工具要带 ID，库存来自当前已提交 SQL 快照。

非工具问题进入 [aiknowledge.Search](../backend/internal/aiknowledge/search.go)：只取当前已发布、未停用的 PUBLIC 或对应 SHOP_PUBLIC；检查分块完整性和模型/endpoint/维度指纹，再执行 query embedding 与本地精确相似度检索。外部等待之后还要复核知识 snapshot 和 hit 版本。当前适用于小规模知识，不是引入了独立向量数据库。

返回 [assistant.go](../backend/internal/aichat/assistant.go) 的 `alive`、`close` 与 `replay`：发送及持久化终态前，检查 context/deadline、当前登录身份、会话与锁归属、知识版本。缓存工具结果重放会重新查实时数据，不直接把旧库存当新查询。失败清理去重 claim 并退还逻辑预算；逻辑额度不是人民币结算预算。

[Compatible.Chat](../backend/internal/ai/compatible.go) 使用有界并发、context 和响应大小，单次 HTTP 尝试，不自动重试付费调用。当前 Chat payload 的 `Stream` 默认 false，先拿完整答案，应用再通过 SSE 发送进度、来源、工具和整段 `answer/done`；不能描述为 Provider token 逐字透传。stub 仅证明确定性协议与页面流程；真实模型小集/页面结论按 GO-075/074 原报告使用，取消用量未知项继续保留。

证据入口：[终态守护集成](../backend/internal/aichat/guards_integration_test.go)、[只读工具真实集成](../backend/internal/aitool/review_integration_test.go)与 [票级核验边界](../STATUS.md)。常见追问：退出登录后仍在生成怎么办；停用知识后的缓存答复是否能重放；同 ID 换问题怎样处理；SSE 与模型 token streaming 的区别；取消是否意味着 Provider 不计费。

## 讲解前自查

能够在源码中指出上述函数，再用自己的话解释一个异常窗口；个人职责、设计参与和新增部分按真实经历填写。量化引用统一使用 [独立核验汇总](reviews/2026-10-29/go073-399705a/report.md)：交易 5 次/秒、社区约每秒一对事件均为指定低速负载；缓存为函数级实验；没有证明饱和容量、生产部署、多 broker 灾难恢复或自动追平。Java 历史指标和 Go 实测分别保留来源。
