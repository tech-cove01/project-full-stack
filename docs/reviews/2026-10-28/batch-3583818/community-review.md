# 社区、缓存及Kafka投递独立核验

基线 `358381843d0fe5efb98dfb75cac34c089f0777b8`；范围 GO-050/051/052/053 与 GO-060/061/062 的实际已实现子集。使用专用 `project_full_stack_review_community` schema、MySQL3321、Redis6393/DB3、Kafka9096；未启停共享服务，未调用真实模型，未改主工作区或提交。

## 修复

1. **笔记发布缺持久事件**：原 `note.Create` 只提交公开笔记，不产生社区事件；后续无法从Kafka得知新发布。现在 `NOTE_PUBLISHED`（`BLOG` / `NOTE:<id>:1:PUBLISHED`）与笔记同事务；事件落库失败整笔回滚。没有新增消费者。
2. **关系初始no-op错误生成事件**：对从未关注的作者取消关注，原来返回version1并发事件；首次取消点赞也有同类行为。现在inactive/version0初始化后锁定关系，no-op不发事件；同时唯一键初始化避免RC下首次关注的缺行锁窗口。20并发同目标最终一关系、一事件、version1。原20并发测试没有实际命中duplicate-key，因此不声称实测复现了1062窗口。
3. **部分点赞set覆盖事实计数**：Redis丢键后只SADD本次用户，原 `LikeState` 把SCARD=1替换已提交DB=2。现在读取结果保留已经查询的DB计数；增量set不再被视为完整快照。
4. **无版本投影/重建窗口**：Mark按每用户版本应用，旧版本不能覆盖较新取消；重建保留inactive行版本。`recount`持有与SetLike一致的笔记行锁，读取事实后原子替换set+版本hash；实际行锁竞争证明重建期间并发note更新不能越过。缺失/损坏集合不会继承完整性证书。集合成员使用完整十进制ID，不截断或转Lua数字。
5. **零点赞recount失败**：原DEL后没有集合，Count判断不存在使合法零点赞命令退出1。完整性元数据现在能表达零；实际 `go run ./cmd/recount <自有noteId>` 返回0。命令只修Redis投影，不改MySQL计数。
6. **第8次过期lease堵塞队列**：relay选中attempts>=8的过期PROCESSING直接返回nil，永久挡住后续事件。现在同一认领事务将其置为持久NEEDS_REVIEW，下一轮可继续认领后续事件，原事件不丢失、不谎称DONE。
7. **旧加载覆盖失效**：旧count loader可在Invalidate结束后SET回旧值。现在缓存发布与失效共享可取消的短闸门/代际，Get和GetMany旧加载均不重新污染后续缓存。
8. **取消等待者受阻**：原singleflight follower无条件WaitGroup.Wait。现在用done channel同时等待个人context；取消无需等leader。不同代际不复用旧flight，旧flight退出也不会删除新flight。

额外修正测试夹具清理：`FollowAndFeed`过去只删除作者的FOLLOW事件，却遗漏本人创建的LIKE事件和like事实；改为按自有作者笔记ID清理事件/事实，再删笔记。笔记新增发布事件后，其旧集成测试也按自有note ID清事件。

## 真实验证

- [冻结初轮隔离记录](community-initial-isolated.jsonl)：发布缺事件、故障未回滚、部分set计数、耗尽lease均真实FAIL。其中communitycache因测试包含新增API而编译失败，该包不能用此轮证明业务FAIL；随后单独用兼容原API的最小测试补齐[零点赞初轮FAIL](community-empty-initial.jsonl)。
- [关系no-op初轮](community-follow-initial.jsonl)：原初次取消关注返回version1，FAIL。
- [缓存初轮](community-counts-initial.jsonl)：真实旧SET屏障下DB7被缓存0覆盖、取消等待者被leader阻塞，两个FAIL。
- [最终全包真实结果](community-final-complete.jsonl)：note/social/communitycache/communityrelay四包，共28个顶层测试全部PASS，包括真实Kafka消费读取验证broker已接收、不可达broker重试、SQL事务回滚、投影版本/零值/完整性、真实重建行锁和缓存取消窗口。该Kafka测试读取投递结果，不代表实现了GO-072应用消费端投影。
- [recount零值命令](community-recount-zero.txt)：实际命令退出0；自有命令夹具和两个Redis键已精确删除。
- 相关普通Go测试、`go vet`、gofmt与diff检查通过。最终代码由root统一提交后跑Linux race/CI，本lane本地Windows结果不替代race。
- 最终全包后仅改了旧夹具清理逻辑，另[定向复跑](community-fixture-cleanup-final.jsonl)验证这一改动。最终清理对账见 [community-cleanup-final.txt](community-cleanup-final.txt)：社区outbox、like事实、follow事实、自有测试用户/笔记均0。Kafka随机私有topic保留在专用broker卷，不声称已删除topic。

## 中间失败证据的解释

`community-initial.jsonl`是最早编译与工作树修改重叠的中间记录，包含build失败；`community-initial-frozen.jsonl`另被前序测试漏清事件干扰，不能替代隔离初轮。`community-final.jsonl`是误与冻结测试并行的中止轮，绝不作为通过证据；已仅停止核对过命令行的自有go/test进程，后续全部串行。中断与旧夹具产生的4个精确事件，以及3条已删除笔记/用户的孤立like事实均逐ID核实清理，未删未知数据。`community-final-isolated.jsonl`为补counts修复之前的26项中间PASS；以最终complete28项为准。以上记录全部保留，不覆盖成PASS。

## 范围与仍缺内容

- GO-050/051/052/053核心发布、DB Feed、期望状态关系和可靠Kafka投递已在上述范围核验；Feed现有集成只覆盖同秒少量笔记跨页，不把它写成已完成50+条/多作者大数据量验收。作者/liked从服务端身份和独立查询获取，当前共享缓存只存公共计数。
- GO-060目前是Feed SQL批量卡片装配、一次批量本人liked与公共counts缓存，并非完整page-ID/card/count分层缓存；GO-061为有界固定2秒L1和TTL抖动，不是动态hotkey探测或TTL伸缩。只能标已实现子集通过。
- GO-062为精确集合及可重建版本投影；尚无GO-072的Kafka应用消费、eventId去重及多下游读模型。不能宣称Kafka多投影或完整分布式一致性已完成。
- 历史demo `tb_blog.liked` 未回填成具体用户的like事实；不能凭空推断用户。新发布笔记从0开始可严格对账，旧种子计数应明确作为历史数据，另定迁移/审计策略，不能写成新事实源成绩。
- 未执行持续性能测量、生产broker故障/集群、跨实例缓存即时一致性或大规模内存对比；不引用历史QPS/省内存数字。

## root提交建议

按行为分组：发布笔记+可靠事件；关系no-op/初始化；版本投影与recount；relay耗尽租约；counts失效与取消；测试清理/核验报告另提交。所有修改均在指定模块，未改前端、状态表、CI或platform。
