# Feed 最终原件内部核验

2026-11-01。最终版本在本次开发笔记本环境中，完整 Feed **50 requests/s × 180 秒两轮通过**，合计18001次请求全部完成并通过完整内容校验。当前最终版本最高完整短档为50/s×45秒；100/s首个受限档早停，保留7个unknown，不继续250或更高档。这不是生产容量上限，也不是精细极限搜索。

本记录仅离线核对本批公开原件，没有启动新环境、独立负载器、SQL、HTTP、Go命令或新负载；不替代CAP-09E外部独立核验。

## 最终身份与场景

- 应用源码：`7a80186b568b521c54736b735c08dce8ef962e19`；二进制SHA256：`9f70db9ad869eaac4935f714c00980f3e5c173404fe584c9b725fdb943405fd7`。独立clean checkout构建记录见[build-b](build-b.json)，[CI原件](ci-b.json)同SHA四项success。
- 环境SHA256：`a1e1abff73f147cbc04933b7c27cef4bf169c2f1f9618c6e323fc57d77dc9e97`；Feed夹具SHA256：`63d77ccf658b9a5fe69309584a2cdefa794d0b927ebaeca204e4607828d5a150`。各command/result与[最终冻结](final-application.json)相符。
- 真实鉴权`GET /feed/notes?limit=20`，multi轮转；6400篇、32作者、32配置读者、每作者200篇、正文256B、每页20卡、640个公开计数key。校验database来源、ID/顺序/作者/门店、标题正文图片、时间与游标、公开计数和本人状态。
- 同API PID37664、SQL池20、Redis池10、GOMAXPROCS4、GOMEMLIMIT1GiB，四依赖及固定ES256MiB堆。所有最终Feed资源样本的tail均为false；预定关闭额外CPU/profile，没有新profile工件。不是省略事实RR、公共卡片或计数的轻接口。
- [执行计划](final-execution-plan.json)记录同机发压、用户报告临时关闭安全软件的开发机条件。旧轮未提供可核同状态时，不仅凭相同容器环境哈希宣称旧100/s与最终结果之间存在纯代码时延收益。

## 原生结果

| 原件 | 预定场景 | 发起 / HTTP / 完成 / 成功 | unknown / dropped | 全样本、成功样本P95 / P99（ms） | 最大值（ms） | 结论 |
| --- | --- | --- | --- | --- | --- | --- |
| [final-stair-1](feed-final-stair-1-stage.json) | 50/s×45秒 | 2250 / 2250 / 2250 / 2250 | 0 / 0 | 24.374890 / 35.902858 | 112.4949 | 完整通过 |
| [final-stair-2](feed-final-stair-2-stage.json) | 100/s×45秒 | 1158 / 1151 / 1151 / 1151 | 7 / 0 | 225.0062 / 263.9046 | 279.4534 | 原生99早停；未完成45秒 |
| [hold-1](feed-hold-1-stage.json) | 50/s×180秒 | 9000 / 9000 / 9000 / 9000 | 0 / 0 | 99.418035 / 215.303151 | 409.7210 | 完整通过 |
| [hold-2](feed-hold-2-stage.json) | 50/s×180秒 | 9001 / 9001 / 9001 / 9001 | 0 / 0 | 63.8898 / 201.0490 | 1255.8166 | 完整通过 |

各档系统、HTTP、内容错误均为0。全与成功分位在这些原件中相同；100/s失败档只覆盖已返回的1151个时延样本，7个unknown不补终态或零时延。正式两轮分位分别报告，没有合并或平均成一个P95/P99。第二轮确有一次超过1秒的响应，不能声称消除长尾。

`nativeScenario`绑定k6原生开始与配置时长；两轮均完整180000ms、原生exit0、没有外部停止。发起达成分别为9000/9000和9001/9000，不能把早停档分母缩短来换取通过。[原生summary 1](feed-hold-1-load/stable-summary.json)、[summary 2](feed-hold-2-load/stable-summary.json)与各result计数相符。

[正式冻结](freeze-feed.json)早于两轮command，锁定同源码、二进制、环境、夹具、50/s与180秒两轮；两command的freezeSHA256均为`2e380454cc5927ddb2fb01a49e083e0456e1b2f658db6286957c4904a9ce86a8`。目标依据完整50短档及其余量，不用旧53fa的100/150档或带诊断开销的100档替补。

## 资源、时基与下游

两轮分别37/37份资源观测完整，四个完整CID及挂载归属对应固定环境；资源样本中各容器内存未触85%，API RSS未触1.5GiB，k6加Node未触2GiB，宿主内存未触88%。正式1宿主有效CPU峰87.7%只有一个连续超85%样本，没有触原定连续三次停机门槛；不能写成所有CPU样本都低于85%。正式2有效宿主CPU峰72.9%。两轮电源事件采集可用且为空。

原helper时间守卫及字节哈希保持。正式1MySQL Uptime增180秒、客户端状态窗口中点差182020.5ms；正式2分别185秒与184580.5ms，均通过原容差。两轮counterResets为false，digest/prepared丢失为0；没有为本组放宽阈值。

每轮41份下游样本均完整，所有阶段LIKE事件数为0，Outbox待办、三组缺失收据及三组Kafka lag这七条队列均为0，PARKED无新增；最大相邻起始间隙分别5.005644秒和5.001897秒。原生结束后，按SQL/Kafka真实完成时间复算的首次干净观测分别为1.073073秒与4.699551秒，均小于60秒。队列采用5秒非原子采样，不是连续瞬时证明；Feed不产生LIKE事件，不能用这两轮推导点赞消费者写入容量。

## 关闭范围与旧证据

100/s首受限后本次最终阶梯关闭，250及更高档未执行；这不是因较大升档步长判定性能退化。两个可选final-hot均未启动且本批关闭，见[收口决定](final-scope-closure.json)；该决定记录`explicitUserStopCommand=false`，不冒称用户下达明确停压命令，也不把旧hot当最终SHA复跑或同SHA复用。

旧CAP-07/08失败、旧B1未知、本批首probe ES资源停止、固定回归的Uptime拒绝、旧250/s早停及本次100/s早停均保留。1991年上海夏令时/游标契约继续保留在应用测试中。[分配对照](feed-allocation-comparison.md)仅支持有界采样下重复zone链未再被采到及采样总量变化；不是精确每HTTP分配或本组时延/吞吐因果解释。

## 清理闭环

[最终清理](cleanup.json)与[单次恢复记录](cleanup-recovery-1.json)均为complete；已离线核对两者原字节相同。正常注销Feed32、Like512个会话，记录两类Redis剩余会话均0，私有token/phase输入已移除，observer删除已验证。API37664身份匹配后退出15，原件明确没有Go graceful shutdown主张。

四个原完整CID均停止、无OOM、挂载相符；6个数据/匿名卷保留，6个自有端口关闭，549条登记子进程身份均无活跃匹配，11个登记私有env已精确删除。这不是手动终止549个进程或删除数据卷。

初次清理因Kafka/ES的143退出码被过严的`exit==0`断言拒绝；[Kafka日志](cleanup-kafka-shutdown.txt)有Shutdown completed，[ES日志](cleanup-elasticsearch-shutdown.txt)有stopped/closed。恢复只检查已停止身份、卷和端口并删除尚存登记env，未重复logout或stop。[首次失败进度](cleanup-progress.json)原SHA256为`626039964bc6649f273e11195501b14a257e1d60c231dcf6aedd97fdb70dec85`，与最终记录匹配；[恢复脚本审计快照](cleanup-tail-recovery.py.txt)哈希也已复核。没有追加任何负载。
