# CAP-08 独立核验：观测与回归成立，正式容量失败，按预算结束

2026-10-31（Asia/Shanghai）。核验交付`2c1ef04b7b1d1ae10d98767e18e6c12ff697166c`，差量自`1de1735`，实际负载应用固定`46ddefc2a16d41e4bb01a3d20d08bdfcaeff465f`。在隔离worktree完成审查、相关真实回归和一项工具小修，不修改实现者原始证据，不扩业务或开启新容量组。

**结论：默认关闭的请求/shared分段和有界在途观测通过本轮独立核验，既有业务功能回归通过。正式第2轮原生失败及访问时基拒绝均成立，CAP-08稳定容量验收未通过。** 本批已按预算结束，不自动继续CAP-09；不能把单轮成功、CI通过或工具小修写成已修B1/容量整组完成。

## 审查与一个独立修复

- [生产源码审查](code-review.md)：shared trace保留应用life及原有2s deadline，首caller取消不终止共享任务；请求与shared计数分开。每阶段1024个固定在途时钟、短内存锁、溢出显式不完整，End once，最多32慢记录。默认nil recorder不分配clock/trace；未变业务SQL、RR、池、TTL、身份、公开版本/个人状态或缓存失效规则。未发现可复现P1/P2生产缺陷。
- [工具审查](tool-review.md)：loopback/禁redirect、在途年龄与池增量触发、有限profile/慢记录、首末和间隙覆盖、不可变名额、首败停止及进程收敛符合本次范围。
- 发现P3工具完整性缺口并单独提交`a40ffc03c94fc8dacc6013652c04490183a58559`：两个策略谓词把缺少`failedStartOrException`字段当成正常null。现显式读取、沿既有KeyError处理拒绝不完整记录。[同一反例旧FAIL→新PASS](guard-fix.md)，两子项旧失败，全12项纯检查新通过，另经非作者审查/检查。三轮实际记录都有显式null，[原分类保持通过/通过/失败](guard-actual-stage-classification.json)。不改Go、原测量工具516历史、原始工件、阈值或名额。

## 本轮独立执行的验证

新项目`fullstack-review-cap08-2c1ef04`、新命名卷、回环MySQL3464/Redis6564和预检无重叠网段10.254.64.0/24，Go1.27.0 Windows/amd64。基线001/002初始化，版本001—013应用后重复迁移全部skip。原MySQL4CPU/2GiB、Redis2CPU/512MiB/持久化保持；[选取环境](environment.json)、[独立覆盖](qa-compose.yaml)、两次migration原件均在本目录。不归档完整环境或凭据。

```text
# backend；六包普通75个通过项、30个显式SKIP，exit0
GO_INTEGRATION=0 go test -count=1 -json ./internal/platform/taildiag ./internal/communitycards ./internal/social ./internal/platform/app ./internal/platform/diagnostics ./internal/communitycache
go vet ./internal/platform/taildiag ./internal/communitycards ./internal/social ./internal/platform/app ./internal/platform/diagnostics ./internal/communitycache

# backend；新真实依赖，CAP08_CONTROL_EVIDENCE为新目录，130个通过项、1个专用容量测量SKIP，exit0
GO_INTEGRATION=1 go test -count=1 -p 1 -json ./internal/communitycards ./internal/social ./internal/communitycache ./internal/platform/taildiag ./internal/platform/app ./internal/platform/diagnostics

# 根目录；无服务/网络/容量负载的12项工具纯检查
python -B -X utf8 scripts/cap08-check.py
```

通过项含子项，不同运行/子审查有重复，不能相加为独立用例总数。[普通输出](ordinary-tests.jsonl)/[退出码及vet](ordinary-exits.json)、[真实输出](real-tests.jsonl)/[退出码](real-exits.json)、[统计/SKIP](test-summary.json)保留。真实HTTP用例确认auth/facts/cards/counts与shared独立累计；原FC、版本/取消/降级和计数失效回归保持。Windows本轮未跑race。

专用受控用例实际启用并PASS：[释放前](controlled/before-release.json)明确barrier未释放、首caller已取消、测试active上限1/生产仍8；共享SQL与活动槽排队仍在途。真实动态loopback collector在放行前按约352ms年龄抓到1份11706字节未截断profile，[manifest](controlled/collector/manifest.json)、[collector返回时仍未释放](controlled/collector-exit.json)与[释放后](controlled/after-release.json)的阶段归零一致。该barrier位于GORM Row到driver之前，不证明MySQL服务器锁等待或旧B1根因；这是接线验证，不是第2次完整诊断/容量成绩。

独立线上核实交付精确`2c1ef04`的[41071168943](https://github.com/tech-cove01/project-full-stack/actions/runs/41071168943)四项success，[元数据](ci-delivery.json)/[步骤](ci-delivery-jobs.json)/[选定PASS及SKIP日志](ci-selected-tests.txt)归档。精确应用46ddefc的[41065266414](https://github.com/tech-cove01/project-full-stack/actions/runs/41065266414)也在线确认success。CI中的专用受控开关为SKIP，本轮本地另实际运行；不能混成Linux受控场景通过。Python守卫修复以12项本地纯检查为证，不冒称原CI已执行新用例。

## 容量失败的独立复核

[证据专项](evidence-review.md)独立核对三阶段native计数/分位、131份原始文件加manifest共132份/3490228字节、30个历史运行工具hash、实际exe、slot/预算、覆盖与精确清理。早期failure-window的CRLF/LF追加补正语义一致，原native未更改；不拿当前修复policy替代运行时516工具hash。

| 阶段 | 发起 / 已分类成功 | unknown / dropped | 已记录P95 / P99 ms | 结论 |
| --- | --- | --- | --- | --- |
| diagnostic-1 | 18001 / 18001 | 0 / 0 | 19.3259 / 26.6919 | 有效未重现，非正式成绩 |
| formal-1 | 18001 / 18001 | 0 / 0 | 17.3377 / 23.9781 | 单轮通过 |
| formal-2 | 16133 / 15933 | 200 / 57 | 24.18684 / 84.048128 | 原生dropped阈值触发提前退出99，整组失败 |

formal-2计划仍180秒，发起达成89.6278%、成功/发起98.7603%，均低于99%，unknown/dropped各自违反0预算。P99在预算内，**这轮不是P99阈值失败**；max3656.4646ms为另一个已记录事实。200 unknown没有分类终态、实际耗时/结果未留原始分布，57 dropped是未开始的迭代，不能相加伪造257个已发HTTP错误，或把已完成样本的P99代表所有发起。三类已分类错误为0不等于整轮无问题。

访问计数另有独立拒绝：MySQL Uptime+161秒，Windows全局状态采样中点间隔164.1015秒，差3.1015秒超过原2.261秒容差。原result缺`result`汇总字段，保留而不倒填；各原始计数未负增、容器restartCount0不能证明实际reset或排除VM/宿主暂停。Redis原快照没有uptime，不能推导它的同窗时基。这一后处理拒绝不能解释掉前面的原生失败，也不支持修改容差宣告达标。

诊断182次1Hz采样覆盖、5278条stage字段、无溢出/不完整年龄/触发/慢记录成立；正式按协议关闭额外collector，第二轮33/33安全样本完整、无resource stop/forced。最后采样cards活动/队列均0、内容SQL此前近30秒未增长，SQL/Redis等待随后增长；这与旧B1的现场不同，不足以归因卡片回源或宣称任何根因已修。采样缺口和非原子时点继续保留。

**本轮独立核验没有运行k6、代表容量档、热点或升档。** 失败已可由原始native与独立复算确认，不为求通过再补轮；一次新成功也不能补齐原组。

## 清理与收尾判断

本轮相关真实回归结束后，按两个自有完整CID正常stop，均exit0/OOMfalse、卷保留，[清理](cleanup.json)。测试自有行/key/身份由通过的用例清理，没有容量token cohort、observer、私有容量env或独立API；未动CRM服务，未flush/删卷，未重跑前端/付费AI。原实现者32会话/observer/API/CID/4私有env清理另由专项报告核其记录，不冒称本核验者执行了原清理。

**可以冻结功能开发并准备简历/演示；不能按“稳定容量全部通过”收尾。** Go业务、B端、社区/Kafka、ES、业务AI及CAP-07缓存机制沿既有核验成立，本次观测接线也有独立证据。按CAP-08既定第二种终态结束本批：功能已完成、容量未通过、已知/未知和保守可写指标单列，不自动扩CAP-09或新功能。

简历可讲已核验的交易幂等/补偿恢复、真实Feed公共片段/批量/同批合并/版本取消、可靠事件与搜索；受控8→1、冷/热内容SQL注明场景。不能写本批100/s重复稳定通过、5000+QPS、容量提升比例或长尾根因已修。旧版本30/s交易/hot200等历史数字须按各自版本与报告引用，不平移为当前所有路径容量。材料和录屏可以基于功能版整理，原未知性能限制如实保留；本核验不替用户降低原性能判据或开启新定位。
