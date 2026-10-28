# GO-074 压测前独立核验

2026-10-28。固定交付 `bca0cedea475430b02ab991e1df30ef3c297c1b9`，应用功能提交截至 `b97b03adaa845136773da5e7cba16c8ab2677520`；追加修复后的main功能版本为 `ad49718361605cf793d936cb85721398f47fb245`。核验范围为 `998b89e..bca0ced` 的工作台入口、公开知识来源/scope、真实AI页面证据、门店/活动工具及首次MySQL就绪探针，以及下列独立修复；前序报告继续按各自SHA适用。

**结论：GO-074在本报告范围内核验通过，两个必要功能缺口已分别修复回归，最终功能版本CI四作业成功。可以进入GO-073；本轮未启动压测，未声称项目全部验收完成。**

## 需求与源码结论

后端、前端及范围/证据分别独立审查。当前公开知识、版本/epoch/索引READY约束未放松；来源URL对齐实际 `/ai/knowledge/:id` 路由。活动工具每次读取当前SQL库存，ID/金额/库存保持字符串，保留公开券和ACTIVE过滤；门店卡片展示真实name/address并校验内部详情链接。逐门店订单/核销/退款仍由后端实时权限守护。MySQL健康探针检查业务用户的TCP和schema，不再误认首次初始化socket server已可用。

核心流程均有可达入口：登录→商铺→活动→抢券/成单→订单支付/取消；商家工作台→活动/逐店订单核销退款/审计/FAQ；搜索→公开笔记详情→关注/点赞→Feed；AI→来源详情/本人只读工具。范围审计发现发布入口及其加载状态两个必要小修，见下方；没有据此增加新业务模块、框架或数据库。

## 独立修复（每个问题一提交）

1. **QA1 发布页无入口**：`/notes/new` 和发布表单已存在，但界面没有任何发布链接。仅在已登录主导航补“发布笔记”，保留既有路由登录守卫。缺口由源码全局入口检查确认；新增入口的匿名隐藏/直接访问回跳与已登录点击已实际验证。
2. **QA2 中断详情后发布页卡骨架**：详情请求使 `loading=true`，切到同组件发布路由后取消请求，旧finally跳过清理，发布表单永久隐藏。仅在compose分支显式清loading；[修复前](note-nav-before-fix.json)实际为skeleton1/form0，[修复后](note-nav-after-fix.json)3断言通过。负例采集于“QA1已加、QA2未修”，不冒充QA1原版运行失败。
3. **QA3 CI漏跑真实活动工具回归**：普通 `./...` 会SKIP该包的集成测试，原真实依赖步骤未列aitool。只把已有4子组真实工具测试加入原串行 `-race` 步骤，未新增镜像/SDK/付费测试。独立本机真实复跑已通过。

main分别提交：QA1 `047a822109743d63b61a079ec2ec49795173072b`；QA2 `ad49718361605cf793d936cb85721398f47fb245`；QA3 `c03dc11f7309b83d776ad92616d5baf6b7c8c272`。修复均经过非实现者审读后分别提交，未改写原交付历史。

## 本轮运行与证据

独立worktree `.reviews/go074-bca0ced/worktree`，全新Compose项目 `project-full-stack-review074`；MySQL3360、Redis6430、API8130、Vite5230，Kafka/ES不启动，AI仅stub，Key空。未读取CRM私密配置、未调用付费模型、未连接原开发数据库。初始化使用当前Compose和001/002，显式迁移003—012。

| 检查 | 本轮实际结果 |
| --- | --- |
| Go基础 | mod verify、空gofmt输出、vet、全普通 `go test -count=1 -json ./...`、build通过；[命令](commands.jsonl)、[普通测试](unit-network.txt) |
| 五包真实依赖 | aitool/aiknowledge/aichat/merchant/merchantorder **14个集成入口、49子组通过**；两个付费Provider测试SKIP；[原始](integration-initial.txt)、[独立计数](test-summary.json) |
| B端页面 | **34断言通过、cleanup=true**，包含OWNER/MANAGER/STAFF、混合门店角色、实际支付后核销退款、旧Token撤scope/停成员、越权无副作用及网络失败恢复；[结果](merchant-initial.json) |
| 实时工具页面 | **23断言通过、cleanup=true**，5轮实际Go工具；同会话新消息查询库存3→正常抢券成单→SQL/Redis2→卡片字符串2，大ID/价格/详情链接、未知ID拒绝；[结果](tools-initial.json)。stub仅承载协议，工具分支直接读真实SQL，不作为模型质量证据 |
| 发布入口/加载状态 | **3断言通过、cleanup=true**，保留QA2实际失败/截图；[最终](note-nav-after-fix.json) |
| 来源详情协议差量 | **6断言通过、cleanup=true**，实际stub SSE含PUBLIC/SHOP_PUBLIC两种v1来源，实际点击详情核对URL/scope/正文/版本；[结果](source-scope-stub-initial.json)。平台scope以公开合成快照/向量SQL夹具建立，没有伪造HTTP/SSE响应 |
| 前端/脚本 | 既有锁文件npm ci、build含vue-tsc、format及merchant/tools脚本语法通过；修复后[最终构建](frontend-build-after-qa.txt)与[最终格式](frontend-format-after-qa-fixed.txt)通过；[前端小结/命令](ui-review.md) |
| 初始化/迁移 | 本轮全新MySQL/Redis初始化及健康成功；003—012实际apply，停应用后复跑checksum一致且全部skip；[首次](migrate-initial.txt)、[重复](migrate-repeat.txt) |
| 最终功能CI | 精确`ad49718361605cf793d936cb85721398f47fb245`的[run40704122782](https://github.com/tech-cove01/project-full-stack/actions/runs/40704122782)四作业成功：backend-go、community-kafka、frontend、归档Java；含Linux普通/真实MySQL与Redis/Kafka与ES的race及启动冒烟，新增aitool真实回归已启用；[原始run](ci-run.json)、[jobs/steps](ci-jobs.json) |

运行的是Windows Go1.27.0和Chrome/上海时区，MySQL8.4/Redis7.4。[20份关键源码与二进制清单](source-manifest.json)核对当前字节与main最终功能`ad49718`一致；实际API二进制来自交付bca0ced、`vcs.modified=true`，记录SHA及build info。三个QA只改Vue/CI，没有后端源码改动，不把二进制改称最终SHA干净构建。

Go普通测试第一次在沙箱内因loopback访问被拒而失败：HTTP请求报Windows socket access permissions、AI本地HTTP报transport_failed。原[失败日志](unit-initial.txt)保留；同一代码用允许本地网络的权限复跑全部通过，没有修改产品或删测。

QA1新增导航行第一次format检查未通过，按已有Prettier只格式化该行后通过；[原失败](frontend-format-after-qa.txt)保留。未重跑无关页面，未用反复重试掩盖业务失败。发布QA2的网络暂停不返回成功JSON，最终截图已人工查看，导航没有溢出、表单与来源详情可读。

## 原始真实AI证据独立复核

本轮没有再次付费。逐项核对[原真实AI报告](../../../evidence/2026-10-28-998b89e/GO-074/ai/report.md)、原答案/来源/工具/取消、人工截图和累计账本，**35断言/7轮**与原始文件一致：有据答案匹配合成停车FAQ、两种scope来源可点击，本人订单工具/他人拒绝、无据拒答、实际停止/恢复，以及停用后新轮无旧sources。

首轮3次286已知tokens，修正采集器后第二轮新增9次769已知tokens，总计12次转发尝试：7次Embedding共130 input；5次Chat中4次完成为826 input+99 output，取消id10 usage未知；已知之和1055。未核账单，金额/取消结算未知，应用逻辑预算退款不代表供应商退款。与GO-075的79次/7673已知tokens及2次未知分开记账。

原二进制6575610+modified与工具b97b03a+modified的限制保留，不能改称最终HEAD干净构建。真实Provider使用stream=false，本地SSE提供阶段事件/完整答案及取消，不宣传模型逐token流；观察器只旁路实际reader，无成功JSON替换。本轮源码与非付费真实页面差量验证当前href/scope/工具，模型质量仅引用原有限合成集，不宣称通用质量或重新运行30题。

## 资源与清理

各页面脚本均cleanup=true，所有浏览器已关闭。父方额外[最终审计](final-cleanup.json)验证本轮已知合成用户/商家/门店/订单/活动/知识文档为0，交易与社区未DONE outbox、OPEN review、PARKED为0；登录/code/AI键为0，交易Stream长度0、PENDING0。精确删除由各脚本按本人ID及持久结果/ACK门槛执行，没有flush、清未知消息或删共享group。

[进程收尾](stack-cleanup-initial.json)：API CTRL_BREAK退出0、Vite受控TerminateProcess退出1，两者无fallback kill且8130/5230可重绑。再次验证项目标签/映射端口后，仅正常停止`project-full-stack-review074`的MySQL/Redis，两容器exit0、无OOM，命名卷保留；没有down/rm/删除卷或停止其它项目服务。没有宣称所有持久历史、日序号和缓存均被清空。

## 冻结边界

本轮不执行GO-073。功能核验/必要修复和最终相关CI通过后，可以进入既定持续负载、Kafka/ES延迟与积压恢复、库存/订单/点赞计数对账。压测发现影响目标行为的问题时分别修复并复测；通过后据实记录指标、条件、限制与简历素材，冻结本轮功能，不再扩业务模块。固定QPS目标、生产部署、零停机重建、接管旧Java生产数据均不作为新增结束条件。
