# CAP07 内部非作者审查

日期：2026-10-30。审查者为协作子 agent `cap06v_fix`；被审 HTTP 测试由 root 编写，容量工具由另一子 agent 编写。本记录属于实施团队内部审查，**不是用户交付后的独立外部核验**。审查者此前参与的 app/diagnostics 接线不纳入本次非作者结论。

## 实际执行与边界

- 只读检查 `scripts/cap06-env.py` 差量、`cap07-env.py`、`cap07-stage.py` 和本目录 plan/runbook；三脚本 AST 语法检查通过，锁修复后的 stage 也由 Python 实际 import 成功。
- 执行 **19 项纯 protocol 正反例**：以内存 stub 代替历史轮次的 `passed`/`capacity_failure`，检查 probe 次数和速率、A1/B1/A2/B2 顺序、阶梯顺序和首失败、仅 SLO 失败可作一次严格括区缩步、最高 100 复用、较高档两次持有上限与最后 hot 顺序。全部通过；这不是 CLI、负载或真实环境执行。
- 锁修复后独立执行 **12 项纯内存终态用例**：fake 文件及 fake wrapper handle 覆盖完整终态、监督失败但已收敛、未启动 sampler 的完整失败、wrapper 仍活跃、缺 control/done/harness/sampler 终态，以及 owner 创建身份、wrapper PID、stage tag 不匹配。全部通过；没有用真实进程替代这些 fake。
- 只读审查真实 HTTP 测试设计和生产调用点。**本审查者未执行 GO_INTEGRATION、登录请求、真实 MySQL/Redis、Docker、API、k6 或正式负载**；root 的实际执行与原始输出另行归档，不计为本审查者复跑或外部验收。

## 工具结论与修正

最初发现外层 `subprocess.run` 被中断后，`finally` 会直接删除活动锁，而内层监督可能仍在收敛。作者改为保留 Popen handle、记录 owner PID/创建时刻/exe、原子追加 wrapper 身份，并在异常或中断时保留锁。复审继续发现“wrapper 已退出”不足以证明其子任务收敛；作者再增加本轮原生 `control/exit-status.json`、`done.json`、结束时刻、harness 退出码及已启动 sampler 退出码的终态前置。外部硬杀导致终态缺失时不解锁。以上两点已复审关闭。

当前未发现阻断：CAP06 默认路径与 guard 保持；CAP07 cohort/runtime/observer 独立，固定 project/loopback 端口，替换只针对重新核实且已停止的完整 CID；新随机卷与旧卷均保留。API 用 PID、创建时刻、exe/hash、health version 核身份。stage 独占保留 slot/command，失败占额，手工单轮执行，与 plan/runbook 的有限预算一致。正常清理限定本轮 32 会话、observer、记录的 API 和两个不可变 CID，按已登记路径删除私有输入，不递归清目录或删卷。

这些结论覆盖源码守卫与纯检查；不证明实际宿主资源、进程或容器状态，也不替代逐轮读取原始结果、资源停止原因和恢复材料。

## HTTP 回归设计审查

- `TestIntegrationFeedCardsHTTPColdHotAndFacts`：自有用户通过真实认证 service 发送验证码并登录，HTTP Feed 携真实 token；以全字段事实读取作基线。冷读断言一批内容 SQL/两行，热读零新增内容 SQL；单 SQL 连接且无新增池等待可检出目录事务未释放时嵌套回源。两用户共享正文但点赞不同，另覆盖同秒游标、退关、下架和新发布版本。fixture 仅清自有 SQL/Redis 对象；此前忽略的 Redis 删除、phone 查询及资源 Close 错误已改为测试错误，复审通过。
- `TestIntegrationFeedCardsHTTPVersionRaceAndFailure`：独立 GORM 实例的 Row 前置 callback 匹配实际内容 SELECT；回调同步发生在目录 RR 结束后、内容 SQL 执行前。受控修改发布版本/公开状态可迫使旧目录 Ref 不匹配，并断言恰一次新 RR 全字段回退；SQL 故障断言 503、零回退与无 L1 空结果。该证据属于存储竞争测试，不能宣称交付了编辑/下架产品功能。
- `TestIntegrationFeedCardsHTTPSameBatchBurstAndFirstCancellation`：先确认首个真实内容 SQL 进入 barrier，再启动七个 HTTP follower；观察到七次 flight join 后才取消首 caller，检查其错误为 `context.Canceled`，随后放行。七个 follower 的成功 envelope/状态/正文/图片以及一 flight、七 join、一内容 SQL/两行与热读零新增 SQL，能够检出共享 loader 错绑首请求 context 或未合并。无 sleep 推测并发；这是受控八请求突发，不是持续 QPS。
- `TestIntegrationFeedCardsHTTPProjectionCompleteness`：只为自有两篇笔记构造匹配 author/version/published_at 的投影；真实 HTTP 首次必须为 projection source。精确删掉其中一条目录投影后，必须从 database source 返回两条授权内容且页序保持，内容 SQL 总批次仍为一。它覆盖完整/缺口目录的真实 Feed 接入和同版本公共片段复用，不冒充 Kafka 投影消费链路测试。

上述当前测试设计未发现阻断。真实运行是否通过，以及最终功能 SHA、CI/Linux race、容量效果和旧秒级长尾风险，须分别依据 root 的执行证据及后续独立核验，不由本记录推定。

## 固定 A 构建

本审查者另完成固定 A 的隔离构建，见 [build-a.json](build-a.json) 与 [build-a.txt](build-a.txt)。新建 sibling runtime 中的 detached worktree，源码固定 `f3d99ee43dfb422a0cba525de93e9266f07c4ee5`，构建前后源码 checkout 均干净；exe 在源码 checkout 外。link revision 为 `capacity-<完整 SHA>`，`go version -m` 确认 `vcs.revision` 匹配且 `vcs.modified=false`。主工作区当时有其它实施修改，未拿它构建或声称它干净；未执行产物、启动服务或负载。

## A3 组件追加非作者审查

只读审查作者冻结后的 `communitycards/cards.go`、`cards_test.go` 差量及新增 `integration_test.go`，未自行编译、执行普通或真实组件测试。未发现生产代码阻断：排序去重后的 `(ID,Version)` 缺失集合生成有分隔的批次键；共享 loader 从应用 life 派生独立两秒 context，调用者仅取消各自等待。`begin`/`startFlight` 的 WaitGroup Add 与 Close 关门在同一 mutex 内，关门后才 Wait，避免新任务越过收敛边界。完成结果先写字段再关闭 done，接收方经 channel 同步读取，最终逐请求 clone 图片切片；锁内只改内存状态，不做网络操作。flight、active load、等待 caller 分别有 64/8/1024 上限，L1 原预算与版本键保持。

发现并交作者修正一项普通测试问题：原 `TestActiveQueueAndWholeLoadDeadline` 在 caller 的第一次 Redis 预查时即发信号并取消，可能尚未建立第二个共享 flight；若后来确实进入共享重查，测试 hook 还可能重复 close channel。现 hook 用 sync.Once，取消前明确观察 `FlightStarted=2 / Inflight=2 / ActiveLoads=1`，取消后仍核共享任务存在，Close 后断言三个 admission gauge 为零且仅一次 SQL。最终冻结段已复审通过；作者执行结果另由其原始测试证据负责，本审查者未复跑。

v1 晚归测试在 fakeBackend 已捕获 v1 rows 后暂停，先并发完成 v2 再放回 v1，并检查 v2 L1、两个独立 Redis 版本命名空间及原 TTL 范围。它是受控存储响应注入，不是实际 MySQL 版本变更或编辑 API。真实组件用例则覆盖坏 JSON/字段类型/Redis 类型/过大 payload、服务端自有 key 到期重建、真实内容 SQL 错误与版本缺失不发布部分结果。Redis 降级用例明确关闭专属真实 client 后走真实 SQL，不能写成 Redis 服务停机或网络断连实测。

真实慢 pipeline 设计有效：专属 client 在自有 SET 前插入 CLIENT SETNAME 和对唯一空 key 的 BLPOP；先以另一连接的 CLIENT LIST 确认该命名连接 `cmd=blpop`，再 Close 缓存，检查 caller ErrClosed、pipeline 错误和任务 gauge 收敛。清理先关闭专属 client 并等待服务端命名连接消失，再由较早注册的 fixture 清理精确 key 并 EXISTS=0，避免阻塞连接晚执行排队 SET 污染清理后的键。本记录只认可证据设计，实际结果由 root 的真实执行记录确认。

## 只读 audit 工具补审

另只读审查 `scripts/cap07-audit.py`，并独立执行 **10 项纯内存 native_score 合成用例**，覆盖阈值边界、分类计数不一致、发起不足、内容错误、drop、P99 超限、缺成功分位、错误率恰 1%、外部 stop 与非零 stable exit；全部通过。未执行其正式证据 CLI，也未生成 audit 结果冒充尚未执行的阶段。

当前未发现阻断：capacityPassed 同时要求原生 SLO、stage cards 状态和 control 监督成功，wrapper 退出零不能单独通过；A 无组件仍为 null，不能转为零内容 SQL；counter 差与 gauge、两项重叠实例 SQL 分别处理，分位仅保留每轮原生值，不合并或离线重建分布。脚本还核 API/exe/tool/fixture 哈希、新 CAP07 环境来源、slot 与有限协议。它只能验证已有公开记录的一致性；CLI 算术检查成功也不等于最终 B 协议通过，必须读取显式 `finalBProtocolPassed` 和各阶段结果。

## 固定 B 构建与停止编译

根 agent 指定最终 B 后，以与 A 相同的 Go `go1.27.0 windows/amd64` 和构建参数，在新 sibling runtime 的 detached checkout 构建 `30b6571227b5508d0f04bc8c4cfc1bcfbc313275`；见 [build-b.json](build-b.json) 与 [build-b.txt](build-b.txt)。构建前核目录和证据不存在，不覆盖已有产物；checkout 前后 clean，link revision 为 `capacity-<完整 SHA>`，VCS revision 匹配且 modified=false，exe SHA256 已保存。完成后停止本审查者所有编译/测试，只轻量补写本文；未操作 API、Docker 或负载，正式阶段由 root 统一执行。
