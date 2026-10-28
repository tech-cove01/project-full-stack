# GO-075c 实际质量评测

固定集与预算在运行前写入[eval-plan.md](eval-plan.md)、[eval-cases.json](eval-cases.json)。真实原始结果：[eval-real-initial.json](eval-real-initial.json)，命令日志：[eval-real-initial.txt](eval-real-initial.txt)。该轮一次通过，未筛选成功轮替换失败结果。

实际配置：百炼北京`https://dashscope.aliyuncs.com/compatible-mode/v1`，Chat `qwen3.8-flash`、关闭思考、输出上限400；Embedding `text-embedding-v4`，1536维；topK5/minScore0.5，单线程，整集51.39秒。知识为12条合成公开短文档，每份一个黄金chunk；6条门店FAQ经实际商家发布，6条平台FAQ经受控SQL准备。MySQL3331/Redis6403为专用实例。

运行时生产代码等同已提交`77aa81041e33175e92abfadcde6d82dd240943ba`，评测代码/数据当时未提交。随后`33bdb4d91d7bb83cc73af45cc36849a6bfc2b448`仅修正JSON null向量拒绝；本轮真实返回全为合法数值，没有重跑付费30题来替换原结果。最终真实生命周期会在该修正之后再调用相同Provider。

## 结果

| 分组 | 实际结果 | 判定边界 |
| --- | --- | --- |
| 有据12题 | 12/12事实正确且合法来源支持；宏Recall@5=1，Hit@5=1 | 每题只有1个黄金chunk，不能外推大规模召回 |
| 无据6题 | 6/6明确未查到或无法确认 | 模型可能看到相关门店其它公开规则，但没有据此编造答案 |
| 本人只读工具6题 | 6/6 | 本人订单状态/活动金额/门店来自真实SQL；他人订单与不存在一致，工具路径不调用模型 |
| 多轮6题、12轮 | 6/6 | 切店、取消与退款规则区分、实时状态刷新、停车v2、停用后的拒答 |

根agent与另一只读审查者分别对照原文和返回来源逐题复核，[逐题判定](eval-review.md)保存原因和输入/输出哈希；不是让模型自评。初始词语候选为知识11/12、多轮5/6：K07用`2 小时/5 元`代替汉字；M01第二轮正确答“没有免费时长”，问题没有要求收费标准；两项语义均通过，原始候选标记不改写。

重建Service时新增文档embedding调用0，随后一次查询仍从持久向量命中。显式`rebuild`仍为付费重索引。草稿canary未到Provider边界。本人/对照订单最后状态与显式夹具动作一致，无额外AI写操作；该断言只覆盖检查过的事实，不是全库副作用扫描。M04直接SQL状态变化仅用于验证实时刷新，不代表允许已支付取消。

本轮通过真实Assistant、Redis会话、知识服务和只读工具的事件emitter，未走外部网络HTTP或浏览器；真实SSE取消另列。没有重测所有旧权限攻击、真实提供商429、全局金额预算或模型容量。配置ready只表示本地配置完整，不是生产可用性保证。

## 调用与费用

实际70次远端HTTP：Chat28次、查询/文档Embedding42次。Chat输入5936、输出627，Embedding输入695，共7258 tokens，全部有Provider返回usage，无本轮未知usage。工具8轮无付费生成；36轮问答、13次文档嵌入（含新版本）、1次重启后查询共同构成70次。原始逐调用usage、阶段/回答/总耗时见JSON；上游TTFT不测，首个缓冲完整回答可见耗时不冒充TTFT。没有并发/稳态吞吐结论。

执行时官方北京原价：Chat输入0.8元/百万tokens、输出2.7元/百万tokens（[模型价格](https://help.aliyun.com/zh/model-studio/qwen3-8-flash)）；Embedding输入0.0005元/千tokens（[向量模型价格](https://help.aliyun.com/zh/model-studio/text-embedding-synchronous-api)）。按未打折输入计算：`5936×0.8/1e6 + 627×2.7/1e6 + 695×0.5/1e6 = 0.0067892元`。这是基于响应usage的原价估算，未核对账单、缓存折扣、免费额度或套餐；实际扣费未知，不声明零费用。日期2026-10-28，生命周期取消的未知结算另算。

## 复跑

凭据仅从用户授权的CRM本机已验证加密记录读入内存，未写明文文件、源码或日志；Go产品本身不依赖CRM。另一个干净环境只需按`backend/.env.example`配置自己Provider，再运行plan中的双开关测试。缺Key/任一开关为SKIP，不能记真实通过。原环境所有自有用户、知识、订单、会话和去重key由夹具精确清理；最终额外SQL/Redis收尾记录在总报告。

提交前评测实现由根agent读完整测试与固定集并审查，普通预算/集契约测试通过；provider_adapter独立复核全部实际答案和来源。票级独立核验仍待其它核验流程。
