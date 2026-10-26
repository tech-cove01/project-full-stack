# 当前交付核验与收尾单

核验日期：2026-10-26（Asia/Shanghai）。源码提交：`1af11be029722fd0ab66e8043bf8b6956750d83b`。本轮未修改应用代码、重启原服务、停用真实依赖或调用外部 AI 模型；新增本审查、复现脚本及脱敏结果。

## 1. 结论与项目定位

**核心功能亮点已落地，当前不能把全部验收标成通过。** 普通交易流程可演示，异常恢复、混合角色权限及 AI 生命周期仍有明确漏洞。下一轮以修复和证据收尾为目标，维持当前技术栈。

用户已确认：本项目用于补齐前后端、交易和 B 端能力；另一个项目侧重主 Agent，实习主要做 RAG。因此这里的 AI 保留为业务助手，不继续扩成独立 AI 平台；ES、微服务、新消息中间件、复杂推荐、MCP 和多 Agent 均不进入本轮。

## 2. 本轮实际执行

| 项目 | 结果 | 边界 |
| --- | --- | --- |
| 后端 Maven verify / 打包 | PASS：59 tests，0 failures / errors | 本机 JDK 24 下 Mockito 动态 attach 停滞；停止本次测试 JVM 后，以显式 Mockito javaagent 重跑成功。没有改 pom |
| 前端类型检查 / 生产构建 | PASS | `npm.cmd --prefix frontend run build` |
| 前端格式检查 | FAIL：12 个文件 | 格式问题，低优先级；不是功能失败 |
| 真实依赖健康检查 | PASS | 本机 8081 `/actuator/health` 为 UP；MySQL/Redis 端口可用 |
| 真实交易回归 | PASS | 发布幂等、店员发布拒绝、异步成单、跨用户查单拒绝、取消重买、支付、核销重试、退款重试与退款后不重买 |
| 回归最终库存 | PASS | 初始 3；取消、核销、退款各一单后，DB / Redis 均为 2；测试活动随后下架 |
| Redis 补偿脚本异常 | **BUG_REPRODUCED × 2** | 非整数库存、错误类型资格键，均可产生已写 done 但未完成补偿的状态 |
| AI 当前能力接口 | enabled / ready / openai-compatible | 只读查询，未发起 Chat 或 embedding；真实模型质量仍引用历史证据 |

原始结果：[runtime-check.json](runtime-check.json)。复现脚本：[verify-local.py](scripts/verify-local.py)。脚本限定 localhost、使用 dev 登录，创建标记活动与账号；它会写测试数据，运行前阅读说明。独立 Redis 故障键由 finally 精确删除。

该脚本用于核验/复现，退出码 0 仅表示执行结束；必须读取 `trade.result` 与 `lua_faults[].finding`，其中 `BUG_REPRODUCED` 表示问题存在，不能被 CI 当作验收通过。

本轮命令（Mockito 路径是本机缓存路径，换机器应使用对应依赖实际路径）：

```powershell
mvn -B -ntp -f backend/pom.xml '-DargLine=-javaagent:D:\develop\apache-maven-3.9.12\mvn_repo\org\mockito\mockito-core\5.17.0\mockito-core-5.17.0.jar' verify
npm.cmd --prefix frontend run build
npm.cmd --prefix frontend run format:check
python docs/reviews/2026-10-26/scripts/verify-local.py
```

本次保留活动 `1024`（首轮测试未等待准备完成，未成单）与 `1025`（最终回归），两者均已下架。`1025` 对应三笔订单的状态为取消、已核销、已退款，供复核；没有删除业务历史。首次脚本因沙箱禁止 socket 未执行请求，获得本机执行权限后才运行。首轮活动“尚未发布”是测试未等待 ready 所致，修正等待后通过，不记作应用缺陷。

本轮 HTTP 检查针对现有运行中实例，该实例没有公开构建 SHA；源码审查和 Maven / 前端构建明确绑定上述 HEAD。没有用本轮结果替代未执行的数据库中断或真实 AI 异常验收。

## 3. 亮点覆盖

| 方向 | 已落地 | 还不能宣传的内容 |
| --- | --- | --- |
| 全栈 | 用户区、订单列表/详情、商家活动/订单/核销/审计、AI 页，接口真实联通 | 不能将所有浏览器、移动端、异常场景视为已回归 |
| 交易 | Lua 预扣、Stream、事务落单、取消重买、模拟支付退款、核销及超时关闭 | 异常恢复完全可靠、任何情况下零丢失 |
| B 端 | OWNER/MANAGER/STAFF 和门店范围、操作审计 | 跨商家混合角色审计权限尚有漏洞 |
| 并发 | 多账号争抢、同账号幂等、Redis 限流、未读消息重启、pending 接管 | 200 人同时并发、持续 822 RPS、数据库故障后自动收敛 |
| AI | 实际 Chat/Embedding、RAG、来源卡片、Redis 会话、B 端公开 FAQ、SSE 阶段反馈 | LangChain4j、模型 Function Calling、Agent 编排、答案准确率 11/12 均不能按当前实现/评测直接宣称 |
| 工程化 | 版本迁移、构建 CI、历史真实环境与页面证据 | CI 尚未覆盖真实 MySQL/Redis；新交易/AI 模块缺少相应自动回归 |

AI 当前为自写兼容 HTTP Provider、内存精确检索和服务端关键词路由。18 条 FAQ 的本项目规模下可保留此实现；换框架不能替代修复边界。规则路由的只读工具是有效功能，但它不等于模型在 tools 协议中自主选择和调用函数。

## 4. 需要修复的代码问题

### T1：DB 结果未持久核对就 ACK（优先修）

位置：[VoucherOrderServiceImpl.java:199](../../../backend/src/main/java/com/fullstack/platform/service/impl/VoucherOrderServiceImpl.java#L199)。

第 5 次 DB/提交异常后仅写 Redis 死信和 24 小时预约，再 ACK；没有数据库复核记录或已交付的恢复工具。DB 恢复后该订单不会继续自动消费，预约过期后查单还可能失去结果。现有单测反而固定断言此路径 ACK，测试通过不代表满足设计。

收尾：无法持久记录结果时保留 pending 并退避；恢复后查询订单实际提交状态，再安全重试/持久记录。若 ACK 给人工复核，复核记录必须可持久追踪，且有受控重放命令。**验收：DB 不可用超过重试阈值后恢复，受理订单有明确归宿，不提前释放不确定库存。** 本轮为代码核验，没有中断用户数据库。

### T2：补偿 Lua 会部分执行，去重生命周期也不完整

位置：[order-restore.lua:6](../../../backend/src/main/resources/order-restore.lua#L6)、[OrderCompensationService.java](../../../backend/src/main/java/com/fullstack/platform/order/OrderCompensationService.java)。

先 SETNX done，再 INCR/SREM/HGET；未先验证库存可转换整数、资格集合和受理映射类型。真实 Redis 复现：

- stock 为非数字 string：首次报错但 done=1，重投返回 0，库存恢复永远被跳过。
- stock=5、购买资格键为错误 string：首次 stock 变 6 后报错，资格/映射未释放；重投返回 0，未完成效果被当作成功。

Lua 错误不自动回滚前序写入。收尾需先完整校验全部参数、值与类型，再写业务效果/去重记录；异常必须可恢复。

另，done 标记 7 天过期，而 PENDING outbox 可保留更久。补偿已执行、标 DONE 前退出并长时间停机后，标记到期会使重投重复加库存。应统一去重账与允许重放的生命周期；只延长 TTL 不等于解决。

**验收：两种异常输入零部分变化，修复输入后同事件完成一次；正常重投、标 DONE 前退出、去重保留/清理边界均不多回补。**

### T3：ACTIVE 缺键会被自动重建并开闸

位置：[DevDataInitializer.java:40](../../../backend/src/main/java/com/fullstack/platform/dev/DevDataInitializer.java#L40)、[ActivityPublicationService.java:69](../../../backend/src/main/java/com/fullstack/platform/activity/ActivityPublicationService.java#L69)。

默认 dev 重启时从 DB 库存补缺失 Redis stock，并为 ACTIVE 缺失 gate 写 OPEN；ACTIVE 发布重试也可能走准备脚本。已有受理未落库预约时，DB 库存尚未反映预扣，重建会多放行。DB 条件扣减仍能防止实际订单超卖，但受理与库存一致性契约已被破坏。

**验收：首次 INITIALIZING 可准备；ACTIVE 丢 stock/gate 后重启或重试仍关闭，产生待核对差异；处理完预约与补偿后才允许显式恢复，不能盲写 DB 剩余量。**

### T4：混合角色的审计范围越权

位置：[MerchantOrderService.java:185](../../../backend/src/main/java/com/fullstack/platform/merchant/MerchantOrderService.java#L185)。

先检查该账号在任意商家有管理角色，再使用全部 visibleShopIds 查询审计。账号是 A 的 OWNER、B 的 STAFF 时，B 的 STAFF 门店也会进入审计范围。

**验收：单独计算具有 OWNER/MANAGER 权限的商家/门店范围；混合角色账号只能读 A 审计，B 审计返回拒绝或不可见；普通订单查询仍可在 B 的 STAFF 授权门店工作。** 本轮没有修改成员数据制造跨商家身份。

### A1：知识停用后仍可发送或重放旧答案

位置：[AiAssistantService.java:170](../../../backend/src/main/java/com/fullstack/platform/ai/AiAssistantService.java#L170)、[AiAssistantService.java:200](../../../backend/src/main/java/com/fullstack/platform/ai/AiAssistantService.java#L200)、[AiAssistantService.java:241](../../../backend/src/main/java/com/fullstack/platform/ai/AiAssistantService.java#L241)。

只在 Chat 生成前检查版本，返回后直接发答案；缓存重放不核验 epoch；旧答案还会放回模型上下文。收尾：缓存保存来源依赖和有效门店；发送前复核，失效则重试/冲突；记忆清除失效事实。

**验收：用可控 stub 暂停生成，在 B 端停用来源后放行，不能收到该规则的新答案；同消息 ID 重放也不能复活停用规则。** 无需为此调用付费模型。

### A2：取消、删除、去重和预算的生命周期需收口

位置：[AiConversationService.java:70](../../../backend/src/main/java/com/fullstack/platform/ai/AiConversationService.java#L70)、[AiConversationService.java:105](../../../backend/src/main/java/com/fullstack/platform/ai/AiConversationService.java#L105)、[AiAssistantService.java:97](../../../backend/src/main/java/com/fullstack/platform/ai/AiAssistantService.java#L97)、[AiDedupeService.java:17](../../../backend/src/main/java/com/fullstack/platform/ai/AiDedupeService.java#L17)。

- 删除仅 DEL，运行中 append 能重新创建会话；前端 abort 未关联后端任务或上游取消。
- 新消息先写去重记录、后抢会话锁；抢锁失败没有清理 NEW 记录，原消息 ID 可卡在 IN_PROGRESS 一小时。
- 去重 begin 不是原子操作，摘要没有 shopId；同消息/同 ID 换门店会得到原门店缓存。
- 有全局 semaphore，但每用户限频、日预算、usage 和整轮 deadline 未落实；maxPerMinute 配置未使用。

**验收：删除 generation fence、原子 begin/抢锁失败清理、有效门店入摘要、结束释放资源；用户限频和保守调用预算生效，整轮耗时受控；无法取消上游时记录限制和未知计费，不声称免费。**

### A3：敏感过滤与真实凭据格式不匹配

位置：[AiAssistantService.java:25](../../../backend/src/main/java/com/fullstack/platform/ai/AiAssistantService.java#L25)、[UserServiceImpl.java:107](../../../backend/src/main/java/com/fullstack/platform/service/impl/UserServiceImpl.java#L107)。

AI 只拦 64 位 hex 和手机号；实际登录 token 是 32 位无横线 UUID，带标签的 6 位验证码同样未处理。这些输入可以先进入 embedding 请求，违反页面和知识说明中的承诺。

**验收：使用合成凭据与带标签验证码，捕获型 stub 确认 embedding/Chat 请求里没有原值；保留正常订单号/金额查询，避免简单屏蔽全部数字。**

## 5. 证据与口径修正

1. **并发正确性成立，容量尚未证明。** 历史 raw 是 20 VU × 10 次，共 200 个用户请求，100 受理、100 售罄；不是 200 人同时并发。约 0.24 秒的 822 req/s 不能写成持续容量。首轮 200 VU 的 connection refused 根因尚未定位，不能归因为已经证实的 Tomcat 参数。
2. **29.38ms 不是成功受理专属 P95。** [seckill.js](../../../scripts/load/seckill.js) 对全部响应记 admission，混合成功与售罄；还没记录受理至最终建单耗时。业务检查须对未预期 success=false 失败，不能只看 HTTP/JSON 可解析。
3. **重启证据准确但覆盖有限。** 未读消息重启、假消费者 pending claim 已验证；DB 提交后 ACK 前、DB 断连到重试耗尽尚无精确窗口证据。
4. **AI 分数是来源/卡片检查，不是完整回答正确率。** [ai-real-eval.mjs](../../evidence/2026-10-24-cbeef75/ai/scripts/ai-real-eval.mjs) 用来源标题计算知识 11/12、多轮 6/6；工具 7/7 只比较 card type；拒答用关键词。保留原始结果，增加逐题事实/引用支撑复核、实时快照，以及真实多轮指代测试。无需换模型刷分。
5. **脚本有归档，但复现入口不对。** 七个 AI 脚本已提交在 evidence 的 scripts 下；报告命令仍指向忽略的 frontend/.qa，评测还依赖工作目录和未提交账号池。统一受控入口、数据准备/依赖说明及报告路径，不能简单声称“脚本完全没提交”。
6. **CI / 文档需如实。** CI 只有单测和前端构建。README 同时写 AI MVP 与“仍为规划”，设计仍指定 LangChain4j，均需按实际结果同步；格式检查的 12 文件一起处理。

## 6. 下一轮只做三批收尾

| 顺序 | 必做内容 | 结束条件 |
| --- | --- | --- |
| R1 交易与权限 | T1—T4，包含 T2 的去重生命周期 | 真实 DB 异常/恢复、补偿错类型、缺键关闭、混合角色权限有对应回归；正常链路不回退 |
| R2 AI 边界 | A1—A3；已发布 FAQ 能形成新草稿/版本，或明确并验证“新建替换＋停用旧版”的最小维护路径 | 用受控 stub 覆盖停用、缓存、删除/取消、去重、凭据、预算；保留真实模型正常调用历史证据 |
| R3 证据与交接 | 当前 SHA 干净环境/真实依赖冒烟；一轮几分钟分档负载，分类延迟/错误、最终成单和基本资源；重放/对账受控命令；文档/格式/脚本入口 | 复核者可运行，结果能回溯；未做项有准确限制，关键失败没有被隐藏 |

可以保留同步小文档嵌入和手写 Provider，前提是大小/时间/并发有界、启动故障不拖垮交易，且文档按当前实现描述。首版不以引入 LangChain4j、向量数据库或 LLM Function Calling 为收尾前置条件。

**冻结标准：** 正常交易、权限拒绝、库存守恒、必要故障恢复和 AI 边界通过；一段真实前后端演示；一份可复现性能/正确性记录；三条能讲清需求、取舍、失败场景和证据的项目亮点。达到后冻结功能，进入项目讲解与面试准备。新扩展另立范围，避免长期停留在开发阶段。
