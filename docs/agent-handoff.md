# 给实现 agent 的 Go 迁移交接

更新：2026-11-04。接续先读根AGENTS、[协作与授权记录](collaboration-agreements.md)、[当前STATUS](../STATUS.md)和[总方案](implementation-plan.md)。状态仅由根STATUS维护；本页保留执行顺序与交付规则，不另列一套历史状态表。

## 当前交接：S23/S24受限，consumer64候选运行中

用户要求取得可写简历的稳定至少3000成功变更/秒。R1—R4仓库整理已完成并同步远端，原ECS及四依赖已恢复健康，最终容量API、新1996夹具及PTS三文件已实际核对通过4引擎/2000VU。不扩业务，不将候选观察值或失败阶段收尾写成目标完成。以根STATUS最新段为准，不能再沿用旧“关机不连接”快照阻止已授权接续。

S23配置3010×60秒，180299次实际成功变更，完整分母为3004.983333次/秒；HTTP/DB计数一致，timing/unknown/drop均0，事实终审通过、恢复1.455063秒。P95/P99为266/355ms，`apiBudget=false`、`candidateMargin=false`，全窗与末15秒下游均失败，最终`performance_limited`。H2/H3未运行、新正式freeze未建立、负载owner已闭合；没有正式稳定3000成绩。全窗search lag为2027>1653，末15秒三组收据增长922/950/1237、Kafka lag为631/708/1042，均超各自预算301.56，不能用全成功覆盖这些失败。

S24仅将pool64/64→128/128、consumer32保持，新进程及新13样本空载通过（CPU约2.6%、内存约13.3%）。候选完整60秒实际约3004.98次成功变更/秒，P95/P99为198/320ms、API预算通过，但候选160ms余量未过，全窗及末15秒下游失败，仍为`performance_limited`；timing/unknown/drop均0、事实终审通过、恢复4.183483秒。全窗收据增长5476/4804/5722、Kafka lag5317/4677/5499均超预算1655.36；末15秒收据增长2952/2728/3240、Kafka lag2372/2212/2596均超预算301.38。未运行正式，也不能调用仅供完整候选PASS的freeze/formal脚本。

当前业务源码c464c1fa4d153806fb44ac746312552655ebda63、容量API、observer和精确CI绑定见[STATUS](../STATUS.md#已交代码工具与最终产物)，S23/S24实际使用该组合。旧计数矩阵FAIL与独立接续PASS分开，组件SQL6→4不代表容量。原S20的268/358ms、timing134与新S23的266/355ms、timing0分别保留，[S20/S23脱敏原摘要](evidence/2026-11-01-cap10/cloud-like-S20-S23-summary.json)不重采样或改grade；源码/夹具/包/boot变化不能拼成纯业务优化倍率。S24详细公开报告后续归档。

准备失败同样保留：BOOT首FAIL瞬时原因未精确取证，之后只读复核和显式最终化通过，不重复resize/CREATE；authority的H1/S22前后报告同名`FileExists`已定位，原H1退休证明经实际public grade通过，以同助手显式新attempt完成准备，不重复assembler/API/idle。八助手字节未变，不改写首FAIL或旧H1失败。

pool128另用独立三源副本：authority两处报告路径加入原command标签避免重名，其余两源沿用原字节，原八源不动。控制台紧凑结果与原pretty文件SHA不同时，原driver已在写work/inputs前拒绝；后用实际文件SHA经原CLI接续，不覆盖失败或改driver。

下一小票为[CAP10-CONSUMER64](../STATUS.md#配置试验与下一小票)：保持pool128，仅consumer提交批32→64；relay8×32、GOMAX8、源码/产物、原资源及双1保持。新API已实际启动、新13样本空载通过（CPU约2.4%、内存约14.5%），S25的authority已通过，候选运行中、尚无结果。原函数须显式传pool128/consumer64 policy，新deployment/进程身份不复用旧配置；原50ms、零unknown/drop、候选160/400ms、全下游与事实、native结束后60秒恢复等门槛全部通过，才同freeze两轮各180秒正式，正式200/500ms不变，第一轮失败不跑第二轮。空载和配置变化不提前证明收益。

S20—S22已知8704 VUM，加S23实际1122+2895=4017，累计12721；47279仅为未扣S24的计划余量，S24实际待提取，不能称当前余额。具体操作复用[接续清单](evidence/2026-11-01-cap10/offline-resume-checklist.md)及本机已审私有RUNBOOK，旧待办以根STATUS最新段覆盖；不将私有配置、Token或云资源ID复制进公开仓库。

旧H1/512、未压4096、前1996已正常logout并删除凭据，主库/卷保留；旧环境/上传的登记JSON保留作审计，核其绑定目标缺失，不要求登记数0。不能复活旧Token。原完整历史、旧指令与核验范围见[交接快照](../archive/docs/agent-handoff-history-20261104.md)、[状态快照](../archive/docs/status-history-20261104.md)和[功能交付快照](../archive/docs/go-delivery-history-20261104.md)，均非当前执行授权。

## 单任务格式与完成证据

G0—G7 是迁移里程碑，不是一阶段一提交。任务范围见方案第6节和本轮第9节的GO-*，在根STATUS展开当前票即可，不另建 tracker/tickets 或第二份总清单。

~~~text
任务ID / 名称：
依赖：真实阻塞的任务ID；无则写无
交付行为：完成后能演示或验证的一个结果
范围：必要的数据/API/页面/worker；不混入独立扩展
验收：对应现有验收ID、关键成功和失败行为
验证边界：公开行为及持久DB/Redis不变式
验证结果：实际命令、PASS/FAIL/未执行、原始证据路径
版本：开始SHA、完整交付SHA；有新改动附清单/diff
状态：待做 / 进行中 / 自测通过待核验 / 已验收 / 受阻并写原因
~~~

例如 GO-010 交付真实 Vue 登录/刷新/退出与失效拦截；GO-030 交付准入到 Stream 成单，不把支付/退款/重建一起隐藏在“交易完成”中。任务如仍过大，可追加 GO-030a 等子任务，并明确依赖和可观察结果。

## 自验与独立核验

库存、状态竞争、重复/乱序、权限和 AI 生命周期缺陷先复现失败窗口再修。测试预期来自规格或独立算例，不能复制内部实现判断。优先通过 HTTP/页面/消息边界验证，必要 SQL/Redis 对账继续保留；HTTP200、无异常、Stub有文字不足以证明完成。

Go 执行适用的 gofmt/go vet/go test；有环境的 Linux CI 跑 race。真实 MySQL/Redis/Kafka 用于约束、Lua、ACK、事务与恢复；mock 不能代替这些证据。真实模型评测与 Stub 的取消/失败/权限协议分别报告。与当前修改无关且已有有效证据的重型实验不重复执行；文档/格式改动不新增形式测试。

独立核验分两方面：需求符合度检查漏做/做错/扩大范围/虚报；正确性审查检查事务、并发、权限、资源、迁移和可维护性。复杂批次可由两个只读审查者分别执行。核验者记录基线/HEAD/工作区及实际执行边界，新版本变化后补差量核验，不能拿旧PASS覆盖新代码。

审查至少覆盖本批已提交、暂存、未暂存和相关未跟踪文件：

~~~sh
git rev-parse HEAD
git status --short
git diff <明确基线> HEAD
git diff --cached
git diff
git ls-files --others --exclude-standard
~~~

本项目沿用 docs/ 规范，尚未初始化 OpenSpec；没有安装 Matt Pocock 整套 skills。这里借鉴其 [任务拆分](https://github.com/mattpocock/skills/blob/main/skills/engineering/to-tickets/SKILL.md)、[行为测试](https://github.com/mattpocock/skills/blob/main/skills/engineering/tdd/SKILL.md)、[双维审查](https://github.com/mattpocock/skills/blob/main/skills/engineering/code-review/SKILL.md)，无需实现 agent 再安装才开工。未来使用 OpenSpec 时显式传入 change 路径，不维护重复任务状态。

## 每个小任务自测后独立 commit（用户已授权）

- 一个提交包含该任务必要代码、测试、迁移和说明；无关格式化、其他任务、核验者报告独立提交。
- 暂存明确文件/hunk，提交前检查 status 和 cached diff，不用 git add . / git add -A 收走其他 agent 修改。
- 消息带 ID，例如 `feat(GO-010): implement login and session expiry`。交付记录写父SHA、完整交付SHA、实际命令与证据。
- 自测后状态是“待核验”；核验失败追加修复提交，不为整理历史 amend/rebase 他人的成果。
- 回退优先可追踪 revert，并核对后续依赖；Git回退不会撤销数据库迁移，单独说明数据兼容/恢复，不删卷替代回退。
- 原始证据按日期/SHA另存，保留失败轮次；不覆盖旧报告，不提交Key、登录Token或完整核销凭据。
- 核验者的修复、适用验证、分步提交及向 origin/main 正常推送已获用户持续授权（2026-10-26）。推送时保留其他 agent 未提交内容，不强推；部署/发布及其他外部操作仍依会话授权。

## 文档优先级

范围/技术栈/顺序以 [总方案](implementation-plan.md) 为准（2026-10-28补充范围见第9节），状态以根 [STATUS.md](../STATUS.md) 为准，docs/README仅保留产品与历史详情。业务规则分别查交易、AI、社区设计；验收条件分别查对应 acceptance。旧文档中的 P/H 编号保留为业务场景标识，旧 Java 类名/启动命令仅作参考，不能覆盖最新 Go 决策。

交付包含环境版本、初始化/迁移/启动/停止命令、验收对应关系、脱敏原始证据、未执行项与限制。不要把短突发受理响应当持续QPS，成功/售罄/限购/429/系统错误分开，受理耗时与最终成单耗时分开。
