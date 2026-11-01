# CAP-09 真实点赞最终工件内部审读

最终结论：multi 的20/s×45秒短档通过；50/s短档、预先冻结的20/s×180秒正式第1轮均按原延迟门槛早停，正式第2轮按首失败规则关闭。本批未取得完整180秒、两轮可重复的点赞稳定吞吐证据。早停也不能反推“若继续跑满180秒，其最终P95必定失败”。最终可选hot未启动并已关闭；环境和会话已按精确身份收尾，本文件完成公开工件范围的内部审读。

这是实现团队内部对root保存的公开工件进行复算，不是CAP-09E在新环境、新数据上的独立容量复跑。审读未执行Go、SQL、HTTP、k6或新的剖析，也未修改负载工具或生产代码。

## 固定版本和范围

- 应用：`7a80186b568b521c54736b735c08dce8ef962e19`；二进制SHA256：`9f70db9ad869eaac4935f714c00980f3e5c173404fe584c9b725fdb943405fd7`。
- 环境SHA256：`a1e1abff73f147cbc04933b7c27cef4bf169c2f1f9618c6e323fc57d77dc9e97`；Like fixture SHA256：`bc7544b422cc2bc9fbdf797ee653c44aa0e78a7a5a9a648dbd95b0b95e670224`。
- [最终应用](final-application.json)、[精确SHA的CI原件](ci-b.json)、[事前正式档冻结](freeze-like.json)相符。正式第1轮command的`freezeSHA256`与冻结文件字节相符；没有在早停后修改目标档。
- multi配置池为512个liker、512篇独立笔记、512条独占关系、1个作者；每VU独占一条关系，最多1在途。正文256B，预分配256VU、上限512VU。实际发生正版本变更的参与者另由前后RR事实差派生，不把512可用池当512实际写参与者。
- 正常路径始终真实鉴权PUT：MySQL同步提交关系、公开计数与唯一Outbox，随后同步失效、回查和Redis Mark。没有替换成缓存受理接口。最终extra tail与CPU profile关闭。

## 主场景结果

| 原件 | 预定档 | attempted / 已分类HTTP与mutation | SQL版本/event增量 | unknown | 已捕获样本P95/P99 ms | 结果 |
| --- | --- | --- | --- | --- | --- | --- |
| [最终阶梯1](like-final-stair-1-load/result.json) | 20/s×45s | 901 / 901 | 901 / 901 | 0 | 58.8572 / 178.8933 | 完整短档通过；901/45≈20.022次真实变更/秒，仅此45秒范围 |
| [最终阶梯2](like-final-stair-2-load/result.json) | 50/s×45s | 570 / 568 | 569 / 569 | 2 | 204.09581 / 287.862182 | native99，约11.4秒早停，终止该阶梯；没有25/30或更高插档 |
| [正式第1轮](like-hold-1-load/result.json) | 20/s×180s | 230 / 229 | 229 / 229 | 1 | 201.61158 / 264.009124 | native99，约11.5秒早停，未完成180秒 |
| 正式第2轮 | 原计划20/s×180s | 未执行 | — | — | — | 第1轮失败后关闭，不降档重补 |

三次原件中no-op、业务拒绝、已分类系统/HTTP错误、content error与dropped均为0。失败行仍保留原计划2250/3600的分母，不用实际短时间重算为完整窗口通过。其分位来自已完成并采到时延的HTTP/变更样本；未知请求没有完整终态样本，不能因此写“全部发起均成功”。

50档的`attempted-completed=2`、`effectUnknown=0`，未知上下界均为2；SQL多于已分类mutation的1次变更没有洗掉原始未知。正式第1轮对应上下界均为1，SQL增量恰等于已分类229次，仍保留1个缺终态的发起。所有失败均保持`nativeScenario.complete=false`及API/整体不通过。

逐关系正版本差还确认：20短档256用户/256笔记/256关系，123条关系变更3次、133条变更4次；50档256/256/256，199条变更2次、57条变更3次；正式第1轮已核实229/229/229，各变更1次。失败阶段这里只称已核实变更的参与者，不声称覆盖了所有未知尝试的身份。

## 事实、Redis与三组下游

前后快照、最终reconcile及原生summary已交叉复算。各关系active/version、连续且唯一的`LIKE_CHANGED`事件、canonical事件校验和公开`tb_blog.liked=active关系总数`一致。逐笔记Redis成员集合及每个已有关系的版本hash与SQL相符；不把可选`__complete`当完整性证据。50档和正式早停后均未重现旧版取消造成的Redis派生遗漏。

| 场景 | multi对账：512篇笔记累计版本/事件 | observer：513篇笔记全cohort累计事件 | 最终状态 |
| --- | --- | --- | --- |
| 20短档 | 4297 | ready3397 + 本阶段1101 = 4498 | 全DONE；三组各4297条multi范围IGNORED；Redis一致 |
| 50短档 | 5117 | ready4498 + 本阶段820 = 5318 | 全DONE；三组各5117条multi范围IGNORED；Redis一致 |
| 正式第1轮 | 5546 | ready5318 + 本阶段429 = 5747 | 全DONE；三组各5546条multi范围IGNORED；Redis一致 |

两个范围的差201来自旧hot预热事件。每阶段observer范围含warm与stable，不能把1101/820/429全部计作稳定变更。三组均为`follow-v1`、`feed-v1`、`search-v1`，LIKE的正确收据是IGNORED，APPLIED为0；收据不是Redis点赞更新。最终无PARKED/NEEDS_REVIEW，真实三个Kafka group均已归零lag。最终排空没有覆盖任何早停失败。

20短档使用完整落在native窗口内的SQL样本保守分母800，1%预算8；outbox净增6、三组receipt gap各净增7、三个真实broker lag各净增2，均在门槛内。最后15秒描述性斜率仍有正值，未改作额外门槛。按完整SQL/Kafka观察终点，native结束后4.788239秒观察到全链回基线，最大相邻采样起点间隔5.00035秒。

50档和正式第1轮没有完整目标窗口；其`pipeline.passed=false`且`nativeRecoveryBudgetVerified=false`。observer自身valid仅代表完整采集及降载后收敛，不能升级为45/180秒稳态、末分钟预算或重复稳定通过。

## 资源和时基边界

| 场景 | 完整资源样本 | API RSS采样峰 MiB | k6+helper RSS采样峰 MiB | 有效区间API CPU峰，单核% |
| --- | --- | --- | --- | --- |
| 20短档 | 10 | 38.54 | 430.28 | 55.6 |
| 50短档 | 3 | 39.62 | 436.80 | 70.3 |
| 正式第1轮 | 3 | 38.37 | 436.86 | 56.6 |

各样本无采集错误，四个依赖CID、资源限额和卷身份完整；护栏、工具字节、供电和自有子进程收敛检查均通过。上述是采样所见峰值，不是全程绝对峰值。首个短CPU区间标为invalid并排除CPU峰统计。容器内存最高约ES65.11%，但不能概括为所有CPU闲置：Kafka Docker采样峰约202–206%（容器2CPU配额口径），50档host单样89.5%未达3连续样本止损。没有仅据这些相关峰值指定HTTP尾延迟根因。

下游observer进程另有12/9/6份资源记录，RSS采样峰分别16.71/16.05/16.23MiB，有效区间CPU峰分别1.2/1.1/1.4%单核；不混入k6+helper的2GiB合计口径。

通过的短档以k6原生scenario.startTime加45秒作为窗口终点；wrapper启动/退出时间单列。失败行仅保留原生起点与预定时长，不把预定结束当实际完整运行，也不将wrapper退出后的等待移入native窗口。各阶段预热、停止探针及恢复等待分别留档，不混入稳定mutation计数。

## 对照和待补工件

旧main50及诊断均没有完整可比稳定基线，不能从旧失败到当前45秒短档或派生修复计算稳定吞吐提升。旧hot只完整执行了10秒201次warm，因旧工具warm延迟门槛提前结束，45秒稳定段未启动；后续政策修复仅向前生效，旧失败与名额保留。

旧hot的初态为0关系/0点赞，201次均为首次true插入。若沿同fixture继续，初态已含这些关系，不能忽略flip方向和首次insert比例的差异，或据10秒warm宣称严格同条件代码提升百分比。

Like的唯一可选final-hot20记为`not_run_closed`，没有启动，不是通过或补跑；见[最终范围关闭记录](final-scope-closure.json)。这是root基于尽快收尾偏好关闭可选范围的决定，不是用户显式要求停止，也没有取消必需正式组或重置失败名额。旧hot201次warm仅保留原始观察，不能补主场景正式组失败或冒充热点容量成绩。

## 精确清理与退出码守卫修复

[原清理进度](cleanup-progress.json)在完成正常logout、observer删除验证和精确API停止之后，被“所有容器exitCode必须0”的尾部断言拦住。MySQL/Redis实际均为0；Kafka/ES均为143、OOM=false、status=exited。Kafka日志有BrokerServer `shut down completed`及Raft `Graceful shutdown completed`，ES日志有`stopped`/`closed`，支持本次143为停止后的SIGTERM状态，而不是OOM或SIGKILL。

`cap09-env.py`的最小守卫修复仅对Kafka/ES接受0或143，MySQL/Redis仍要求0；精确CID/project/service、停止状态、端口配置、限额和卷检查保留，不接受137/OOM。该修复经非作者只读审查，不改变任何性能门槛、原负载或正式组结果。

按[恢复方案](cleanup-recovery-plan.json)只执行一次尾部恢复，没有再次logout、SQL删除observer、停止/重启服务或发起负载。[最终清理](cleanup.json)与[恢复记录](cleanup-recovery-1.json)内容一致；原progress字节SHA256仍为`626039964bc6649f273e11195501b14a257e1d60c231dcf6aedd97fdb70dec85`，原AssertionError保留。实际执行脚本的[原字节快照](cleanup-tail-recovery.py.txt)哈希与恢复记录相符。

- 512个Like会话和32个Feed会话均正常logout，撤销前512/32、撤销后均0；两份私有凭据输入已移除，SQL fixture保留。
- 按本cohort nonce删除observer并验证的步骤已在原progress成功完成；恢复没有重放该SQL。
- 最终API PID37664身份匹配后停止，原始exit15、`goGracefulShutdownClaimed=false`照实保留，不宣称Go优雅退出。
- 四个停止CID与环境记录一致，6个原卷逐一验证并保留；3471/6571/9391/9271/8271/8371六端口已关闭。
- 549条已登记子进程身份按PID及创建时刻核对不再活跃；这是身份记录数，不声称549个不同进程。登记过的阶段私有输入已检查收尾，11个注册私有env文件按精确同runtime路径移除，无递归清理。

上述结论来自公开清理工件及实际脚本字节的内部复核，不是本审读者重新访问依赖或进行新环境独立容量复跑。完整失败、可选范围关闭和旧B1未知均不由这次清理改写。
