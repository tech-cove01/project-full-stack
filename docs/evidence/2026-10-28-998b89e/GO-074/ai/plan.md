# GO-074b 真实 AI Vue 差量矩阵（执行前）

起点 `998b89e81bed42786d25d0cebbadb401fd506f70`。对应 FIN-05 / AI-03/04/05/06/09/11/12；GO-075已核验的30题不重跑。使用 workspace-delivery 与 playwright-expert：角色/placeholder定位、Page Object、明确真实状态等待；本票少量付费与同一FAQ变更流程刻意串行，避免并发扩大费用或污染状态。保留首次失败；截图和脱敏SSE摘要替代包含凭据的完整认证trace。

## 固定环境与调用边界

仅 root 管理专用 project `project-full-stack-go074-ai`：MySQL3381/Redis6451/API8121/Vue5221；本脚本不启停容器，不使用GO-076核验端口。真实Provider由root沿用用户授权的CRM配置，仅在内存传递Key；不把CRM数据/配置复制到仓库。API经可信loopback透明观察器9291转发到固定真实百炼HTTPS，观察器只记operation/model/status/usage/cancel元数据，不改成功正文，不把代理当供应商。向量是本票全新公开合成FAQ。

预计知识发布1次Embedding；有据、无据、取消、恢复4轮各最多查询Embedding+Chat，共约9次上游尝试。上限12次真实HTTP尝试、最多20000已观察tokens，单Chat输出按root配置有界，整脚本8分钟内结束。真实取消可能没有结算usage，明确标未知，不能算0费用；观察器不确定是否已到供应商计算也不能宣称停止计费。不做真实429、生产超时等待、吞吐或重跑30题。

## 差量矩阵

| ID | 操作 | 通过条件与证据 |
| --- | --- | --- |
| UIAI-01 | 新建本人真实Vue会话，空白发送 | 显示空态，空白消息不发请求；能力为openai-compatible且Ready，页面不伪报stub |
| UIAI-02 | 提问公开停车规则 | 实际鉴权POST/fetch SSE，meta/status/sources/answer/done；回答有正确两小时/超时价格与当前合法来源；页面可见，来源链接实际点击后打开正确版本详情 |
| UIAI-03 | 本人订单与另一用户订单号 | 本人卡片与SQL状态/金额一致；他人订单拒绝且无其卡片/状态；这两轮不新增模型请求、无业务写副作用 |
| UIAI-04 | 没有公开依据的服务问题 | 明确未知/无法查到，不能编造有无该服务；允许相关但不足的来源候选，不把候选卡片算有答案 |
| UIAI-05 | 实际生成期间点停止 | 先等观察器operation=chat/status=pending（真实上游fetch已开始），再点Vue停止；不得先完成后延迟返回。观察取消/失败语义，无answer/done、无新增assistant持久消息；本人lease/dedupe/逻辑预算占位释放。取消账单未知 |
| UIAI-06 | 停止后重新正常提问 | 同一Vue会话可恢复发送，真实答复与来源正确；只保存一个新的完成assistant消息；没有假装是同clientMessageId幂等重放 |
| UIAI-07 | 在同会话停用唯一合成FAQ再问 | 实际商家Disable使旧公开版本失效；新答复不复述旧两小时/金额、不含旧来源，旧来源详情拒绝。检查后端历史与新轮结果，不宣称Vue已实现刷新历史恢复 |
| UIAI-08 | 平台/门店来源类型差量 | 针对PUBLIC与SHOP_PUBLIC正确显示标签；必要PUBLIC合成对照复用完全相同公开正文的已存向量，不新增付费Embedding、不混用向量空间。对照是受控平台fixture，不宣称新增平台管理API |
| UIAI-09 | 身份与费用/清理 | U2不能GET U1会话（续聊协议沿用GO-075，本轮不新增）；只记录鉴权存在布尔值，不保存值。归档safe screenshot/SSE事件摘要和观察usage；注销自有session，等待本人lease结束，再精确清理自有FAQ/version/chunk/审计/业务行，以及本人conversation/messages/dedupe/budget/code/throttle键 |

## 已发现独立问题（修复前）

1. GO-074b-QA1：后端来源href为`/knowledge/:id`，Vue详情实际为`/ai/knowledge/:id`。先补现有真实依赖stub测试的精确路径预期，保留FAIL，修复后PASS与真实来源点击复验；独立提交。
2. GO-074b-QA2：两个Vue页面使用PLATFORM判断平台规则，但Go来源契约为PUBLIC；公开详情DTO还缺scope，无法判断。该来源类型显示问题独立修复/提交，不与href或脚本合并。

本人订单工具是本轮只读工具范围；现有活动/商铺卡片还有与DTO不一致的空库存/评分显示，本轮不据orders通过宣称该部分已验证。根据独立问题安排后续，不扩大本次付费矩阵。

## 证据与复跑

脚本 `scripts/go-ai-provider-ui-smoke.mjs`。运行必须显式给出上述专用API/WEB/MySQL/Redis、`PLAYWRIGHT_MODULE/CHROME_PATH/AI_UI_OBSERVER_URL/AI_UI_EVIDENCE_DIR`，输出目录必须为空，所有成功响应来自真实API/模型，不mock。不得将登录Token、Key、完整手机号、完整会话ID或未脱敏驱动异常写入证据；新失败用新目录，保留已有输出。

上表是执行前门槛，本计划本身不作为通过证据。root提供环境、观察器与必要非作者审查后才执行真实调用。结果另记[report.md](report.md)，包括精确代码SHA、实际调用/usage/未知费用、截图/SSE、首次失败、清理与未覆盖项；此实现者自验不替代最终独立核验。

## 首轮后采集修正与继续边界

首轮已有3次真实请求、286已知tokens，产品页面完成正确回答，但Playwright在Vue于done事件后取消reader时无法再读取`response.text()`，导致测试采集失败。原始round-1输出、截图及失败保留；此结果不算产品失败，也不算全矩阵通过。CDP被动采集替代方案的本地快速流探针仍失败，源码与探针原件保留本目录，不作为实际脚本依赖。

继续轮显式`AI_UI_OBSERVER_BASELINE=3`，启动核对完整journal已有3次且无pending；不清零观察器，所有轮累计仍最多12次，剩余最多9次；最终费用包含首轮3次。新增测试辅助`scripts/lib/sse-reader-observer.mjs`，只旁路观察Vue实际读取的bytes，不tee、不增加reader/read，不替换Response/reader/read结果或原Promise，不修改cancel/error。它增加少量回调，不能称零干扰协议抓包；本票主要验真实页面行为，不重做GO-075协议验收。零付费`sse-reader-probe.mjs`核对快速完成、done后cancel、原生Abort及本地server close，且用内层spy逐一验证原Promise/Response/Reader/read结果引用。
