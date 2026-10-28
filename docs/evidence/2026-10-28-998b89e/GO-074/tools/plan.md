# GO-074c：AI只读工具卡片的真实页面差量

本票仅补既有只读工具的DTO/页面契约，模型质量与已完成真实Provider 12次调用不重跑。依赖已核验的Go工具路由、公开商铺/活动与真实交易链路，使用workspace-delivery和playwright-expert。本计划先写，实际结果另记，不能以计划当通过。

## 拆分与范围

- GO-074c2：仅修 `AssistantView.vue` shops 分支，按实际DTO显示name/address；缺href时落到该字符串id的 `/shops/:id`。同一门店的有效站内item.href保留兼容，不读取不存在的area/score，不改后端或活动卡片。
- 活动DTO缺stock/shop href由另一实现者独立修复提交，保持既有可见活动过滤；本脚本验证真实库存变化，不改活动页面分支或Go代码。
- 工具专项脚本、报告与脱敏证据另行提交；真实Provider测试仪器/费用不并入本票。

## 环境与不付费边界

仅由主实现者启动专用MySQL3381/Redis6451/API8121/Vue5221，配置AI_PROVIDER=stub，真实Provider关闭。脚本验证capabilities明确stub，并只发送能命中Go确定性AITool路由的“门店 <id>”“活动 <id>”问题；没有知识发布/检索问题，不生成embedding，SSE应出现tool_result而不出现retrieving/generating阶段。无外部Provider凭据、无付费请求；这不是模型质量或Provider协议验收。

## 实际页面矩阵（执行前）

| ID | 操作 | 预期 |
| --- | --- | --- |
| TOOL-01 | 真实登录后同一Vue会话询问自有大ID门店 | 真实tool_result为shops，页面显示实际name/address，无虚构评分；真实点击进入精确大ID的商铺详情 |
| TOOL-02 | 店主真实HTTP发布库存3/价格1900的活动并等待ready | 同会话询问活动，真实卡片stock=3、price='1900'、页面¥19.00，链接进入所属门店详情 |
| TOOL-03 | 本人真实抢券准入→Stream持久成单，后在原会话再询问同一活动 | SQL/Redis库存均2，新的tool_result和页面显示2，不复用原库存3；不把缓存历史作为实时结果 |
| TOOL-04 | 未知门店/活动id | 明确没有找到，新增该轮没有tool_result卡片、无虚假链接或价格，仍为确定性工具结果 |
| TOOL-05 | 隐私与精确清理 | 不保存trace/HAR/Token/手机号/核销凭据；只保存白名单SSE工具字段及截图，等待本人lease/PEL/outbox完成后精确删自有行/消息/Redis键，保留共享Stream/group/日序号 |

脚本只造自有合成账号/商家/门店/活动。未知ID先用SQL确认无记录，消息保留空格分隔大ID以命中当前明确工具路由。成功响应只读真实API，不mock成功JSON；截图不含认证字段，完整会话ID和clientMessageId只在进程内。需要解析SSE时只观察Vue实际read结果，不tee/clone/增加reader；使用已通过零付费探针的观察辅助，明确存在小回调开销。

## 当前状态

shops单文件修复经构建/格式与主实现者审查后独立提交 `13d5f49c787740f9bf2b83b9c843fb4ce3d6e1c1`；活动DTO依赖独立提交 `b97b03adaa845136773da5e7cba16c8ab2677520`。脚本由另一实现者执行前读审，2026-10-28 在主实现者准备的专用 stub 栈首轮实跑 23 项自测通过，cleanup=true；原始数据、截图、边界及清理见 [report.md](report.md)。不把自测通过写成独立验收通过；后续失败轮次仍须另存，不覆盖。
