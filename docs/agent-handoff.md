# 给实现 agent 的 Go 迁移交接

更新：2026-11-04。接续先读根AGENTS、[协作与授权记录](collaboration-agreements.md)、[当前STATUS](../STATUS.md)和[总方案](implementation-plan.md)。状态仅由根STATUS维护；本页保留执行顺序与交付规则，不另列一套历史状态表。

## 当前交接：先整理，再继续稳定3000

用户要求取得可写简历的稳定至少3000成功变更/秒。最新工作是R1—R4仓库归档与导航整理，完成后继续原点赞计划；不扩业务，不将失败阶段清理写成目标完成。用户报告ECS已关，本轮不连接或自动启动云机，准备完成后通知用户恢复同一实例。

当前业务源码c464c1fa4d153806fb44ac746312552655ebda63、正确容量API、observer和精确CI绑定见[STATUS](../STATUS.md#已交代码工具与最终产物)。旧计数矩阵FAIL与独立接续PASS分开，组件SQL6→4不代表容量；新代码尚未云端部署。八助手v2只证明离线接线，真实boot、清理回执SHA、当前4GiB缓存完成证明、新1996 Token、新包和新idle均不能借旧记录替代。

原三候选均失败，正式两轮未跑；下一候选建议配置3010但按完整窗口实际成功数计量，原时序、余量、下游和恢复标准保持。8704 VUM为六报告已知消耗，约51296只作计划，实际剩余开机后核。具体操作顺序只用[接续清单](evidence/2026-11-01-cap10/offline-resume-checklist.md)及本机已审私有RUNBOOK；不将私有配置、Token或云资源ID复制进公开仓库。

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
