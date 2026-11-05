# CAP10-FORMAL-GROUP-SUPERSESSION

2026-11-05。保留已经真实启动的失败正式组，允许在明确资源变更后开始一组新的正式两轮；不修改旧成绩，不复用旧组剩余标签补成通过组。

## 问题与边界

S28完整60秒候选通过，随后H2正式窗因Kafka内存保护中止；更早的四条PTS调度违规是独立失败。H2未完成180秒，unknown并非精确0，原失败与全部原件保留，H3没有启动。原`formal_start_retirement`只接受零native启动失败，不能用于真实运行过的H2。

新增独立`closed_failed_formal_group`证明，含旧freeze原字节归档、该freeze下全部原command/stage/result、两phase的原Start和真实签名EndTime、全部登记owner退出、原stable完整journal及失败之后的新鲜完整SQL/Redis对账。它只证明失败组已经闭合，不返回退休、容量或恢复通过。旧H1继续原零native退休入口。

本票只允许明确的`mysql8-v1`→`mysql8-kafka2-v1`资源接续：同容器/boot下Kafka内存1GiB→2GiB，额外swap allowance保持1GiB。新metadata及实际资源证明必须逐SHA绑定，其余依赖、MySQL事实和redo证明保持原值。新deployment和环境身份必须显式不同。实际资源变更、云调用及新候选结果不由本票构造。

MySQL只逐项冻结原14个配置/身份字段及4GiB buffer、resize code/text和空闲条件；uptime须单调不减、resize progress只验原合法范围，不能把动态读数当成必须逐字不变的配置。首版源码审查发现的uptime整对象比较已在真实资源变更前修正，旧freeze保留；正例包含uptime增长，负例保留倒退、busy及durability漂移。

## 两阶段合同

闭合证明可在新candidate之前产生，只绑定已知的新deployment ID、环境文件与实际Kafka变更证明，不要求未来candidate或预知active SHA。原operator在`check_reservation`前已发布active；验证器在此读取并比对该真实active，不产生预约。新candidate仍须原独立终态、完整事实、空载、资源与全部候选门槛。

证明固定放在`formal-group-supersessions/<旧freeze SHA>-<新deployment ID>.json`，原freeze字节放在`freeze-history/<旧SHA>.json`。私有登记器默认只读；显式写入只归档原freeze并登记已采集的引用，不发布active、不调用API或SQL、不预约。登记器的prospective context只校验显式拟采用身份，不能当作API已健康或新candidate已通过；实际operator仍须完整发布和验证active。

只有新candidate完整通过，新的freeze生产器才可替换旧freeze。原`create-authority-freeze.py`保留不改；新副本通过显式选项接受已闭合失败组，并沿用原CI、当前API、原候选计数/分位/下游/恢复检查。新freeze记录`closedFailedFormalGroups`，后续两command的reservation必须保留同一映射，不能中途换证明或把旧H2纳入新组。

每个精确freeze最多两次正式预约，第一轮失败即终止该组；新组使用实际下两个未用H标签，若H3未占用则为H3/H4。累计历史、slot、预约秒数和原执行authority全部保留，不重置预算或失败次数。完整180秒、200/500ms、零unknown/drop、原50ms、下游稳态、完整事实和恢复等标准均不改。

## 验证与限制

新增聚焦合成检查覆盖真实native失败不能走零native退休、缺闭合原件拒绝、签名报告/owner/完整审计/资源链错误拒绝、新组两个未用标签及首FAIL终止、两次映射一致和累计预算不重置。仅本地纯检查；真实H2闭合原件、新资源变更、新candidate及新正式组均由实际执行另行提供，不用合成夹具冒充云端准入。

本地直接验证：新增7项聚焦检查通过（最终2.193秒），原H1退休/同freeze两轮/首FAIL终止的3项选定回归通过（2.528秒），新私有producer的3项默认只读、显式开关与原字节保留检查通过（最终0.400秒）；AST检查通过。最初缺入口的RED保留，未重跑整套旧matrix。以上均为合成文件检查，不是实际H2闭合、新资源已准入或新正式成绩。

## 原机新boot的严格接续

运行时buffer与redo配置不能借用旧boot证明。新增显式`same-containers-new-boot-v1`桥：先逐SHA复核旧失败组及原Kafka资源变更，再独立绑定旧资源metadata、新boot健康服务恢复前后快照和恢复证明。原metadata与失败原件不改写；新metadata保留原`mysqlActual/redoActual/kafkaMemoryActual`历史段，以`bootRestoreActual`另绑当前boot的实际证据，不将旧idle或旧资源快照当作本次准入。

纯接口`boot_restore_identity(root, metadata_reference)`只核此独立资源桥并返回原parent ref、当前boot及恢复完成时刻，不读取active/candidate。私有资源policy复用同一规则，再验证父环境原redo/Kafka证明；正式组入口另保留完整旧H→I验证和原失败组闭合，不能把该纯返回值当作预约或候选通过。

跨boot保留原CID、镜像、完整挂载、卷、网络、配额、SQL UUID及14项固定事实。恢复前只接受已知空闲状态的128MiB或4GiB buffer、100MiB或2GiB redo；恢复后必须实际4GiB buffer、requested/effective均2GiB且状态OK。新boot内恢复前后PID、启动时刻和restartCount必须一致；与旧boot的PID不要求数值不同。新API、60秒/13样本idle及完整fresh audit仍独立执行；闭合原H2夹具的audit须开始于本boot恢复完成后，不能借旧boot对账。

仅在比较副本上将完整mount字典按canonical JSON排序，保留重复项及全部字段，不修改raw。真实历史快照曾仅因MySQL mounts顺序不同而被原严格列表比较误拒；容器摘要的`volumes{name,destination}`也按实际完整mount的`Name/Destination`精确对应，不把两种schema直接比较。卷名、目标、完整挂载字段、重复项或网络漂移仍拒绝。

本次聚焦检查扩为13项，并增加私有producer的新boot默认只读检查；另用已封存的原H→I五份资源原件离线验证该资源门及重封错误卷目标的拒绝。它只核历史资源原件，不代表真实新boot、完整失败组或新候选已准入。原同boot失败、原RED和检查准备失败记录保留；新producer副本继续原两阶段合同，无新candidate PASS前提循环。新candidate通过后才创建新freeze，所有原预算、未用标签、首FAIL终止及正式门槛保持。
