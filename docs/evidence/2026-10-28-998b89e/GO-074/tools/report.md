# GO-074c：只读工具卡片真实页面差量结果

2026-10-28 首轮 [round-1.json](round-1.json) 自测通过，23 项检查、5 轮真实 Vue/SSE 对话，精确清理成功。运行前由另一实现者读审脚本，stock 按既有字符串契约断言；本专项没有失败运行轮次。此结论交主实现者核验，不替代独立验收。

## 版本与运行边界

- 门店卡片独立修复：`13d5f49c787740f9bf2b83b9c843fb4ce3d6e1c1`，仅按既有 shops DTO 显示 name/address，并提供同一字符串 ID 的详情链接。构建/格式见 [shops-build.txt](shops-build.txt)、[shops-format.txt](shops-format.txt)。
- 活动 DTO 独立修复：`b97b03adaa845136773da5e7cba16c8ab2677520`，补当前 stock 与所属 shop href。本脚本不修改该 Go 实现或活动卡片。
- 专用栈：MySQL `3381`、Redis `6451`、API `8121`、Vue `5221`；真实 API revision 为 `go074-b97b03a`。归档 [binary-buildinfo.txt](binary-buildinfo.txt) 的源码 revision 为 b97b03a，且 `vcs.modified=true`，据实保留，不声称二进制来自干净 checkout。
- `AI_PROVIDER=stub`、真实 Provider Key 为空，Provider observer 未启动。本次只问能够命中 Go AITool 确定性分支的问题，没有知识发布、检索或生成操作，没有重新运行已封存的真实 Provider 12 次调用，也不评价模型质量。

## 实测结果

| 验收项 | 实际结果与证据 |
| --- | --- |
| TOOL-01 门店 DTO、地址、大 ID 导航 | 自有门店 `9007206678540896` 大于 2^53，真实 shops DTO 提供 name/address，没有 href。Vue 显示实际门店名、`桂花路88号合成地址-GO074Tfc5b1cd834`，无虚构评分、undefined 或 NaN。DOM href 为 `/shops/9007206678540896`，实际点击后的精确 URL 与详情 heading 均通过。见 [门店卡片](round-1-shop-card.png)。 |
| TOOL-02 真实发布、库存与价格 | 真实 HTTP 发布自有库存 3 的活动，等待准入 ready；第一轮真实 SSE 的活动卡片 stock=`"3"`、payValue=`"1900"`，Vue 显示库存 3、¥19.00。见 [首次库存](round-1-stock-3.png)。 |
| TOOL-03 同会话刷新真实交易后的库存 | 真实抢券准入，经 Redis Stream 持久成单 `653792879713452033`，SQL 与 Redis 库存均变为 2。保留同一 conversation、使用不同 clientMessageId 再询问，新的 SSE 卡片 stock=`"2"`、价格仍 `"1900"`。活动 href 为该门店精确字符串路径，实际点击后的 URL/详情 heading 通过。见 [同页库存 3→2](round-1-stock-2.png)；完整会话 ID/消息 ID 仅用于进程内比较，不落证据。 |
| TOOL-04 未知 ID | 先确认未知门店/活动 ID 不存在。两轮真实 SSE 均明确“没有找到”，无新增工具卡片、链接或价格。见 [未知活动](round-1-unknown.png)。 |
| TOOL-05 运行边界与隐私 | capabilities 明确 stub。全部 5 轮 HTTP 200、answer/done 完整，观察到的阶段只有 `tool`；未知 ID 没有 tool_result。无 retrieving/generating/sources，无未捕获浏览器错误。只归档白名单工具字段、公开合成内容和脱敏截图。 |

人工已查看库存 3→2 与门店卡片截图，确认价格、库存变化、名称/地址可读且无虚构评分。两类卡片进入详情的证据来自真实浏览器点击后的精确 URL 和 heading 断言；未将卡片截图当作详情页截图。

## 脚本与清理

脚本为 [go-ai-tools-ui-smoke.mjs](../../../../../scripts/go-ai-tools-ui-smoke.mjs)。它使用已提交的 SSE reader 观察辅助，只观察应用原本消费的 read 结果，不创建额外 reader/read，不 clone/tee，不改变原 cancel；回调带来少量观察开销。未开启鉴权 trace/HAR，也不保存 Token、完整手机号、核销凭据、完整会话 ID 或消息 ID。脚本语法与格式检查见 [script-validation.txt](script-validation.txt)；文本证据完整手机号/32位会话Token模式与trace/HAR文件检查均为0，见 [privacy-check.json](privacy-check.json)，同时人工核对截图和白名单字段。

实际执行为 `node scripts/go-ai-tools-ui-smoke.mjs`，显式传入上述专用 API/WEB、MySQL/Redis 环境和固定本地 Playwright/Chrome 路径，`UI_EVIDENCE` 指向全新 `tools/round-1.json`。没有 mock 成功 JSON，没有直接伪造库存结果。

脚本先等待自有 AI lease 归零、实际订单持久化、自有 outbox 完成及 review 无未结项；1 条自有 Stream 消息的 PEL 为 0，unfinishedOutbox=0、unresolvedReview=0，再精确删除自有 SQL 行、已 ACK 的自有 Stream 消息与自有登录/AI/交易 Redis 键。未补发 XACK，未 flush，未删除共享 group、日序号或卷。`cleanup=true`，清理后自有 SQL/消息/Redis 键再次核对为空。

主实现者另行归档 [environment-final.json](environment-final.json)：最终业务数据/登录/AI/交易键和 PEL 均为空，工具 API/Vue/专用容器正常停止、卷保留，其他运行容器集合不变；这部分停止操作由主实现者执行。本实现者在运行完成后未再发起服务请求。

本专项只覆盖既有工具路径、当前 Chrome 与自有有限合成数据；没有扩展模型质量、真实 Provider 调用、吞吐或压测结论。
