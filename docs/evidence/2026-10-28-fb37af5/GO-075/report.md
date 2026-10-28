# GO-075 真实 Provider 与小集评测

起点：`fb37af549d5e8d0d3e5e3ed66a542fb7e99601eb`。状态：自测通过待独立核验。依赖：已核验 GO-040/041/042 的知识、权限与 stub 协议。此票不改变 GO-072 待独立核验状态。

## 范围和拆分

- GO-075a：net/http 兼容 Chat/Embedding 适配；有界输入输出、显式维度、取消/超时、无隐式付费重试、脱敏错误与真实 usage 观察。
- GO-075b：显式配置接入实际 API/知识服务；默认关闭、缺失/错误配置只使 AI 不可用，交易仍可运行；能力状态正确区分 stub、真实配置和远端健康未知。
- GO-075c：固定 12 有据、6 无据、6 工具、6 多轮小集；真实知识/向量与工具数据、来源判定、Recall@K、版本失效与重启复用；逐题结果保留。
- GO-075d：真实请求取消/期限与失败后恢复的差量验证。确定性 HTTP 故障与真实模型质量分别报告。

每个小功能完成必要验证和独立审查后单独提交，不把整票积攒成一笔。新发现独立问题追加修复提交。

## 真实调用前预算与隔离

2026-10-28用户明确授权沿用 CRM 的本机模型配置。CRM `.env.local` 提供加密配置，Key 经本机数据库只读取得并仅保留在父/子进程内存；不改 CRM 数据、配置或代码，不记录 Key、登录令牌或完整个人数据。独立 MySQL `3331` / Redis `6403`，Compose project `project-full-stack-go075`，不启动 Kafka，不复用开发库消费组。

候选真实配置：百炼北京兼容接口，Chat `qwen3.8-flash`（关闭思考，完整缓冲），Embedding `text-embedding-v4`，1536维；topK=5、minScore=0.5、max_output_tokens=400，单次远端最多40秒、整集最多10分钟。使用合成公开知识和本人测试订单。首次运行最多90次实际远端调用、观察到的token合计不超过150000；一旦上限或usage未知导致预算无法核对，停止继续生成。另列生命周期差量调用，执行前记录新增上限，不自动重试付费失败请求。

实际费用按响应usage和执行时官方模型/区域单价估算；缓存折扣/免费额度/取消后结算无法核实时写未知，不能写零费用。API输出400tokens、总调用数和合成输入均有界；有限质量集不测提供商吞吐，不做付费并发压测。

## 验证与限制

| 范围 | 结果与证据 |
| --- | --- |
| 真实HTTP适配与本地故障 | 10适配器主组通过，错误/重试/取消/边界与指纹，保留两轮夹具失败；[适配器](provider-report.md) |
| 配置/应用 | 默认关闭、缺Key/错误配置只禁AI，实际模型名与远端健康未知；[接入](wiring-report.md) |
| 索引身份与完整性 | 首次真实复现静默空集FAIL，最终8子项PASS，包括匿名HTTP慢embedding期间停用和多分片缺失；[索引](index-report.md) |
| JSON null向量 | 新增两条先FAIL后PASS；合法数值0不受影响；[独立修复](vector-report.md) |
| 真实质量固定30题 | 70次远端、7258tokens；有据12/12、无据6/6、工具6/6、多轮6/6，来源/版本人工对照；[质量](eval-report.md)、[独立逐题复核](eval-review.md) |
| 真实网络生命周期 | 9次尝试、415已知tokens、2次usage未知，5场景通过；[终态](lifecycle-report.md)、[独立复核](lifecycle-review.md) |
| 核心交易隔离 | AI关闭/缺Key/不可达，各实际交易9项；[隔离](isolation-report.md)。发现演示残留12session后单独修复，最终归零；[QA1](session-cleanup-report.md) |
| 最终普通Go | gofmt无输出、vet/全包build/`go test -count=1 ./...`通过；[普通全套](go-test-final.txt)、[验证元数据](verification-final.json) |
| 真实知识/会话回归 | 专用MySQL/Redis，包间串行、付费开关关闭；[原始回归](knowledge-chat-real.txt) |
| 最终启动/关闭 | 6场景全部通过，正常CtrlBreak退出0、无强制清理且端口可重用；[启动证据](bootstrap-final.json) |

所有小功能提交前经过根agent和非作者agent对应代码审查，发现的问题在提交前闭合或作为独立追加修复；不代表整票已由用户的核验流程验收。无新增依赖/SQL迁移/前端源码，未为此票重复无关前端构建。现有开发库仍须应用此前009—011后再启当前API。

## 小提交（完整SHA）

| 独立行为/问题 | SHA |
| --- | --- |
| GO-075a 有界兼容Chat/Embedding | `129526ddc763f14c9c14bf33bdaed8be55fd077a` |
| GO-075b 配置和实际应用接入 | `49abf3af8ba1f92413c0a31f15cc2737f9cd5bb0` |
| 索引兼容/完整性与撤权守护 | `77aa81041e33175e92abfadcde6d82dd240943ba` |
| JSON null组件独立修复 | `33bdb4d91d7bb83cc73af45cc36849a6bfc2b448` |
| GO-075c 真实30题与逐题复核 | `09663f059363f6e858d514014792f8734963f907` |
| QA1 演示登录session清理 | `31fb563214739dbc7f010f6e773bad16b1aab7d8` |
| GO-075d 实际HTTP终态 | `62c3f36679e7122f897969f44a9439efc11f4247` |
| GO-075e 核心交易隔离和证据保护 | `8286a707a9808598f95130e34cc2a1bce2037461` |

文档/最终验证收尾另提交，不amend/rebase既有历史。实现提交仅在本地，未推送/部署。最终Git HEAD以`git rev-parse HEAD`和交接消息为准，避免报告自引用SHA。

## 复跑与收尾

普通：在backend执行`go mod verify`、`gofmt -l .`、`go vet ./...`、`go test -count=1 ./...`、`go build ./...`。真实存储：显式专用MYSQL/REDIS和`GO_INTEGRATION=1`后`go test -count=1 -p 1 -run '^TestIntegration' -v ./internal/aiknowledge ./internal/aichat`，不设置`GO_REAL_AI`时付费测试必须SKIP。真实Provider命令和事前预算分别见评测/生命周期计划；普通CI无Key/双开关不付费。

本机隔离环境：根目录设置`GO_MYSQL_PORT=3331`、`GO_REDIS_PORT=6403`，`docker compose -f compose.go.yaml -p project-full-stack-go075 up -d --wait mysql redis`；数据库沿用`project_full_stack_go`，按`backend/.env.example`配置主机/用户，应用迁移`node scripts/migrate.mjs`。本票首次应用003—011全部成功，[迁移](migrate-initial.txt)；未改已应用SQL。结束执行同project的`stop --timeout 60 mysql redis`，卷保留，两容器退出0且非OOM。原来Docker未运行，本票启动Docker Desktop后其配置的CRM容器自动恢复，未改或停止这些服务。

SQL自有测试用户/商家、知识/向量、订单/outbox/review均0：[数据库清理](cleanup-db.txt)。`login:token:*`/`ai:*`键0：[身份清理](cleanup-redis.txt)。专用交易Stream/派生键作为正常演练运行痕迹保留，未flush或操作其它实例。[容器/二进制/用量收尾](cleanup-final.json)。最终二进制revision `go075-33bdb4d`、SHA256 `e1176c6495aee38cc413273cb802cbdadb79bb310f69a7717d040d05a5dbcebc`；随后提交只增加测试/演示清理与文档，不伪称为整票Git revision。

合计79次实际远端尝试，7673已知tokens，另有取消/超时2次未知usage。已知部分按官方北京原价估算0.0071748元，未核账单/套餐/缓存折扣；总扣费未知。30题与网络生命周期分别报告，未知调用不得填0费用。

未执行：本票远端CI/Linux race、真实供应商429/金额预算、浏览器真实AI页面、持续容量和并发AI失败隔离。旧CI绿色不替代新代码；20ms测试期限不冒充45秒生产期限；此票没有ES搜索或最终负载结论。下一开发票GO-076公开笔记全文搜索，GO-074补差量页面、GO-073在最终功能固定后量化；全项目尚未全部完成。
