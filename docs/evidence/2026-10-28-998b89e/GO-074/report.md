# GO-074 压测前页面收尾

2026-10-28，压测前既定功能及必要页面差量已完成，实现者**自测通过，待独立核验**。起点 `998b89e81bed42786d25d0cebbadb401fd506f70`，功能/专项提交截至 `2811dc6f89fc7405a4f72c7f4bb102fa76d8bfed`；本文和最终环境证据另做文档提交。依赖GO-020/032角色交易、GO-040/041/042及075真实Provider，验收FIN-05及FIN-06运行/交接子集。**GO-073持续压测没有启动**，最终HEAD远端CI/Linux race没有执行，本票不提升为独立验收通过。

## 已交付行为与独立小提交

| 独立问题/行为 | 完整SHA |
| --- | --- |
| a1 工作台开放已实现订单/核销及公开规则入口 | `0365e5dd46f725796cd1cbb4e8efd432027f4348` |
| QA0 首次MySQL健康改为实际TCP用户/schema探测 | `6f267715a1d4b3dcb8e2f6bf99d35d610f1e6396` |
| b-QA1 AI来源href对齐实际Vue详情路径 | `6575610e0c9c43b349f65abb42c5bc539b7ef2c7` |
| b-QA2 公开知识scope DTO及PUBLIC/SHOP_PUBLIC标签 | `fa47463b34705b88d65e7bf3ae02ec9e82ef7b7e` |
| a2 真实B端角色/逐店核销退款/撤权页面矩阵 | `882c4473dc9b18b8d0dca490a1ea7d6d37d6b11e` |
| b 真实Provider页面、来源、取消恢复与用量 | `05f0baa52f14566566339786b10ef8d4193cb4ed` |
| c2 门店工具卡片按真实地址字段显示并进入详情 | `13d5f49c787740f9bf2b83b9c843fb4ce3d6e1c1` |
| c1 活动工具补当前SQL库存及所属门店入口 | `b97b03adaa845136773da5e7cba16c8ab2677520` |
| c3 工具页面同会话刷新真实交易后的库存与详情导航 | `2811dc6f89fc7405a4f72c7f4bb102fa76d8bfed` |

没有新增SQL迁移、工具种类、SDK或业务写权限。每个独立缺陷分别修复提交，专项浏览器脚本/证据另外提交；未amend/rebase历史。途中独立核验方的GO-076文档提交 `5620d73f40c0114f8ee58c903df32f259f2cab41`、`37f645bfd9a3a3f5af0899175fe6fcf81117a90f`完整保留，不算本票作者工作。GO-076现已按 `998b89e` 范围独立核验，见 [其报告](../../../reviews/2026-10-28/go076-998b89e/report.md)，不将该结论延伸至本票新代码。

## 实际验证

| 范围 | 结果与原始证据 |
| --- | --- |
| 关键B端页面 | **34项断言通过**，6身份/4门店、10个实际支付订单；OWNER/MANAGER可核销退款，STAFF仅本店核销，混合角色不外溢；6次越权写403及2次越权读200空页，均核对订单/库存/审计/outbox无非法副作用；旧Token撤scope/停成员后刷新缩小范围。真实补偿、审计、空态和网络abort→真实reload恢复。[报告](merchant/module-report.md)、[最终34项](merchant/round-3.json) |
| 真实AI页面 | **35项断言通过，7轮对话**：空态、真实回答与两类来源实际点击、本人订单/他人拒绝、无据拒答、实际停止与恢复、停用后新轮无旧知识。逐轮人工读答案/来源/工具并查看截图；不是正则或模型自评。[报告](ai/report.md)、[原始](ai/round-2/result.json) |
| 活动/门店工具页面 | **23项断言通过，5轮真实GoAITool**。同会话以新消息先查库存3，经正常HTTP准入/Stream持久成单后SQL/Redis库存均为2，再查询卡片为字符串2；价格¥19.00，活动/门店大ID链接实际进入详情，未知ID无卡片。环境为stub但这些分支直接执行真实只读工具，没有检索/生成阶段，不用此结果冒称模型质量。[报告](tools/report.md)、[原始](tools/round-1.json) |
| 缺陷真实回归 | 来源href [先FAIL](ai/href-initial.txt)/[PASS](ai/href-final.txt)；scope [先FAIL](ai/scope-initial.txt)/[7子组PASS](ai/scope-final.txt)；活动卡片 [先FAIL](ai/c1-activity-initial.txt)/[4子组PASS](ai/c1-activity-final.txt)，含同Service库存1→0、字符串大ID/金额及公开/ACTIVE过滤 |
| 普通Go检查 | Windows Go1.27.0：gofmt、vet、`go test -count=1 ./...`、build、mod verify全部PASS。[原核心结果](go-checks.json)/[输出](go-final.txt)；新增c1之后再完成 [最终检查](go-tools-checks.json)/[全套输出](go-tools-final.txt)，新改动是该次重跑的原因 |
| 前端 | nav、scope、shops的必要build（含类型检查）与format均PASS，见 [导航](merchant/nav-build.txt)、[scope](ai/scope-checks.json)、[最后shops构建](tools/shops-build.txt)/[格式](tools/shops-format.txt)；脚本语法/格式检查按模块记录 |
| 启动/关闭 | 本轮实际Go二进制 [6场景全部PASS](bootstrap-final.json)，健康场景exit0，预期失败场景各按退出码通过，无强制清理/密码日志且端口可复用。c1只改工具查询/DTO，未再重复与之无关的六类配置故障；新工具二进制实际启动/健康/关闭记录另归档 |
| 初始化/迁移 | 两个新项目首次载入001/002并完成003—012；[AI首次迁移](ai-migration-initial.txt)、[merchant迁移](merchant-migration-second.txt)。最终 [merchant复跑](merchant/migration-final.txt)、[AI复跑](ai/migration-final.txt)、[工具阶段复跑](tools/migration-final.txt)全部checksum一致并skip；应用不自动迁移 |

条目中的断言数、对话轮数、真实依赖子组分开统计，不相加成更多业务场景。本轮桌面Chrome/上海时区；已有交易UTC/上海各26项、社区/FAQ/stubAI各10项，以及GO-076两时区搜索各25项/公开边界4断言按原适用范围复用，没有重跑无关页面或30题质量集。

## 真实调用与采集边界

只复用用户既有授权的CRM本机凭据，在root子进程内存中加载；没有复制CRM私密内容、写入Key或修改CRM配置/数据。只发送本票合成公开FAQ。Chat `qwen3.8-flash`，Embedding `text-embedding-v4`1536维；通过固定百炼HTTPS的loopback观察器记录安全usage元数据，真实端点/取消及12次记录见 [完整账本](ai/provider-observer.json)。该测试中向量空间使用新观察器endpoint指纹，未复用另一endpoint的旧向量。

首轮采集失败消耗3次/286已知tokens，修正仪器后从明确baseline3继续，第二轮新增9次，**合计12次转发尝试、1055已知tokens，取消Chat id10的usage未知**。其中7次Embedding共130 input；5次Chat尝试，4次完成为826 input+99 output，另1次取消未知。账本没有清零或重置；1055不是结算总量，未核本轮单价/账单，金额和取消结算未知，不能按零费用。逻辑预算退款4→4只表示应用计数，不是供应商退款。

SSE观察器只旁路记录Vue本来消费的字节，保留原fetch/read Promise、Response、Reader、读取结果与cancel/error，不clone/tee/多read，不伪造成功JSON；小回调有开销，不称零干扰抓包。本地三例非付费probe与原对象引用/原生Abort断连证明见 [reader probe](ai/sse-reader-probe-final.txt)。真实取消时只观察到上游HTTP转发已发起，不能证明供应商内部已经生成token。取消后无answer/done、assistant历史4→4、lease/dedupe释放，随后新消息正常恢复且只新增一条assistant。

旧已渲染历史卡片仍可留在DOM，本票只证明停用后的新轮/详情当前权限失效，不承诺追溯删除历史显示；会话续聊越权和同ID重放沿GO-075原独立证据，本次不扩其协议矩阵。c1/c2关闭了真实AI报告当时登记的活动/门店卡片缺口，使用工具专项差量；没有因只读工具分支变化再付费重跑不受影响的35项。

## 失败、审查与版本对应

- 初选3350/6420与正在启动的GO-076核验端口冲突，bind预检直接拒绝，尚未创建容器/迁移；改用3380/6450及3381/6451，[记录](initial-port-preflight.json)。没有停止核验服务。
- 新merchant迁移首次helper报错未保存原始node诊断，稍后成功；另在全新AI实例真实捕获socket ping先成功、TCP/schema尚失败，独立修复健康门槛。[因果限制、26次观察及最终健康](mysql-health-report.md)。health-final数组顺序为merchant、AI，不冒称初次诊断已完整保留。
- 初次launcher在二进制编译结束前启动，FileNotFound且无子进程，保留 [初次记录](merchant/stack-start-initial.json)；之后正常启动完成页面，不把它当产品启动失败。
- merchant前两轮定位器误用ElSelect隐藏input，真实业务未提交核销；失败JSON/遮罩图保留，两轮各自fixture清理成功。最终用有依据的可见select wrapper完成真实操作，未用假成功响应。
- AI首轮真实页面已答对，但事后读response.text失败；替代CDP快速流probe也失败，全部原始证据保留。最终实际reader旁路方案本地验证后才继续余9次，未盲目付费重试；独立probe遇沙箱loopback限制后以本地权限重跑，非产品失败。
- 最终静态检查首轮把自身尚未生成的输出链接误报缺失，[原始](evidence-audit-initial.json)保留；仅对该次检查自身即将写出的产物处理自指，其它文件链接照常核对，随后最终检查通过。

每项代码由非作者审读：root审页面/Go修复，ai_eval复核健康、c1、两套浏览器脚本/清理及真实原始问答；provider_wiring第二眼复核采集/预算仪器。最终无遗留阻断发现。非作者代码/证据审读仍与用户安排的独立完整核验区分。

实际商家/真实AI二进制版本为 `go074-qa2-worktree`，其 [buildinfo](binary-buildinfo.txt) 是6575610+vcs.modified=true，包含QA1/QA2工作树行为，不能称fa474或最终HEAD干净构建。工具差量运行 `go074-b97b03a`，其 [buildinfo](tools/binary-buildinfo.txt)为b97b03a+modified=true；最终应用源码对应b97b03a，后续仅脚本/文档。二进制SHA256及当前源码摘要见 [source-manifest.json](source-manifest.json)，未改写原始元数据。最后HEAD远端CI/Linux race未运行；GO-076精确998b89e绿色不自动覆盖本票。

## 最终资源与交接

本票两个Compose项目：merchant `project-full-stack-go074-merchant`（3380/6450/API8120/Vue5220），AI `project-full-stack-go074-ai`（3381/6451/API8121/Vue5221）；Kafka/ES关闭，搜索复用独立证据。真实模型完成后停API/Vue/观察器，追加工具差量仅重启同一AI项目的保留卷、使用stub且Key空，观察器9291没有再启动，12次账本保持不变。

- [merchant最终对账](merchant/environment-final.json)、[真实AI阶段收尾](ai/environment-final.json)、[最后工具阶段收尾](tools/environment-final.json)：自有用户/商家/门店/FAQ、订单、未投递outbox、未解决review均0；登录/验证码/AI/活动/预约/补偿键0，交易PEL0。只在持久结果及ACK门槛通过后清自己的行/消息/键，没有XACK未知结果、flush、重置自增、删共享group/日序号或卷。
- 各阶段API退出0，Vite在Windows Terminate后exit1，均forced=false且端口可复用；观察器退出0。见 [merchant进程](merchant/stack-final.json)、[AI进程](ai/stack-final.json)、[工具进程](tools/stack-final.json)，不声称所有进程exit0。
- 最终四个MySQL/Redis容器均停止、exit0、无OOM，四个命名卷保留，所属端口及9291无TCP监听。只停止明确项目/服务并核对标签；正确的无过滤总集合减所属集合的前后快照相等，其它14个容器仍在运行。没有操作CRM或GO-076容器。

[证据静态检查](evidence-audit.json)解析JSON、检查本地报告链接并扫描凭据/手机号模式，配合模块截图人工审读；没有归档带鉴权trace/HAR，网络仅保留方法/去敏路径/状态/鉴权存在布尔及必要合成数据。有限静态扫描不等于证明不存在所有可能秘密。

复跑入口和环境要求见 [开发说明](../../../development.md#go-074-页面差量复跑)及三份模块报告，输出必须使用新的文件/目录；真实调用需授权配置并保留累计账本，不复用已耗尽的观察器。该票后仅剩GO-074独立核验/最终相关CI，以及用户后续授权的GO-073持续负载、延迟、恢复和对账；未执行持续压测、生产部署、移动端/跨浏览器、真实429/账单或供应商内部计算核验，不因本轮补新的业务模块。
