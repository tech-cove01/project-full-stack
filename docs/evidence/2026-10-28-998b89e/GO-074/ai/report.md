# GO-074b 真实 AI Vue 页面差量

2026-10-28，FIN-05本票实现者自验通过：真实Chrome + Vue + Go + MySQL/Redis + 百炼兼容API，第二轮35项断言、11.541秒，5张页面截图。未重跑GO-075的30题；不把本次小页面差量作为模型整体质量或生产容量结论。执行前矩阵见[plan](plan.md)，完整原始结果见[round-2/result.json](round-2/result.json)、[运行输出](round-2-run.txt)。

## 版本、真实边界与独立问题

起点`998b89e81bed42786d25d0cebbadb401fd506f70`。页面执行时HEAD为`37f645bfd9a3a3f5af0899175fe6fcf81117a90f`，其新增提交包括外部GO-076核验文档。运行Go二进制在QA2提交前工作树构建，[buildinfo](../binary-buildinfo.txt)明确为`vcs.revision=6575610e0c9c43b349f65abb42c5bc539b7ef2c7`、`vcs.modified=true`；本次直接复验QA1/QA2的实际行为，不将二进制声称为fa474/37f精确干净构建。本票脚本/证据在执行时尚未提交，最终交付SHA由上级汇总，不伪称测试使用未来提交。

- GO-074b-QA1 来源链接：后端原href命中Vue不存在的`/knowledge/:id`。精确路径预期首次FAIL、修复后PASS，独立提交`6575610e0c9c43b349f65abb42c5bc539b7ef2c7`，见[href报告](href-report.md)。本轮实际点击两类来源，正确打开`/ai/knowledge/:id?version=1`、标题及冻结正文。
- GO-074b-QA2 来源类型：Go契约PUBLIC/SHOP_PUBLIC，旧Vue误用PLATFORM且公开详情缺scope。补DTO/SQL与两个页面标签，范围内真实依赖测试及前端构建/格式通过，独立提交`fa47463b34705b88d65e7bf3ae02ec9e82ef7b7e`，见[scope报告](scope-report.md)。

专用project `project-full-stack-go074-ai`，MySQL3381/Redis6451/API8121/Vue5221；不启动或停止容器，不触碰GO-076核验环境。Provider沿用户已授权的CRM本地配置，由root仅在内存注入Key。Chat `qwen3.8-flash`、Embedding `text-embedding-v4`1536维。本票新建合成咖啡停车FAQ和本人/他人测试订单；PUBLIC对照复用同正文的本票已存向量，没有复制CRM业务数据。

Go通过root管理的loopback9291可信观察器发往固定真实百炼HTTPS。观察器不改正常成功正文，只记有界operation/model/status/usage/cancel元数据，累计上限12次。`pending`只能证明真实上游HTTP转发已发起，不能证明供应商内部已生成token或已计费。截图不含Token/Key/完整手机号；不保存认证Playwright trace；完整会话ID/clientMessageId只在进程内用于精准事实核对/清理。

## 实际页面与逐轮人工复核

以下逐条读完原始answer、sources、tool和5张截图作出的人工判断；正则断言只作初筛，没有调用模型自评。

| 矩阵/轮次 | 人工判断及依据 |
| --- | --- |
| UIAI-01 空态 | PASS。[空白截图](round-2/empty.png)显示助手空态，输入3个空格不发turn；实际capabilities为ready/openai-compatible，没有stub提示。 |
| UIAI-02/08 grounded | PASS。回答明确消费顾客、东侧停车场、免费两小时、超时每小时五元，均与合成正文一致，没有新增承诺。来源7为SHOP_PUBLIC、来源8为PUBLIC、均v1，摘录相同且合法；两个标签正确，真实点击后详情标题/正文/scope一致。[回答与来源截图](round-2/grounded-sources.png)。正文中的`**`按现有纯文本展示，事实可读；本票未增加Markdown渲染。 |
| UIAI-03 own-order | PASS。回答“订单状态：待支付。”，真实订单工具status=1、payAmount字符串2500；Vue卡片显示本人订单、待支付、¥25.00，与SQL夹具一致。此轮不调用模型。[工具截图](round-2/read-only-tools.png)。 |
| UIAI-03 foreign-order | PASS。回答“没有找到这个订单。”，无工具卡、无他人状态/金额；本人和他人订单最终仍为1/2。此轮也不调用模型。 |
| UIAI-04 unknown | PASS。回答只说明查到停车规则、没有查到宠物政策，没有猜测允许或禁止。仍显示相关停车来源7，不把该候选当宠物政策依据。[停止截图上半部](round-2/stopped.png)同时保留该轮可见答复。 |
| UIAI-05 cancel | PASS。先观察到新chat id10 pending，再点击真实Vue“停止”。应用实际读取事件止于meta/status/sources/status，没有answer/done；显示“已停止本次查询”。观察器最终client_cancelled且clientCancelled=true，usage未知。assistant历史4→4，逻辑日预算4→4，等待该轮lease/dedupe不存在后继续。[停止截图](round-2/stopped.png)。 |
| UIAI-06 recovery | PASS。同一Vue会话再次提问，真实模型回答免费两小时，合法来源7/v1；只新增1条完成assistant历史。属于新clientMessageId的正常恢复，不宣称同ID重放。恢复回答在[停用后截图](round-2/disabled-knowledge.png)上半部可见。 |
| UIAI-07 disabled-knowledge | PASS。受控PUBLIC重复文档已先停用，再通过真实商家API停用唯一SHOP_PUBLIC文档。随后当前轮明确没有已发布依据，未复述旧两小时/价格、无旧sources，旧详情API404。该轮不调用Provider。[停用后截图](round-2/disabled-knowledge.png)。已渲染的旧聊天卡片仍在页面上，本结论只覆盖新轮检索/回答及旧详情权限，不宣称追溯删除旧DOM或页面刷新恢复历史。 |
| UIAI-09 身份/清理 | PASS。本轮U2 GET U1当前会话404；本人订单不跨用户，全部turn只记authorizationPresent布尔值。最终自有SQL行0、AI键0、cleanupErrors空。续聊越权终态的后端协议沿用GO-075，本轮未单独发U2续聊请求。 |

## 首次失败与测试仪器限制

[round-1/result.json](round-1/result.json)和[首次运行输出](round-1-run.txt)保留：首轮实际3次请求，页面已有正确两小时/五元回答和两类合法标签，但Vue读到done后取消reader，Playwright事后`response.text()`无法取正文，导致采集断言失败。[首轮截图](round-1/failure.png)由root与实现者人工查看，属于测试仪器失败，不能判产品失败，也不能把首轮算完整矩阵通过；首轮清理已成功。

替代CDP被动观察的零付费探针也经历[首次失败](sse-capture-probe-initial.txt)、[快速流采集失败](sse-capture-probe-second.txt)，保留[失败helper源码](sse-cdp-capture-initial.mjs)及可复跑[CDP探针](sse-capture-probe-cdp.mjs)，不作为实际脚本依赖。

最终脚本使用应用实际读取数据的旁路观察辅助`scripts/lib/sse-reader-observer.mjs`，为原fetch/read Promise挂小回调，保留原Response、reader、read结果对象及cancel/error，不tee、不增reader/read、不二次读取SSE。它有少量回调开销，不能称wire/CDP抓包或零干扰。[零付费reader探针](sse-reader-probe.mjs)覆盖快速done、done后reader.cancel、原生Abort→真实本地server close，并逐个断言原Promise/对象/cancel引用；[首次reader探针](sse-reader-probe-initial.txt)、[格式后结果](sse-reader-probe-final.txt)均PASS。root与ai_eval非作者审读通过，ai_eval独立重跑该probe通过；其首次运行遇沙箱loopback限制、用本地权限重跑PASS，由独立审方保留说明，不声称所有仪器实验首次成功。

## 用量与取消结算

观察器全程未重置。首轮3次、286已知tokens；第二轮显式baseline3，新增9次，完整累计12次已达硬上限，之后不再付费调用。完整12条安全观察元数据在round-2/result.json，包含首轮，不只统计成功二轮。

| 范围 | 实际HTTP尝试 | 已知usage tokens | 未知usage |
| --- | ---: | ---: | ---: |
| 首轮 | 3 | 286 | 0 |
| 二轮增量 | 9 | 769 | 1（取消Chat id10） |
| 两轮合计 | 12 | 1055 | 1 |

合计7次Embedding全部完成，130 input tokens；5次Chat尝试，其中4次完成：已知826 input +99 output=925 tokens，另1次取消Chat未知。1055只包含有usage的返回，不能当实际总token或结算总额。未核验本轮单价/账单，人民币金额与取消结算均未知，不能按0计费；逻辑预算退款只代表应用计数恢复，不代表供应商退款或停止计费。

## 运行与清理

代码检查：`node --check scripts/go-ai-provider-ui-smoke.mjs`、`node --check scripts/lib/sse-reader-observer.mjs`与本地Prettier检查通过。复跑reader探针只访问自己临时loopback HTTP服务，不使用真实API/Provider。

真实页面执行命令为`node scripts/go-ai-provider-ui-smoke.mjs`，此前显式设置专用API/WEB/MySQL/Redis、数据库凭据、本机Playwright module/Chrome、`AI_UI_OBSERVER_URL=http://127.0.0.1:9291/metrics`、`AI_UI_OBSERVER_BASELINE=3`、`AI_UI_EVIDENCE_DIR=docs/evidence/2026-10-28-998b89e/GO-074/ai/round-2`。脚本强制专用端口和新输出目录，不能覆盖原证据。真实供应商Key仅由root负责注入服务，不是脚本参数；不得在目前已达12次的同一观察器上再次运行付费流程。

终态后等观察器无pending、本人AI租约结束，再DELETE本人会话、注销3个自有登录Token，精确删除本人conversation/messages/dedupe/rate/budget及自有code/throttle键；然后清理本轮FAQ/version/chunk/审计、2订单、券、商家成员/店/商家/3用户，SQL事务提交并核对残留0。首轮与二轮均cleanup=true，没有flush、删卷或操作未知夹具。运行服务的最终停止和总环境对账由root负责，本报告不预写其结果。

本票范围为桌面Chrome、上海时区、单合成FAQ的必要浏览器差量。无手机/跨浏览器、生产容量、真实429/账单、供应商内部计算证明；活动/商铺工具卡片旧DTO缺失显示字段已另报，本次仅订单工具通过，不宣称库存/评分展示已核验。执行前脚本经过非作者审查，最终原始结果另交复核，仍与外部独立核验区分。
