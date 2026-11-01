# CAP-09：完整 Feed 与真实点赞性能收尾

2026-11-01，实现者自测已结束。**完整 Feed 在本机 50 requests/s、180 秒两轮通过；真实点赞只有 20 mutations/s 的 45 秒短测通过，20/s 正式 180 秒组第一轮早停，整组未通过。** 不再追加阶梯、热点或补成功轮。这里的非作者源码/原件审查不等于在独立环境复跑；CAP-09E 的独立代表档尚未执行。

## 最终版本与复现边界

- 两条最终链路均运行干净源码 `7a80186b568b521c54736b735c08dce8ef962e19`，Go 1.27.0 Windows/amd64。二进制 SHA256 `9f70db9ad869eaac4935f714c00980f3e5c173404fe584c9b725fdb943405fd7`，`vcs.modified=false`；见 [build-b.json](build-b.json)、[final-application.json](final-application.json)。后续报告/清理工具提交不是被测应用版本。
- 精确该源码的 [CI 四项 success](https://github.com/tech-cove01/project-full-stack/actions/runs/41281366121) 已归档为 [ci-b.json](ci-b.json)，包含 Linux race、真实依赖、前端和 Java 历史检查；这不代表在 Linux 执行本轮容量测试。
- Windows 笔记本，20 逻辑 CPU、约 32GB RAM，Docker 约 16GB；与既有 CRM 容器共享宿主。本轮独立 Compose、端口、数据、合成会话。MySQL 4CPU/2GiB，Redis 2CPU/512MiB、AOF everysec，Kafka/ES 各 2CPU/1GiB；Kafka heap256–512MiB，ES heap256MiB。API `GOMAXPROCS=4`、`GOMEMLIMIT=1GiB`、SQL 池20/Redis 池10、worker1。完整冻结配置见 [environment.json](environment.json)。未更改持久化、一致性和正式 200ms/500ms 延迟门槛。
- Feed：真实鉴权 `GET /feed/notes?limit=20`，6400 篇、32 作者、32 读者、256B 正文；轮转读者，逐页核验排序、游标、作者门店、公共卡片、个人 liked 和计数。点赞：真实 `PUT /notes/:id/like`，512 个合成会话池，最终两个 multi 短测实际各256用户/256笔记/256关系，正式早停窗口实际229用户/229笔记/229关系，均独占分散笔记；同一关系串行交替 true/false，已提交事实/计数/Outbox 及提交后 Redis 更新均计入响应。实际参与者明细见各档 result/verifiedChangedParticipants，不以池大小冒充活跃用户数。
- 正式组关闭额外 tail/CPU profile。所有原生负载已停止；配置、工具哈希、原生时间窗、资源/供电、对账和失败原件逐档保留。结果仅适用于这个共享开发环境，不是生产容量或全站 QPS。

## 最终原生结果

P95/P99 为该档已完成且有延迟样本的请求，单位 ms。失败档的 unknown 没有延迟终态，不能用已完成样本分位数代表全部发起。两轮分位数不合并、不平均。

| 链路/用途 | 目标/s × 计划秒数 | 发起/成功 | unknown/drop | P95 | P99 | 结论 |
| --- | --- | --- | --- | --- | --- | --- |
| Feed 最终短测1 | 50 × 45 | 2250/2250 | 0/0 | 24.375 | 35.903 | 完整通过 |
| Feed 最终短测2 | 100 × 45 | 1158/1151 | 7/0 | 225.006 | 263.905 | 约11.6秒 native99，阶梯结束 |
| Feed 正式1 | 50 × 180 | 9000/9000 | 0/0 | 99.418 | 215.303 | 通过 |
| Feed 正式2 | 50 × 180 | 9001/9001 | 0/0 | 63.890 | 201.049 | 通过 |
| 点赞最终短测1 | 20 × 45 | 901/901 | 0/0 | 58.857 | 178.893 | 901次真实变更，完整通过 |
| 点赞最终短测2 | 50 × 45 | 570/568 | 2/0 | 204.096 | 287.862 | 约11.4秒 native99，阶梯结束 |
| 点赞正式1 | 20 × 180 | 230/229 | 1/0 | 201.612 | 264.009 | 约11.5秒 native99，整组未通过 |

点赞正式2按首败规则关闭，不降档另补一组；最后热点两个可选名额均未启动并关闭，见 [final-scope-closure.json](final-scope-closure.json)、[final-group-closure.json](final-group-closure.json)。早停不证明完整180秒的最终分位数一定超限，但它没有取得完整正式窗口，因此不能交付“20/s 重复稳定”。45秒短测约20.022 mutations/s 与 HTTP PUT/s 相同；失败档按原计划秒数保留分母，不用早停前瞬时速度冒充正式吞吐。机器可读原值见 [results-summary.json](results-summary.json)。

Feed 两轮合计18001次全部正确成功，system/HTTP/content error、unknown、dropped 均0；最终事实无差异，下游、资源、供电和访问时基检查通过。第二轮最大延迟 **1255.8166ms**，第一轮409.721ms；即使 P95/P99 通过，仍不能宣称秒级离群或旧长尾根因已经消失。冻结档选择依据见 [freeze-feed.json](freeze-feed.json)、[freeze-like.json](freeze-like.json)。

## 业务与下游核对

点赞20短测的901次响应变更与SQL迁移及新增唯一事件901一一匹配，意外 no-op=0。50短测有568次已分类变更、SQL569次；unknown2保留。正式失败有229次已分类变更、SQL229次，unknown1仍保留，不能凭总数相等推断所有请求已知。各档降载后的 active/version、每笔记 liked、唯一 LIKE_CHANGED、Redis 派生投影均核验无差异。

观察全 cohort（含早先热点）共5747个唯一 LIKE_CHANGED/DONE；最终multi对账范围5546个，两个范围不能混用。follow-v1/feed-v1/search-v1 三组对这些点赞事件均为 **IGNORED**，不是 APPLIED，也不是三份有效业务投影更新。20短测末段积压增长7、预算8，完整原生窗口与恢复通过；正式失败没有完整180秒的下游稳态证据，即使最后排空也不改通过。详见 [like-final-review.md](like-final-review.md)。

Feed 两轮本身新增事件0，七项队列/停车项均0，降载观察满足60秒恢复预算；这是读场景保持下游无积压，不是 Kafka 发布吞吐。正式两轮各37/37资源样本完整；第一轮宿主内存最高82.9%，CPU单样本87.7%未达到连续三次停止条件，第二轮最高内存74.5%、CPU72.9%。阶梯有 Kafka 接近/超过其2CPU配额的样本，不能泛称所有组件CPU低；现有证据也没有证明负载器已经饱和。详见 [feed-final-review.md](feed-final-review.md)。

## 必要改动与优化证据

1. **提交成功后的取消收尾**：`9d8d9f6a91ded7b1cfc972afa6834e1df8c29643`。原场景取消后 SQL 已提交而 Redis 仍旧；现在仅在事务明确成功后，用脱离 caller cancellation、上限2秒的 context 同步完成失效/回查/Mark。事务内仍使用请求 context，提交前取消仍回滚。真实旧版本 FAIL→新版本3项 PASS，包含提交前取消和旧版本写入围栏。没有改为异步受理；提交结果不明、Redis 故障或2秒耗尽不在“全部自动修复”的承诺内。
2. **Feed 复用 IANA 时区**：`e6bfc7710e364bc9a76cc1c6b0f9530d7badebd1`。懒加载一次 Asia/Shanghai，保留历史 DST 与游标语义。同100/s×45秒的两次有效诊断观察到加权采样分配628040710→519527344字节，时区加载链108040796→未匹配采样；这是分配热点证据。两份分配采集窗口长度和结束后尾部不同，不能称精确每请求节约17.28%、RSS降低或容量提升；P99反而199.15→298.55ms，不宣称整体尾延迟改善。完整限制及非作者核算见 [feed-allocation-comparison.md](feed-allocation-comparison.md)。
3. **串行 relay 不等凑批**：`5e561b87d2129743ef3aba1fe42bdc645ad9d9d2`。该relay一次认领一个、同步发出再Done，因此将writer BatchSize设1，免去每次等待固定10ms。RequireAll、确认后Done、key/header、租约和重试围栏均保留。5项相关测试（含真实MySQL/Kafka）与vet通过。优化前没有完整有效的点赞45秒主测基线，不报告稳定 TPS 提升比例。

工具另独立修复收据 join 的字符集转换方向，完整主键 eq_ref 检索保持精确字节语义；同一RR快照2415事件的单项查询2232.882→12.788ms、计数相同。这是采集工具改进，不是业务查询/数据库索引收益。预热现在只单列延迟，正式窗口原P95/P99中止阈值保持；所有旧失败不重判。真实直接回归、构建、CI和各小问题审查按原提交保存，不用CI代替性能验收。

## 失败、关闭与收尾

旧CAP-07/08容量未通过、旧B1原因未知继续保留。CAP-09初始ES heap512时资源探针拒绝，调整为256发生在基线前；一次Feed低速回归因MySQL/Windows访问时基容差拒绝，不改成业务失败或睡眠定因，也未放宽容差。点赞原基线的SQL/Redis差异已修并留旧FAIL；诊断1采样超时、诊断2预热参数拒绝、hot只有201次预热且P95超限都保留。初始Feed150短测通过不替换最终100早停，不据此下结论称代码容量下降或精确天花板为50。未测5000/s，不再追加定位或压测。

[cleanup.json](cleanup.json) 记录544会话正常logout、观察账号移除、自有API按完整身份停止，四个精确CID停止且无OOM，六个卷保留，11个登记私有环境文件移除、全部自有端口关闭。API实际exit15且不宣称Go优雅关闭。Kafka/ES正常停止留下143，初次工具的零退出码断言失败原件保留；验证shutdown完成日志及卷/身份后只恢复剩余清理，不重复停服或撤销操作，见 [cleanup-tool-review.json](cleanup-tool-review.json)。CRM及未知服务未操作。

本批实现、自测和内部交叉审查已收口；独立核验者如执行CAP-09E，只按STATUS在新自有环境每条链路最多一个180秒代表档，复核事实与范围，不重跑全阶梯。当前不自动启动该独立负载，不更新未核验的求职成绩。可讲解的项目亮点是完整业务负载与事实核对、已证取消一致性修复、基于profile的分配热点优化；稳定写入通过尚无证据。

[原件清单](raw-manifest.json)逐项记录798份采集/元数据文件、13068226字节的SHA256，含失败尝试；不含清单自身、Markdown说明及手写SQL输入。最终[交付交叉审读](delivery-review.md)未发现报告与公开原件不一致。
