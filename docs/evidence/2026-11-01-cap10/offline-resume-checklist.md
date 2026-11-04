# CAP-10：离线收尾与同一云机接续

2026-11-03历史快照，当前以以下11-04接续和[STATUS](../../../STATUS.md)为准。功能版已收尾；本阶段只测真实点赞写入，不补Feed、收藏或集群功能。当时用户确认同一ECS节省停机、保留原卷，未自动连接、开机、应用资源、预约PTS或发压。

2026-11-04当前接续：费用优先的3000档已执行S20，用户自行购买3万VUM×2，共6万VUM、119.80元。全新1996/4/2000包已同步，API15f ready，三MD5/仅CSV SplitCsv/2000/4真实准入PASS。S20主窗179700次mutation全部成功，按完整60秒为2995/s，unknown/drop=0、P95/P99=268/358ms；但调度违规134次，下游full/tail增长超1%预算，stage=generator_limited，无稳定3000成绩。恢复4.497616秒，资源/时基/采集/业务终审通过；batchFatal=false、boundaryEligible=false，不能归因单一后端瓶颈。S21的2400/s×60秒candidate已完成新ability/admission与预算核对，首次全包络估耗9151/60000 VUM；本记录截至Start前，待启动、候选未PASS、正式未跑。见[费用接续票](cost3000-continuation-plan.md)。

本次窄修复`CAP-10-PROFILE-STOPNULL`：原Linux停止结果可能记录`exitCode:null`。资源预检只在同label原API metadata与cohort/PID/有限正birth严格绑定时识别该结果，仍检查原API真实PID与创建时间；不能凭null或旧stop文件推断进程消失。未知结果、缺伴随记录和活owner继续阻断，旧整数退出结果规则保留。此修复不改变资源/压力预算或业务确认语义。

独立小票`CAP-10-PROFILE-MOUNTORDER`：STOPNULL后真实只读捕获因Kafka完整挂载列表仅次序变化而误拒，原FAIL保留。仅规范排序比较副本，raw、所有字段和重复项不变；身份或数量漂移仍拒。53项直接检查、独审及真实只读捕获通过；该票当时停在发压前，现已依用户继续指令接资源应用与当前S20。

11-04较早停止点（历史，现已继续）：两修复`4597d43`、`a02abe1`推送部署、真实只读捕获及7原件下载SHA通过；当时MySQL4CPU/2GiB/128MiB且API关闭、无新预约。当前已真实应用同CID/卷的mysql8-v1（8CPU/8GiB/4GiB），原swap/持久化保持；`669e36d`、`0ce8d1a`工具修复推送部署，API15f/observer5c6原产物不变。attempt2真实60秒/13样本PASS，CPU中位2.65%、内存最高10.4%，15原件下载逐SHA核验，原0样本FAIL保留。preflight实际观察旧4096范围，当前1996只按同资源metadata/SHA/profile显式引用；不改写idle或借旧业务基线。

## 固定候选与已完成准备

- API：干净提交`15f8eb558498455b033e193182e4394cec75f591`，ELF内实际版本为`capacity-15f8eb558498455b033e193182e4394cec75f591`，二进制SHA256=`f29fe6a9c76d54a5ddd1cbd8b3ae143c53a4d348176b1a98bd7b7291949b4499`。精确[四项CI通过](https://github.com/tech-cove01/project-full-stack/actions/runs/41576389620)。
- observer：干净提交`5c6c8e5c6ede16f493dfd55b4ba2c7a90a38ba67`，二进制SHA256=`3fc4631973775a68878e4e00af8de5a3f5d7a6ef5e222319a53b4b1642591b28`。单文件构建不内嵌VCS，使用原Go源及模块SHA绑定；精确[四项CI通过](https://github.com/tech-cove01/project-full-stack/actions/runs/41577337428)。
- 工具代码：`e53534f33b368bb26e246ae0f0cc5ac2e48395fe`已含[阶段去重](phase-audit-plan.md)，本次补至`669e36d`/`0ce8d1a`并真实部署。实际工具HEAD/全部脚本SHA与API/observer产物分别绑定，不复用旧active/freeze/准入。
- 当前配置：MySQL同CID的`mysql8-v1`，8CPU/8GiB与Buffer Pool4GiB；API GOMAXPROCS8、SQL池64/64、显式参数插值、relay8路/批32、consumer批32，flush1/sync1和ACK保持。当前全新1996关系/4引擎/2000VU、dedicated-v1与128帧；原4096/9/4105方案保留为已造未压范围，512费用备选仅本地准备。这些配置不是3000容量保证。
- 七个私有准备助手已有独立1996副本并实际同步，配置/夹具/新包/最终注册与真实准入分别留证；不覆盖原release、4096或512备选。凭据与用户云资源标识不入公开仓库。

## 真实执行顺序

1. 已完成：用户恢复同一ECS，核SSH来源、实例/原卷/四容器身份；原工作树/12项工具修改及014同字节迁移归档保留。实例与卷未另建，运行和费用状态以用户控制台为准。
2. 已完成：部署精确工具与API15f/observer5c6原产物，实际应用mysql8-v1并保留前后证据，持久化不变。显式空载attempt2 PASS，原FAIL、退出closure及成功原件逐SHA下载校验。
3. 已完成：原4096夹具已建且未发压；费用调整后另建1996夹具，旧H1/512独立保留。新H1退休proof首次误引latest日志SHA，现仅修回原collector-terminal引用并经公共严格全验PASS，原H1 FAIL/unknown=null及30 raw不改。
4. 已完成：新1996/4/2000 namespace的JMX/CSV/私有配置由用户上传保存，三MD5与SplitCsv经真实接口准入PASS；停原API并按新包最终登记环境重启API15f ready。参数组装绑定实际启动器的环境注册，不借旧alias。
5. 当前终态：同资源metadata/SHA/profile显式复用attempt2 preflight，4096观察范围保持；新1996进程/夹具/包与逐窗基线另验。S20在60秒完成179700成功变更、unknown/drop0，但134次调度违规及full/tail增长超1%预算，generator_limited；资源/时基/采集/业务终审通过、恢复4.497616秒，仍不能判候选通过或有效后端边界。
6. S21启动前快照：2400/s×60秒candidate准入与预算核对完成，新的ability/admission已验证，待Start；不改源码、PTS包或门槛。完整通过候选后才同版本/同资源/同4引擎包正式两轮180秒。每轮前核实际VUM，不追加主题/整组，旧失败、用量与slot不重置，原计量/正确性/延迟/下游/恢复门槛保持。
7. 收尾待执行：分别报告实际成功数/完整时长、P95/P99及错误/unknown/drop/timing、积压与恢复。证明停压/退出后下载校验原件；旧H1/512、未压4096、当前1996三cohort各精确正常登出，其中4096须在最终停API前单独finish并核自有会话0，再按登记清理上传副本。保留用户密钥、卷与全部失败，由用户处理实例停机或释放。

## 开机前必须知道的测量边界

此前完整下游通过的S19为800/s×60秒；S16的1000/s接口通过但下游受限。新S20实际mutation速率2995/s，调度违规134次且full/tail增长超预算，generator_limited、boundaryEligible=false，不能作稳定3000或单一后端边界。正式180秒成功组仍无，5000未测。组件收益与窗口值不能跨条件合成提升倍率，见[档案](offline-improvements.md)。

完整审计唯一大历史观察是本机1CPU/4GiB、2GiB缓存下1225500事件耗时106.077秒，离120秒约13.9秒；造数hash无效且缺事实，业务对账失败。当前3000/s每180秒名义新增约54万事件，候选加两轮约126万，另有预热/normalize；累计审计预算仍须实际验证，不能由该本机成本外推。原5000方案约90万/180秒、累计200万以上也从未证明；8CPU不保证单SQL同比提速，初始E接近0不消除全局projection/失败扫描。超120秒须保留首FAIL、真实会话退出及未完成字段，报告测量受限，不改恢复时限或把不完整审计判容量通过。

N2真实SQL/Redis→Go factory、Go摘要golden与阶段故障检查已过；当前S20真实终审也通过业务正确性，但调度与增长门槛未过，不能将组件或业务对账通过等同稳定3000。后续候选与正式仍待执行/判定。Done点更新分档继续暂缓、未采纳，本次不补功能。
