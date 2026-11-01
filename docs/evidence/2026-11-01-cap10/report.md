# CAP-10：本机预检拒绝，尚未进入容量测试

2026-11-01，Asia/Shanghai。**本批工具和点赞分段接线已交付；唯一一次本机空载预检因时基不一致未通过，未启动S1，尚无新的容量或优化效果。** 已在正式负载前告知用户，等待选择已有Linux环境、先看临时云机步骤，或按环境受限收尾。没有购买或部署云资源，没有重跑Feed/hot，没有重开空载或补成功轮。

## 四个问题的当前答案

| 问题 | 已证实的答案 |
| --- | --- |
| 慢在哪里 | 尚无本批压力下的分段证据，不能指定点赞代码/SQL/Kafka瓶颈。当前先遇到MySQL计时域与宿主不一致的可测性问题，根因未确认 |
| 做了什么处理、优化多少 | 完成默认关闭的Like分段观测、真实初态/窗口与有界搜索工具；生产性能优化0项。没有优化前后同率负载，不能报告收益比例 |
| 最高完整短档L、首受限U | 均未取得。空载拒绝不等于50/s服务失败，也不代表容量0 |
| 重复180秒稳定档R | 未取得，正式组未启动。CAP-09旧20/s×45短测、Feed旧稳定成绩保持各自版本/场景，不平移为CAP-10结果 |

## 首窗前发现的环境问题

唯一一次60秒观察共13个5秒样本；其他agent暂停构建/测试，服务后台消费者正常运行，没有业务负载或k6。

| 项目 | 结果 |
| --- | --- |
| 宿主内存最高 | 75.1%，满足≤80%开测余量 |
| 宿主有效CPU中位数 | 28.2%，满足≤50%；无连续3样≥85% |
| 样本节奏与覆盖 | 13样/12有效区间，5秒节奏、60秒覆盖通过 |
| 电源/依赖 | AC接电、节电关闭，所查睡眠转换为空；四个自有CID健康，无重启/OOM |
| 下游 | 只读采样完整、空LIKE增量，三个组已提交起点0且lag0；不声称处理过业务事件 |
| Windows墙钟/单调时钟 | 样本首末60.000493/60.0004891秒，差约4微秒 |
| MySQL Uptime | 1637→1694，仅57秒；两次查询宿主单调中点相隔59.908861秒 |
| 时基门槛 | 差2.908861秒，超过2.1235839秒（含查询不确定度）的既定容差，拒绝 |

原件：[idle-result.json](idle-result.json)、[逐样本](idle-samples.ndjson)、[观察器](idle-downstream/summary.json)、[只读离线复算](idle-clock-offline-audit.json)。未修改原结果或放宽容差重判。宿主墙钟/单调时钟一致，排除了这份原件中“拿不同宿主时钟比较而误报”的解释；它仍不能确认MySQL内部计数实现、guest壁钟校正或VM调度的具体原因。

[同批Docker健康日志](idle-health-offline-audit.json)出现跨服务共同的额外间隙：第一处Redis与ES各约2.82秒，第二处Redis/Kafka/ES各约2.80秒。它们支持共享Docker/VM计时或调度异常候选；没有独立guest单调时钟参考，不能据此证明VM暂停、CPU/内存耗尽或旧B1事故原因。没有新增时钟观察或负载。

按[STATUS](../../../STATUS.md)，预检无效先暂停、由用户选择环境，不能先跑两轮再归咎机器。当前S0/H0，L/U/R均未知，不把预检失败当服务端U；初始累计Kafka节流也不算压力同窗瓶颈证据。换环境建议、明确配置与费用参照见[环境选择说明](environment-decision.md)。

## 已交付的接线与验证

实际应用为干净源码`0602ac08a0b458851b2cc6a8b595bbafb443bdaa`，Go1.27.0 Windows/amd64，二进制SHA256 `f8e7365497f11b17df1e1ba81ad731ee95f9875aaecd142e262b34bea436dee9`；见[干净构建](build-a.json)。后续工具/文档提交不是另一个已测容量版本。

Like使用独立、默认关闭的`GO_LIKE_DIAGNOSTICS`，在鉴权前记录请求，覆盖auth、service/HTTP总耗时、事务包络、实际driver Commit调用包络、提交后Invalidate/LikeState/Mark；仅固定阶段、内部序号与时间，沿用32条慢请求/256段上限及loopback限制。Feed开关独立，本次没有Feed负载。原事务和成功提交后独立2秒WithoutCancel同步收尾、版本围栏、一致性/持久化语义不变。

一个有限真实功能用例仅做2次handler状态变更，核关闭/开启、阶段完整性、提交后取消、active阶段归零、SQL/Outbox/Redis一致及合成会话清理；它不是吞吐测量。四个相关包普通测试/vet通过；精确该源码的[CI四项success，含Linux race](https://github.com/tech-cove01/project-full-stack/actions/runs/41286794821)已归档[ci-a.json](ci-a.json)，不代表Linux执行器或容量通过。

新工具使用512个用户各独占笔记/关系，真实同步PUT翻转，60/180秒完整窗口末判延迟。每窗预热、合成SQL/Redis初态归一化及主窗单列，稳定前existing512/active0，版本/历史不回退，setup事件不计吞吐；主窗前后独立保存capacity/数据库计数。预置v0明确为夹具，不冒充真实写入。实际[prepare](cohort-prepare-exit.json)和[只读初态核验](fixture-inspect-beforeidle/inspect.json)为512existing/inactive-v0、事件0、派生无差异；主窗归一化和真实容量负载尚未执行，不能以纯检查代替运行验收。

有界策略覆盖S1=50、倍率升档/折半/夹逼、最终源码边界、S8候选保留、160/400余量、两轮正式首败结束，以及unknown/no-op/业务差异整批停止。最终33项Like纯检查、21项runner检查、26项独立策略反例通过，见[执行记录](tools-validation.json)、[策略原件](grade-checks-final.json)、[驱动审查](like-driver-review.json)、[策略审查](policy-review.json)。早期纯检查夹具/隔离stub失败有说明或原件，未消耗真实槽。部分纯检查stdout只在工具记录、没有单独文件，执行记录已明确该限制。

prepare使用的helper旧hash `a68c6877…` 与最终登记私密输入顺序修复后的`a9004e4e…`区分记录；[旧源码快照](helper-used-for-prepare.mjs.txt)完整SHA匹配旧hash。该差量没有改prepare分支或夹具，最终phase先登记路径再写凭据，故障清理有直接纯反例。

资源采集保留CAP-09最终CPU/内存/池/持久化，新增实际cgroup v1/v2归属与节流原数/增量。最初private/v2假设在接线时已拒绝，随后按本机真实v1挂载修正并验证四服务4/2/2/2CPU配额；未改配额、未发低速容量探针。P_S采集保留仪表/consumer启用状态与缺失项，嵌套时间不可相加，累计MAX不能差分，端点gauge不作累计增量。实际执行前还按[最后守卫修正](supervision-review-3.json)允许同runtime、同应用配置增加CAP夹具字段；源文件哈希已记录，修改发生在唯一空载预约前，没有重开观察或改门槛。见[监督审查](supervision-review-2.json)、[数据库接线](database-observation-review.json)。

## 清理与接续边界

[cleanup.json](cleanup.json)确认512会话正常logout且剩余0，观察账号移除，API27976按创建时间/绝对路径/hash停止，四个完整CID停止，六个卷保留，3个登记私有env与凭据输入删除。API实际exit15，不宣称Go优雅关闭；MySQL/Redis退出0、Kafka/ES143，无OOM。没有操作CRM、未知进程、用户安全/电源配置或旧卷。

当前等待用户环境选择，未将CAP-10B—E或整批性能目标标完成。若选择Linux，只能用剩余8短窗/2正式/最多1优化继续同一批，必要平台采集适配及新夹具如实记账；若选择受限收尾，就交该结果并停止性能工作。不默认追加CAP-11、核验容量轮或Feed。预算/未知风险与旧批失败保留，README没有追加性能或工作流。

后续用户表示可准备云机并要求GitHub静态展示素材；已给出Linux配置并等待SSH资料。已读CodeBuddy部署skill，只新增经审查的[截图/导出计划](showcase-capture-plan.md)，没有建站或发布，也不增加容量轮。见[后续状态](environment-user-followup.json)。

本机阶段原件大小与SHA256列于[清单](raw-manifest.json)，工作预算按[保守交接](budget-handoff.json)承接；Linux后续只能追加，不能重发本批预算或改本机拒绝。
