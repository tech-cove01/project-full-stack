# 工作流与任务状态

更新：2026-11-09。本页是唯一任务状态入口；协作授权见 [长期约定](docs/collaboration-agreements.md)，产品与设计文档见 [文档导航](docs/README.md)。旧票据和执行过程见 [状态历史快照](archive/docs/status-history-20261104.md)，不能作为当前指令。

## 最新交接：展示页面与交互演示对齐四条亮点（实施与自验）

2026-11-09 用户确认简历 v21 先保持，要求两个项目的已部署展示页面与演示按当前亮点调整，由各项目动手 agent 实施，完成后用户验收。2026-11-10按本交接开始实施本项目，两层均纳入；当前尚未交用户验收。下方业务和压测已收尾的结论继续有效；此前“等待后续反馈”已收到本次新需求，不应据旧记录忽略此任务。

本项目沿用四条顺序：**异步交易一致性与故障恢复 → Feed 分层缓存与并发回源控制 → 可靠事件与 ES 搜索一致性 → 高并发点赞链路优化**。以当前已确认的项目表述组织讲解，明确“解决什么问题、做了什么设计、操作后能看到什么”，避免每条都套“针对”的句式。入口为 `showcase/index.html`、`showcase/details.html`、`showcase/performance.html` 和 `showcase/demo/`；参考现有 [展示规格](docs/showcase-plan.md)、[演示规格](docs/showcase-demo-plan.md)，保留既有配色、访客引导、顾客/商家业务流程与移动端适配。

### 两层页面都要修改，按同一条体验路径验收

用户补充确认：**原始项目展示站，以及从展示站超链接进入的交互演示页，都在本次范围内；只完成其中一层不能报完成。** 已部署入口是[项目首页](https://tech-cove01.github.io/project-full-stack/)、[设计详解](https://tech-cove01.github.io/project-full-stack/details.html)、[压测实录](https://tech-cove01.github.io/project-full-stack/performance.html)和[互动演示](https://tech-cove01.github.io/project-full-stack/demo/)。这些是现有入口，不代表下述更新已经上线。

- **展示站怎么改：** `index.html` 的核心设计区按上述四条顺序重组，每条用简短的业务问题、关键设计、带来的作用说明价值；`details.html` 对应解释流程和取舍，避免首页与详解各讲一套。原有完整业务介绍和权限等补充内容保留，主次分明。每条同时提供“体验该场景”与设计/证据入口；第四条连接 `performance.html` 的真实报告，保留已认可的原图、切换方式和测试条件，不重做整站视觉风格。
- **演示页怎么改：** 保留顾客/商家/社区的完整流程，为四条分别增加可直达的场景及步骤引导，明确操作起点、要点哪个按钮、结果看哪里；操作应改变示例状态并显示对应结果或过程，不能只弹出说明或固定成功提示。缓存、消息和批处理等内部机制可在可展开的过程面板中解释，不要求用户先看懂全部技术细节。每条至少走通正常流程与一个能体现设计价值的关键异常/对比分支，下表列出的其他机制可沿同一路径展示，不扩展成穷举故障平台。
- **两层怎么连：** 四条名称、顺序、业务场景及结果口径一致；展示页每个体验链接直接选中相应演示场景或引导步骤，不全部落到同一个默认交易起点。演示页可返回对应设计说明和证据。具体 URL 参数或锚点由实现者确定并实际验证，保留通用演示入口与自由体验，不破坏已有有效链接。

### 四条内容与演示验收对应

| 亮点 | 展示页要讲清 | 访客可操作并观察的内容 |
| --- | --- | --- |
| 异步交易一致性与故障恢复 | Redis Lua 原子预占、Stream 异步成单；稳定受理 ID 与 MySQL 唯一约束；结果或待核对任务持久化后再确认消息；补偿校验订单归属。 | 正常抢券后可重试同一请求，看到同一受理记录和唯一订单；可体验重复消息、结果待核对、取消后再购买及旧补偿到达等代表分支，看到库存、订单与购买资格怎样保持对应。明确受理成功和订单已落库的区别，原核销/退款流程继续可用。 |
| Feed 分层缓存与并发回源控制 | 公共正文按笔记 ID 与发布版本进入**进程内缓存与 Redis 两级缓存**，个人点赞和当前可见性单独读取；批量加载、相同缺失批次合并、并发限制与单请求取消隔离。 | 切换两个访客查看同一笔记，看到公共内容可复用、个人点赞互不串用；通过冷/热读取、同批请求和取消其中一个等待者，展示回源合并及其他请求继续完成；修改版本或隐藏笔记后，旧内容不能覆盖新版本或绕过当前可见性。过程说明可放展开面板，模拟缓存计数不能冒充后端测量。 |
| 可靠事件与 ES 搜索一致性 | 笔记变更与 Outbox 同事务保存，Kafka 更新 ES；收据去重、业务版本处理乱序；搜索候选回 MySQL 校验公开状态和版本；失败事件持久保存并支持人工重放。 | 发布/修改笔记后查看索引更新；切换重复、乱序或索引暂不可用场景，看到旧事件不覆盖新版本、失败事件可查看；隐藏笔记后旧索引命中应被过滤；恢复后由访客明确触发人工重放，看到搜索结果恢复。不要把人工恢复改讲为自动追平。 |
| 高并发点赞链路优化 | 展示“点赞接口已成功但下游仍积压”的诊断，以及合并事务 SQL、有界发布/消费批处理减少数据库往返和提交开销的机制；吞吐与数据一致性、下游稳态一起验收。 | 可点赞/取消并查看业务写入、Outbox 发布、消费收据及核对结果的过程，交互解释批处理怎样处理积压；实测区域可切换 H3/H4 的原始报告/截图和对应指标。展示点赞及取消成功 QPS 3000+，16核32GB单机、两轮各180秒内网、P95≤62ms/P99≤102ms及核对条件；不得把浏览器模拟动画当现场3000 QPS测试。 |

### 完成标准与停止条件

实施前在本节补一张简短的“四条亮点 → 展示区块 → 演示入口 → 操作与预期结果”映射，按下面标准自验后交给用户，不需为此再启动一轮泛化设计讨论：

| 亮点 / 小任务 | 展示区块 | 演示深链 | 操作与预期结果 |
| --- | --- | --- | --- |
| 异步交易一致性与故障恢复 / WEB-008A | 首页第1项、详解#trade | `demo/?scene=trade` | 抢券/重试/投递→唯一受理订单；待核对→明确核对后确认；取消重买后投递旧补偿→不释放新预占 |
| Feed分层缓存与并发回源控制 / WEB-008B | 首页第2项、详解#feed | `demo/?scene=feed` | 冷热读取/切访客→公共复用且个人状态隔离；同批等待者取消/版本变化/隐藏→其它继续且旧回源受限 |
| 可靠事件与ES搜索一致性 / WEB-008C | 首页第3项、详解#events | `demo/?scene=events` | 发布/投递/搜索；重复乱序不覆盖，ES失败停车，恢复后手动重放；隐藏候选被过滤 |
| 高并发点赞链路优化 / WEB-008D | 首页第4项、详解#likes、实测页原报告 | `demo/?scene=likes` | 点赞/取消→事实与Outbox变化；暂停消费后积压→有界发布消费清空、同组逐条/批量对比；原实测另看H3/H4 |
| 两层接线与发布 / WEB-009 | 三展示页、四场景入口、通用业务演示 | 保留原`demo/`与锚点 | 四链接实际直达/刷新/返回、场景重置、桌面窄屏及原业务主线通过后发布，标记待用户验收 |

- [ ] 展示首页、设计详解与演示页均已更新，四条完整对应；压测实录及第四条的指标、条件、报告链接一致。
- [ ] 从展示页逐一点击四个体验入口，均能进入对应场景；按可见引导完成操作，看到具体状态、证据或对比变化，并能返回相关介绍。
- [ ] 四条的正常流程及关键异常/对比分支均有实际操作记录；不能用“已有卡片”“按钮能点击”或一张截图代替流程走通。未覆盖的要求单列，不默认为完成。
- [ ] 场景可重置并重复体验，深链刷新和返回导航可用，桌面与窄屏不遮挡主要按钮和结果，原业务主线仍可用，静态模拟与历史实测标识清楚。
- [ ] 交付可访问的展示/演示链接，逐条列出“点击入口 → 操作步骤 → 应看到的结果”，附必要截图或短录屏及实际验证记录；核对线上内容为本次版本，明确尚未覆盖的项。

**以上满足后标记“待用户验收”并停止自行扩展和美化。** 用户未认可前不写“验收完成”；后续根据用户具体反馈改对应问题，不反复更换配色、布局或扩大功能范围。

### 实施范围与交付

- 展示首页与详解按四条建立清楚入口，四条均应有可操作演示及可观察结果，不能仅增加技术名词或说明卡片。保持自然业务流程，细节用可展开说明呈现；页面不出现内部任务号、简历版本、求职口吻或私人材料路径。
- 当前 Pages 演示使用静态示例数据，沿用并清楚标明这一性质；机制模拟与真实验收证据分开。回源合并限相同缺失批次，不包装为跨实例合并；不声称 Kafka 异步写入点赞计数。省略已被否决的“8个请求”小样本宣传数字。真实压测依据 [正式报告](docs/evidence/2026-11-01-cap10/cloud-like-final-report.md)，只使用原已允许公开的资料，不修改原图/原成绩、不新增压测、不恢复已释放云机或扩大后端开发。
- 动手 agent 按本仓流程拆分必要展示任务并实施，完成受影响交互及桌面/窄屏检查；各场景可重置、重复体验，站内链接与部署路径可达。若现有实现不能支持某项陈述，记录差异反馈，不用模拟成功改写真实能力或反向调整简历。
- 完成后在此更新状态，交付部署页面与演示入口、四条各自的操作步骤/预期结果及实际验证记录；区分实现自验、已部署和用户验收。流转为 **待实施 → 实施与自验 → 可访问版本待用户验收**，最终由用户体验确认。本次简历 agent 只修改 STATUS，未改页面、代码、简历，未提交或发布；后续动手 agent 依据已有协作授权完成相应交付。

## 收尾后的项目展示

WEB-007访客引导已交付：提交 `4e50947e2032e2d84218396abb6a853ae22b7f13` 去掉演示页面中的面试相关称呼，首次进入弹窗明确桌面右侧/窄屏上方的操作步骤；开始后定位高亮，支持自由体验、Esc、顶栏重开及重置后再提示。1440/900/390宽定点浏览器与独审PASS，订单刷新恢复和存储禁用降级正常；正常推送后[Pages发布](https://github.com/tech-cove01/project-full-stack/actions/runs/42613555907) success。线上三尺寸首次提示、步骤定位、关闭重开、刷新保留订单及禁用存储买券全部通过，无脚本异常，等待用户后续体验反馈。

互动业务演示已交付：用户要求给HR/面试官可操作的业务体验，按[WEB-005/006规格](docs/showcase-demo-plan.md)实现 `showcase/demo/`。WEB-005提交 `8714c194e64a28e7961e9adc65c4a96abc0d7143` 完成顾客抢券/支付、店员核销、店主管理退款与审计、社区发布搜索点赞、五步业务引导和可重置示例；8项状态测试、三尺寸浏览器完整流程与独审通过，见[验收](docs/showcase-demo-verification.md)。WEB-006提交 `7fda2547f6e56b688058e41adbd1d9f5bffb1682` 将首页首屏主入口设为互动体验、三页导航加入演示入口，均正常推送；[Pages发布](https://github.com/tech-cove01/project-full-stack/actions/runs/42612434051) success。[在线体验](https://tech-cove01.github.io/project-full-stack/demo/)已在1440/768/390宽实走完整主线与取消/退款分支，刷新恢复、重置和存储禁用降级通过，无脚本异常或外部业务请求。不启动真实服务或云机，后续等待用户体验反馈。

首版反馈打磨已交付：WEB-003移除链接箭头、调整中文字体与字号层级，三页九组浏览器检查与独审通过，提交 `4f9d73272471cb786ee1bc88a8c278542023e4af`。WEB-004提交 `e0a23d9b495c62a5df364fe92cd5bc64b7c42c7b` 已将四张PTS原图移至实测页开头，以全宽切换展示，增加原尺寸查看；1440/768/390宽首图分别从355/323/437px处开始，首屏可见。新增截图交互、无JS、九组布局和48链接检查通过，独立差量审查PASS；两提交均正常推送，[Pages更新](https://github.com/tech-cove01/project-full-stack/actions/runs/42579024653) success。线上三页与七项资源HTTP200、首屏图宽1198px/顶部355.47px、报告切换与原尺寸查看通过，四图SHA保持。完整业务和全部原始指标不变，后续等待用户打磨反馈。

用户要求参考已打磨的 CRM Pages 制作本项目 GitHub 静态站，随后指定浅紫配色、完整功能介绍，并明确允许公开两张 PTS 概览截图中的报告 ID。按 [展示规格](docs/showcase-plan.md) 交付 `showcase/` 的项目概览、设计详解和压测实录；功能交付与 CAP-10 结果仍已收尾，不重开业务开发或压测。WEB-001 页面与素材已通过三页九组浏览器检查、48处本地链接/锚点、四图SHA及双独审，见[验收](docs/showcase-verification.md)，提交 `6d77fba61c73368c4bd7effbe89cc5b36dedec85`。WEB-002 提交 `b7b3ecc07857251e6166ba1eb745411773253662` 已正常推送，Pages [发布任务](https://github.com/tech-cove01/project-full-stack/actions/runs/42577230603) success；[线上展示站](https://tech-cove01.github.io/project-full-stack/)三页及七项静态资源HTTP200，真实浏览器样式与轮次交互通过，四张线上截图SHA与原件一致。站点仅发布 `showcase/`，不依赖原 ECS，原图、失败记录和本地简历材料保持原样。展示首版交付完成，后续按用户反馈打磨，不自动增加业务或压力轮次。

## 当前目标与操作状态

用户要求的**稳定至少3000成功变更/秒已在本批固定配置下通过两轮正式验证，且本批归档与精确清理已完成**。本次为点赞与取消点赞的状态变更，1996组独立关系；不再新增票据、压力轮次或云机请求。用户于2026-11-05确认原ECS已释放；此状态据用户回报记录，未通过ECS API独立核验，root不再SSH。两份PTS官方PDF、四张PTS页面图和一张ECS规格图已本地集中备份并逐SHA核对；四张页面图仅显示时刻/时间轴、无具体日期，符合当前展示偏好，无需改图。PTS材料留存不依赖原ECS，也不是实例释放的前置条件。[脱敏收尾摘要](docs/evidence/2026-11-01-cap10/cloud-like-closeout-summary.json)仅记录SHA、计数与状态，SHA=`fad3ccc22592845c2cad3a70f968a2d179041d7957fd9e0164283cec39a266ce`。

**当前检查点：S29候选及同freeze的H3/H4正式两轮全部PASS，不再发压。** 两正式各541499次成功变更/完整180秒=3008.327778次/秒，稳定值取低为3008.3278；H3 P95/P99=62/102ms、恢复1.654332秒，H4为55/94ms、恢复1.875416秒。主窗timing/unknown/drop等错误计数均0，unknown精确、DB计数一致，API、完整事实/派生、下游、资源和采集通过、childrenConverged=true。正式下游按原末60秒保守窗口，tail15未单独输出；不将其写成额外末15秒通过。原S28只保候选身份、H2 FAIL及unknown=null均不改。

新正式freeze SHA=`b9ad14447b94cc903b427f51b24e9db991cf046e8501d93545e267d35cfe70f1`。S29完整60秒180299变更、P95/P99=36/72ms、恢复1.637856秒，另有末15秒下游通过；它不替代正式两轮。[正式报告](docs/evidence/2026-11-01-cap10/cloud-like-final-report.md)及[脱敏原摘要](docs/evidence/2026-11-01-cap10/cloud-like-S29-H3-H4-summary.json)已据原件生成，摘要SHA=`5eb52fb30c1016336737bab98069519a6dc866add4c7dd8eadcfee6722da928b`。本批资源为单机16vCPU/32GiB，MySQL容器8CPU/8GiB、buffer4GiB/redo2GiB，Kafka2GiB，profile为mysql8-kafka2-v1；不把容器配额当整机规格，也不宣称最大容量。 负载由阿里云上海VPC内网的独立PTS四引擎发向被测单台ECS，四引擎不在被测ECS上；P95/P99为该私网HTTP主窗统计，不包含公网用户链路。留存页面中的监控图属于PTS施压端集群CPU、内存和带宽监控，不代表被测ECS利用率。PTS全报告汇总均值与原生180秒主窗成绩分别保留，不用全报告均值替代主窗计数或R7分位数。

H4后当前1996关系完整只读终审通过后，已审清理器实际success_close完成：当前1996会话注销后余0、credential输入删除，63个owned-env登记和8个upload登记对应目标均不存在，审计登记JSON保留。API/collector的8281/8381/8481端口关闭，四原容器停止且无OOM；MySQL/Redis exitCode=0、Kafka/Elasticsearch为143。释放前该次清理未删除主库、卷、网络及旧FAIL；用户原keys保留，未修改或删除。cleanup原件SHA=`114eb9a12bc1b4ef5263818dc3e03aa26fc1698b559e867b0ed94aa933f7fa93`；仅当前API两份活日志按原prefix不变的append-only保存最终SHA，其余原E证据SHA不变。

主395件增量包已下载并逐395个SHA核验通过，原文件总315397060字节，包SHA=`0b56c89dff5753ece53b407e19d5dbc176a3ef3ecfad667fa23522a53e96d9bc`、manifest SHA=`57dc4ae1633e10196f26b0bb62786487c86a9de863d035d1b30d6d01a385d5a1`；收尾22件小包亦已下载逐22个SHA通过，包SHA=`5d088ddad03fc8df1924f3ccaa07bf6a3f0bf29656dbb7fe86a4bece8cb7ed4c`、manifest SHA=`e476e06d8d8f66b52a16d3729f57916ebf2f387c290821af4dfa5f45a4b6ec3b`。它们依赖已核2736+1259+10份原件，是选定增量而非整机镜像；两正式共1082998条raw经独立流式重算通过。warm/preparation noop不计QPS，旧CAP-09/容量FAIL、H1/H2失败及B1未知均保持。

新boot最初只读实测buffer128MiB、redo requested/effective100MiB；随后已用两个明确SET恢复4GiB buffer/2GiB redo并验证，双1、delay0、RR、doublewrite、原UUID/版本保持，Kafka2GiB/MemorySwap3GiB保留。新资源metadata SHA=`472fa2e997bbc7d72501b4ebb0e63ddc337ab222c32c2485509664fc233c4057`，本boot恢复proof SHA=`d67eb9b4c0c0ba00fab5d850a4aaffd248d520b5c1bd9fcffcde7a60fb7425b1`；旧资源证明不改写，不借旧idle。原observer只读账号鉴权通过，没有重复DDL/CREATE或重建。10件authority/slot补包已实际生成、下载并逐SHA校验：包SHA=`256f19862c3aeb018f74cc826d5050db1d9b0a709c435fe2ed3518ca97e8bd3c`，manifest SHA=`4fbfcadd3b2b184f7a0a6413d400c2395f160ba8c98b5f8e7aeea091e52626a2`；补包采集时旧组未用的H3文件不存在，该历史记录保留，新组H3/H4另有真实原件。

本次API首prepare因label与已登记资源环境同名，在Popen前被`FileExistsError`拒绝；原stderr为空、无API或服务端口占用的核验保留。以明确新label和独立attempt2接续，实际ready result SHA=`2695341b4a286b61ba16774727358588870a691a013b9a4c784b5d5677e9f42e`、API metadata SHA=`cf622d29a8b6b7ea930209476fb39a0449fa40807f6c11296e0bd8a7b9dc730e`。业务仍为c464/4565原二进制，当前observer为9502产物，首次失败不覆盖。

旧H2的两报告真实EndTime及全部35个登记owner的PID/birth退出已重新核验；本boot恢复后的214421 events完整facts/derived/downstream对账通过。失败组闭合proof SHA=`21be111aaba1284a5f4e8522e2179c3bda305d235cfc9f99dfdab3ba6b8ffbb8`已实际登记，旧freeze原字节归档保留。它只支持新资源/部署的接续，不将H2改成PASS或retired、不将原unknown=null改成0，也不允许用旧freeze的H3补pair。

11-05凌晨暂停历史：用户已保存新JMX，要求约10分钟后关闭ECS，停机准备截止前未再发压、Start或新API。项目停服和增量证据下载完成后，已告知控制台“停止→节省停机模式”；当时未独立查询ECS停止，当前新boot证明确有主机重启，不追写旧查询结果。

凌晨停机回执SHA=`1eed7539a4a9ad329c137b4e6c7652bd7f5740535ef26a30ffbbf699e571062b`：当时API端口关闭，原四容器均已退出、无OOM；MySQL/Redis的exitCode=0，Kafka/Elasticsearch为143，保留原信号退出码，不写成全0。原CID/卷保留，未删除会话、夹具或数据卷。本地私有增量包69101423字节，SHA=`5623ed8cdeebf10712f8fc68e79fd36460ecf455ebb2159f2d28601dc55a818e`，manifest SHA=`8ad2d5fa8eec5f66a708362423d9b6060810f18255c3a06c71b4cfec6eccaf9b`；独审逐一核1259文件的bytes/SHA、成员集合及路径安全均通过（原manifest另占1成员）。这是选定增量，依赖原2736件基线备份，不是整机镜像；当时未执行的authority/slot小补包已在本次恢复后补齐，如上。私有归档不进入公开仓库。

释放前清理时，主库、卷及失败原件按既有范围保留；前次boot API H曾正常停止，本批正式使用新boot API及新空载证明。本批停服与清理的历史回执保留，原ECS已据用户回报释放，未独立核验关联盘是否随之释放。现场现已无法经原实例接入；已有备份为选定证据增量，未做整机镜像或MySQL全库备份，不推断磁盘已删除或费用已清零。上下文压缩或接续后先读AGENTS、[授权记录](docs/collaboration-agreements.md)、本页、[方案](docs/implementation-plan.md)及[交接](docs/agent-handoff.md)，以顶部不再加压、归档清理指令为准，不重复索取已有授权。

**前次boot负载检查点：** S28配置3010×60秒的完整候选已实际通过：180299次成功变更/完整60秒=3004.983333次/秒，P95/P99为13/52ms，timing/drop/unknown及HTTP错误均0；事实、全窗及末15秒下游、资源和采集均通过，恢复4.732779秒、`childrenConverged=true`。这是候选结果，尚非正式稳定容量。正式freeze已实际创建（SHA=`aac3026375430c276761bcee6b23bd24751f7195dbfa6a7fc1d22f4b4d712230`），H2按同freeze配置3010×180秒，但已实际中止失败，旧freeze下未启动H3补pair。最终c464 API、1996关系/4引擎/2000VU及原门槛保持，当时稳定3000两正式尚未证明；本批新组结果独立见顶部。

S25在pool128、consumer提交批64下已中止：原结果为`correctness_stop`、`batchFatal=true`、`childrenConverged=false`，保持原值。已接受raw中有22068次mutation及1次drop；这不是完整窗口，不计算QPS或P95。一次实际成功HTTP耗时719ms，超过同relation约663ms的调度周期，下一次来不及启动而drop；后续overlap拒帧及原unknown缺失保留。被拒batch未保存完整body，不能推断为“假overlap”。

S25独立真实闭合已通过：原1996关系完整reconcile、两个签名provider EndTime及全部旧PID不存在均已核实，备份原owner字节后仅释放该锁。它不修复旧成绩、不将原`childrenConverged=false`改成true，也不新造terminal。后续S26以同c464、pool128、consumer32、Like诊断开启配置完成3010×60秒诊断：180299次成功变更/完整60秒=3004.983333次/秒，P95/P99为216/333.02ms，drop/timing/unknown均0；API及下游预算失败，原结果为`performance_limited`。事实收敛、原`childrenConverged=true`、恢复6.038893秒及负载闭合均已核实，不是正式容量成绩。

前次boot曾在同一MySQL实例在线将redo容量100MiB→2GiB，配置及验证执行约1.7秒，requested与effective均为2147483648、resize状态OK；没有重启、持久化配置或放宽双1。原诊断API已正常停止，UUID/版本文件只读补证通过，原resize证明不改写。新1996关系夹具及同维度新包已准备，三上传MD5与旧包相同且新namespace只读验证通过，无需用户重新上传。诊断关闭、pool128/consumer32最终API及新60秒/13样本空载通过（CPU中位约2.7%、内存最高约15.9%）。S27实际在warm 250次成功变更后、stable启动前失败：setup原60秒内未匹配同事实摘要，`correctness_stop`、`batchFatal=true`、`childrenConverged=true`；最终facts/derived/downstream对账通过，但没有stable主窗或其资源样本，不能报告主窗QPS/P95或redo收益，该次未进入正式窗。

根因已用原事实文件只读闭环为observer顺序错误：Node和ready摘要按manifest顺序，Go live摘要按内部NoteID词典序；新夹具ID跨十进制位数时，两种顺序不同。在初始版本全0、控制摘要一致且事实未变时，原manifest顺序hash精确匹配ready，词典序hash精确匹配live。原S27 FAIL不追认。顺序修复`b461ec6e9ef60d88176fc199e0bc853cec1e5a66`已提交、正常推送并部署新observer；业务API c464及其原二进制不变，不把API的c464 CI说成新工具CI。

`CAP10-PRESTABLE-CLOSURE`公共闭合入口已本地提交`bb9b0cd85627f622d10eb504bb1aebfc1747abb4`，8项聚焦检查及独审通过，Git bundle已实际部署。原S27布局的只读检查、真实warm签名EndTime/全owner退出和失败后新鲜完整对账均通过，S28原完整authority已实际通过；不回写S27 FAIL，也不造stable报告。新observer空载通过13样本（CPU中位约2.75%、内存最高约16%），实际14条摘要保持manifest顺序一致。S28完整候选通过的[脱敏原摘要](docs/evidence/2026-11-01-cap10/cloud-like-S28-summary.json)已下载并核SHA，保留candidate身份、完整分母与原grade，不改成正式成绩。

H2预热250次成功变更后进入主窗，实际supervisor停止由Kafka容器1GiB内存达到85%保护线触发；在此之前约native第4.7秒，PTS已有4条独立调度违规，偏移为50.10/52.40/54.18/52.87ms，超过原50ms门槛，不能全部归因于后来的Kafka内存停止。已接受raw为32463次mutation、drop为0，但unknown/classificationUnknown为null且非exact；未完成180秒，不计算正式QPS或P95/P99。原结果`correctness_stop`、`batchFatal=true`、`childrenConverged=true`、`factsConverged=true`、`failedStartOrException=null`保持，最终facts/derived/downstream对账通过，不抵消正式失败。Kafka未OOM，未用旧freeze的H3补pair；本批H3/H4是新组。

前次boot的Kafka配额修复已实际执行并验证：同CID内存1GiB→2GiB、MemorySwap 2GiB→3GiB，保留原额外1GiB swap allowance；变更时四依赖PID未重启，原mysqlActual与redo2GiB证明保持，随后按暂停指令正常停服。显式profile为`mysql8-kafka2-v1`，原资源metadata SHA=`0319e92dc2a89014f985629f5e83261c411ba523e36611b899516cbcf271b298`。它只证明当时运行时资源变更完成，不能称已解决调度抖动或取得容量收益；本boot资源/API/空载的实际状态见顶部。用户已保存的新JMX及另两上传文件，已在本boot新packet上再次经真实Get-only三MD5核验通过；JMX SHA=`1c81410efde56af9e034508657d2f582626ca57d2b1bb569fbd8130a480003b1`。旧H2四引擎INFO日志各只取首100行且total更大，无启动/GC命中；不能据部分日志排除GC或认定抖动根因。

远端同步：`b461ec6`及`bb9b0cd`均已实际正常推送；`bb9b0cd`先前多次HTTPS TLS EOF原错误保留，随后通过既有SSH同仓库推送成功，未改全局配置、不强推或覆盖历史。既有同目标推送授权保持，不重复向用户索取授权。

后续工具票已审查、提交并正常推送：Kafka资源合同`fd2d312`、生成器endpoint缓存`ac5752d`、整数校验分配优化`9743ad1`及原失败正式组接续`2b40202`。本次[跨boot接续](docs/evidence/2026-11-01-cap10/formal-group-supersession-plan.md)的13项聚焦、89项原检查和独审通过，公共提交`3bf1f87`已推送；生成报告的原CRLF版本私有保留，仅LF格式修正`4ab583d`已推送，源码不变，云端工具HEAD已同步`4ab583d`。私有三源副本经独审、4项纯检查及实际shape核验接入新boot，原八助手字节不改。新闭合登记不创建新freeze或预约；仍须完整新candidate通过，不重跑旧freeze的H3补pair，也不将H2改成退休/PASS。

两项准备失败原件保留：BOOT首FAIL的瞬时原因未精确取证，不补称确定原因；后续同boot、原CID/事实、账号认证与权限的只读复核及显式最终化通过，不重跑resize/CREATE。authority准备的H1/S22前后报告同名`FileExists`已精确定位；原H1退休证明经实际public grade通过，以同助手显式新attempt完成准备，不重复assembler/API/idle。八个助手源码字节未改，恢复通过不改写首FAIL。

pool128显式接线使用独立三源副本，只有authority的两处报告路径加入原command标签以避免同名，其余两源字节沿用原件；原八助手不改。准备时将控制台紧凑JSON的SHA当作原pretty结果文件SHA，已在写工作目录/inputs前拒绝；之后使用实际文件SHA经原有CLI接续，未改原driver源码或复写失败。

## 本次仓库整理

| 小票 | 范围与验收 | 当前阶段 |
| --- | --- | --- |
| R1 / REPO-ARCHIVE-WINDOWS-BUILDS | 14个已跟踪Windows构建产物移入archive，逐文件SHA不变；不改Go源码 | 已独审提交`579f12fbb4cb74aeabd43724230875f015916281` |
| R2 / REPO-ARCHIVE-CAP06-08 | 12个旧CAP-06—08工具整组归档，原字节和内部相对关系保留；CAP-10工具不动 | 已独审提交`a98a0642953b6eeda1bfa41783578b089aa69e40` |
| R3 / REPO-DOC-NAV | 根状态、交接与文档导航收敛；完整历史归档，重算相对链接并保留旧锚点 | 277个本地链接及6个锚点核对、差异检查和双独审通过，提交`fc7716a2383dd9dbacc01ad683cb6cc20403684b` |
| R4 / REPO-SOURCE-MAP | 校准26个后端模块与前端route/view/API阅读路线，增加后端、前端、脚本和归档目录入口 | 源码路线112处链接、目录导航81处链接通过，双独审提交`ce3c3e549bbf44f195197588b933f48efac149a3`；未重排包或改业务 |

产品首页README继续只展示项目、功能、技术与运行方式，不加入状态或压测入口。归档不移动[原始证据](docs/evidence/)或[核验报告](docs/reviews/)，不改历史FAIL、命令、原件SHA及CAP-10固定路径。

## 已交代码、工具与最终产物

- 业务源码为`c464c1fa4d153806fb44ac746312552655ebda63`，已正常推送；全套普通Go测试、vet及[精确四项CI](https://github.com/tech-cove01/project-full-stack/actions/runs/41828027736)通过。
- Like现有关系变更的业务SQL实测6→4，BEGIN/COMMIT/隔离SET仍各1。公共正确性2顶层/16子例通过；首私有计数整矩阵FAIL的93件原件保留，独立接续8次计数通过的87件原件另存，不合成一次全通过。机制与计数见[Like事务规格](docs/evidence/2026-11-01-cap10/like-tx-rpc-plan.md)。
- Done点驱动`aeb0c96`已交N≥8路径，N1—7保留原路径；32条事务内完成语句32→3，COMMIT仍一次。无符号计数clamp由`f32bc31`单独修复。相关真实MySQL回归与独审通过，组件改善不等于3000容量。
- 生成器三票`a4ca69b`/`c061153`/`09053eb`已交同线程同HTTP节点的独立noop准备、持久确认及Start前协议绑定。17关系/3个本机JMeter进程与准备503反例通过，属于替身接线；S23真实四引擎已完成准备与主窗，timing为0。每phase新增1996次noop，warm+stable合计3992次单列成本，不计入成功变更吞吐。
- 最终Linux API实际版本为`capacity-c464c1fa4d153806fb44ac746312552655ebda63`，SHA=`4565c2422d24ac5ea34ac180890ec80ff767e8084d765f4eb9a809d9078516d1`；正式manifest SHA=`3f8f50b9cd5e7ca28c50a0e8d3fda030778ad6f3df05d628f69db0e491b1e9a2`。原始成功CI响应SHA=`e8cb25f9873cefd54529e0444f9febd76a2553efc2f33d20e424d43b4e626d05`。首产物b5d6缺capacity版本前缀、1b877不是部署schema，均拒用并保留；只重建产物，未重跑已通过的测试。
- S23—S27原observer为`5c6c8e5`单文件产物，SHA=`3fc4631973775a68878e4e00af8de5a3f5d7a6ef5e222319a53b4b1642591b28`，旧原件保留。S28改用顺序修复后的单文件observer，SHA=`b83da5965ce71453fbe068a342f107f02d42eff056c46cd7bb8aac055b15f1d6`；单文件无内嵌VCS，工具源/实际产物另绑，不将c464 API CI当作新observer CI或容量收益。 当前Kafka2GiB profile所用observer SHA=`9502ae36bc13e094d39c3d21924a5deae88670024a5e7f5ff86661159d8bd26d`，原API二进制不变，当前空载通过不作吞吐结论。
- 八个私有助手v2、九成员源码包及RUNBOOK/五请求模板已离线验证并独审；source set=`8047f15a25f7448890ee781374f6d64de1badef72657e48e127dc0061a52b1e7`。真实boot、旧清理回执SHA、4GiB本次完成证明、最终API环境、新会话、三上传文件及新空载均已有实际原件；八助手字节保持不变。首次失败原件不因显式恢复通过而改写。

## 容量与费用边界

S20—S22实际成功变更速率为2995/2396/1597.333…每秒，均为`generator_limited`、`boundaryEligible=false`。业务正确、unknown/drop=0不抵消时序或下游增长失败；当时正式两轮180秒未跑，该旧阶段没有有效正式稳定QPS，H2原FAIL保留；本批新组正式通过见顶部。旧H1失败、CAP-09失败与旧B1未知均保留，不跨窗口或资源拼PASS。原始数据与限制见[接续规格](docs/evidence/2026-11-01-cap10/cost3000-continuation-plan.md)及[改动与收益档案](docs/evidence/2026-11-01-cap10/offline-improvements.md)。

S23全窗search lag为2027，超过预算1653；末15秒三组收据增长922/950/1237、Kafka lag为631/708/1042，均超过各自预算301.56。原S20的P95/P99为268/358ms、timing134，新S23为266/355ms、timing0：生成器时序问题已在本窗消除，但延迟和下游仍受限。两窗源码、夹具、包及boot不同，不能把SQL6→4或该对照写成容量提升倍率。

S20/S23脱敏原摘要见[云测摘要](docs/evidence/2026-11-01-cap10/cloud-like-S20-S23-summary.json)，保留原grade、不重采样。S24的API预算虽过，候选余量与下游仍失败：全窗三组收据增长5476/4804/5722、Kafka lag为5317/4677/5499，均超各自预算1655.36；末15秒收据增长2952/2728/3240、Kafka lag为2372/2212/2596，均超各自预算301.38。S24详细数据见下方原摘要，不用观察到的延迟差异替代稳态结论或推算QPS倍率。

S24/S26的[脱敏原摘要](docs/evidence/2026-11-01-cap10/cloud-like-S24-S26-summary.json)已从实际输出复制并核SHA一致，分别保留candidate/diagnostic身份、失败grade及provider VUM，不将诊断窗口改成正式成绩。

本批配置3010，以完整60/180秒分母计算，原计划发起180299/541499次；本次实际成功计数吻合，分别为3004.9833/3008.3278次/秒，稳定值只取两正式较低值，不再发起后续负载。原能力公式需1890关系，1996关系/4引擎/2000VU可容纳该估算，不保证容量；原50ms、零unknown/drop、候选160/400ms余量、正式200/500ms及下游稳态/恢复门槛保持。

已购60000 VUM。此前已核29295（含S27预热950及旧H2的2053），本批S29为1080+2623=3703、H3为1228+6571=7799、H4为927+6669=7596，共19098，累计48393。相减11607为**未独立查询账户的计划余量，非账户余额**；中止窗口不记为零成本。准备noop、warm、屏障及平台生命周期消耗单列，不按主窗时长猜账单；主机费用另计。

## 配置试验与下一小票

CAP10-POOL128已实际完成：仅连接池64/64→128/128，consumer32保持；新deployment/进程及新空载通过13样本、CPU约2.6%/内存约13.3%，随后S24结果如上。记录这次配置试验的实际观察，不能宣称它已取得稳定容量。

CAP10-CONSUMER64曾保持pool128，仅consumer提交批32→64，源码、GOMAX8、relay8×32、原资源与双1持久化不变。S25原中止与独立闭合分别保留，不能由局部成功计数得出收益或容量结论。

CAP10-REDO2G仅调整同机`innodb_redo_log_capacity`，保原4GiB buffer、8CPU/8GiB MySQL、io_capacity 10000/max20000、双1、delay0、doublewrite、源码c464、relay8×32及consumer32。S27因工具错误未进入stable；S28换修复后observer、独立空载/准入后完整候选已通过，但旧H2正式失败、未接旧freeze的H3；本批新组正式通过独立见顶部。S26诊断开启、S27关闭且换新夹具，S28又修复observer，不能将跨窗差异写成纯redo倍率。机制、实际配置与限制见[redo试验记录](docs/evidence/2026-11-01-cap10/offline-improvements.md#11-05同机redo容量试验收益待验证)，工具闭合边界见[规格](docs/evidence/2026-11-01-cap10/prestable-closure-plan.md)。两轮各180秒及所有门槛不变，不拿候选替代正式。

## 下一步

1. R1—R4已完成必要核对、独审及分票提交，均已正常同步远端main；原始文件和历史完整保留，不重写Git历史。
2. S23/S24失败、S25原中止及独立闭合、S26诊断受限与闭合、S27工具setup失败分别保留；不重判旧FAIL，不为工具修复重新构建未改的业务API。
3. 本批S29与同freeze H3/H4已完整通过，不再发压；旧S28候选和H2正式FAIL、所有旧unknown及原件保持，不运行旧freeze补pair或新增轮次。
4. H4后fresh终审、主395件及后补22件下载逐SHA核验、success_close精确清理和后验状态复核均已完成；释放前该次清理未删除主库/卷/网络及旧FAIL，审计登记索引已保留。用户于2026-11-05回报原ECS已释放，未作ECS API独立核验，不再SSH。
5. 正式报告与原摘要已提交并正常推送`30073a1d26901924a5d2bf5af8e9ba532dd2fe2b`；本轮只补真实闭合及私网口径，不改摘要字节或旧结论。后续引用仅使用两正式较低值、两轮原P95/P99及明确资源/负载模型，不新增本批任务。
6. 已审小提交继续按已有授权正常同步，保留原TLS失败，不重复询问已有推送许可；当前工具已同步`4ab583d`，原私有freeze producer保留，新副本只在新candidate完整PASS后按显式选项使用。

## 权威入口

| 要查什么 | 入口 |
| --- | --- |
| 持续约定与具体授权 | [协作记录](docs/collaboration-agreements.md) |
| 当前执行交接与小提交规则 | [agent-handoff](docs/agent-handoff.md) |
| 产品/模块总方案 | [implementation-plan](docs/implementation-plan.md) |
| 新boot步骤与当前限制 | [接续清单](docs/evidence/2026-11-01-cap10/offline-resume-checklist.md) |
| 优化、未采用研究及证据 | [收益档案](docs/evidence/2026-11-01-cap10/offline-improvements.md) |
| 当前产品与源码阅读 | [文档导航](docs/README.md)、[源码路线](docs/source-walkthrough.md) |
| 历史票据与当时指令 | [状态快照](archive/docs/status-history-20261104.md)、[交接快照](archive/docs/agent-handoff-history-20261104.md)、[功能交付快照](archive/docs/go-delivery-history-20261104.md) |
