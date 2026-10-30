# CAP-03 早停终态计量修复

首个受限档 [hot-200-a-load/result.json](hot-200-a-load/result.json) 触发 k6 原阈值提前停止。旧计数只有 HTTP 调用前的 `attempted=1902` 和各项完成后分类；原始 native `http_reqs=1390`，Rate 分类样本为 1241 成功、149 错误，尚有 512 次调用没有已知分类。原报告已判失败，但缺少显式未知数，系统错误除以所有 started 只能得到 7.834%，不能描述已分类请求中的 10.719%。

修复增加 `cap_read_completed`，放在一次请求的分类、成功延迟和有界诊断完成后。报告分别保存 started、native HTTP 完成（含传输失败）、终态分类和 success，检查 `started >= native >= terminal >= success`，单列未完成/未分类及已完成 HTTP 但未分类数。只有 started/native/terminal 完全相等时才允许通过。HTTP/系统错误可重叠，不能相加为失败总数；这只是客户端可见结果，不是服务端执行或持久化完成证明。

原 HTTP/成功比例/延迟/dropped/资源预算不放宽，不修改应用、数据、查询或超时。名义每秒速率仍除以原计划秒数，早停档明确保留该分母，不能用它声称持续吞吐。

旧 raw、`result.json` 和失败 summary 均不改写。[hot-200-a-summary.json](hot-200-a-summary.json) 的独立 `completionAudit` 使用旧 Rate 的 `passes+fails` 推导已完成分类样本，与本次 native HTTP 数相等，未知 512 显式保留。旧 Rate 插桩点比新 terminal 略早，不能声称两代在任意指令中断点完全同义。此前 hot25/50/100 的所有 started 与 native/旧分类都一致，未知数为 0。

直接验证：两份 Node 脚本 `node --check` 均退出 0。新脚本真实 hot150/30 的 started/native/terminal/success 均 4501，hot160/30 均 4800，unknown 0，原预算均通过；原 hot200 失败保留。新 hot175 早停明确记录 started1806、native/terminal1465、success1443、system/http22、unknown341，已分类系统错率 1.502%，仍按原阈值失败。运行和脚本 SHA 随各轮输出保存。

`cap_trade` 进行了非作者只读审查，核对新字段单调关系、abort/JS 异常 fail closed、原始 hot200 Rate 与 native 计数及分母，未发现阻断。此为内部审查和直接实测，不代替后续独立 QA/race。
