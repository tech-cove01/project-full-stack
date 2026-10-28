# GO-076 独立核验

日期：2026-10-28。结论：**公开笔记全文搜索、第三 Kafka 消费组、持久停车/精确恢复及离线重建在约定范围内通过独立核验，未发现需要追加修复的确定 P1/P2。** 本轮没有修改应用源码或收走 GO-074 的在途文件。

## 版本与隔离

- 固定核验提交：`998b89e81bed42786d25d0cebbadb401fd506f70`，功能/故障代码截至 `6ec7f4afbca21e7a03af08d3e391114d072493a2`。原交付提交清单保留在[实现者报告](../../../evidence/2026-10-28-290cd60/GO-076/report.md)，该报告不是本次独立结果。
- 独立 worktree：`.reviews/go076-998b89e/worktree`；专用 Compose 项目 `project-full-stack-review076`，MySQL3350、Redis6420、Kafka9114、ES9214、API8116、Vite5216，索引 alias=`review-notes-v1`。未接入下一轮开发或 CRM 的数据库、消费者和服务。
- MySQL8.4、Redis7.4、Kafka3.9.0、ES8.19.23；Windows Go1.27.0、本机 Chrome。ES 为单节点、内置 CJK 分析器。AI 关闭，没有调用付费模型或读取真实用户浏览器配置。
- [源码/二进制清单](source-manifest.json)记录 26 份关键文件与两个实际执行二进制的 SHA-256、`go version -m`。源码逐一匹配固定提交；二进制 `vcs.revision` 正确，`vcs.modified=true` 源于本地新增核验证据，不能描述成 clean VCS 构建。独立 worktree 的 Git 差量仅为本报告目录。

## 源码与需求复核

对搜索服务/HTTP/客户端、同步/重建/CLI/SQL、组装及前端分别只读审查，再交叉核对原始运行证据。

- 查询只索引公开笔记白名单字段，中文标题/正文、可选门店、相关性/最新排序和 PIT 翻页有实际入口。大 ID 全程字符串；游标 HMAC 绑定条件、排序、页长与有效期。篡改/错配/失效游标返回400；ES超时、部分分片失败或不可用明确失败，不悄悄用 LIKE 代替全文检索。
- ES 仅提供候选 ID/版本，最终由 MySQL 当前公开状态、业务版本和门店重新筛选，并从 SQL 返回正文。过滤可能产生短页或空页但仍带下一游标，不给出精确总数。相同时间的 ID 在 ES 中按 keyword 字典序，不能宣传成任意位数 ID 的数值顺序。
- `community.search-v1` 独立于 follow/feed；沿用六字段事件、业务版本、文档 hash 和持久收据。先写 ES 外部版本，再于 SQL 事务写状态/收据；ES 已成功而 SQL 回滚时，重投应幂等收敛。这是至少一次投递下的最终效果约束，不是 ES/SQL 分布式事务或 HTTP 写入 exactly-once。
- 停车持久保留原消息/hash/源坐标，精确 Replay 与解除停车同 SQL 事务；完成效果或持久停车之后才能确认消费位点。`lag=0` 不能代替检查 `PARKED` 数。
- Rebuild 采用停 API/消费者后的离线流程、显式确认变量、控制行互斥、RR 分页快照、新物理代次、refresh 与 alias 切换，保留旧代次、收据、失败记录与 Kafka offsets。alias 已切换而 SQL 回滚时用下一次离线重建恢复；未承诺零停机或跨系统原子提交。
- GO-072 公共消费者的本票差量仅扩展允许的 `search-v1` 投影及说明；app/config/Compose/CI 同时检查。本轮六包真实回归覆盖原 follow/feed 和 AI 相关边界，未用原基线结论自动覆盖新代码。
- Vue 公开请求显式不带会话；条件变化取消旧请求并隔离迟到响应，恢复 URL，转义内容，普通翻页失败保留游标，PIT400 清理旧结果后从首页重搜。页面没有泄露索引/offset 等实现信息。

## 本轮实际验证

命令、工作目录、退出码和耗时见 [commands.jsonl](commands.jsonl)；浏览器补充命令、CI 与收尾各有独立 JSON。下面的入口数、子组数和断言数分别统计，不相加伪装成更多业务场景。

| 检查 | 本轮结果与证据 |
| --- | --- |
| Go 基础 | `go mod verify`、`gofmt -l .`（空输出）、`go vet ./...`、`go test -count=1 -json ./...`、`go build ./...` 全通过；[普通测试原始日志](unit-initial.txt)，未设置真实集成开关的测试明确 SKIP |
| 六包真实依赖 | notesearch/communityconsumer/communityprojection/social/aiknowledge/aichat 全通过：**22 个 TestIntegration 入口、77 子组**；2 个付费 Provider 测试 SKIP；[原始日志](integration-initial.txt)、[独立计数](test-summary.json) |
| 搜索/同步重点 | 真实搜索3子组、同步7子组；包括中文正文/公开状态与旧版本过滤、游标/分页、重复/乱序/hash冲突、ES写成功SQL回滚重投、离线重建互斥、alias切换后SQL失败再重建；均在上述22/77内 |
| 前端 | 使用既有锁文件 `npm ci`、`build`（含vue-tsc）、`format:check` 通过；[构建](frontend-build-initial.txt)、[格式](frontend-format-initial.txt)；两个交付脚本 `node --check` 通过 |
| 搜索页面 | 上海 **25项**、UTC **25项**，真实13篇发布→outbox→三组→ES→匿名Vue；[上海](ui-shanghai-initial.json)、[UTC](ui-utc-initial.json)、[页面审查详情](ui-review.md) |
| 公开边界差量 | 新匿名上下文设置合成 storage 标记后，真实请求仍无 Authorization/Cookie；重复q参数拒绝且修正后真实200；2类行为、**4断言**，[结果](ui-public-boundary-module-fixed.json) |
| ES物理故障 | **17个顶层断言**通过，其中交易断言另核对9项子检查，不能相加成26；[故障结果](fault-initial/fault-result.json)、[停机截图](fault-initial/search-es-down.png)、[恢复截图](fault-initial/search-es-recovered.png) |
| 启动/关闭 | 实际本轮 API 二进制6场景通过：正常启动/关闭、缺密码、缺端口、MySQL拒连、Redis拒连、API端口占用；[结果](bootstrap-initial.json) |
| 迁移/CLI | 新专用卷完成003—012迁移，API/消费者停止时离线 rebuild 成功；再次迁移逐个 checksum 校验并全部skip，[首次迁移](migrate-initial.txt)、[重建](rebuild-initial.txt)、[重复迁移](migrate-repeat.txt) |
| Linux/远端CI | 精确 `998b89e` 的 [run40678468785](https://github.com/tech-cove01/project-full-stack/actions/runs/40678468785) **4作业成功**（backend-go、frontend、community-kafka、归档Java）；含普通 `-race`、真实MySQL/Redis和Kafka/ES集成 `-race`；[run元数据](ci-run.json)、[jobs/steps](ci-jobs.json)。不自动覆盖后续 GO-074 提交 |

页面另外覆盖大于2^53的ID、独立SQL排序对照、跨页无重漏、门店/URL刷新返回、中文正文命中、HTML作为文字、真实详情、空态、无效条件、迟到请求取消、真实PIT关闭后的400与重搜200、首次/翻页网络失败及重试。截图已人工查看。网络故障使用 abort，未伪造成功 JSON；`partitions=3` 是三个 group 各自分区0的三条检查记录。

### 物理 ES 停机的实际结果

1. 发布两篇笔记并在真实 Vue 查到。正常 alias 代次为 `review-notes-v1-build-b8cfd768adeeb71b`。
2. 只停止标签/项目/端口全部匹配的核验 ES 容器，真实搜索503、页面清掉旧结果并提供重试。继续发布两篇：事实/outbox/follow/feed均4，search已应用2、停车2；停车源坐标为分区0 offset28/29。
3. 三组 committed/logEnd 都为30、lag0，**仍有2条恢复积压**。ES保持停止时，真实活动→准入→成单→支付→核销→审计的9项检查通过。
4. 启动同一 ES 容器，原代次及旧文档保留。精确重放本人两个失败源坐标，结果 `PARKED=0 / RESOLVED=2 / APPLIED=4`，四个自有文档唯一物化；原页面重试真实200。这里的4只指本轮自有文档数。
5. 恢复后三组位点仍30，无 offset reset、无需重建掩盖重放；随后脚本清理自有数据并通过消费提交屏障。

交付故障脚本严格锁定原作者环境。本轮使用固定新专用环境的本地副本，**仅9处项目/容器/端口/alias字面量替换**，未削弱安全 guard 或断言；[适配hash](fault-harness-adaptation.json)、[完整差量](fault-harness-adaptation.diff)。没有执行脚本去停止原开发容器。

## 失败记录与收尾

公开边界差量第一次在加载 Playwright 前，因本地 wrapper 父目录层数错误发生 `ERR_MODULE_NOT_FOUND`；[原失败](ui-public-boundary-initial.txt)与[元数据](ui-public-boundary-command.json)保留。修正执行器路径后通过，未改产品或覆盖失败证据。原交付报告中的故障失败及offset43属于原作者环境，不能冒充本轮结果。

应用停机与数据审计见 [stack-cleanup-initial.json](stack-cleanup-initial.json)、[final-cleanup.json](final-cleanup.json)：

- API 收到 Windows CTRL_BREAK 后退出0，Vite受控 TerminateProcess 退出1，两者无需 fallback kill，8116/5216均可重新绑定。
- 独立SQL核对自有 user/shop/merchant/note/search-state/note-projection均0；未完成社区outbox、PARKED均0；本专用库最终公开笔记与搜索状态均0，ES alias `_count=0`。未声称已DONE outbox、历史收据和RESOLVED记录全部清空。
- 登录session/code、AI键均0。订单Stream保留1条历史、PENDING=0，没有flush。三个group最终均分区0 offset30/logEnd30/lag0。
- 收尾前重新验证容器精确项目标签和映射端口，只正常stop本轮四容器，无OOM，保留卷及旧索引代次；未down/rm/删卷，也未操作并行任务/CRM服务。实际退出状态以JSON为准。

## 结论适用范围与下一步

GO-076可提升为“已核验（998b89e范围）”，其搜索页面证据可被GO-074复用。没有新增修复提交，仅提交本次独立证据与状态更新。

本轮不证明持续容量、QPS/P95/P99、通用中文检索质量、多节点吞吐、零停机重建、物理MySQL/Kafka停机或灾难恢复。CJK只做小组样例，非中文领域词典/推荐/向量检索。本轮AI关闭，旧真实Provider与质量证据沿用各自固定版本，不把本轮协议回归当成新付费评测。

剩余既定工作：**GO-074**补关键B端角色/拒绝路径与真实AI浏览器证据（其在途变更另验）；**GO-073**在最终功能固定后持续负载、冷/热对照、Kafka/ES延迟与积压追平、成单/库存/计数对账。通过后按STATUS结束本轮功能，不再扩技术栈。求职材料目录仍是固定快照，没有自动升级为本轮实测或指标。
