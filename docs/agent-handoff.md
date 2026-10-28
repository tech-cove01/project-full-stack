# 给实现 agent 的 Go 迁移交接

2026-10-26 用户已明确：全面改用 Go，后续不再开发 Java。实现 agent 负责应用、迁移、脚本和自验；本聊天负责方案与独立核验。当前状态只在 [STATUS.md](../STATUS.md) 维护，[README.md](README.md)保留产品与交付详情，总路线在 [implementation-plan.md](implementation-plan.md) 维护。

## 可直接交付的执行指令

~~~text
请将 project-full-stack 的后端全面迁移至 Go，并补齐已确定的社区亮点。
先读根 AGENTS.md、STATUS.md、docs/implementation-plan.md，
再读交易/AI/社区设计及对应验收，检查 HEAD、暂存/工作区和未跟踪文件。
Java 参考点是 java-baseline-20261026 → 9f8c2b10839ef74556de0f55d19afc3ac215f9da，
它不是全部独立验收通过的声明。

目标栈：Go + Gin + GORM + go-redis/v9 + MySQL8.4 + Redis7.4；
订单保留 Redis Stream 和交易 outbox，社区用独立 outbox → Kafka。
前端继续 Vue3/TS，复用现有 SQL、Lua、API、页面及 HTTP/Python/k6 场景。
不迁移 PostgreSQL，不加 RabbitMQ，不逐类翻译旧 Java 工程。
不以 AutoMigrate 替代版本 SQL；生成列只读；64位ID和金额按契约字符串输出。
Go工具链、依赖和Kafka镜像在首次相应实现时锁定并记录。

先读STATUS继续未完成任务，不重做已核验的GO-001—071；G0—G7仅作迁移历史参考。
继续GO-072 follow/feed消费投影，GO-075真实Provider可在无冲突时并行；
新增GO-076公开笔记ES全文搜索，GO-074随功能补必要页面证据，
最终由GO-073完成持续负载/故障恢复/对账，并按总方案第9节独立核验。
一票交付一段可验收行为，不一次生成全部后端才联调。
每个小任务适用自测通过后独立本地commit，带GO任务ID，
交完整SHA、命令、证据与限制供核验；不积攒一整个阶段。
正常本地提交已获授权，不逐次询问。2026-10-26 用户另已持续授权核验者：
核验发现问题时一并修复、验证、分步提交并正常push至origin/main，无需再次确认；
不包含强推、覆盖他人修改或部署/发布，实现者自测仍只代表待独立核验。

Go 服务在 `backend/` 开发；Java 基线源码已归档到 `archive/backend-java-20261026/`，最终只保留 `backend/` 的 Go 服务。
Java源码由Git标签保留，不继续补Java功能，不长期维护两套运行时。
开发选独立MySQL/Redis测试实例、独立端口；不能两端同时消费同一Stream/group/outbox。
正式切换前停止旧worker并核对pending/outbox/review，前端只指向Go。
不要删现有数据库卷、未知文件或改无关容器，旧虚拟机不作为依赖。

保留取消后可重买、退款后不可重买，OWNER/MANAGER/STAFF逐门店范围，
unknown DB结果不ACK，持久review与补偿可重放；重建RC快照/锁定行与门控版本不缩水。
人工重点审查事务tx、Lua、ACK、权限、JSON大ID、goroutine/context及AI所有终态。
AI仍为公开规则RAG+本人实时只读工具+B端FAQ；不扩Agent或新增向量基础设施。
所有终态与缓存重放复核身份/知识/会话/锁/deadline；Stub与真实模型质量分开。

缓存保留已实现的批量装配、计数缓存、有界L1、singleflight与
(publishedAt,id)复合游标并补量化；共享片段不包含本人liked。
页ID/卡片缓存、动态hotkey、位图/压缩计数均不作为本轮结束前置。
社区MySQL关系是事实源，至少一次事件按版本应用并可恢复。
2026-10-28用户已确认补GO-076公开笔记ES全文搜索，按总方案第9节执行；
新增独立Kafka消费组，不改写正在进行的GO-072 follow/feed范围。
Canal/ELK/压缩计数/分钟热榜不作为本轮前置，不照搬截图QPS或内存数字。
完成状态和README必须与Go实际行为一致，旧Java指标不得改名成Go。
~~~

## 当前四条主线与完成边界（2026-10-28）

以根 [STATUS.md](../STATUS.md) 的最新任务状态为入口，按 [总方案第9节](implementation-plan.md#9-简历版本范围与结束条件2026-10-28) 的范围与FIN-01—06验收：高并发交易、热点查询优化、可靠事件与公开笔记ES搜索、真实业务AI。继续GO-072，独立增加GO-076；GO-075补真实Provider和小集评测；GO-074补必要页面，GO-073在最终功能提交上量化。完成并独立核验后冻结功能，进入面试准备，不把早期动态hotkey/页卡片缓存/删除Java归档重新加为结束前置。

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
