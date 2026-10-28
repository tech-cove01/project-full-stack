# GO-076 公开笔记全文搜索交接

2026-10-28，实现者自测通过，**待独立核验**。起点 `290cd60358e0467f43caef4ddd647704bdbcfac6`，实现/故障收尾 `6ec7f4afbca21e7a03af08d3e391114d072493a2`；本文件和最终环境证据另做文档提交。依赖GO-050/052事实与投递、GO-072事件契约。按总方案第9节GO-076及FIN-01/04/05/06交付；FIN-04持续负载及FIN-05其它页面仍由后续票完成，不据本票宣称整个项目结束。

## 交付行为

- 公开GET `/notes/search` 与Vue“搜索笔记”入口：标题/正文中文检索、可选门店ID、相关度/最新排序、PIT分页与失败重试。ES8.19.23固定版本、内置CJK，无插件或新Go SDK；ID始终为字符串，等时间的ID按关键字字典序排序。当前MySQL公开状态、版本和门店决定最终内容；允许短页/空页后继续，没有精确总数。
- `community.search-v1` 独立消费组，复用既有六字段事件及持久停车协议；ES外部业务版本写入先于SQL版本/hash/receipt提交，重复和乱序收敛。同版本不同事实/同eventId不同内容拒绝，数据库未知结果无持久停车则不提交offset。无关合法事件保留IGNORED收据。
- 012版本SQL新增搜索控制/状态表；首次启用须离线rebuild。新代次写完refresh后切alias，保留旧代次/收据/失败/offset。API/消费者停止并显式 `GO_SEARCH_REBUILD_OFFLINE=1` 是操作前置；ES/SQL无分布式事务，alias切换后SQL失败需离线重建重试，不承诺零停机。
- ES配置关闭或不可达仅使搜索503，核心启动/健康和交易继续可用；真实Vue错误态可重试，实际400游标失效会保留查询条件、清空旧分页并重新搜索。界面不暴露索引/offset等实现参数。

## 分步提交

每笔均包含对应必要自测/审查及模块证据，不改写历史；独立核验者同时在main追加GO-072/075修复和报告，这些提交保留且不算本票作者工作。

| 小任务 | 完整SHA |
| --- | --- |
| a0 固定可选ES资源与配置 | `1d60b90f9fe46b827999629905227b290a48ec61` |
| a1 公开查询、事实守护与游标 | `eaa998be3e05cc87228a32f49c2e79ba81fb2e1c` |
| b 同步、持久停车、精确重放和离线重建 | `38adc978c8b033b2e3776a7eb1e3a84b9dde5ce8` |
| Kafka独立问题：容器CLI与宿主机双监听器 | `0bed2bfdea4d4fc1c4ab6bf4a101a121c39582f2` |
| CI：固定ES及真实Kafka/搜索集成 | `54bc1dd9dd4a08ad07d3dec3be1185d2d1553217` |
| a2 真实API与第三消费者/生命周期 | `5eb99ec4aa33ba747b77712b3bd2fff006e2eed8` |
| a3 Vue公开搜索和两时区浏览器 | `2d8b0b0579e64b37a800615885a98e9d4463b292` |
| c 物理ES故障、交易隔离和精确重放 | `6ec7f4afbca21e7a03af08d3e391114d072493a2` |

GO-072/075的最新独立结论见 [独立报告](../../../reviews/2026-10-28/go072-go075-290cd60/report.md)，适用基线和QA范围不延伸到后续GO-076共享改动。旧 `1b4ebe8b2bbd0494f0d8f9b68bdecd28f4a20592` 的绿色CI不等于最终HEAD绿色。

## 实际验证与原始证据

| 验证 | 实际结果/证据 |
| --- | --- |
| 普通Go最终检查 | Windows Go1.27.0，gofmt无输出、vet、`go test -count=1 ./...`、`go build ./...`、`go mod verify`通过；[全套最终输出](go-test-final.txt)，[初次全套](go-test-initial.txt) |
| 配置/固定资源/CJK样例 | [资源报告](resource-report.md)、[实际版本](es-version.json)、[镜像摘要](dependency-images.txt)、[两组中文切分](cjk-samples.json)；不是通用中文检索质量评测 |
| 查询真实ES/MySQL | 1入口3子组通过：当前公开事实/大ID/门店、PIT空页继续与新增隔离、外部版本；[查询报告](search-report.md)、[原始](search-real-initial.txt) |
| 同步真实MySQL/Kafka/ES | 1入口7子组通过：重复/旧版本/冲突、ES写后SQL rollback重投、停车精确重放、RR重建与增量交错、IGNORED、真实Kafka offset门槛及独立SQL池故障、alias切换后SQL rollback重建收敛；[同步报告](sync-report.md)、[最终原始](sync-real-final.txt) |
| 应用接入/启停 | [接入报告](app-report.md)，最终二进制 [启动关闭6场景](bootstrap-final.json) 全部通过；健康场景API退出0，其余预期失败按对应退出码通过，无强制清理、端口可复用 |
| Vue | build（含类型检查）/format通过；上海25项、UTC25项，真实发布→outbox→Kafka→ES→匿名页面，含大ID、转义、实际PIT失效400→重搜200；[页面报告/失败轮次](ui-report.md) |
| 物理ES停机 | 首轮17项通过：实际Docker stop，页面503，继续发布2篇停车2条，交易9项通过，同容器启动后只重放2条、四篇可见；[故障报告](fault-report.md)、[原始17项](fault-initial/fault-result.json) |
| 迁移 | [初次003—012](migrate-initial.txt)通过，[最终复跑](migrate-final.txt)全部checksum一致并skip |
| Kafka CLI/CI | [双监听器故障与修复](kafka-cli-report.md)、[CI变更及验证边界](ci-report.md)；本票最终HEAD远端CI/Linux race未执行 |

重要边界：ES不可达注入与最终物理容器停机分别报告；独立SQL池关闭不是物理MySQL停机；SQL rollback复现提交窗口不是OS强杀。物理停机时三组均提交到43、lag0，真正待恢复的是2条PARKED记录；恢复后PARKED0/RESOLVED2，offset不变。没有用索引重建、删除消息或重置offset掩盖该轮失败。

## 非作者审查与保留失败

查询与同步由不同agent交叉审读，根agent审查完整事务/版本/锁/HTTP/页面与故障脚本，`provider_adapter`复核app关闭顺序和最终物理故障。审查发现alias acknowledged并不排除单action失败，保留 [先失败](search-alias-initial.txt) 与 [修复通过](search-alias-final.txt)，提交前加入errors/action_results及must_exist核验。页面400重试旧cursor的问题提交前修复，两时区用真实PIT失效复验。最终只读审查无遗留阻断问题，修正“三组/三个分区”的文案为三组分别检查同一分区0（共3条记录）。这些代码审查不替代用户安排的独立核验。

保留同步首次未使用imports编译失败、Kafka CLI跟随错误advertised地址的首次失败摘要、上海脚本详情跳转等待/清理字段错误及原夹具受控清理，不覆盖原始轮次。初次supervisor因stdin EOF提前结束的记录 [保留](stack-stdin-initial.json)；修正为停止标记后完成所有实际页面与故障演练。API/UI完整日志见 [API](api-ui.txt)、[Vite](vite-ui.txt)，初次日志另存。

## 版本与专用环境收尾

专用Compose project `project-full-stack-go076`，MySQL3332/Redis6404/Kafka9099/ES9202，API8086/Vite5176；Chrome156.0.8078.48，Playwright-core1.56.1只装于忽略的backend/bin工具目录，未改应用依赖。AI全程关闭，无付费模型调用或私密CRM材料。

实际页面/物理故障使用 `go076-worktree` 二进制；最终Go源码与 `5eb99ec` 一致（`git diff --name-only 5eb99ec 6ec7f4a -- backend`为空），最终编译并用 `go076-5eb99ec` 二进制重跑6场景。三个本地二进制的实际SHA256/大小保存在 [最终对账](final-reconciliation.json)，不把worktree revision伪称最终提交。

- API正常退出0，Vite在Windows Terminate后exit1；两者forced=false、端口可复用，见 [进程清理](stack-cleanup.json)。不能写成所有进程exit0。
- 页面和故障脚本先等本人outbox DONE/三组receipt/实际broker lag0再精确清自有数据；最终自有用户/门店/商家/笔记、待投递outbox、receipt与failure均0，搜索state和公开笔记事实/alias文档数本轮最终均0，会话/验证码/AI键0。原业务库未做全库清空，不回拨自增水位，不flush Redis或删历史Stream。
- 最终三组对 `community.events` 分区0均为committed=logEnd=43、lag0，原始CLI分别保存于final-offset文件；这是清理前已确认消费完成，不是重置消费位置后的0。
- [四服务最终停止](services-final-stop.json)：MySQL/Redis exit0，Kafka/ES exit143，无OOM；本票端口均已无TCP监听，四个命名卷保留。只对精确project的四服务执行stop，没有down/remove或卷删除。收尾脚本的其它容器筛选条件写错而匹配0条，原本“其它集合未变”的比较无效，已撤回该表述并保留 [初始记录](services-final-stop-initial.json)；后续不带错误过滤的只读查询观察到14个其它容器仍运行，不能用后置快照重建有效的停止前比较。

[源码摘要](source-manifest.json)记录起点到实现HEAD在共享main上的运行/测试/配置文件SHA256，包含同时保留的独立QA改动，不将全部文件归为本票作者。[证据静态检查](evidence-audit.json)通过：JSON可解析、局部报告链接有效、4份trace ZIP CRC正常，匿名网络请求没有非空Authorization/Cookie、登录准备URL或完整手机号URL；文本Key/Bearer模式扫描未发现命中。该有限检查不宣称能证明所有潜在秘密都不存在。

## 核验复跑与剩余范围

启动和首次重建见 [运行步骤](../../../development.md#公开笔记搜索go-076)；实际脚本及完整显式环境见查询/同步/页面/故障分报告。新核验应另建专用project/端口并应用003—012，先停API/消费者跑真实集成，再离线rebuild并启动API/Vue做页面；物理故障脚本刻意限制到本票精确容器/端口，需使用其显式约束环境，不能直接指向其它服务。证据输出使用新目录，不覆盖本轮。

本票未执行最终HEAD Linux race/远端CI、持续容量/投影延迟量化、物理MySQL/Kafka故障、断电/OS强杀、集群切换或通用中文相关性评测；没有新增笔记编辑/下架API、商铺全文搜索、向量搜索或推荐。GO-074仍补关键B端与真实AI页面（可复用本票搜索证据），GO-073在功能固定后完成持续负载与对账。独立核验通过前，STATUS保持“自测通过待核验”。
