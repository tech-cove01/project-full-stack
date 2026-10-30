# CAP-04 Feed 作者索引：013 设计与待验证范围

本小改动只准备 [013-feed-author-index.sql](../../../../infra/mysql/migrations/013-feed-author-index.sql)，新增 `idx_blog_author_feed(user_id,status,create_time,id)`。本设计作者没有执行迁移、发送 SQL/负载、编译或运行测试；由根 agent 在独立 opt-tests MySQL3452/Redis6552 验证迁移与既有 social 真实依赖回归，CAP-03 的 multi 基线收齐后再由根安排应用与同条件对照。当前没有索引后的计划或吞吐成绩。

## 已观察到的问题与现有结构

[完整无 cursor fallback 查询](../cap03/baseline-plan/query.sql)按作者 IN、当前关系 EXISTS、公开状态筛选，并 LEFT JOIN 门店后按 `(create_time DESC,id DESC)` 取 20 条。[阶段间隙单次 ANALYZE](../cap03/baseline-plan/explain-analyze.txt)观察到：`idx_blog_feed(status='PUBLISHED')` 读取 6400 条，作者过滤后 200 条，再与 3 条门店 hash join、Top20 排序，总约 11.3ms。该计划支持先缩小作者候选集的实验；不把单次数据库计划时间当 HTTP 延迟或生产容量。

[原表定义](../../../../infra/mysql/001-schema.sql)为 `user_id/id BIGINT UNSIGNED`、`create_time TIMESTAMP`；[006](../../../../infra/mysql/migrations/006-community.sql)增加 `status VARCHAR(16)`。当前 [SHOW INDEX](../cap03/baseline-plan/tb_blog-indexes.tsv)及所有后续迁移仅含 PRIMARY 与 [007](../../../../infra/mysql/migrations/007-feed-index.sql) 的 `idx_blog_feed(status,create_time,id)`，没有等价的作者起始索引。013 不重复旧索引，也不删除或改写 007；不改变列、字符集、金额/ID 类型或任何数据事实。

[夹具行数](../cap03/baseline-plan/row-counts.json)为 6400 篇公开笔记、每作者 200 篇、32 条关注关系、3 家门店、零点赞事实；hot reader35 当前只关注 author3，页长20、所有笔记同一发布时间。CAP-03 的 multi 是 32 位读者轮转、每位只关注一位作者，不等于“单读者关注多个作者”的查询回归。

## 索引次序与保持的业务边界

`user_id` 放在首位，为已查出的作者集合提供更窄的候选范围；其后 `status` 保留公开状态筛选，时间与 ID 对应已有复合排序/游标。一个固定作者和固定状态时，索引顺序具备服务同向倒序时间/ID 的条件；多个作者的 IN 范围不天然等于全局时间顺序，优化器仍可能排序或选择旧索引。MySQL 的[多列索引](https://dev.mysql.com/doc/refman/8.4/en/multiple-column-indexes.html)与 [ORDER BY 优化说明](https://dev.mysql.com/doc/refman/8.4/en/order-by-optimization.html)是设计依据，实际选择以完整 after EXPLAIN/ANALYZE 为准。

这是普通非唯一二级索引，不是覆盖索引。`title/images/content`、计数、shop 字段均不纳入；当前正文和图片列实际为较长 VARCHAR，不能描述成已为 TEXT 建覆盖索引。主查询仍需读取完整笔记列、连接门店并验证当前关注；不得预先宣称只读 20 条、消除 filesort、消除回表或提升固定百分比。

Go SQL 原文、作者目录查询、最终 `EXISTS(...user_id/author_id/active=1)`、`PUBLISHED`、门店 LEFT JOIN、limit 与复合 cursor 谓词均保持。索引不替代权限检查，不加入 FORCE INDEX，不改变社区投影完整性判断或 fallback。优化器在计划中消解常量谓词，也不表示业务 SQL 可以删除它。

## 代价、应用与回退

每条笔记新增一条二级索引记录，增加存储、buffer pool 占用、插入/删除及作者/状态/时间/ID 更新的维护成本；不索引长正文/图片，仍不能将写入开销记为零。当前仅6400行、小作者集合的实验不足以量化生产写放大或磁盘增量；不从列宽或本次热缓存计划虚构节省比例。

新增索引需要扫描建索引并消耗 CPU、I/O、临时空间与元数据锁等待；DDL 不是可依赖事务回滚的业务操作。即使 MySQL 支持二级索引的在线操作，仍应按[官方在线 DDL 范围](https://dev.mysql.com/doc/refman/8.4/en/innodb-online-ddl-operations.html)及现场条件验证，不能声称零锁、瞬时完成或可直接在负载中应用。本票仅由根 agent 在独占隔离环境执行正常版本迁移，不修改 migration 账本或手工标记成功。

应用二进制回退可保留新增索引，旧查询兼容。若实测必须移除，使用后续独立的前向回退迁移执行以下 DDL；不重写已应用013、不删除迁移记录、不连带删除007：

```sql
ALTER TABLE tb_blog DROP INDEX idx_blog_author_feed;
```

## 根 agent 后续直接验证

1. 在独立 opt-tests 3452/6552 执行正常迁移，检查 PRIMARY、007 与013并存、版本迁移重跑行为正确；运行仓库既有 social 真实依赖回归。索引只增加访问路径，不能用纯静态文件检查替代真实 MySQL 应用结果。
2. 保留 CAP-03 hot/multi 的 before 结果和完整查询原文；在阶段间隙、同数据和相同参数采 after JSON/ANALYZE、SHOW INDEX、行数，核对实际访问行数、join/排序、所选索引及计时。不得只对简化的 `SELECT id` 或去掉 EXISTS/LEFT JOIN 的查询解释收益。
3. 功能回归须覆盖单作者及同读者多个作者、相同发布时间 ID 倒序、跨页复合 cursor 无重漏、空关注和关系停用后不可见、非公开/已撤回笔记不可见、门店标签装配与可空结果；已有 cursor/unfollow 用例在 `TestIntegrationFollowAndFeed`，多作者覆盖仍需核对实际用例，不把32读者模式当替代。
4. 同资源、固定数据/页长/读者与 key 分布、同负载器配置、预热与观察器复跑代表性 HTTP 档，再看 MySQL CPU、SQL池实际等待、P95/P99、成功率和 dropped。before 已在175/200档观察到 MySQL接近4核预算及池等待，这些仍需与各自原始阶段证据对应；本设计不新增成绩，也不同时调整池或 Go 查询。

此处列的是迁移后的必要检查，由根 agent 调度；本文件只交付可审阅的最小索引候选及回退，不标记实测通过。
