# GO-076b 公开笔记 Kafka → ES 同步与受控恢复

起点 `290cd60`（GO-075 完整交接）；依赖 GO-050/052/072 的事实与六字段事件契约，以及 GO-076a ES 客户端。实现者自测通过，待独立核验；本模块未自行提交，完整 SHA 由根交付报告记录。

## 交付行为

- `community.search-v1` 是独立稳定消费组。只为 `NOTE_PUBLISHED` 索引当前 MySQL 公开笔记的白名单字段；合法但无关的关注/点赞事件持久记为 `IGNORED`，未知或损坏消息不伪造成功。
- 012 只新增本搜索的控制与版本哈希表，不改写 GO-072 的 follow/feed 控制行。共享原有 receipt/failure 表的泛型 projection 键，旧运行时仍只接受原两类投影。
- 校验原始 topic/partition/offset、key、六字段 envelope、可选事件头及大小上限。应用锁搜索控制行，再锁当前公开笔记事实；作者与版本必须合法。实际 ES 文档取数据库当前版本、标题、正文、作者、门店及创建时间，不能用消息负载制造公开内容。
- 先成功执行 ES `external_gte` 写入，再在同一 SQL 事务写文档版本/哈希及 eventId receipt。相同事件不同 hash、相同业务版本不同公开文档均拒绝；低版本消息可索引更高的当前事实，不能把新结果降级。未初始化或配置换了 alias 时要求受控重建，不能靠旧 receipt 跳过新资源初始化。
- ES 与 SQL 没有分布式事务。ES 成功、SQL 未提交时重投相同文档版本收敛；ES 409 不盲目当成功。只有持久效果或持久停车成功，现有同步提交 offset 的 worker 才能前进。数据库未知结果不提交。
- 停车保留原始坐标、key/body/headers 和不可变摘要；精确重放锁失败行、复核摘要，应用效果和 `RESOLVED` 同一 SQL 事务。重放不修改消息、不重置 offset，损坏的原消息仍保持停车。
- 重建持有独占控制行，按 MySQL RR 同一快照游标每页 250 条构建新物理 generation，完整写入与 refresh 后原子切换 alias。保留旧 generation、receipt、失败记录和 Kafka offset。CLI 要求停止 API/消费者并显式声明离线重建；不宣称零停机。

## 运行

应用版本迁移 003—012，配置专用 `MYSQL_*`、`REDIS_*`、`ES_URL`/`ES_INDEX`/`ES_TIMEOUT_SECONDS`。恢复命令只连接 MySQL 和 ES，不消费 Kafka；配置装载仍要求项目既有完整核心环境。

```powershell
cd backend
go run ./cmd/note-search stats
# 首次启用以及受控全量恢复：先停止本环境的 API / 消费者
$env:GO_SEARCH_REBUILD_OFFLINE='1'
go run ./cmd/note-search rebuild
go run ./cmd/note-search replay <topic> <partition> <offset>
```

普通 stats/replay 限时 30 秒，rebuild 限时 5 分钟；命令错误输出固定摘要，不输出正文、凭据或驱动诊断。切换 alias 成功后若 SQL commit 或网络确认失败，不能声称旧 alias 未动；保留物理 generation，在 API/消费者停止状态下重跑 rebuild，使实际 alias 与 SQL 元数据重新收敛。代码回退不会自动撤销 012；新增派生表可保留，回退不删除业务事实或数据卷。

## 实跑证据

专用 MySQL `3332`、Redis `6404`、Kafka `9099`、ES `9202`；ES 8.19.23。测试期间没有 API/其他消费进程共享 fixture，持有 `go076_integration` 数据库命名锁。私有 ES alias/topic 随机生成；清理仅删除该 alias 生成的物理索引、自有 topic、精确 SQL fixture/receipt/failure，并恢复测试前搜索控制与派生状态。没有删除共享 topic/group 或旧开发环境资源。

```powershell
# 在 backend/，先加载上述专用环境
$env:GO_INTEGRATION='1'
go test -count=1 -timeout 3m -v -run '^TestIntegrationSearchSync$' ./internal/notesearch
go test -count=1 ./internal/notesearch ./internal/communityconsumer ./cmd/note-search
go vet ./internal/notesearch ./internal/communityconsumer ./cmd/note-search
```

[最终真实集成](sync-real-final.txt) 1 入口、7 子组通过：

1. 3/2/1/重复事件最终 ES 与 SQL 都为当前 v3；不同 payload 的同 eventId 和不同文档的同业务版本拒绝，换未初始化 alias 不利用旧 receipt 误报成功。
2. 实际 ES 写入后强制外层 SQL rollback，ES 已有文档而 receipt/state 为 0；重投恢复到恰好一条 receipt。
3. ES 连接不可达后原消息持久停车；同坐标内容篡改拒绝，恢复真实 ES 后精确重放并幂等解析。
4. RR 快照正在真实 ES 写 v6 时，另一连接提交 v7 事实，增量应用受控制行阻塞；新 generation 激活后增量索引 v7，旧 generation 和失败/receipt 历史保留。
5. 合法 FOLLOW 事件得到唯一持久 `IGNORED` receipt，重复不产生第二条。
6. 真实 Kafka worker 消费成功/重复/毒消息/ES 不可达四条，只有持久索引或停车完成才到 broker offset 4；ES 故障记录可重放，毒消息原样仍失败。关闭一个独立 SQL 连接池后，后续消息应用/停车都失败，offset 保持 4；健康消费者重投后到 5 并实际索引。
7. 真实 alias 已切换后强制 SQL rollback，观察到 alias 与 SQL 控制元数据分别持久；再次受控 rebuild 使二者一致，并保留 receipt/失败历史。

[普通模块测试](sync-unit-final.txt)、vet、gofmt/diff 检查通过。独立只读审查由 `provider_adapter` 执行，检查事务、锁、版本、停车重放、离线 CLI 和迁移，无需修复；该审查不把实现者的真实自测改为正式核验状态。

初始证据 [sync-real-initial.txt](sync-real-initial.txt) 是添加后续 fixture 时暂存未使用 imports 的编译失败，未访问真实依赖；已修复并保留。随后 [sync-real-second.txt](sync-real-second.txt) 5 子组通过，最终新增无关事件与 alias/SQL 歧义窗口后 7 子组通过，不覆盖原始轮次。

## 验证限制

- 关闭独立 SQL pool 是真实驱动访问失败，不是物理 MySQL 停机；ES `127.0.0.1:1` 是连接故障注入，不是 ES 容器停机。实际容器故障与页面降级由根 GO-076c 另行取证。
- 控制 rollback 复现 ES/SQL 的提交窗口，没有执行 OS 强杀或断电。外部管理员绕过本协议直接改 alias/ES 文档，不在该收敛保证内，需停止应用后受控重建。
- 这里通过直接修改自有事实并显式发送对应事件验证版本交错，不冒称笔记编辑/下架 API 已上线。公开状态由搜索查询侧实时 MySQL 复核，ES 滞后不是授权依据。
- 本模块没有浏览器、付费模型或持续负载结果；新代码的远端 CI/Linux race 未由本 agent 执行。重建的逐条 ES 写入与独占控制行是当前小规模恢复取舍，不宣称生产集群吞吐或零停机重建。
