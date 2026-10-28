# GO-075d 真实 HTTP/SSE 终态

按[事前计划](lifecycle-plan.md)，实际百炼运行一次通过11.78秒：[原始JSON](lifecycle-real-initial.json)、[日志](lifecycle-real-initial.txt)。代码使用真实loopback HTTP Server/Client、Gin路由、开发登录/退出和专用MySQL/Redis，未mock成功JSON；不是Vue浏览器验证。

五项全部通过：

1. Chat进入实际HTTP适配器100ms后取消客户端，远端请求观察为cancelled；无answer/done/assistant，lease/dedupe消失，逻辑预算0。
2. 测试包装器给真实Chat20ms deadline，观察timeout约20.52ms，SSE error且无完整答案，清理/预算同上；未声称等了生产45秒。
3. 生成期间用真实退出接口撤销旧Token，Provider成功返回但终态身份检查丢弃答案；无assistant落库，预算退款。
4. 同用户另一有效Token重试成功，一次answer/done、一条assistant记录，lease释放、逻辑预算保留1。
5. 再发消息达到1/min、1/day限额，明确error，远端新增尝试0，旧答案不冒充新成功。

本人/对照业务订单在前后均保持未支付。全部自有知识/用户/订单/session/code/throttle/conversation/dedupe/budget由精确夹具清理；不清整个Redis或共享数据。

实际9次远端HTTP尝试，7次返回usage合计415 tokens；客户端取消/期限的2次usage未知，是否远端已结算也未知。已知用量为Embedding62输入、Chat315输入/38输出；按评测报告同日官方北京原价仅已知部分约`0.0003856元`，总费用不能据此算作完整账单或零费用。未请求真实供应商429；本地HTTP429/no-retry协议由适配器测试覆盖。

[独立只读审查](lifecycle-review.md)核对了源代码与实际结果，不增加付费调用；根agent另外读完整实现和输出。票级独立核验仍待外部核验流程。
