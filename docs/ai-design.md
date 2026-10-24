# AI 业务助手与商家知识维护设计（P1-D，MVP 已实现，真实模型待 Key）

日期：2026-10-24。对齐交易规则提交 `eac1b9249ec38ab0666629c4a3156d4c78a9de7e`。应用实现状态见 [AI 证据](evidence/2026-10-24-cbeef75/ai/report.md)：已用 stub 完成确定性验证，并已用 SiliconFlow 真实 Chat/Embedding 完成 UI 与 31 题固定集（知识 11/12、拒答 6/6、工具 7/7、多轮 6/6）；黄金 chunk Recall@K、真实 429/abort、预算与成本数据仍未执行。本文保留为设计契约，不代表通用模型质量已经验收。

## 1. 目标与交付顺序

在 P1-C 交易/履约闭环后交付 P1-D；AI 实现可与 P2 的测试脚本准备并行，P2 包含独立 AI 验收。优先做一个与现有业务相连的助手：用户问门店规则、找活动、查本人订单，店主维护公开规则。ES 仍为 P3 可选项，AI 不依赖 ES。

| 任务 | 最小交付 | 完成条件 |
| --- | --- | --- |
| AI-D01 | 后端模型适配、配置与能力接口 | 关闭/未配置可启动；锁定依赖后真实编译；密钥只在后端 |
| AI-D02 | 知识表、种子、分块与版本发布 | 新库/旧库迁移；重启恢复向量；失败保留旧发布版本 |
| AI-D03 | OWNER/MANAGER 维护知识，STAFF 只读 | 本商家门店权限在服务端生效；发布、停用、重建有审计 |
| AI-D04 | 用户助手、RAG、只读工具、短期记忆 | 来源可查，查单仅本人，多轮不会串用户或继续使用停用规则 |
| AI-D05 | fetch POST SSE 与异常交互 | 展示真实阶段、一次最终答复、取消、失败、限流、未配置状态 |
| AI-D06 | 固定评测数据与复现说明 | 交付 [AI 验收](./ai-acceptance.md) 所需数据、脱敏证据和真实模型结果 |

首版不做文件上传、PDF 自动解析、外网搜索、语音、微调、多 Agent 编排或独立 MCP 服务。学习 PDF 是设计参考，不把整份课程/面试讲义当成面向用户的业务知识库。

## 2. 能力与数据边界

- **规则问答**：检索已发布平台 FAQ、当前门店公开 FAQ；展示来源卡片。没有足够依据时明确未查到规则，不臆造退款承诺、营业规则或优惠。
- **实时查询**：只读工具 `searchShops(query, page)`、`listShopActivities(shopId)`、`getMyOrder(orderId)`。金额、库存、活动可售性、订单状态从服务层实时查，标明查询时间，不向量化。
- **业务操作**：回答可附站内商铺/活动/本人订单按钮；支付、下单、取消、退款、核销、预约均由用户在已有业务页明确操作，模型无这些写工具。
- **公开知识**：首版仅 `PUBLIC` 平台规则及 `SHOP_PUBLIC` 门店规则；私密资料、草稿不参与检索或模型上下文。平台 FAQ 用受限本地种子/命令维护，商家无平台全局权限。
- **数据最小化**：不向外部模型发送登录 Token、手机号、验证码、核销凭证、环境密钥。工具返回专用白名单 DTO；用户输入若包含上述敏感数据，先拒绝或遮蔽，再调用模型，不能只清洗工具结果。日志和评测证据同样脱敏。
- **领域口径**：未支付取消/超时后可以重新购买；已支付退款后不可重新购买。规则、工具和提示词均与 [交易设计](./transaction-design.md) 一致，模型不能覆盖服务端裁决。

## 3. 技术决策与 Windows 运行

沿用 Java 21 / Spring Boot 3.5.16，采用 LangChain4j 核心库与模型适配模块、显式 Spring Bean。实现时锁定同一兼容版本/BOM，记录实际版本并运行 Maven 编译/测试；不直接照抄 PDF 中旧 starter 或为 AI 升级整个业务框架。

首选可配置百炼 Qwen 的兼容 Chat API 和 `text-embedding-v4`（首版显式固定 1024 维）；可替换为已经可用且通过契约验证的兼容提供方。Chat 模型必须实测支持选用的工具调用；结构化输出可先用受限 JSON 加解析，不把提供方原生 schema、token 流、工具调用三者同时支持设为前置条件。

后端配置至少包括 `AI_ENABLED=false`、`AI_API_KEY`、`AI_BASE_URL`、`AI_CHAT_MODEL`、`AI_EMBEDDING_MODEL`、`AI_EMBEDDING_DIMENSIONS`，有需要时分开配置 embedding 的 Key/Base URL。地址按账号业务空间、地域和官方文档填写，不硬编码旧域名，不承诺免费额度；环境文件不提交密钥。

Windows 原生 Java/Node + 现有 MySQL/Redis 容器即可开发；无需先整理旧虚拟机，也无需 GPU、Ollama 或新向量数据库。`AI_ENABLED=false` 或未配置时，核心业务照常运行，能力接口说明原因；请求助手返回明确未启用/未就绪，不伪造模型回答。

### 向量存储取舍

首版使用支持 metadata 过滤的 `InMemoryEmbeddingStore<TextSegment>`，单实例演示、最多 2000 个当前发布分块；知识版本、分块和向量 JSON 持久化到 MySQL，启动时恢复，无需每次重启重新付费嵌入。

这是内存精确遍历，不是 ANN，也不是 MySQL 原生向量索引。现有普通 Redis 7.4 容器不被视为开箱可用的向量检索服务。达到数据量、内存或延迟瓶颈后，再按测量选择 Qdrant/pgvector 等，并重新验收 metadata 过滤；首版不宣称支持多实例索引同步。

## 4. 最小持久化与发布协议

| 表 | 最小字段语义 |
| --- | --- |
| `tb_ai_document` | 字符串 ID、稳定 `seed_key`（可空唯一）、scope、shop_id、标题、草稿正文、published_version（可空）、epoch、创建/修改人和时间、停用标记 |
| `tb_ai_document_version` | document_id + version 唯一、不可变正文/内容摘要、`INDEXING/READY/FAILED`、embedding 模型/维度、脱敏错误、创建时间 |
| `tb_ai_chunk` | chunk ID、document_id、version、顺序、原文、embedding JSON、模型/维度；索引 `(document_id, version, ordinal)` |

迁移沿用版本化入口，验证空库与已有库升级，不删除卷。原文与向量都从 DB 恢复；不要求额外保存整个 embedding store 的序列化快照。对维度、数值合法性和缺块进行校验，坏数据不进入 READY。

发布过程：

1. OWNER/MANAGER 的服务端范围校验通过后保存草稿；只有明确提交发布的公开内容才生成不可变 `INDEXING` 候选并允许调用 embedding，记录内容摘要与操作审计。未提交发布的草稿与私密内容不外发；候选完成前不进入问答检索或 Chat 上下文。同文档只允许一个待发布任务；重复请求返回已有任务，不启动第二个嵌入任务；请求结果未知时记录待核对用量，不承诺提供方绝不重复计费。
2. 单个有界后台 worker 分块/嵌入并保存向量；可从 300–500 个中文字、重叠约 50 字开始，topK 从 3–5 开始，阈值由评测调整，不能当成通用最优参数。
3. 全部分块完成且模型/维度一致后准备新内存快照，再以短 DB 事务将版本置 READY、原子更新 published_version 和 epoch；请求只认当前 published_version。旧发布内容在此前继续可用。
4. 嵌入失败置 FAILED，旧发布指针不变；后台重启从 DB 恢复 INDEXING 任务，有限重试后展示失败。索引未恢复/未追上新指针时拒绝该知识查询并恢复索引，不拿旧版本冒充新版本。
5. 停用清空发布指针并递增 epoch，立即阻止后续检索/引用；尚未完成的任务不能在停用后自动重新发布。重建须显式授权，只重建当前选定内容，不隐式恢复已停用知识。
6. 请求先从 DB 确定公开门店与最新发布版本白名单，再以 metadata 前置过滤检索；不要先全库 topK 再过滤。送模型前、最终发给用户前再次核验来源版本/可见性；期间已变更则丢弃该答复并提示重试。

metadata 至少含 `scope/shopId/documentId/version/epoch/embeddingModel/dimension`。平台规则可与当前公开门店规则一起召回，门店 ID 由服务端确认存在、公开；客户端 scope、merchantId 或模型参数不构成授权。版本更换、停用和发布异常均写操作审计。

## 5. 请求链路与只读工具

```text
登录校验 → 输入长度/敏感数据检查 → 用户限流与并发预算 → 会话锁/消息去重
→ 解析当前门店与意图 → 最新公开版本检索 / 只读业务工具
→ 无证据则确定性拒答；有证据则构造最小上下文 → 模型生成受限答复
→ 输出结构/来源/权限复核 → answer + done → 保存允许复用的短期记忆
```

- `searchShops` 仅返回公开门店的必要字段和站内链接；`listShopActivities` 返回服务端当前可见活动与查询时间，不保证到点击时仍有库存。
- `getMyOrder` 只能以当前已登录用户限定查询，响应仅含本人订单状态、金额、期限和站内详情链接；不能按任意 userId/手机号查券，不能返回核销 token。不同用户的订单统一不可见。
- 工具复用现有服务层，参数有长度/页数/ID 校验和结果上限；不把模型生成的 SQL、URL、角色或用户 ID 直接执行。公开知识中的“忽略权限/调用工具”等文本始终是数据，不是系统指令。
- SSE/worker 的异步线程显式携带服务端创建的不可变 `AuthContext`，不只依赖 `UserHolder` 的 ThreadLocal；工具执行时复查登录有效性及当前资源权限，不继承客户端身份字段。
- 实时金额、库存、订单状态用后端校验过的结构化卡片展示；关键交易规则/业务承诺使用确定性模板或原文引句。模型可组织解释、识别意图，但不能产生新的交易承诺。
- 来源 ID 只能取本次合法召回集合；后端生成 `{chunkId, documentId, version, title, scope, shopId?, excerpt, href}`，href 仅站内受控知识详情/公开门店链接。模型自造来源、外链、动作路径丢弃。
- ID 合法不代表原文支持结论。回答的引用支持度仍需固定样例人工评测；本方案不声称自动语义校验或 RAG 可以消除全部幻觉。

## 6. 会话、去重与资源隔离

Redis key 按 `userId + conversationId` 隔离，conversation ID 不作为访问凭证；GET/DELETE/发送均检查本人。TTL 初始 24 小时，限制输入长度、每会话轮数和模型记忆 token 窗口；达到上限提示新建会话，不能无限增长。

UI 可保留带时间的历史答复；模型记忆与历史展示分开。检索片段、工具结果和含实时事实的答复不长期复用，订单引用只记 ID，每轮重新查；知识 epoch 改变后清理相关旧事实上下文。截断时 tool call/result 成组保留或成组移除，不能留下孤立工具消息。

同一会话同时只处理一个请求，其他返回 409；Redis 锁具有租约、所有者 token、有限续期及 compare-and-delete 释放。`clientMessageId` 与输入摘要原子去重：同 ID 不同输入返回 409，处理中重试不触发第二次模型调用，完成后可返回缓存结果并标明原查询时间。

重放也复查身份、权限和知识 epoch，来源失效返回冲突并要求用新消息 ID 查询；缓存工具快照不宣称为最新状态。会话内去重记录保留至会话到期，设置轮数上限；失败/中断有明确状态，UI 的主动重试使用新 ID，不能无限自动重试花费。

AI 使用独立有界线程池/队列和单独连接/调用超时，不占住下单消费者或交易线程池。至少配置每用户频率、全局在途上限、输入/输出 token、工具轮次/返回条数、总耗时和每日预算；先用保守可调默认值，再根据真实提供方配额校准。

前端取消或连接断开及时中止可取消的调用、释放资源；无法取消的底层请求也受总超时限制，不产生第二次业务调用。429、预算耗尽、提供方超时、格式错误不伪装成规则答案。仅记录脱敏 requestId、耗时、错误分类、token/费用估算、检索和工具统计，不记录密钥/完整敏感提示词。

## 7. 计划 API 与前端契约

下列均待实现。普通 API 沿用 `Result`、字符串 ID 和现有 `authorization` 请求头；401/403/409/429/503 区分鉴权、权限、冲突、限流和能力不可用。

| 方法与路径 | 最小契约与权限 |
| --- | --- |
| `GET /ai/capabilities` | 返回 `enabled/ready/reason` 及能力开关，不返回 Key、内部地址或敏感错误 |
| `POST /ai/conversations` | 已登录，新建本人会话，返回 conversationId |
| `GET /ai/conversations/{id}` | 本人、受限条数的历史；历史回答带时间及引用失效标记，不把它作为当前规则；不再提供停用来源的全文/摘录详情 |
| `DELETE /ai/conversations/{id}` | 本人；取消进行中任务，清记忆/去重，删除后原请求不得重新写回会话 |
| `POST /ai/conversations/{id}/messages` | `{clientMessageId, message, shopId?}`，本人；响应 `text/event-stream` |
| `GET /ai/knowledge/{documentId}?version=...` | 已登录，只返回当前 READY 发布且 PUBLIC/SHOP_PUBLIC 的公开来源；旧版/停用不可读取，不能访问草稿 |
| `GET /merchant/knowledge?shopId=...` | OWNER/MANAGER 查看本商家的草稿/发布状态；STAFF 仅授权门店已发布公开 FAQ，只读；均分页 |
| `POST /merchant/knowledge` | OWNER/MANAGER，`{shopId,title,body}`，只创建 SHOP_PUBLIC 草稿 |
| `PUT /merchant/knowledge/{id}` | OWNER/MANAGER，本商家，修改草稿；不原地改已发布版本 |
| `POST /merchant/knowledge/{id}/publish` | OWNER/MANAGER；返回版本与 INDEXING/READY/FAILED，UI 轮询列表 |
| `POST /merchant/knowledge/{id}/disable` | OWNER/MANAGER；同步使发布来源失效，有审计 |
| `POST /merchant/knowledge/{id}/rebuild` | OWNER/MANAGER；显式重建，不能绕过停用/版本与范围校验 |

用户端新增助手入口/门店上下文、来源卡片、真实数据卡片和业务页跳转；商家区新增简单 FAQ 列表/编辑/发布状态页，不先做文档中心或大屏。STAFF 只读授权门店的公开 FAQ，不读取后台草稿正文。

首版使用 `fetch` POST、读取响应流，携带现有 authorization 头和 AbortController；原生 EventSource 不能直接满足这里的自定义请求头与 POST。请求进入前错误返回普通 JSON；SSE 开始后错误使用 error 事件，不再假装能修改 HTTP 状态码。

SSE 事件约定：`meta`（requestId/conversationId）、`status`（检索/查询/生成阶段）、`sources`（合法来源卡片）、`tool_result`（已脱敏结构化卡片）、`answer`（一次最终答复及合法 citationIds/actionRefs）、`done`（完成时间/用量）、`error`（稳定错误码/可重试性）。每个请求恰有一个终结事件 done 或 error，流意外断开显示“已中断”，不显示成功。

这是阶段反馈 SSE，**不是逐 token 直传**；先缓冲模型答复，校验后发送一次 answer，不人为分片打字。无依据业务在证据门槛处拒答，不能先流出业务断言再撤回。已有合法来源/工具卡片可先展示，但都需发送前复核，真实业务数据不经模型改写。

## 8. 固定知识与证据

准备 8–12 条稳定 `seed_key` 的平台 FAQ：模拟支付/退款、异步受理与成单、销售期与核销期、超时关单、取消可重买/退款不可重买、核销与联系商家等。门店 A1/A2/B1 各 2–3 条独立公开规则，其中至少一组规则刻意不同，用于验证门店过滤；种子版本、预期答案和依据 ID 一同保存。

按 [AI 验收](./ai-acceptance.md) 提交固定评测、跨用户/门店拒绝、提示注入、发布切换/停用、重启恢复、重复消息、超时取消和交易隔离的证据。Stub 可检验协议与权限，不能代替真实模型、真实 embedding 和真实 MySQL/Redis 端到端验收；没有 Key 时把这些项目明确写为未执行。

P2 报告绑定提交、模型/维度、提示词/知识版本、机器、样例与原始脱敏结果。AI 小并发/延迟/成本与交易压测分开，额外比较 AI 开/关时交易链路；不把模型提供方吞吐称为本项目交易容量，不给尚未测量的准确率或延迟数字。

## 9. PDF 重点采纳与纠错

页码按 336 页 PDF 的**物理页，从 1 开始**：

| 页码 | 采纳 / 修正 |
| --- | --- |
| 67–78 | 智能客服、会话、工具与业务结合的构思可采纳；作者第 67 页也说明方案较浅，不能当成已验证的生产架构。提及的 `consultant.zip` 只是 PDF 附件引用，当前没有找到可复用本地源码 |
| 71 | 截图中的“手机号查用户券”不直接复制；本项目以当前登录身份查本人订单，手机号不作为授权凭证 |
| 74–76、84–86 | 话术、业务问答与工具示例转成固定评测/只读工具；不把提示词当作权限控制 |
| 87–88 | MCP 是模型上下文交换协议，不能将其等同于提示词、RAG 或任意 Function Calling；本项目首版内部工具无需额外建设 MCP 服务 |
| 151–152 | 保留检索→补充上下文→回答的 RAG 思路；补齐版本、权限、持久化、拒答与评测。RAG 降低部分幻觉风险，不能保证零幻觉 |

## 10. 实现时核对的官方资料

- [LangChain4j RAG](https://docs.langchain4j.dev/tutorials/rag/)、[工具调用](https://docs.langchain4j.dev/tutorials/tools/)、[Chat Memory](https://docs.langchain4j.dev/tutorials/chat-memory/)、[Spring Boot 集成](https://docs.langchain4j.dev/tutorials/spring-boot-integration/)：概念与 API 需按实际锁定版本核对。
- [内存 EmbeddingStore](https://docs.langchain4j.dev/integrations/embedding-stores/in-memory/)、[存储能力比较](https://docs.langchain4j.dev/integrations/embedding-stores/)：原型存储及 metadata 过滤能力；MySQL 持久化是本项目的设计选择。
- [百炼 OpenAI 兼容接口](https://help.aliyun.com/zh/model-studio/compatibility-of-openai-with-dashscope)、[文本向量接口](https://help.aliyun.com/zh/model-studio/text-embedding-synchronous-api)：账号空间/地域地址、模型、维度、限流和计费以实现时官方配置为准。
- [MCP 官方架构](https://modelcontextprotocol.io/docs/learn/architecture)：区分协议与本项目内部的只读工具调用。
