# CAP-08 工具、名额与失败分类独立审查

2026-10-31（Asia/Shanghai）。交付为 `2c1ef04b7b1d1ae10d98767e18e6c12ff697166c`；实际测量工具来源为 `516d5342af95d51adf8f6ec9a8c3af321fefb4fa`，应用来源为 `46ddefc2a16d41e4bb01a3d20d08bdfcaeff465f`。本方向审查cap08-stage、policy、check、collector、audit、env适配及cap06-env差量，结合plan、终态守卫说明与formal-2失败记录。

结论：本批停止与失败表达正确，formal-2不能解释成仅后处理工具错误。发现一个P3终态字段缺失守卫问题，已由主核验者独立提交 `a40ffc0` 修复，本审查者复核最小差量及12项纯检查通过。该修复不改变实际三阶段分类，不是B1业务修复，不启动新负载或重开CAP-09。

本审查者未启动API、容器、SQL、Redis、压测或profile，未修改源码和原始证据。本人执行仅有内存中的字段缺失反例、现有无外部依赖纯检查，以及读取实际工件的policy分类；纯检查只创建并清理它自己的新临时子目录。本文是按分工新增的唯一审查文件，不暂存或提交其他agent的修改。原生数据全面复算、应用观测代码与真实依赖回归由主核验者及其他方向另记，本文不冒称本人实跑。

## 终态字段缺失：已修复的P3

交付policy在 `passed` 与 `measured_slo_failure` 中使用 `stage.get('failedStartOrException') is None`。writer正常结束明确保存null，异常结束保存异常类型；字段缺失应为终态未知，旧判定却将其等同明确null。

本人保持原文件不动，仅在内存中删除实际合法formal-1 stage的该字段：原件 `passed=True`，删除字段后仍为True。对等价的合法受限阶段合成数据，`measured_slo_failure` 同样在缺字段时为True。这会削弱后续正式轮放行与服务容量括区对不完整工件的守卫。早先工具说明虽披露这一schema边界，但没有当前writer或合法历史工件需要兼容缺字段。

主核验者的最小修复只将两个取值改为 `stage['failedStartOrException'] is None`；原有KeyError捕获使缺失返回False，显式null与晚期KeyboardInterrupt保持既定行为。新增 `test_stage_without_explicit_exception_field_is_incomplete` 分别验证通过门及受限括区门，未改应用、测量链或旧证据。本人重跑 `python -B -X utf8 scripts/cap08-check.py`：12项纯检查全部PASS。

本人用三份实际工件复核当前policy：

| 阶段 | passed | measured_slo_failure | 实际终态 |
| --- | --- | --- | --- |
| diagnostic-1 | true | false | wrapperExited=true、显式null、wrapper exit0 |
| formal-1-multi100 | true | false | wrapperExited=true、显式null、wrapper exit0 |
| formal-2-multi100 | false | false | wrapperExited=true、显式null、wrapper exit1 |

formal-2存在访问计数拒绝及缺失正常result对象，不能充当可用于上探括区的纯SLO失败。修复没有改变它的失败，也不能拿补丁后的policy字节替换command中原516工具hash。

## 名额、顺序与进程终态

`cap08-stage.py` 在API／依赖验证前以exclusive文件保留slot和command，真实启动失败及异常仍占名额；同slot竞争不会默默覆盖。policy固定最多两次诊断、正式顺序multi100／multi100／hot200、同一最终源码，以及原有限可选阶梯／邻近／持有条件。正式开始后不再允许诊断阶段，正式任一轮未通过便不能启动下一轮或可选上探；不因剩余名额自动继续定位。

四小时开发时钟采用保守wall-time，不扣未验证等待；正式gate要求限内工作完成、实际诊断与相关回归／精确CI，并区分not-reproduced与treated的证明。gate仍是操作者有证据的明确结论，不是工具自动推断根因。

stage记录wrapper、collector与原生预算的独立终态。异常时保留ownership lock，沿已有 `release_completed_lock` 核wrapper退出、stage与监督终态及done后才释放；不能仅因外层helper停止便认定所有工作结束。collector超时只结束该确切子进程，不按名字处理其他进程。formal phase显式禁tail且不启动额外collector。

当前闭批实际只用了受控1、诊断1、业务修复0、正式2；后续hot／升档／持有未执行。本文审查及P3修复不增加这些名额，也不把停止后的工具修复计为业务机制修复。

## 在途触发、覆盖与安全边界

新collector按请求／共享任务stage的active与最老年龄（至少100ms）触发，也读取SQL／Redis全池等待正增；它不再只等请求结束后的slow trace。因此有能力在共享loader仍阻塞时取得触发机会。缺失必要stage、无效字段、不完整最老年龄均明确使有效诊断失败，不能将缺失解释成零等待。

完整覆盖判据包括stable创建至首poll、末poll至supervisor done及最大poll起点间隔均不超过2.5秒；只有一次成功请求不代表覆盖180秒。任何HTTP／stage解析失败、失败或截断栈、不完整年龄均不能放行“有效未重现”。受控模式另限动态loopback、一次capture、最多5秒，只证明屏障下接线，不声称稳定窗口覆盖。

实际诊断固定127.0.0.1:8351且禁止redirect，capacity最多2MiB、每份栈最多1MiB，顺序约1Hz，最多三次栈，前一次捕获完成至下一次发起至少15秒。不连续CPU profile、不枚举进程或导出环境／SQL／key。真实API／k6身份由原五秒supervisor及stage前后守卫承担；collector自身不把文件中的PID当成独立进程验证。

抓栈和capacity不是同一原子时刻；当capacity迟到或超时时可能无法在同一阻塞峰值取得新栈，失败会留证并使覆盖不成立，不能用恢复栈排除此前等待。约1Hz采样也不能保证捕获每个短脉冲。

slow归档最多保留32份不同实际payload，按已观察总时长选择；timeline／window及诊断前后端点只存sequence引用和原stage等字段，未归档的引用明确缺payload。HTTP原始字节SHA与重新序列化的JSON子集区分，不能从子集恢复完整response或逐请求时延，也不能把ring／轮询未看到的记录填0。stage／pool／共享任务区间嵌套，不作可相加的单请求拆分。

## 生命周期适配差量

cap06-env只增加固定cap08 cohort、CAP08私有runtime／证据根、diagnostic／formal phase和对应active-lock边界；cap06／07既有根保持各自限定。纯检查包含不同cohort拒绝彼此私有路径和非法cohort。API clean构建、精确PID／UTC创建时间／exe hash、CID／project／port／挂载及精确清理协议继续复用，没有放宽为任意路径或任意服务。

正式环境在logout和observer核验完成后才按确切身份停止API／容器，保留新旧卷。Windows terminate仍不声称Go优雅信号关闭。本文未执行任何环境命令或清理。

## formal-2：原生失败与访问计数拒绝分别成立

原native summary已有16133发起、15933完成成功、unknown200、dropped57，以及计划180秒分母下发起比例不足。P95／P99仅包含已记录完成观察，不能用其达标抵消未完成和丢迭代；unknown不能补零或用三条警告替代。因此即使移除后处理guard，该轮也不能通过。

另一个结论是访问计数时基拒绝：MySQL Uptime增161秒，Windows前后读取窗口中点差164.1015秒，差3.1015秒超过同版helper既定2.261秒不确定度。`counterResets=true`是包含时基不一致的复合标志，并非实际重启证明；当前正向delta、restartCount0也不能解释宿主／VM时钟或调度原因。Redis uptime同窗没有采到，应继续记缺失。

helper因此没有写正常 `result.result`，原件缺失保留；native summary与failure-final、只读audit另行给出原生失败。该后处理拒绝不能概括成“只有脚本失败”，也不构成放宽容差或追跑的理由。访问计数仍不作为有效每HTTP SQL量或缓存收益，重叠Com_select／Execute不相加。

audit按实际516历史Git blob核运行工具hash，另列当前补丁字节是否相同；当前policy变化不倒写旧工具来源。它有意针对该已闭批工件，算术／字节审计退出0不等于capacityProtocolPassed；缺失result仍按缺失，原生分位仅核native汇总，不称离线重建。

除已修P3外，本方向未发现改变本批分类的确定性可操作工具缺陷。既有失败、未知根因和闭批停止继续成立；本文不重开CAP-09、增加功能、补成功轮或安排新压测。
