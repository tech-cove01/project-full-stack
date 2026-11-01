# CAP-09 最终交付内部审读

2026-11-01。结论：报告与公开工件一致，无剩余交付审读阻断。**完整Feed实现者正式组50 requests/s×180秒两轮通过；真实点赞仅20/s×45秒短档通过，20/s×180秒正式组未通过。** 本批负载和清理已结束，不追加成功轮或使用剩余名额重新定位。

本审读交叉核对root的[报告](report.md)、[机器汇总](results-summary.json)、七个最终阶段的command/result/native summary/stage、冻结记录、CI与清理原件，未启动服务、Go、SQL、HTTP、网络或新负载。它是实现团队内部跨作者的报告和原件审读，**不是CAP-09E在新环境、新数据上的独立容量复跑**。审读者参与过本批部分生产修复，对应源码的非作者审查由其他成员完成，不把本文件当作这些实现的独立复跑证明。

## 固定身份与版本

- 七个最终阶段的应用均为`7a80186b568b521c54736b735c08dce8ef962e19`，二进制SHA256均为`9f70db9ad869eaac4935f714c00980f3e5c173404fe584c9b725fdb943405fd7`，与[干净构建](build-b.json)、[最终应用记录](final-application.json)及API PID37664的元数据一致。
- 全部command的环境SHA256均为`a1e1abff73f147cbc04933b7c27cef4bf169c2f1f9618c6e323fc57d77dc9e97`，与当前归档environment.json字节相符；Feed/Like夹具分别为`63d77ccf658b9a5fe69309584a2cdefa794d0b927ebaeca204e4607828d5a150`、`bc7544b422cc2bc9fbdf797ee653c44aa0e78a7a5a9a648dbd95b0b95e670224`。最终tail关闭，不混诊断profile。
- [CI-B](ci-b.json)的headSha恰为该应用SHA，backend、backend-go、community-kafka、frontend四项均success；Linux race/CI通过不等于本轮在Linux执行容量测试。
- 两个Feed正式command引用同一冻结文件字节hash`2e380454cc5927ddb2fb01a49e083e0456e1b2f658db6286957c4904a9ce86a8`；Like正式command引用`26ad9bbe083618911987d2efb5404914723d5d0f7547c003e087d715bb128957`，均与文件实际hash相符且冻结早于对应command。
- 清理工具修复`dba9c750f28f5d2d9a2ae4a2599932d531921b22`及后续文档/交付HEAD不是被测应用SHA；报告、STATUS和交接已明确区分，不把交付提交的CI或HEAD替换成实际测试版本。

## 七个最终阶段复核

下表计数、分位与native summary/result相符，机器汇总的result及passed逐行与原件相符。P95/P99单位ms，失败行只覆盖已完成且保留下来的时延样本。

| 阶段 | 预定场景 | 发起/已分类成功 | unknown | P95/P99 | 原件结论 |
| --- | --- | --- | --- | --- | --- |
| feed-final-stair-1 | 50/s×45秒 | 2250/2250 | 0 | 24.374890/35.902858 | 完整短档通过 |
| feed-final-stair-2 | 100/s×45秒 | 1158/1151 | 7 | 225.0062/263.9046 | native99早停，阶梯关闭 |
| feed-hold-1 | 50/s×180秒 | 9000/9000 | 0 | 99.418035/215.303151 | 完整正式轮通过 |
| feed-hold-2 | 50/s×180秒 | 9001/9001 | 0 | 63.8898/201.0490 | 完整正式轮通过 |
| like-final-stair-1 | 20/s×45秒 | 901/901 | 0 | 58.8572/178.8933 | 901次真实变更，完整短档通过 |
| like-final-stair-2 | 50/s×45秒 | 570/568 | 2 | 204.09581/287.862182 | native99早停，阶梯关闭 |
| like-hold-1 | 20/s×180秒 | 230/229 | 1 | 201.61158/264.009124 | native99早停，正式组关闭 |

正式Feed合计18001次正确成功，两轮均完整180000ms，system/HTTP/content error、unknown、dropped为0；每轮37/37资源样本、41份完整下游样本，供电、时基、工具字节及子进程收敛检查通过。七条下游队列净增长均0，无新增LIKE事件；恢复观测分别在native结束后1.073073/4.699551秒完成。这是读负载的下游检查，不是发布写吞吐证明。第二轮最大延迟**1255.8166ms**，第一轮409.721ms，P95/P99达标不代表秒级离群已消失或旧长尾根因已修复。

Like短档901次mutation与SQL版本/事件增量901一致；50档已分类568次、SQL569次，unknown2仍保留；正式失败已分类229次、SQL229次，unknown1仍保留。相等的最终计数不能消除未知请求。三次no-op、已分类系统/HTTP/content错误与drop均0；实际正变更参与者为256/256/229条独占分散关系，不把512配置池当作512活跃参与者。

Like最终multi对账累计5546个事件，observer全513篇cohort累计5747个，差201来自旧hot预热；三组LIKE收据均**IGNORED、APPLIED=0**。SQL、公开计数、连续唯一事件及Redis集合/版本最终一致，真实三组lag最终0。20短档保守窗口分母800、预算8，队列最大净增长7、native结束后4.788239秒观察恢复；失败的50短档和正式轮没有完整目标窗口或正式下游稳态证明，最终排空不改变失败。

Like正式第二轮按首败规则关闭，不降档另补；早停既不能交付完整180秒成绩，也不能反推“若继续跑满，其最终P95必定超限”。各失败仍用计划45/180秒分母，不用早停前瞬时速率替代正式吞吐，不平均两轮分位数。

## 优化、失败与边界

- 取消修复仅覆盖Transaction明确成功后的同步、有界派生收尾；Commit ACK不确定、Redis故障或2秒耗尽仍保留边界。串行relay只设BatchSize1，RequireAll、同步确认后Done及租约/重试/key/header未变。
- [Feed分配对照](feed-allocation-comparison.md)只支持采样总量628040710→519527344估计bytes及重复zone链未再被采到。快照跨度78.612/112.682秒、native后尾部18.582/52.853秒不同，含后台/预热/排空；不能写成精确每请求节约17.28%、RSS或吞吐收益。诊断P99约199.15→298.55ms，不能声称尾延迟全面改善。
- Like优化前没有完整有效的45秒main/hot基线；旧hot仅10秒201次首次插入warm，与后续关系初态不同，不计算稳定TPS或严格同条件代码提升百分比。warm延迟政策只向前生效，正式稳定门槛不变，旧hot失败和诊断2启动前拒绝均保留并占名额。
- 原ES资源拒绝、时间守卫拒绝、取消后派生差异、诊断采样超时/预热参数拒绝、全部升档早停及unknown均保留。旧CAP-07/08失败、旧B1未知不改；未测5000/s，不推断精确容量天花板。
- [最终范围关闭](final-scope-closure.json)中两个final-hot均started=false、not_run_closed，explicitUserStopCommand=false；这是root关闭可选范围的决定，不冒称用户显式停压或已测热点。两链路首受限后未继续插档，[最终组关闭](final-group-closure.json)不重新发放诊断、补测或剩余额度。

## 清理与文档收口

[cleanup.json](cleanup.json)与[恢复记录](cleanup-recovery-1.json)原字节hash同为`0ee7612b0e7baf85dba20112ff375d973b85d6682dbbfb8b5b10d2bde7c64601`。原progress仍为`626039964bc6649f273e11195501b14a257e1d60c231dcf6aedd97fdb70dec85`，AssertionError保留；实际恢复脚本[字节快照](cleanup-tail-recovery.py.txt)为`59be238564993a2516c34fef2aaa36bc96d0868602252108295218e21ac1e915`，均与记录相符。

已记录Feed32和Like512会话正常logout、剩余均0，observer删除验证完成，阶段私有输入移除。API身份匹配后实际exit15，明确不宣称Go优雅关闭；四个完整CID退出码0/0/143/143、无OOM、原挂载与限额保留。Kafka关闭完成和ES stopped/closed日志支持本次143状态；守卫修复仅Kafka/ES可接受0或143，不接受137/OOM。尾部恢复没有再次logout、删除observer或停止/启动服务，只复核身份/六卷/六端口并移除11个精确登记的私有env；549是核验过的身份记录数，不称549个独立进程。六卷保留，全部自有端口关闭，CRM及未知服务未操作。

已核对根[STATUS](../../../STATUS.md)、[交接](../../agent-handoff.md)、[总方案](../../implementation-plan.md)的新终态与报告一致：Feed实现者正式组通过，Like正式组未通过，CAP-09E独立环境代表档尚未执行，功能冻结，不自动开新批次或更新未核验求职成绩。首页README保持产品/架构/运行说明，没有追加本批性能数字或工作流入口。交付者仍需正常提交并推送本轮最终证据与文档，向核验者交最后交付SHA及上述被测应用SHA。
