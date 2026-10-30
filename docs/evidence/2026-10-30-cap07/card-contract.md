# CAP-07A1 公共卡片契约与资源边界

2026-10-30。按[既定设计](../../feed-card-cache-design.md)与根STATUS执行。本文在实现前固定接口/上限，不是已交付能力或性能成绩；先完成CAP-06V留证及已证缺陷修复，随后按用户决定带原长尾未知风险推进。原事故归因与新缓存行为分别记账。

## 公共片段与事实目录

组件包为`internal/communitycards`。目录仍由social在原事实读取路径决定当前关注、公开状态、发布版本、门店、个人点赞、时间顺序与游标；组件不缓存整页、用户身份、门店名、动态计数或个人状态。目录事务结束后才调用组件，组件只持应用生命周期和安全池，不持事务或请求/Gin对象。

拟定导出接口：

```go
type Ref struct { ID, Version uint64 }
type Card struct { Title, Content string; Images []string }
func New(appCtx context.Context, redis *redis.Client, db *gorm.DB) *Cache
func (*Cache) GetMany(ctx context.Context, refs []Ref) (map[Ref]Card, error)
func (*Cache) Stats() Snapshot
func (*Cache) Close() error
```

ID必须大于0，版本0兼容历史公开事实。输入按`(ID,Version)`排序去重，返回映射与输入顺序无关；social负责按目录恢复顺序。调用者取得独立map和Images切片，不得修改另一个请求或缓存持有的对象。

Redis键为`community:note-card:v1:<canonical ID>:<canonical version>`；payload只含格式版本1、规范十进制字符串ID/版本、title/content/images。读取严格匹配格式、字段类型、ID/版本并拒绝前导0、坏JSON/尾部值，坏值按miss。内容SQL一次批量读取ID集合的公开行，再逐项核对所需版本，绝不把新版本正文装进旧目录。暖v1片段可以服务已建立的v1目录快照；新请求仍查询当前事实。

## 固定上限

| 项目 | 首版固定值与范围 |
| --- | --- |
| batch | 每次最多50个输入Ref，排序去重后批量MGET/SQL，不拆逐卡SQL |
| L1 | 最多2048条，序列化payload合计最多8MiB；这是内容预算，不等于进程RSS上限 |
| L1 TTL | 2秒，不能为规避原长尾延长既有计数TTL |
| Redis TTL | 60秒加0至不足9秒的受限抖动 |
| 单卡缓存 | 编码payload最多8KiB；更大的合法内容完整返回但不存L1/Redis，不截断/拒绝合法正文 |
| 冷载入 | 最多64个在途载入批次，最多8个执行载入；超出返回`ErrBusy` |
| 等待者 | miss/载入等待调用者最多1024；不为无界等待创建后台任务 |
| 载入期限 | 整体最多2秒，包含排队、缓存二次检查、内容SQL及回填；不叠加无界独立超时 |
| Redis操作 | 每次最多250ms且不超过载入/调用者剩余deadline；写失败不把已读内容改成失败 |

锁只保护本地状态，不持跨所有key的锁执行Redis/SQL。L1按预算移除过期/最旧条目，载入与关闭有显式生命周期，不能无限保留任务/条目。

## 错误与一次回退

- `ErrVersionChanged`：任一目录要求的公开版本已不可取得。组件不返回空卡/部分成功；social按原输入游标至多一次新RR全字段Feed回退，该函数不再调用内容cache。
- `ErrBusy`：资源上限已达。social同样至多一次全字段回退，沿用当前请求剩余deadline，不递归cache。
- 数据库错误、`ErrClosed`：按既有失败契约返回，不缓存空内容或伪造200。
- Redis断开/坏值：组件内部按miss有界回源；Redis写失败仍返回已读正确内容。

没有合法编辑/下架新API。测试可对自有SQL夹具受控改变版本/公开状态，并明确是存储层注入。旧载入允许在旧版本命名空间结束，不得写新版本或无版本的“当前卡片”键，旧键由TTL回收。

## 两个独立交付阶段

**07A2先交真实批量缓存链路。** 组件实现L1/Redis、批量版本SQL、资源/超时上限及生命周期；各caller自行载入，不宣称跨请求singleflight。root接入窄目录并完成实际HTTP冷/热后单独提交。此阶段`flight_started/flight_joins`为0，不能把热命中当合并证据。

**07A3再交同批回源合并。** 以规范化缺失Ref集合为key，同进程完全相同batch共享一次在途批量载入，赢家执行SQL前再查缓存；`[A,B]`与`[B,C]`允许各自加载，B可重复。共享载入从应用生命周期派生独立2秒context，首caller或其它等待者取消只结束自身等待，全部离开后允许任务到短deadline；应用关闭取消并等待其收敛。手工有界map+done通道实现相同batch的singleflight语义，不为库名引入装饰性依赖或跨批逐key调度器。

## 低基数观测

`Stats()`只暴露固定计数/gauge，root挂到原loopback诊断，不进入产品响应：requests、l1_hits、redis_hits、miss_items、loads_started、flight_started、flight_joins、content_sql_batches、content_sql_rows、content_bytes、load_errors、oversize_uncached、redis_errors、invalid_payloads、busy_rejected、expired、evictions、inflight、active_loads、waiting_callers、l1_entries、l1_bytes，以及当前构建是否已启用coalescing。

`content_sql_batches`在每次实际内容SQL尝试时增加（含失败），rows是实际返回行数，bytes是这些SQL行的title/content/原images UTF8字节量，不是SQL wire字节或RSS。hit/miss按每caller去重后的Ref计，载入/合并按在途批次计；这些计数与全实例SHOW/Redis访问量分开。并发gauge不是跨字段原子快照。正式效果及FC-01—08按既定A/B/有限阶梯留证，不据契约预测收益。
