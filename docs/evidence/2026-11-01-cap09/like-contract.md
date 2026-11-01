# CAP-09 点赞写入与下游对账契约

2026-11-01。按本轮 STATUS，只读审查当前同步点赞事实写入及三组消费者。本文没有启动服务、执行SQL/EXPLAIN、运行测试或负载；下列SQL是供新自有observer采用的模板，实际执行与结果由本轮探针/阶段证据另记。使用共享 `sql-pro` 审查集合聚合、NULL与JOIN范围；不套用其固定耗时目标或自行加索引。生产代码先保留，候选瓶颈不等于归因。

## HTTP与提交边界

- 真实鉴权 `PUT /notes/:id/like`，JSON必须显式包含 `{"liked":true|false}`，不是toggle。返回200且success=true时，data为 `{noteId:string, liked:bool, version:int, likes:int64}`。`GET /notes/:id/like` 返回同样字段并直接读取MySQL事实，可以用于未知结果后的恢复确认。
- `social/like.go:SetLike` 使用RC事务：先锁公开笔记 `tb_blog ... FOR UPDATE`，再锁 `(blog_id,user_id)` 关系。无关系且请求false为no-op/version0；首次true建立version1；真实翻转每次version+1、公开 `tb_blog.liked` ±1、唯一 `LIKE:<note>:<user>:<version>` 的 LIKE_CHANGED Outbox同事务提交。重复相同目标不增加关系版本或Outbox。
- 事务提交后，响应前仍执行公共计数缓存Invalidate、LikeState事实回查及Redis Likes.Mark版本更新；本轮完整PUT耗时包括这些步骤。缓存失效错误被忽略，Mark失败记warn，所以HTTP200不是派生状态/下游健康的充分证据。无关系且false的no-op同样会走提交后的失效/回查；不能用重复false制造写吞吐。
- LikeState已读取当前公开计数，但同一笔记别的用户可继续写，故响应公开likes只校验合理类型/非负，结束降载后再精确对账；每个关系同一时刻至多一个writer时，正常mutation响应的version应恰为之前版本+1且liked等于本次目标。
- 超时、HTTP0/5xx或分类缺失均不能凭客户端猜测提交结果。先GET事实再恢复该关系；恢复后的继续翻转不消除原unknown。结束时若存在SQL已提交但Redis派生没完成，原差异和失败需保留，不自动repair后宣称原档通过。

源码： [HTTP](../../../backend/internal/social/http.go)、[同步写与状态读](../../../backend/internal/social/like.go)、[同事务enqueue](../../../backend/internal/social/follow.go)、[计数失效](../../../backend/internal/communitycache/counts.go)、[Redis版本栅栏](../../../backend/internal/communitycache/bitmap.go)。

## 下游不是三次点赞投影更新

三Kafka组分别为 `community.follow-v1`、`community.feed-v1`、`community.search-v1`，对应SQL projection值 `follow-v1`、`feed-v1`、`search-v1`。LIKE_CHANGED在三组均是合法的**IGNORED**：follow只应用FOLLOW_CHANGED，feed/search只应用NOTE_PUBLISHED。因而每条唯一LIKE事件最终应有三份IGNORED收据、零APPLIED；不能把这些收据计为3次有效业务投影更新。

点赞Redis集合/hash实际由PUT返回前同步Mark更新，Kafka三组不会修复该点赞派生状态。键为 `community:likes:<note>`（set）和 `community:likes:versions:<note>`（hash，userId→version）。正常增量Mark不自动创建完整性证书，`__complete`缺失不等于投影失败；对本轮全部自有关系分别核HGET版本、SISMEMBER和最终SCARD与MySQL活跃事实，不能拿未认证的Count回退值当0。底层set是可重建投影，不是业务事实源。

Outbox状态仅有 `PENDING`、`PROCESSING`、`DONE`、`NEEDS_REVIEW`；retry回PENDING并设置next_attempt_at。`PROCESSING`带30秒lease，最多8次尝试，退避有界。消费者失败表为 `PARKED`/`RESOLVED`，Apply三次不成后可durable park再提交Kafkaoffset；因此lag归零本身也不能覆盖PARKED或缺收据。

当前relay单worker每Poll处理一条：claim事务→同步Kafka RequireAll确认→Done SQL。各consumer组也逐条durable SQL Apply/IGNORED→同步CommitMessages。`search-v1`即使忽略LIKE也先锁自己的search control。它们是可能先于HTTP受限的下游串行/I/O候选，必须由实际积压斜率与耗时证据支持，不先改架构。

源码/表： [relay](../../../backend/internal/communityrelay/worker.go)、[Outbox状态与lease](../../../backend/internal/communityrelay/repository.go)、[组配置/同步commit](../../../backend/internal/communityconsumer/worker.go)、[follow/feed收据](../../../backend/internal/communityprojection/repository.go)、[search收据](../../../backend/internal/notesearch/sync.go)、[006事实/Outbox](../../../infra/mysql/migrations/006-community.sql)、[009收据](../../../infra/mysql/migrations/009-community-projections.sql)、[010失败表](../../../infra/mysql/migrations/010-community-consumer-recovery.sql)。

## 配置与必须记录的范围

`KAFKA_BROKERS`非空才在app组装relay及follow/feed消费者；`KAFKA_TOPIC`默认community.events。`ES_URL`有效构成searchClient时才组装search消费者，另有 `ES_INDEX`、`ES_TIMEOUT_SECONDS`。本轮要求Kafka及三组正常运行，不能只开前两组。LIKE忽略路径本身不写ES，但ES/search组配置与可用性仍应记录。

当前SQL池固定maxOpen20/maxIdle10，Redis PoolSize10、MaxRetries=-1；Redis读写timeout由 `PROBE_TIMEOUT_SECONDS`决定。环境应固定MySQL/Redis/Kafka/ES资源、API GOMAXPROCS/GOMEMLIMIT、持久化和topic分区数。记录三组真实offset/lag；SQL收据缺口是逻辑未完成量，不冒充Kafka offset lag。API ready不等于三个组均已接收/提交过事件，低速探针至少让一条真实LIKE事件走到三份IGNORED并核组就绪。

源码：[app组装](../../../backend/internal/platform/app/app.go)、[配置](../../../backend/internal/platform/config/config.go)、[ES配置](../../../backend/internal/platform/config/search.go)、[池](../../../backend/internal/platform/database/database.go)。

## 新自有observer只读SQL模板

输入只从本轮公开manifest提取数值ID，不输出token或完整私有env。`@cap_relations_json`为去重的实际分配关系列表，例如 `[{"noteId":"123","userId":"456"}]`；不要笛卡尔展开全部用户×笔记。`@cap_notes_json`为参与该mode的笔记ID字符串列表。下面是**完整cohort累计**对账；每阶段另保留before/after快照求增量，不把跨stage累计值当稳态mutation。新关系均从无行/version0开始；探针及warm阶段的真实版本/事件也必须纳入累计或显式扣除。

### 每关系事实、事件唯一性与连续版本

```sql
WITH rel AS (
  SELECT note_id,user_id FROM JSON_TABLE(@cap_relations_json,'$[*]'
    COLUMNS(note_id BIGINT UNSIGNED PATH '$.noteId',
            user_id BIGINT UNSIGNED PATH '$.userId')) AS j
), events AS (
  SELECT r.note_id,r.user_id,COUNT(*) AS events,
    COUNT(DISTINCT o.aggregate_version) AS distinct_versions,
    MIN(o.aggregate_version) AS min_version,MAX(o.aggregate_version) AS max_version,
    SUM(NOT (BINARY o.event_id <=> BINARY CONCAT('LIKE:',r.note_id,':',r.user_id,':',o.aggregate_version))) AS bad_event_id,
    SUM(NOT (JSON_UNQUOTE(JSON_EXTRACT(o.payload,'$.version')) <=> CAST(o.aggregate_version AS CHAR))) AS bad_payload_version,
    SUM(NOT (JSON_UNQUOTE(JSON_EXTRACT(o.payload,'$.blogId')) <=> CAST(r.note_id AS CHAR))) AS bad_payload_note,
    SUM(NOT (JSON_UNQUOTE(JSON_EXTRACT(o.payload,'$.liked')) <=> IF(MOD(o.aggregate_version,2)=1,'true','false'))) AS bad_payload_state
  FROM rel r JOIN tb_community_outbox o
    ON o.aggregate_type='BLOG' AND o.aggregate_id=CAST(r.note_id AS CHAR)
   AND JSON_UNQUOTE(JSON_EXTRACT(o.payload,'$.userId'))=CAST(r.user_id AS CHAR)
   AND o.event_type='LIKE_CHANGED'
  GROUP BY r.note_id,r.user_id
)
SELECT CAST(r.note_id AS CHAR) AS note_id,CAST(r.user_id AS CHAR) AS user_id,
  COALESCE(l.active,0) AS active,COALESCE(l.version,0) AS version,
  COALESCE(e.events,0) AS events,COALESCE(e.distinct_versions,0) AS distinct_versions,
  e.min_version,e.max_version,COALESCE(e.bad_event_id,0) AS bad_event_id,
  COALESCE(e.bad_payload_version,0) AS bad_payload_version,
  COALESCE(e.bad_payload_note,0) AS bad_payload_note,
  COALESCE(e.bad_payload_state,0) AS bad_payload_state
FROM rel r LEFT JOIN tb_blog_like l ON l.blog_id=r.note_id AND l.user_id=r.user_id
LEFT JOIN events e ON e.note_id=r.note_id AND e.user_id=r.user_id
ORDER BY r.note_id,r.user_id;
```

新cohort有效关系应满足 `events=distinct_versions=version`；version>0时min=1/max=version、所有bad字段0、active=version奇偶。缺关系version0应events0/active0。唯一event ID加完整1..version范围证明每次真实变化一条事件；单看COUNT或最终状态相同不够。若SQL原来存在非本cohort关系，必须保留初始状态/版本/事件基线，不能直接套奇偶或eventcount=version假设。最终总version增量可验证累计mutation；稳态窗口的mutation另用分类/窗口前后事实及事件边界核定，不把预热混入。

### 每笔记公开计数，包含该笔记所有用户

```sql
WITH notes AS (
  SELECT note_id FROM JSON_TABLE(@cap_notes_json,'$[*]'
    COLUMNS(note_id BIGINT UNSIGNED PATH '$')) AS j
)
SELECT CAST(n.note_id AS CHAR) AS note_id,b.liked,
  COALESCE(SUM(l.active=1),0) AS all_active_relations
FROM notes n JOIN tb_blog b ON b.id=n.note_id
LEFT JOIN tb_blog_like l ON l.blog_id=n.note_id
GROUP BY n.note_id,b.liked ORDER BY n.note_id;
```

每行要求liked=all_active_relations，不能只算本轮用户而忽略其他活跃事实。结束后再按每关系Redis HGET/SISMEMBER及每笔记SCARD交叉核验；不因缺 `__complete` 证书而伪造0，也不自动rebuild掩盖原差异。

### 逻辑待办与三组收据

下例对本轮**已确认唯一**LIKE事件集合执行；`@cap_start_outbox_id`为本轮fixture/probe之前保存的水位，不是按当前时间临时估计。全cohort水位覆盖探针/warm，阶段水位另记。使用私有新实例且所有LIKE事件均来自本cohort时，可按该范围计算；否则必须再JOIN实际owned note/user范围，不能把别人的事件算进来。

```sql
SELECT delivery_status,COUNT(*) AS events,
       MIN(create_time) AS oldest_created_at,MAX(id) AS max_id
FROM tb_community_outbox
WHERE id>@cap_start_outbox_id AND event_type='LIKE_CHANGED'
GROUP BY delivery_status;

WITH projection_groups AS (
  SELECT 'follow-v1' AS projection UNION ALL SELECT 'feed-v1' UNION ALL SELECT 'search-v1'
)
SELECT g.projection,COUNT(o.id) AS expected_events,
  SUM(o.id IS NOT NULL AND p.event_id IS NULL) AS missing_receipts,
  COALESCE(SUM(p.state='APPLIED'),0) AS applied,
  COALESCE(SUM(p.state='IGNORED'),0) AS ignored
FROM projection_groups g LEFT JOIN tb_community_outbox o
  ON o.id>@cap_start_outbox_id AND o.event_type='LIKE_CHANGED'
LEFT JOIN tb_community_projection_event p
  ON p.projection=g.projection AND p.event_id=o.event_id
GROUP BY g.projection;

SELECT projection,state,COUNT(*) AS failures,MIN(created_at) AS oldest_created_at
FROM tb_community_consumer_failure
WHERE projection IN ('follow-v1','feed-v1','search-v1') AND topic=@cap_topic
GROUP BY projection,state;
```

每组最终expected=ignored、applied=0、missing=0；PARKED/NEEDS_REVIEW不得新增。Outbox PENDING/PROCESSING、每组missing、Kafka lag分别报告，**不相加成一个“积压总数”**，因为它们代表同一事件在不同边界的重叠工作。收据存在与offset提交也不是完全同一时刻。

稳定末分钟至少在边界前后保存累计事件数与各待办/lag/停车值；按同一范围的增量判新增待办≤同期新增事件1%。降载后60秒内回预定基线，原生早停/unknown保持失败；最终排空不补稳态通过。频繁监测优先使用本阶段Outbox主键水位和汇总，完整JSON/关系聚合仅在降载后做；这些模板没有实测EXPLAIN，不先把它们部署成高频采样查询。

## 低速探针必须先成立

1. 同一新关系GET为false/version0；PUT true→v1；重复true保持v1且无新event；PUT false→v2；重复false保持v2。幂等探针单列，不计写吞吐。
2. 两个不同用户/不同笔记真实flip、单热笔记多用户flip，响应本人状态/版本与最终关系及公开计数一致。不要使用同关系并发toggle充吞吐。
3. 多个in-flight关系的结果未知恢复只GET事实，保留unknown类别；在相同关系继续写之前确认事实版本，不能猜commit。
4. 每真实事件有唯一Outbox，三组均IGNORED并提交offset；最终无PARKED/NEEDS_REVIEW、Redis派生与事实匹配。恢复/降载只能证明恢复，不能把受限档改通过。
5. k6的GET恢复与PUT统计按固定name拆分，`http_reqs`全量不能直接与PUT attempted比较。unknown和真正未开始的dropped分列，HTTP成功率不能替代经SQL核实的mutation/s。

## 09A工具非作者审查（真实探针前）

已只读审查 `cap09-like.mjs`、`load/cap09-like.js`、共享contract/purecheck及 `cap09-downstream.go`。正常负载每VU独占一条512关系池中的关系，阶段前以SQL快照灌入事实；不混入bootstrap GET，未知恢复GET与PUT native子metric分开。pre-warm、pre-stable与post-stable快照分列，稳定mutation不混预热；最终累计事实、连续唯一事件、公开计数、Redis和三组IGNORED均另核。

审查收紧了取消竞争窗口的统计：缺终态与响应效果未知可能重叠，不能直接相加称精确unknown；现保留上下界，只有上下界相同才给确切值，零未知预算要求两通道均0。事件核验增加BLOG、binary canonical ID及严格JSON字段类型/版本全文匹配，避免宽松数字CAST掩盖异常。

下游observer使用一个持久SELECT权限SQL连接、只读RR快照，每5秒观测；新阶段以干净起点水位过滤，Kafka三组真实committed/end offsets顺序采集且保留非原子边界，缺值与负差不补成0。原生库v0.4.47的本地listoffset测试有同partition同时FirstOffsetOf/LastOffsetOf的用法，客户端合成PartitionOffsets符合本工具调用。启动允许最多15秒保留尝试的clean-ready等待，不是重发负载；连续3个不完整样本写安全stop，整体不超过360秒。

observer的 `valid` 仅代表完整采集与control-done后收敛，已明确 `nativeRecoveryBudgetVerified=false` 和 `fullChainStableBudgetVerified=false`。root仍须按native stableEndedAt与timeline独立核60秒恢复及末分钟积压，不能因helper自身先等收敛而延后起算。阶段范围SQL及三组receipt join随事件量增长可能增加观察开销，durationMs/错误必须保留，不把采集自身受限当后端单因。

上述离线代码复核无剩余阻断，可以进入root控制的低速探针；真实SQL、HTTP、三组消费和停止/清理仍需实际证据。这次审查没有执行observer main、连接依赖或发起负载。
