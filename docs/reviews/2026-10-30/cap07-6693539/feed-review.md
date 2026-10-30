# CAP-06V／CAP-07：Feed、计数分片及应用装配独立审查

2026-10-30。交付快照为 `6693539ae3715cc28f1b52b7d1583a713659ca4e`，审查差量起点 `52cd5c4015d2ac4b745f1bea9006e8dba4e359e9`。先读目标 STATUS 与 AGENTS，再核实际 social/feed.go、follow.go、projection.go、HTTP入口、communitycache/counts.go、应用／私有诊断装配及直接集成测试，并阅读本批 controlled、phase1、phase2、harness-report 和总报告。

**本方向未发现新增P1/P2或需要阻断功能验收的确定性生产缺陷。** 公共内容已实际接入Feed，旧嵌套借池依赖未在新装配中重现；B1池等待与长尾不能仅凭现有采样指定为某一生产bug。功能边界成立不等于容量通过，B1失败及尚未执行的后续性能档必须保留。

本审查者只读应用与既有证据，另独立执行四包无外部依赖的普通测试；没有启动业务API、容器、压测或付费Provider，没有修改应用、STATUS或原始证据。普通测试含测试自身的受控listener，不是新业务环境或真实依赖复跑。共享卡片组件内部、原始性能工件和主核验者的新卷验证由其他角色另行记录，本文不将其冒称为本人执行。

## 真实业务接入、快照与内容装配

`backend/internal/platform/app/app.go` 创建一个应用生命周期的 `communitycards.Cache`，实际注入 `social.NewService(...).WithProjections().WithCards(cards)`，并将同实例交给私有诊断统计。不是只存在一个未调用的组件。

`backend/internal/social/feed.go` 的窄目录查询保留作者集合、当前关注EXISTS、PUBLISHED、发布版本、门店连接、本人点赞及 `(create_time,id)` 排序／游标。projection完整性检查与其读取仍处于原只读RR快照；不完整或可选投影查询失败时，保留当前事实SQL回退和source语义。`title/content/images`从窄目录中移除，公开计数回退值和个人字段仍从事实取得。

`readSnapshot` 成功返回、只读事务提交归还连接后，才调用卡片 `GetMany` 及既有计数 `GetMany`。没有将请求事务绑定进共享内容Cache，也没有在持有事实连接时等待flight或二次借同池。当前app始终启用WithProjections，A/B使用相同RR事实语义；显式无projection但接cards的Service也使用RR，不把这一可选装配路径的差异当作正式A/B收益。

卡片映射按照原refs顺序逐项装配，仅覆盖标题／正文／图片，不覆盖个人点赞、门店、动态计数或游标。`Service`快照复制共享的是原Cache指针与 `cardFallbacks` 的atomic指针，没有复制正在使用的atomic值。

## 版本变化与一次新快照回退

内容组件返回 `ErrVersionChanged` 或 `ErrBusy` 时，Feed只执行一次新的 `readSnapshot`，沿用原输入cursor与limit，调用不经过Cache的全字段 `feedPage`。这次快照重新决定当前完整页及个人状态；没有将新正文配到旧目录，也没有递归调用已经走缓存的Feed。数据库错误不伪装为空成功页；公共计数仍在新事实事务结束后按既有容错规则装配。

回退成功后再装配计数，不意味着重回内容Cache。当前实现没有无上限重试。暖旧版本片段可服务其已建立的RR事实快照，下一请求重查公开状态／版本；该语义不是“响应发出瞬间最新”，也不允许缓存跳过事实门禁。

## CAP-06V分片门与锁序

`communitycache/counts.go` 将一个全局publication门／代次替换为固定4096个惰性分片，以 `noteID % 4096` 选择。同分片的epoch、publish及Invalidate保持同一顺序边界和代次检查，发布或删Redis时仍串行；不同分片不会共享这一网络锁。代价是碰撞note仍互相等待／保守跳过旧代次回填，不能宣传任意两个key都完全隔离。

GetMany逐项读取epoch，每次即释放，不同时持多个分片门；批量MGET和IN查询仍保留。publish／Invalidate的顺序是分片门再local内存锁，其他读本地缓存路径不反向持local锁再取分片门；inflight map的短锁也不跨Redis／SQL操作。未发现分片间锁序环或该差量导致的确定性死锁。

受控测试 `counts_isolation_integration_test.go` 使用明确的Redis SET／DEL进入／释放barrier：A尚未放行时读取已预热且不同分片的B，同时确认同key及有意同分片碰撞等待者已经进入等待后独立取消。已有代码还在放行后验证旧SET／Invalidate次序和新事实。归档同测试仅替换旧counts.go的overlay，SET／DEL均因B达到300ms deadline失败；新实现两项通过。该旧FAIL→新PASS支持分片隔离机制修复，不支持将其称为旧1.137秒或本批B1事件的唯一根因。

## 直接行为证据与覆盖边界

已审查新增真实测试的实现，不以测试名称代替行为：

- `feed_cards_integration_test.go` 的HTTP冷／热用例使用真实登录会话及MySQL／Redis，核冷页1批2行内容加载、另一用户热页0新增内容加载、与原全字段事实页相同。SQL池限1且事后无新增等待／in-use0，直接约束事务归还。两用户个人点赞不同，同秒游标、退关、当前非公开及新版本均有断言。
- 同文件的加载前callback使用另一真实SQL连接对本次夹具更新版本或status；验证恰一次全字段新RR回退，不混版本。实际缺表查询错误返回503、不增加fallback、不写空L1。它是存储层竞争／故障注入，没有新增编辑或下架API。
- `feed_cards_flight_integration_test.go` 用真实HTTP和SQL进入／释放barrier建立8个caller的同批冷突发，确认7个join后取消首caller；首caller独立取消，其余7个取得完整正文，统计为1flight／7join／1内容批次2行。完整／缺一条投影的HTTP分别保持projection／database来源，并复用相同公共正文。
- `counts_isolation_integration_test.go` 增补了真正进入门等待后的取消与受控在途栈。旧单键取消测试是在调用前取消，不能单独承担这一新增证据。
- app及diagnostics普通用例核关闭错误不丢失、diagnostics→cards→pools顺序、装配失败也先关闭cards，以及诊断统计只导出固定计数。

本文没有重新执行这些真实依赖用例。反序／部分重叠批次、共享任务超时／Close、坏payload／过期／Redis慢写等组件内部覆盖，由卡片组件审查及主核验者实跑另记；不把本方向审查升级为所有FC已独立实跑。FC-08包含的最终容量协议仍未通过，不能以组件／HTTP正确性或CI绿色覆盖。

## 本人执行的普通验证

在本隔离worktree的backend目录，显式清空 `GO_INTEGRATION` 和专用缓存测量开关，执行：

```text
go test -count=1 -json ./internal/social ./internal/communitycache ./internal/platform/app ./internal/platform/diagnostics
```

退出0，四包均PASS；按JSON的具名test/subtest终态计数为35个pass、22个显式skip、0个fail。social/communitycache中的真实依赖入口因开关未开而跳过，不能计作真实集成通过。包耗时分别为0.252／0.339／0.508／0.328秒；这些是测试执行时间，不是业务延迟或吞吐。

## B1等待现象与结论限制

交付的B1在约58秒因P99约1449.57ms早停，5788发起／5787完成成功／1unknown。末资源样本SQL in-use20、cards active8及inflight25，说明当时存在排队和回源活动；它不证明存在死锁。生产Feed的RR已在内容／计数加载前归还，loader只持安全池，分片门不同时持多个门，未见可机械推出旧嵌套借池重现的调用环。

同一组A2也有池等待和较高max，低采样CPU又不足以排除短暂调度／传输停顿。本文未取得可将B1归因于缓存、SQL池、Redis或宿主的确定性证据，也未据此扩大池、改TTL、替换存储或启动新诊断。

最终应分别保留：06V已证结构修复、公共卡片业务接入与受控功能结果、原事故未归因，以及本组B1失败／B2不能补齐／后续阶梯与hot门控未执行。功能审查通过可以与容量验收未通过同时成立；本方向没有可凭当前材料直接修复的确定性生产bug。
