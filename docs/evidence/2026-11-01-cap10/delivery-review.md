# CAP-10 本机预检与交付原件内部审读

2026-11-01。**当前报告与交接口径通过内部审读，无剩余文档/原件交付阻断；环境准入仍未通过，容量目标尚未完成。** 本轮仅完成Like观测、工具及一次本机空载观察，S/H预约和主负载均为0，L/U/R未知，生产性能优化0项。环境方案仍等待用户选择，不能自动重开本机预检、启动S1或实施云采购/部署。

本审读核对[报告](report.md)、[环境选择说明](environment-decision.md)及[机器记录](environment-decision.json)、idle原件与离线复算、构建/CI、工具记录和清理，并对照STATUS/交接/总方案。此次只读取文件和已有结果，没有执行测试、SQL、HTTP、依赖操作、网络或新负载。审读者此前编写并执行过纯策略checker；这部分明确属于合成文件检查，不作为该checker自身的非作者验收或真实容量证据。

## 预检拒绝及其解释边界

唯一60秒观察保存13样/12有效CPU区间，5秒节奏和覆盖通过；宿主CPU中位28.2%、内存峰75.1%，AC、所查睡眠事件、四个依赖健康/无重启OOM及干净下游检查通过。唯一拒绝项如下，未重判或放宽门槛：

| 比较 | 原件与复算 |
| --- | --- |
| Windows样本墙钟首末 | 60.000493秒 |
| Windows样本单调时钟首末 | 60.0004891秒；13点与墙钟偏差最多约8.8微秒 |
| MySQL Uptime | 1637→1694，增加57秒 |
| 两次SQL查询宿主单调中点 | 相隔59.908861秒 |
| 拒绝差额 | 2.908861秒，大于已冻结容差2.1235839秒 |

[时钟复算](idle-clock-offline-audit.json)与sample.at/elapsed一致，现有原件不支持Windows墙钟跳变、QPC/墙钟混用或采样追赶造成该差值。[健康日志复算](idle-health-offline-audit.json)也与同批去重日志相符：扣除前一次检查耗时和名义3/5秒间隔后，多个服务出现共同约2.82/2.80秒额外间隙，含一条预检前的Kafka间隙已单独标记，不混作本窗证据。

这些是共享Docker/VM计时或调度问题的候选线索，**没有独立guest单调时钟参考，不能确认VM暂停、guest壁钟校正、硬件耗尽、点赞代码瓶颈或旧B1根因**。按本轮冻结协议暂停合理；预检拒绝不是API的服务端U，也不等于容量0或50/s失败。没有在不适测状态下继续正式两轮。

原件SHA256：idle-result为`80043ae9faf96bc25820f4da0b4e46164a3ccd7527f84dcb4f6b3784b6a84662`，idle-samples为`657a1e4eb247c69755422ed1cf234cd67681813b317d572dbff711bf2315f309`。离线复算没有重新采样，也没有改写这两份原件。

## 版本、接线与验证范围

- 实际启动的应用是干净源码`0602ac08a0b458851b2cc6a8b595bbafb443bdaa`，二进制SHA256为`f8e7365497f11b17df1e1ba81ad731ee95f9875aaecd142e262b34bea436dee9`，API PID27976，与[build-a](build-a.json)、API元数据和idle记录相符。[CI-A](ci-a.json)精确同SHA，四项均success；Linux race不代表Linux执行器或容量通过。
- 默认关闭的独立Like开关、鉴权前HTTP/auth、service、事务/commit包络及提交后阶段，有真实有限2次变更用例和相关测试证据。原事务、独立2秒WithoutCancel收尾、版本围栏及同步提交语义保持。有限功能用例不算吞吐窗口。
- [工具验证记录](tools-validation.json)的11个最终文件hash均与现文件相符。33项Like、21项runner纯检查属于作者既有执行记录；26项独立grade反例有[结果原件](grade-checks-final.json)，覆盖完整性能miss与未知/事实错误的区分、搜索/回退边界、8短窗含候选、840秒总额、双队列窗口、正式冻结和首败关闭。部分作者stdout/单项时间未单独归档的限制已明确，不补造执行证据。
- 初次策略检查23/24的失败来自合成SQL端点留下10秒证据缺口，grader正确拒绝；[初次结果](grade-checks-initial.json)保留，最终checker同时验证该拒绝和合法7秒端点场景。没有消耗真实S/H。
- prepare实际helper源码快照hash为`a68c68776654884914f3d5dc3ad0dfc5740f2d16bbb9a6406ea31dc740f6809b`；最终phase私密输入顺序修正后的helper为`a9004e4e4ed938e96119fd55e7c57bbeda2661c219010ddaf3511f78808e42a8`。报告已区分，修正不改prepare分支或已准备夹具，不能将新hash冒充原prepare源码。
- [最后idle守卫记录](supervision-review-3.json)固定实际执行源hash`99d68e13834d371278247fb23d34ebe5e21edc5826c7c78e03da5b8296aae0ca`，与现文件相符。四行前置修正仅接受同runtime、同非CAP应用配置且source/revision一致的派生配置；原PID/创建时间/exe/hash/健康验证仍在。它发生在唯一invocation/reservation前，旧守卫没有失败执行或stderr，不虚构一次重试；旧review-2候选hash保留，60秒门槛未变。
- 本轮prepare/inspect确认512条合成existing/inactive-v0关系、事件0、Redis一致；v0是夹具预置，不能算真实mutation。每窗warm后归一化、native前后数据库快照及容量执行路径虽有静态/纯检查，**尚未在真实S窗执行**，不能把工具通过升级为真实搜索、归一化或容量验收。

本次实际cgroup v1挂载与配额接线、原private/v2假设拒绝均单列保留；启动以来累计节流不当作负载同窗瓶颈。数据库采集保留disabled/missing、嵌套重叠、累计MAX及端点gauge边界。后续工具/文档交付HEAD与上述实际启动应用SHA分开，尚不存在另一个已经测过容量的最终应用版本。

## 名额、清理与接续

公开目录未发现S/H的slot或command，机器决定记录为shortUsed=0、formalUsed=0、optimizationCount=0，L/U/R均null。唯一一次本机空载已用；若用户选择换一次环境，只能延续同批剩余8×60秒短窗、最多2×180秒正式、主负载840秒及最多一项优化，仍受整批3小时有效开发约束。等待不是授权，不能重发预算、复用已撤销会话或自动追加Feed/hot/新批次。

[cleanup.json](cleanup.json)为complete、exceptions为空，SHA256为`518d5b99671c4092defad6da1b7deccf515aaa9484a859e802784fbe73bda6a2`。512会话正常logout、剩余0，observer删除验证完成，凭据与3个精确登记私有env已移除。API27976身份匹配后实际exit15，明确没有Go优雅关闭主张；四个完整CID与环境记录一致，退出码0/0/143/143，OOM=false、restart=0，六个原卷及挂载保留。停止后的health状态不替代空载期间的健康检查。上述结论来自既有清理记录，本审读没有重新访问进程或依赖。

根[STATUS](../../../STATUS.md)、[交接](../../agent-handoff.md)、[总方案](../../implementation-plan.md)与报告一致：当前停在环境选择，CAP-10B—E未完成，没有本批稳定吞吐或优化收益；保留CAP-09失败与旧B1未知，已有Feed成绩仍属旧范围。云方案只是供用户选择的提议，没有确认采购、部署或切换；README未加入新性能/流程。当前可正常提交这些工具和受限结果，最终交付SHA由交付者提供，后续动作仍以用户环境选择及同批剩余预算为前提。
