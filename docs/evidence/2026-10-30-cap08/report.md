# CAP-08 交付：观测有效，正式容量组未通过，停止本批

2026-10-30。先读 [STATUS](../../../STATUS.md)。本轮完成默认关闭的请求/shared分段与有界在途年龄、真实受控接线、相关回归和精确源码CI。有效诊断1未重现B1失败，没有证实业务机制，未改池、TTL、deadline或业务。随后预定正式组第1轮通过，第2轮失败；**CAP-08容量未通过，本批已结束**。hot200、可选升档和持有均未执行，不补轮，不自动续CAP-09。CAP-07已独立核验的功能结论保留，原B1及更早长尾仍未知。

## 实现与来源

- 起点干净 `1de173517a54c3be46748779437b592f7bcdbc58`；[08A失败窗口](failure-window.md)提交 `32c7067b2ef78e8a4f17e4e2d11a5affabdd2458`，没有重跑旧A/B组。
- 观测应用提交及全部实际负载固定源码 **`46ddefc2a16d41e4bb01a3d20d08bdfcaeff465f`**。请求等待与app-owned shared任务分别计数，保持原共享生命周期、2s context和8活动/64flight/1024caller等限制。固定阶段各1024个在途追踪槽，超界明确不完整；至多32条慢记录，不记用户、token、正文、key或SQL参数。默认关闭路径保持原行为。
- [一般六包真实回归/vet](observation-related-checks.json)、实际HTTP请求/shared分离用例和[受控接线1](observation-report.md)通过。受控用例在GORM BeforeRow barrier释放前抓到内容SQL与槽等待337/336ms的不同年龄、10604字节有效栈；首caller取消后shared继续，最终归零。它证明接线，未模拟MySQL服务器执行阻塞，也不是B1归因。
- [精确源码CI](https://github.com/tech-cove01/project-full-stack/actions/runs/41065266414)四作业success，[元数据](ci-46ddefc.json)与[选定日志](ci-46ddefc-selected-tests.txt)保留Linux race/真实依赖范围。专用受控开关在CI显式SKIP，本地实际启用通过；未将SKIP算通过，也未追加浏览器或付费AI。
- [干净独立checkout构建](build-46ddefc.json)记录VCS匹配、modified=false，exe SHA256 `e65decd1f87a2667d3391954aa20de3b9ae3fcec3e99f41b2812a29d43da0ce2`。实际测量工具来源 `516d5342af95d51adf8f6ec9a8c3af321fefb4fa`；正式结束后另补[工具终态守卫](tool-guard-review.md)，没有换工具重测或改变应用。

## 本轮实际测量

三轮均固定同一新[fixture](fixture/fixture.json)，SHA256 `88a3028dc705e18dc80cb32575b7c64a4c3deb60b63d055f116bea24206733cd`，6400篇/32作者/32读者/正文256B/页20/multi640key、001—013、RR事实路径、SQL20/Redis10、API GOMAXPROCS4/GOMEMLIMIT1GiB及原依赖资源/持久化。预热10秒25/s，pre/max VUs256/512，计划multi100/s×180秒；没有同档A对照，不写缓存提升倍数。

| 轮次 | 原生发起 / 终态成功 | unknown / dropped | 全/成功P95 / P99 ms | max ms | 结论 |
| --- | --- | --- | --- | --- | --- |
| diagnostic-1，额外collector开启 | 18001 / 18001 | 0 / 0 | 19.3259 / 26.6919 | 81.2575 | 有效未重现；不作正式成绩 |
| formal-1-multi100，额外观测关闭 | 18001 / 18001 | 0 / 0 | 17.3377 / 23.9781 | 63.5345 | 通过单轮 |
| formal-2-multi100，额外观测关闭 | 16133 / 15933 | **200 / 57** | 24.18684 / 84.048128 | 3656.4646 | **失败，立即停止本组** |

各轮已完成响应的内容、系统与HTTP错误计数均0；formal-2有200次未取得分类终态，不能据这三个0称全部正确。全/成功分位来自各自native summary，没有逐请求延迟原始分布，不称离线重建或跨轮合并。

[诊断审查](diagnostic-report.md)记录182次成功1Hz采样、0次失败、最大间隙1000.8774ms，覆盖及在途字段完整。没有阶段年龄≥100ms或池等待正增，所以栈/慢payload均0；这是无触发，不是采集缺失。未出现支持第二轮单项变化的新候选，未补无信息重复。root据相关回归、精确CI和有效未重现建立[formal gate](formal-gate.json)，明确原B1未知、无业务修复。

## 正式第2轮失败与边界

完整事实、末窗及反例见[失败审读](failure-final.md)。原生k6因dropped阈值提前退出99，stable命令墙钟163.653秒；判据分母仍是事前180秒。发起达成16133/18000=89.6278%，终态成功15933/16133=98.7603%，分别低于99%，unknown200、dropped57也各自违反0预算。P95/P99仍在预算内，不能说本轮因P99失败；max约3.66秒只作单独事实。

此外，原[result](formal-2-multi100-load/result.json)因访问计数一致性守卫失败而没有`result`汇总字段，`meetsBudget=false`。报告保留原文件，从[stable-summary](formal-2-multi100-load/stable-summary.json)单独核对原生计数，不伪补原汇总。MySQL Uptime增161秒，Windows两次全局状态采样中点间隔164.1015秒，差3.1015秒超出旧helper的2.261秒容差。复合`counterResets`标志不能证明实际计数重置；现有访问delta未见负数/丢失，但该轮不能给出有效访问计数成绩。原快照只有Redis commandstats，没有Redis uptime，缺项保留。

原5秒监督33/33样本完整，无资源stop、无forced。停止前的[两容器身份](post-failure-container-identity.json)为restartCount=0/OOM=false；这不排除宿主、VM或依赖的短暂停顿。末窗Redis池等待增长与SQL连接占用可见，cards活动/排队为0且内容SQL此前近30秒未增长；不能据此归因于卡片回源，也不能把本轮现象当作旧B1根因。正式额外分段/抓栈已关闭，不能补造未采集的现场。

## 名额、清理与交接

事前[协议](plan.md)、[起点](budget-start.json)及不可变slot保留：受控1/2、诊断1/2、业务修复0/2；正式启动2/3并在第2轮失败后停止。未使用的名额不是继续定位或开新组的授权。保守开发起点13:10 UTC，正式gate于13:54:19 UTC完成必要工作，未扣负载/CI等待亦在4小时内；[收尾记账](closure.json)保留累计571.708516秒顺序负载等待及CI作业起止，不从开发计时扣除，CI并行时段不冒充操作人空等。本批没有环境调整、补成功轮或新业务。

[cleanup](cleanup.json)完整：32正常logout、Redis会话剩0、observer删除核验、精确API PID66608已停、两只完整CID exited/exit0/OOMfalse；旧新卷和合成事实保留，4份登记私有env已精确删除。Windows terminate退出15如实保留，不声称Go优雅退出。

[只读算术/协议审计](audit-final.json)检查3阶段，errors为空、capacityProtocolPassed=false；30份运行时工具hash严格匹配历史Git516，没有用当前policy替代。[原始文件清单](raw-manifest.json)冻结131份/3463134字节；[Git字节审计](git-byte-audit.json)核131份加manifest共132份/3490228字节，全匹配。原始结果与审计提交为 `9b188b6a17afeff5a4563f475f1c2659c7a0ce5a`；仅早期派生JSON入库换行另有[字节补正](raw-byte-correction.md)，追加 `a9ea133bffab8c34263d255656d0e474b02478a6` 保存未改原件，不amend历史或改变JSON语义，字节审计针对该追加提交。

原CAP-06/07失败不改写。内部[非作者审查](internal-review.md)与开发自验不替代外部独立核验。交下一位核验者审代码/失败材料；若实际复跑，最多使用[新卷单代表档](representative-rerun.md)，新QA结果不能补齐本组失败。

保守可讲的是已核验的Go业务功能、已实现的有界卡片/合并机制及本次观测接线；不能写100/s重复稳定通过、容量整组完成、性能提升比例或根因已修。最终性能指标、材料与实际录屏仍待独立结论，本轮不继续开发新功能或追压测。
