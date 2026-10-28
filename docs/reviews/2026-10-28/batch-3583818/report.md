# 2026-10-28 批量独立核验

结论：本轮已核验GO-031—GO-071共15票的实际交付范围，发现的问题已追加修复并复验。GO-060/061/062仅验收当前缓存/投影子集；真实Provider、Kafka消费投影与持续量化不算完成。状态只在[STATUS.md](../../../../STATUS.md)维护。

基线 `358381843d0fe5efb98dfb75cac34c089f0777b8`，隔离分支 `review/batch-3583818`。三个分工独立检查交易、AI、社区，随后交叉复核修复；根核验者重跑整合后的全部真实依赖与页面。未改其他项目或用户开发服务，未使用私有实习材料、付费模型或真实用户数据。

## 实际修复

| 范围 | 确认问题及处理 |
| --- | --- |
| 商家交易 | 核销/退款等待期间撤权仍可写；事务内锁定当前门店/商家/成员/scope，保持原券→库存→订单锁序 |
| 交易补偿 | `RESERVATION_REJECTED`没人认领、payload不兼容；接通精确事件链，真实订单冲突转待核对；Lua完整预检且旧预约不能删新预约资格 |
| 恢复/重建 | 漏拒绝补偿、Redis成功DB回滚后重试卡版本、无group未读消息被当空闲；当前锁定检查、单调版本重试与fail-closed门槛 |
| review | REQUEUED中断后无法再投、另一身份可借旧记录作为ACK依据；同ID可安全重投并核对原绑定 |
| FAQ | 重建计算前越权、缺审计、公开旧版/非公开scope、草稿标题污染公开版本、编辑正文缺字段/店员403；补鉴权/同事务审计/公开过滤/008标题快照与正确列表DTO |
| AI回合 | 生成与缓存重放未复核知识/身份、旧assistant历史充当新证据、预算重试/失败处理与DTO断点；统一deadline/session/lease/conversation/knowledge守护，实时工具重查，敏感输入在落盘前拒绝 |
| 实时工具 | 活动售价查错SQL列；修正公开ACTIVE条件及精确金额，补工具卡片与来源链接；长业务ID不再误匹配内嵌手机号 |
| 社区事件 | 笔记发布未写事件；同事务NOTE_PUBLISHED；初次取消关系错误递增版本/发事件，改为无变化不发 |
| 点赞投影 | 部分set替换完整DB计数、迟到写/重建覆盖较新事实、零值recount失败；DB事实优先，版本及完整性元数据，持笔记行锁重建 |
| 缓存/Kafka | 旧loader在失效后回填、取消的等待者被leader阻塞、第8次过期lease卡队首；代际隔离、独立取消与持久NEEDS_REVIEW |
| 页面 | Note/Feed未注册El组件；旧笔记响应写入下一篇状态；全局最高角色显示其他门店退款按钮；AI能力/来源/会话契约不匹配、旧SSE回写；发布后FAQ不能再编辑。已逐项处理，保留真实失败与页面回归 |
| 演练与CI | SQL ID转Number丢精度、清理漏BLOG/活动事件、旧GO-031脚本仍断言补偿未实现；改精确字符串/自有资源清理与当前业务预期，CI补review真实测试和前端格式 |

具体触发条件、文件、失败/修复后证据及未覆盖窗口分别见[交易报告](trade-review.md)、[AI报告](ai-review.md)、[社区报告](community-review.md)。所有提交保留原实现历史；完整分步SHA见[提交清单](commits.json)。用户再次要求的一问题一提交已写入AGENTS；后续新增独立问题分别追加，不重写已推送历史。

## 独立验证

- `go mod verify`、gofmt、vet、全套普通Go测试与构建通过；[普通结果](go-unit.jsonl)。本机真实`GO_INTEGRATION=1 go test -timeout 180s -count=1 -p 1 -json ./...`通过25个有测试包，包含47个真实入口、149个一级真实子组，无fail：[整合真实结果](integration-final.jsonl)。入口与子组是不同粒度，不相加成“196个场景”。
- 专用MySQL3321/Redis6393/Kafka9096；交易/AI/社区分别独立schema和Redis DB1/2/3，根整合使用DB0。首次版本SQL升级、重复校验与008新增迁移在四schema通过。没有让并行claim器消费另一分工的事件。
- 前端类型、生产构建和格式通过。真实Vue产品流程UTC/上海各10项：[UTC](product-final-v2-product-utc.json)、[上海](product-final-v2-product-shanghai.json)，覆盖发布笔记/点赞/关注/Feed、切笔记的迟到响应、FAQ创建发布重建与已发布规则编辑正文、stub问答及来源。截图已人工查看关键状态。
- 真实订单生命周期UTC/上海各26项：[UTC](lifecycle-final-lifecycle-utc.json)、[上海](lifecycle-final-lifecycle-shanghai.json)，覆盖支付/取消/到期、凭据可见性与截图脱敏、丢响应/延迟确认、20次并发支付、实际补偿后可获得不同的新订单ID。
- 三流程HTTP演示22项、30人抢5库存短突发9项、Redis物理停机恢复6项、MySQL物理停机的消费故障7项通过：[汇总](http-final-ui-summary.json)。MySQL演练直接XADD私有fixture到隔离共享Stream以确定故障窗口，**没有执行Lua预占**；它证明无持久结果时保留PEL、恢复后有一笔持久结果才ACK，不证明真实准入数据跨Redis故障零丢失。
- 最终二进制六类启动/关闭回归通过，Go正常退出无强杀：[bootstrap](bootstrap-final.json)。最终源码及二进制hash见[来源](source-manifest.json)。短突发不作持续QPS、P95/P99或性能提升结论。
- 最终代码 `a43e176152b16a3e463d70d19e270ce26aca2ef6` 的四个GitHub Actions job全部成功，包含Linux race、真实MySQL/Redis/Kafka、前端格式/构建：[最终代码CI](https://github.com/tech-cove01/project-full-stack/actions/runs/40645333486)、[结构记录](ci-final-code.json)。较早修复代码另有[CI](https://github.com/tech-cove01/project-full-stack/actions/runs/40644618661)，不冒充最终结果。

## 迁移与边界

新增 `infra/mysql/migrations/008-ai-version-title.sql`。本轮只迁移了专用核验库；用户自己的开发库启动新程序前仍须按既有环境执行 `node scripts/migrate.mjs`。新增nullable标题列后回填迁移时已知标题，历史未存储的原始标题无法恢复；以后缺标题快照的旧writer版本被新reader拒绝。回退代码不删除列或改已应用迁移校验和；旧writer又发布过NULL快照时需重新发布完整版本。无AutoMigrate替代DDL。

本次证明受控交易恢复、单进程计数缓存、版本集合与stub协议；没有据此证明任意跨存储时刻的线性一致性、灾难丢失零丢单或生产容量。具体仍待做：

- GO-072 Kafka应用消费、eventId去重、版本应用及第二投影；目前只有投递侧和读取投递结果的测试消费者。
- GO-073稳定持续负载、查询次数/冷miss、投影延迟/积压及计数对账；无可用于简历的Go QPS数字。
- GO-074已补本次两时区页面子集，B端核销退款的完整浏览器角色矩阵仍可补；不能把后端并发集成当全部页面验证。
- GO-075实际Chat/Embedding provider实现与真实模型质量/引用评测、全局模型并发与付费token成本；当前只有stub，界面明确提示。
- 计划中的页ID/卡片独立缓存与动态hotkey/TTL伸缩尚未实现；已有的是批量SQL、公共counts缓存和固定2秒L1。历史seed的liked没有可反推的用户事实，不能拿其数值当新关系数据对账。

## 失败与环境收尾

各lane原始FAIL、夹具错误和中间build/中断均保留，并在分报告区分，未改写成PASS。根页面首轮真实确认未注册组件；一轮Vite原生退出`3221226505`造成连接拒绝，根因未确定，重跑通过。词法stub对“停车怎么收费”未命中并明确拒答，随后用发布原文验证引用协议；没有通过降阈值或更换断言宣称语义质量通过。

旧GO-031页面断言“Redis补偿仍延期”在新功能下失败。该首轮JSON因复用标签被后续成功重跑覆盖，原始文件未保留；[恢复记录](lifecycle-initial-recovered.json)从实际工具stdout恢复元数据并明确标注，不能当原始raw结果。另两次新页面运行因前个故障脚本仍占专用端口而安全拒绝启动，未写夹具。最终以product-final-v2与lifecycle-final成功证据为准。

API/Vite已退出；自有SQL fixture、待处理交易事件与PEL均为0，早期BLOG及演练活动漏删事件按明确作者范围/已报告voucher逐项核实后清理：[最终对账](final-cleanup.json)。过期缓存、序列、私有Kafka topic及卷保留，不声称整个Redis为空。专用容器已停止：[退出记录](containers-stopped.json)；没有停止其他项目或开发服务。早期脚本cleanup=true仅表示该轮SQL检查，不能代替本次最终事件/PEL对账。
