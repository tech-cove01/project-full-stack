# GO-072 / Kafka 消费投影交付

2026-10-28。**自测通过待独立核验**。开始SHA `8c3fb0e0fe1169ad98e19faaf7ae49f907362530`。先读根STATUS、AGENTS、交接和10-28核验报告，确认GO-031—071的15票已按实际范围完成核验/修复；本次交付下一票新功能，不重复或覆盖上一轮结论。

## 小功能提交

| 范围 | 完整SHA | 证据 |
| --- | --- | --- |
| GO-072a 事件协议/持久版本投影 | `c90c3e57b9ec59eadc80c3e1fbd7f3b5181a1af1` | [repository报告](repository-report.md) |
| GO-072b 持久失败/重放/重建 | `942034a0c43a058486bc34a07a58746cb2b4d338` | [恢复报告](recovery-report.md) |
| GO-072c 实际关注/Feed读入口 | `b070bb94b64b8fb64d13cddf5a4e668436f03115` | [查询报告](queries-report.md) |
| GO-072d 消费者与应用组装 | `d4c81ec56a02cb98e0dd71ec54fc02eb9f83e5a9` | [消费报告](consumer-report.md) |
| GO-072e CI覆盖 | `c0f282dc5501ad4ca3b67cf2a18e53ebde4f2c29` | [CI说明](ci-report.md) |
| GO-072f HTTP到Kafka双投影贯通 | `acd26af3b1b0f8c2053d868993ad7fa75157302c` | [贯通报告](flow-report.md) |

代码/必要测试/说明随各小功能提交，最终状态与来源另作文档提交。共享main期间进入的方案提交 `f1df2f28480210df75a6acf24753b07cc81e1578` 已保留，不将ES或真实Provider纳入本票在途实现，也不改写他人历史。

## 实际交付行为

- 配置Kafka后启动两个稳定独立group：`community.follow-v1` 维护 `(userId,targetId)` 关注正反索引，`community.feed-v1` 维护公开笔记作者/版本/时间目录。不同group分别提交自身offset，已知无关类型记IGNORED；例如LIKE_CHANGED不在本票新增另一套点赞事实。无Kafka配置时不启动relay或这两个消费者。
- 保留原六字段envelope、字符串业务ID、数字version，兼容FOLLOW的字符串/布尔active；拒绝重复字段、异义eventId、key/header不一致及越界值。eventId+规范内容hash收据与投影同MySQL事务提交；同ID异义拒绝，旧version不覆盖新状态，关系版本按双方用户而非仅作者分组。
- `FetchMessage`→持久Apply或Park→同步`CommitMessages`，不使用自动提交的ReadMessage。三次应用失败后保存原source/key/body/headers/hash为PARKED；DB不能持久化时不提交、不越过当前消息。提交offset失败只重试该提交。持久停车是保留待处理证据，不是效果已完成。
- CLI `community-projector stats|replay|rebuild` 提供按组计数、精确原消息重放与单投影重建。重放效果/RESOLVED同事务；重建使用控制行独占锁挡住Apply共享锁，保留收据与失败记录、不重置Kafkaoffset。原文仍无效时保持PARKED，不编辑消息制造成功。
- 既有Relationship与Feed可返回 `source=projection|database`，分别表示公开关注计数或候选目录来源。本人状态、正文、当前关注与公开条件仍查DB；同只读RR快照检查完整性，缺历史、半目录或可选查询错误回退DB，分页不漏。取关后的下一请求立即按新事实隐藏内容，即使消费者滞后。

此实现是作者目录+实时pull，不是每用户fan-out队列、持久Feed版本或全局停写generation。既有Redis点赞集合不被替换；不宣传跨MySQL/Kafka exactly-once、灾难零丢失、跨实例强一致或持续容量。

## 已执行验证

专用Compose `project-full-stack-go072`，MySQL3330/Redis6402/Kafka9098与独立卷；未接触运行中的开发3308/6380、旧参考3307/6379或CRM服务。Go1.27.0 windows/amd64、镜像MySQL8.4/Redis7.4/apacheKafka3.9.0；源码/二进制SHA256见[来源清单](source-manifest.json)。二进制revision `go072-worktree` 为当时已完成生产代码的工作区构建，后续仅补测试/文档。

| 检查 | 实际结果 |
| --- | --- |
| 版本迁移 | 009/010/011首次应用与校验和重复跳过通过；[首次](migrate-initial.txt)、[恢复表](migrate-recovery.txt)、[键扩展](migrate-key.txt)、[重跑](migrate-repeat.txt) |
| repository真实MySQL | 9主组PASS，含20并发、乱序/异义、原子回滚、bootstrap与控制行屏障；[最终](repository-real-final.txt) |
| Runtime恢复真实MySQL | 5主组PASS，含原始毒数据、停车不可变、Replay整体回滚、缺fact恢复、重建与增量互斥；[最终](recovery-real-final.txt) |
| 实际查询 | 1真实入口/20子组PASS，同秒三页、历史/半缺、草稿/取关、真实SQL错误回退、受控RR窗口；[最终](queries-final.jsonl) |
| Kafka消费者 | 6真实主组PASS，broker OffsetFetch证明提交位点；[最终](consumer-real-final3.txt)；普通7主组、格式/vet通过 |
| 真实HTTP handler贯通 | 登录→note/follow/like三事件→真实relay→Kafka→两组→实际查询；offset3→4、无事件历史回退、消费前取关守护通过；[最终](flow-real-final.txt) |
| 全套普通Go与构建 | mod verify、gofmt、vet、build通过；`go test -count=1 ./...`全包通过，[日志](go-test-final.txt) |
| 应用六类启动/关闭 | 带Kafka配置的最终二进制6场景PASS，正常退出0、未强杀、端口可重绑；[原始结果](bootstrap-final.json) |
| 索引/CI | 反向关系count与作者时间目录查询EXPLAIN命中覆盖索引，[原始结果](projection-explain.txt)；仅小测试库，不是大规模性能证据。YAML解析通过，GO-072远端CI/Linux race未运行 |

无前端源码改动，未重跑无关Vue构建或GO-074浏览器矩阵。按任务做模块定向验证、全套普通检查与交叉只读审查，没有再次跑上一轮全部真实交易/AI故障。

复跑：显式配置上述专用 `MYSQL_*` / `REDIS_*` / `KAFKA_BROKERS`，根目录执行 `node scripts/migrate.mjs`；API消费者停止时在 `backend/` 执行：

```powershell
go mod verify
gofmt -l .
go vet ./...
go build ./...
go test -count=1 ./...
$env:GO_INTEGRATION='1'
go test -count=1 -p 1 -v ./internal/communityprojection ./internal/communityconsumer
go test -count=1 -v ./internal/social -run TestIntegrationCommunityProjectionQueries
```

定向组均持 `go072_integration` 命名锁，不能与应用relay同时运行。Kafka测试只建自有私有topic/group，清理精确数据，不清共享 `community.events`。CLI用法见恢复报告，API启动/关闭沿既有bootstrap脚本。

## 失败、审查与限制

- 初始repository真实并发复现MySQL1213；由重复INSERT错误后锁升级改为精确主键no-op duplicate UPDATE先拿排他锁，20并发和最终9组通过。GORM binary扫描形状的测试问题也在原始失败中区分，未删FAIL。
- 只读交叉审查发现nil/空byte存库后hash不一致；[新增用例先FAIL](recovery-digest-initial.txt)，统一非nil字节规范后最终5组通过。毒数据的key/header域校验不能阻断持久停车，011扩展原始key存储，不修改已应用010校验和。
- consumer首轮fixture缺merchant_id，后两轮私有writer命中新建topic元数据缓存；各轮保留。测试显式等待leader并给私有writer独立Transport后6组通过，没有重复未知发送来掩盖位点。未把这些夹具问题记成生产消费者缺陷。
- 初轮沙箱内HTTP回环/依赖连接遭拒；具备正常本机访问权限后同配置全套普通测试与flow通过。flow初始失败日志保留；全套普通测试初轮输出在工具记录，最终磁盘日志是正常权限下的实际PASS，不能写成产品回归修复。
- consumer故障使用独立关闭的DB池和受控context/reader重启，未做MySQL物理停机、Kafka物理断线或OS强杀。brokeroffset与持久行都有时间线；1189/1666ms是功能观察而非持续RPS/P95、硬性5秒SLA。持续负载/积压量化留GO-073，GO-072新增代码尚无远端CI/race结果。
- 查询RR是请求一致快照，允许已在途请求看开始时的事实；不是提交撤权后对所有在途读的线性屏障。缓存公共计数沿原有短TTL语义，source不代表每个字段都来自投影。

## 收尾与后续

[DB核对](cleanup-db.txt)中自有用户/商家/笔记、未完成社区事件、停车记录和新收据均0；测试精确删除私有topic/groups。重建可能保留原demo事实的派生行、计数器和数据卷，不声称整个Redis/MySQL为空。

专用三容器已停止，MySQL/Redis exit0，Kafka在compose正常停止后观察到exit143（SIGTERM）、OOM=false；未强杀、不写成exit0。原开发和CRM服务继续运行。[收尾记录](cleanup-final.json)。启动用户开发库前须正常应用009—011；代码回退不删除表/失败证据或改历史migration checksum，先停两个consumer，再按依赖revert。重建不恢复已丢失的业务事实/任意Kafka保留期历史。

本票交完整SHA供独立核验。按当前STATUS的新方案，后续GO-075真实Provider、GO-076独立ES搜索、GO-074差量页面与最终GO-073持续量化分别进行，不将它们冒充本次完成。
