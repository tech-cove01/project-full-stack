# CAP-06A/B：短探针与三轮有限诊断执行记录

2026-10-30。固定干净应用`bb03d3fe9172f3bb369cbbaa473a2ab041ec4db5`，API二进制SHA256 `a8ef9aa640164b0eaad729139e348491bafbcd19220c6a62b7a97fcc152a6d79`。root实际执行了新卷依赖准备，harness agent执行同二进制off/on探针、获root逐轮放行的三个诊断及最后精确收尾；没有第四轮、重启夹具、清缓存、改TTL/池或进入CAP-07。

**结论：三个诊断窗口均完整满足原工作负载预算，但未复现上轮独立核验的秒级长尾，原事故根因尚未确认。** 它们带额外采证，不当CAP-06D正式组，不称原事故已修复；旧[独立失败](../../reviews/2026-10-30/go073-cap-09b4921/report.md)继续保留。

## 固定环境及开关验证

使用本票固定read项目/端口、新随机MySQL/Redis卷、新6400篇/32作者/32读者合成夹具和真实会话；老实现者/核验者卷均保留。MySQL4CPU/2GiB、Redis2CPU/512MiB、API GOMAXPROCS4/GOMEMLIMIT1GiB/SQL池20/Redis池10，Kafka/ES/AI关闭，页长20/正文256B/013索引/鉴权及持久化不变。[环境](environment.json)、[准备](preparation.json)、[迁移](migrate.txt)、[夹具来源](fixture-provenance.json)保存身份与来源。

off PID39952与on PID59564按同二进制/同夹具切换，唯一应用配置变化为`GO_FEED_DIAGNOSTICS`。四次短探针分类和资源完整，实测开关字段符合配置，见[探针报告](switch-probe-report.md)及[机器汇总](switch-probes.json)。off/on次序、自然缓存状态和共享宿主不同，不能从短测计算观测开销收益百分比。

## 三轮诊断的实际结果

三轮均multi100/s、稳定180秒，预热10秒/25s⁻¹，pre256/max512VU；启动前预约exclusive slot，最多3。每次HTTP响应都走原20条内容/来源/身份检查，全与成功分布相同，started/native HTTP/终态/成功四计数一致，unknown、dropped及系统/HTTP/内容错误均0。

| 诊断轮 | 完整成功 | P95 / P99 ms | 原资源完整样本 | 额外成功poll / 失败 | 本轮slow trace / 抓栈 |
| --- | --- | --- | --- | --- | --- |
| [1](baseline-diag-1-load/result.json) | 18001/18001 | 26.4453 / 43.7021 | 37/37 | 183 / 0 | 39 / 1 |
| [2](baseline-diag-2-load/result.json) | 18001/18001 | 29.2595 / 40.4513 | 37/37 | 183 / 0 | 0 / 0 |
| [3](baseline-diag-3-load/result.json) | 18000/18000 | 31.5160 / 40.9273 | 37/37 | 182 / 0 | 0 / 0 |

总计54002次成功。每轮原harness/sampler及额外collector均实际退出0，没有剩余运行中阶段。[三轮汇总](diagnostic-rounds-summary.json)保留全部分母/来源/首末池及stage差分/原始文件hash；各轮`-load`、`-control`、`-diagnostic`目录保留原件。第二、三轮的API ring仍含第一轮旧trace，汇总按本轮stable命令起止过滤，不将旧32条重复算成新慢请求。

第1轮末段出现约≤238ms的39个慢请求小簇；SQL全局WaitCount首末增加10，其余两轮SQL/Redis首末等待无新增。第1轮触发时的[goroutine清单](baseline-diag-1-diagnostic/manifest.json)记录10:03:41.912–41.926 UTC、13.78ms、6013字节，stable PID43332前后都存活；捕获相对慢请求峰值的滞后及栈内容由源码/离线因果审读另解释，不能仅凭抓到了文件就称捕获了阻塞峰值。

三轮自然cold SQL批次/缺失项分别为462/1324、435/1377、419/1369，对应Redis写1324/1377/1369；均为collector首末观测区间差，不是精确逐请求或原子窗口计数。真实自然过期/再填充经过了多次，不能将“未复现”解释成全程预热命中；也不能从存在冷加载直接认定它就是原长尾根因。

collector保留约1Hz的实际开始/计划迟到/错过间隔、HTTP wall、响应app时刻、触发前20秒/后10秒窗口和profile前后stable身份。它增加观测开销，5秒资源样本亦不是瞬时全覆盖；池累计等待与嵌套请求stage时间不相加为单请求耗时，无法可靠拆出的SQL/Redis单独借连接时间明确缺失。

## 实际收尾

三轮结束、root明确结束放行后，先对已落188份本票公开文件与32个token加observer密码共33个唯一私有值逐值扫描，匹配0；确认7个probe/diagnostic stage及3个collector全terminal、诊断slot用满3，见[清理前核对](pre-cleanup-private-value-scan.json)。这里只声明销毁前这批文件的比较范围，不宣称之后能重新读取已删token重做逐值扫描。

同一已审生命周期脚本一次完成收尾，无失败重试：正常HTTP logout32后精确Redis EXISTS由32降至0，删除仅本轮随机observer；API59564按exe/PID/UTC创建时间/二进制hash守卫终止，Windows native terminate不称Go优雅退出；新MySQL/Redis的不可变CID正常stop并核exit0/OOMfalse，保留本轮新卷和所有旧卷/SQL事实。仅删除记账的精确私有env以及token输入/空叶目录，不递归删除未知文件。见[逐步进度](cleanup-progress.json)、[最终收尾](cleanup.json)、[会话finish](fixture/finish.json)。未停止CRM等无关服务。

诊断预算已用尽，本记录交根agent做因果材料与状态收口；若没有进一步可验证证据，按STATUS保持未闭环，提交原因未确认/排除范围及下一最小诊断。不能把三轮通过挑成正式06D或用下一阶段缓存抵消原独立失败。
