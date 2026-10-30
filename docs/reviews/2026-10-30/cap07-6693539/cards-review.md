# CAP-07 公共卡片组件独立源码审查

日期：2026-10-30（Asia/Shanghai）。审查者 `cap_code_review`，非本批实现者。交付 `6693539ae3715cc28f1b52b7d1583a713659ca4e`，差量基线 `52cd5c4`；先读取目标 STATUS、AGENTS、[设计](../../../../docs/feed-card-cache-design.md)与[实现前契约](../../../evidence/2026-10-30-cap07/card-contract.md)。审查范围为 communitycards 的三个生产文件和两份测试，追读 social 接入及 app 生命周期作为组件使用背景。首次检查隔离 worktree 跟踪文件干净；本报告是本子任务唯一新增文件，未改应用、STATUS、原证据、main 或提交 Git。

**结论：本次组件范围未发现新增 P1/P2 正确性、竞态、资源无界或隐私问题。** 设计要求的同进程同缺失 batch 合并、独立取消、版本命名空间、可变对象复制与有界降级均有实际实现及相关断言；这不等于 FC-01—08 全部独立实跑，更不等于容量验收。B1 长尾失败、旧秒级事故未知及 CAP-07 正式容量未通过的状态不因本审查改变。

## 组件实现与契约对应

| 关注点 | 独立源码结论 |
| --- | --- |
| 公共片段与规范化 | [cards.go](../../../../backend/internal/communitycards/cards.go) 的 Ref 仅 ID/version，Card 仅 title/content/images。normalize 在 I/O 前限制最多50个输入、拒绝零ID、排序去重，保留历史版本0；batchKey 用规范整数与明确分隔符，输入顺序/重复不同而集合相同可合并。不缓存 token、本人状态、计数、游标或门店名。 |
| 批量与合并 | GetMany 批量检查 L1/Redis 后，以实际缺失集合进入 flight。startFlight 在短 mutex 下查询/注册同批任务，网络不在该锁内；执行者取得 active 槽后再次检查 L1/Redis，再由 [storage.go](../../../../backend/internal/communitycards/storage.go) 一次 IN 内容查询及一次 SET pipeline 回填。不同/部分重叠 batch 可各自加载，不声称共享项跨批唯一，也不退化成每卡一次 SQL。 |
| 首调用者取消 | shared task 的 context 来自 cache.life＋独立2秒期限，不来自首 HTTP context；加入者独立等待自己的 context，首 caller 取消不取消其他人。全部 caller 退出后共享工作可以运行到自身短期限，这是明确选择，不声称引用计数立即取消。 |
| 准入与有界性 | 固定64个在途 batch、8个活动 loader、1024个 miss caller。caller 预算在 Redis 首查之前申请，flight 数已满返回 ErrBusy；活动槽的排队计入共享2秒期限，Redis每次250ms且受父deadline限制。命中 L1 不为等待资源创建后台任务。 |
| Close 与注册竞争 | begin/startFlight 的 WaitGroup.Add 与 closed 检查都受 cache.mu 保护。Close 在同锁内禁止新增并取消 life，再 Wait；飞行结束发布 immutable 结果、移除 map 项、释放 in-flight 槽并关闭 done，最后 Done。没有在 Wait 开始后从零重新 Add 的入口，也没有取消某 caller 就错误删掉仍服务他人的 flight。 |
| L1 与复制 | [local.go](../../../../backend/internal/communitycards/local.go) 用短本地 mutex、LRU链和条数/内容字节双预算；访问时检查2秒到期，压力下逐项淘汰。写入、读取及 shared result 返回时复制 Images，各 caller 有独立 map；没有可由一个响应修改另一个响应/L1的共享切片。字符串本身不可变。8MiB是编码payload内容预算，不是RSS上限；过期为惰性检查，不宣称后台即时删除全部到期项。 |
| 版本与旧加载 | Redis key 与 payload 均带格式1、规范ID和发布版本。内容SQL只读当前 PUBLISHED 行，再严格检查每个所需 Ref 是否存在；缺一个版本就整批 ErrVersionChanged，不发布部分新内容。旧加载只回填其自身版本命名空间，不能覆盖新版本键。暖旧片段可服务先前已建立的旧RR目录，符合既定快照语义。 |
| 坏值与异常 | decode 限制缓存payload8KiB，拒绝坏JSON、尾部值、未知字段、错误类型/格式/ID/version和null字段/图片项；非字符串 Redis MGET 值按miss。Redis读/写故障有界退化，合法超大内容完整返回但不缓存；SQL错误/版本缺失不写空值。回填失败不直接抹掉已读到的正确公共结果，调用者自身剩余deadline和应用关闭仍可终止等待。 |

app 创建对象时只注入安全池和应用生命周期，关闭顺序包含 cards.Close 在 SQL/Redis 池之前；组件不持 Gin 或事务对象。组件结果缺版本/资源已满后的至多一次新RR全字段回退由 social 负责，不由本组件递归实现；该业务接入的独立完整验证以主核验报告为准。

## 测试断言复核与本子任务实际运行

审阅 [cards_test.go](../../../../backend/internal/communitycards/cards_test.go) 和 [integration_test.go](../../../../backend/internal/communitycards/integration_test.go)。普通测试不只检查内部map：同批共享用进入/释放屏障，确认另外三人 join 后才放行；首 caller 已取消，而其余 caller 得到完整结果且只有一批SQL。反序/重复输入规范化、部分重叠两个独立batch、L1条数/字节/过期、跨响应图片复制、晚v1与并行v2、活动队列/独立取消及Close收敛都有断言。

实际独立执行如下，无外部依赖、无新服务或负载：

```powershell
$env:GO_INTEGRATION='0'
go test -count=1 -json ./internal/communitycards
```

退出0，包结果：

```text
ok  github.com/cityloop/fullstack-go/internal/communitycards  0.293s
```

13个普通顶层测试及其子项通过，5个 TestIntegrationCards* 入口因显式 GO_INTEGRATION=0 跳过；不把子项数量、作者结果及主核验者随后实跑相加为本子任务的新验证总数。本子任务未运行 race、真实 Redis/MySQL、实际 HTTP 或容量测试。

真实测试源码覆盖：坏JSON/null-image/错误Redis类型/超大payload经真实SQL重建；只对自有键设置短PEXPIRE后观察真实过期；独立关闭Redis client的真实SQL降级；缺版本/真实SQL错误无空缓存；专属连接 pipeline 前插入自有BLPOP，并用 CLIENT LIST 确认真正阻塞后Close。其清理先等待精确命名连接消失，再删自有键，避免迟到SET污染清理。短PEXPIRE、断开client和受控BLPOP均有明确范围，不是全局停Redis、生产TTL修改或原B1根因证明。上述是测试设计审查，实际执行结果由主核验者及原报告分别记录。

## 性能解释与诊断边界

- singleflight 只合并相同缺失batch的在途内容加载，不合并每个caller的初次Redis查询；不同batch/部分重叠可重复读共享卡片。这是契约范围，不应把Load减少直接等同HTTP吞吐增加。
- shared task 使用应用context，按设计不继承首HTTP的 taildiag trace。原HTTP trace的SQL阶段不能逐一归因该共享内容SQL、活动槽排队或回填；组件Stats提供批次/行/内容字节等计数，不提供本轮每段等待时间。不能由某HTTP span没有对应等待而排除shared task。
- 内容SQL后Redis回填仍同步等待，最多250ms且占active槽；L1在写Redis前填入，其它已命中卡片不因全局网络锁被拖住。这有成本，但没有分段证据就不能指定它是B1长尾根因，也不能先改成无界异步回填换取数字。
- 内容loader计数不等于完整Feed SQL计数；事实/个人状态/计数链路仍有SQL。序列化内容字节不等于SQL wire字节或RSS，统计gauge不是跨字段原子快照。

没有从本次源码审查推导QPS、提升倍数、旧事故已修或B1失败可忽略。继续保留失败及未完成容量验收范围。
