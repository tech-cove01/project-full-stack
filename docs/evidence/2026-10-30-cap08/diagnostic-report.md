# CAP-08C：首轮有效诊断与正式准入意见

2026-10-30。本文作者 `cap08_evidence` 只读本轮已落工件，与实际负载执行者分开；没有新增HTTP/SQL/编译、服务或负载。以下是实施团队内部准入审查，不是外部独立验收，也不是正式稳定性组结果。

**诊断1有效，声明条件下未重现B1同类失败，可由root按 `not-reproduced` 建立预定正式组的gate。** 没有证实需要业务修复的机制，不为凑修复改业务；没有支持第二轮单项变化的新候选，不追加信息重复的诊断2。原CAP-07 B1与更早秒级长尾原因仍未知；本轮较低分位不能写成已修根因或缓存提高容量。

## 来源、负载与原生判据

- 应用固定 `46ddefc2a16d41e4bb01a3d20d08bdfcaeff465f`，工具提交 `516d5342af95d51adf8f6ec9a8c3af321fefb4fa`；[command](diagnostic-1-command.json)记录source/tool/脚本hash及预算，结束时`toolBytesStable=true`。应用差量为默认关闭的有界观测，没有业务修复。
- [API metadata](api-diag1.json)为PID44828、phase diagnostic、tailEnabledRequested=true；新[fixture](fixture/fixture.json)为同一46ddefc来源、READY、32作者/32读者/6400篇/每作者200篇/页20，manifest SHA256为 `88a3028dc705e18dc80cb32575b7c64a4c3deb60b63d055f116bea24206733cd`。
- 事前[prediction](diagnostic-1-prediction.json)固定鉴权multi100/s×180秒、预热10秒/25每秒、pre/max VUs256/512，保留原RR/公开版本/个人状态与完整分类器。collector属于诊断开销，不能当作正式成绩。

[result](diagnostic-1-load/result.json)与[native summary](diagnostic-1-load/stable-summary.json)一致：发起/native HTTP/终态/成功均18001，unknown、内容/系统/HTTP错误和dropped均0；计划发起18000，实际达成100.0056%。全请求与成功请求P95/P99均19.3259/26.6919ms，max81.2575ms，warm/stable exit0，`meetsBudget=true`。stable命令墙钟181.504秒含启动/退出开销，名义判据分母仍为180秒。

本轮没有逐请求时延原始分布；这里核对的是原生汇总及其口径，不称离线重建，也不跨轮合并分位。

## 采集有效性与在途边界

[collector manifest](diagnostic-1-diagnostic/manifest.json)与[timeline](diagnostic-1-diagnostic/timeline.ndjson)一致：

| 项目 | 实际观测 |
| --- | --- |
| 1Hz成功poll / 失败 | 182 / 0 |
| 最大poll起点间隙 | 1000.8774ms，低于事前2.5秒上限 |
| stable新进程创建→首poll / 末poll→done | 42.938ms / 427.571ms |
| window/poll coverage、stage fields、active-age、slow visibility | 全部true |
| timeline错误、年龄不完整/overflow、missed intervals、trigger rows | 均0 |
| capacity fetch最大耗时 | 27.0237ms；所有返回PID44828、tail enabled |
| goroutine captures / 已保留慢记录 | 0 / 0 |

本轮没有阶段最老年龄达到100ms、没有pool wait正增，因而未触发抓栈；不是触发后失败、截断或采集缺失。首轮[受控接线](wiring-result.json)已另外证明释放前目标阶段和排队年龄可见并能触发collector；它不冒充本轮负载内栈。当前[slow-records](diagnostic-1-diagnostic/slow-records.json)记录0个观察到的慢payload、0个无效记录、visibility_complete=true；timeline保留parsed subset和可见sequence，不另保存完整slow响应。

以下为182次采样所见的最大在途年龄/数量，不是每请求耗时、完整峰值或纯借池时间：

| 阶段 | 最大观测active | 最大观测oldest ms |
| --- | --- | --- |
| auth.resolve | 1 | 1.8483 |
| feed.facts | 2 | 27.0744 |
| feed.cards | 1 | 4.2871 |
| feed.counts | 1 | 7.0161 |
| sql.begin_acquire_roundtrip | 1 | 12.1013 |
| sql.transaction_hold | 2 | 25.4423 |
| cards.shared | 1 | 3.2322 |
| cards.slot_wait | 0 | 0 |
| cards.recheck | 1 | 0.5466 |
| cards.cold_sql | 1 | 2.1745 |
| cards.fill | 1 | 0.5215 |

`cards.slot_wait`的完成计数仍增长575，说明实际执行过该边界；1Hz未抽到其短暂active，不能把表中0写成所有调用都零排队。跨stage数值并非原子，父子/请求与shared阶段重叠，不能相加推算总延迟。

## 请求、共享任务与pool口径

[前](diagnostic-1-capacity-before.json)/[后](diagnostic-1-capacity-after.json)端点窗口为预热前至wrapper收尾后，含预热与后台，不是稳态精确计数：

- tail.requests增18251，与250次warm加18001次stable一致；auth/facts/cards/counts各完成18251、错误0，card fallback增0。
- tail.shared_tasks与cards.shared/slot_wait/recheck/cold_sql/fill各增575、错误0，与[卡片差量](diagnostic-1-cards-delta.json)的575个flight/575批内容SQL一致。shared任务没有混入HTTP/direct请求计数；两端点所有这些在途值均0。
- 内容SQL实际1920行/538920个title/content/原images UTF8字节，L1/Redis hits分别310620/52480，miss items1920，joins0，load/Redis/坏值/拒绝错误0。joins0不否定受控并发已证明的同批合并，只表示本轮没有观察到同批重叠加载。
- SQL/Redis两池wait_count与wait_duration_ns的前后差均0。不能用其它轮的累积等待替代本轮，也不将全实例Com_select/Com_stmt_execute相加或当作内容loader计数。

## 原安全采样与正式准入

[control](diagnostic-1-control/exit-status.json)为wrapper/harness/sampler exit0，37/37完整原5秒资源样本，错误列表空、无stop、无forced；[stage exit](diagnostic-1-stage-exit.json)为wrapperExited、cardsEvidenceValid、toolBytesStable均true，collector exit0，failedStartOrException=null。resource各样本API均44828、k6均59428，与collector稳定进程记录对齐。

[资源原始记录](diagnostic-1-control/resources.ndjson)中最大有效host CPU47.1%、内存78.9%，API RSS39.3594MiB、k6 RSS172.3477MiB；未触原安全预算。5秒资源与1Hz阶段采样都不保证捕获所有瞬时峰或停顿，这些低值不能用于排除旧B1的宿主/依赖候选。

精确46ddefc的[CI元数据](ci-46ddefc.json)显示[41065266414](https://github.com/tech-cove01/project-full-stack/actions/runs/41065266414)四项success；相关真实依赖/race见[选定日志](ci-46ddefc-selected-tests.txt)，相关本地回归见[checks](observation-related-checks.json)。据以上本轮有效诊断、已完成回归及精确CI，内部意见为允许root按预定路径关闭额外collector/抓栈，以同一最终46ddefc源码执行multi100×180两轮、均通过后hot200×180一轮；任一正式失败即结束，不补成功轮。

本意见仅是进入正式组的依据。没有业务修复、没有历史根因闭环，也没有提前宣称正式组或可选高档通过；最终状态由root依据后续实际工件记录，旧失败保持原结论。
