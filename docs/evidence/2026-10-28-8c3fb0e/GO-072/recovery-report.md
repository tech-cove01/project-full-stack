# GO-072b / 持久停车、精确重放与投影重建

开始 `8c3fb0e0fe1169ad98e19faaf7ae49f907362530`，2026-10-28，自测通过待独立核验。依赖已提交投影基础 `c90c3e57b9ec59eadc80c3e1fbd7f3b5181a1af1`。本功能交付 Runtime、受控CLI、失败键扩展迁移011；消费者及业务查询另提交。

Runtime先校验Kafka key与已验证envelope的aggregateId一致；有eventId/eventType/version header时还须一致且不重复。Apply只有去重收据及投影同事务提交才成功。超过业务范围的key/header、坏JSON或不一致事件不能应用，但仍可把原始字节保存在MySQL。Park按投影/topic/partition/offset唯一且内容SHA256不可变；同坐标异内容拒绝覆盖，固定原因码不保存driver异常文本。失败表没有TTL；运维可按投影统计PARKED/RESOLVED、收据数量与最后应用时间。

Replay持有准确失败行FOR UPDATE，在同一外部事务内调用Apply（GORM嵌套savepoint）并将原失败行RESOLVED；任一出错全部回滚。并发/重复重放安全，不能通过编辑原JSON或重置offset制造成功，仍无效的消息保持PARKED。CLI只输出结果/计数，不输出原始body、headers、密码或用户凭据。

重建一个投影时，独占控制行；增量Apply在同控制行取共享锁，因此不会写入未发布的半张快照。DELETE与INSERT SELECT当前MySQL事实、rebuilt_at在同一RC事务提交，失败回滚旧模型。保留去重收据和失败表，不改Kafka offset。重建行保存当前事实版本和空哈希；只有相同版本、相同目标状态/绑定/时间的合法事件才能首次补齐哈希，旧事件不能降级。社区事实写入可继续，后续变化通过既有outbox增量收敛；这不是停写全局generation或每用户fan-out重建。

```powershell
cd backend
go run ./cmd/community-projector stats follow-v1
go run ./cmd/community-projector replay feed-v1 <topic> <partition> <offset>
go run ./cmd/community-projector rebuild follow-v1
go run ./cmd/community-projector rebuild feed-v1
```

需要已显式配置的隔离MYSQL/REDIS环境（CLI实际只连接MySQL）及009—011迁移。重建会替换该环境下对应整张派生模型，不能把测试命令指向未知数据库。移除代码不会撤销新增表；回退保留迁移校验和、失败记录及事实数据，先停消费者，不删卷。

验证在专用MySQL3330/Redis6402、API停止且持`go072_integration`测试锁时执行：`GO_INTEGRATION=1 go test -count=1 -v ./internal/communityprojection -run TestIntegrationProjectionRecovery`。[最终5组](recovery-real-final.txt)通过：空字节规范/毒消息不可变、停车大key与超限header、Replay效果与RESOLVED整体回滚后幂等重试、缺笔记事实恢复、重建独占窗口阻止增量写入且释放后安全恢复。测试重建的是专用数据库投影，未改业务事实或共享Kafka消费位点。

交叉只读审查发现nil与空byte经DB存储后哈希不同，原始[复现FAIL](recovery-digest-initial.txt)保留；哈希先统一非nil空字节后相同用例通过。第一次4组[初轮PASS](recovery-real-initial.txt)保留，不能替代新增边界的最终5组。Park保留毒消息不表示自动修复毒数据；无效原文重放仍报错且不标RESOLVED。

本功能直接真实MySQL事务验证，不冒充真实Kafkaoffset提交证据；后者由consumer报告提供。未做灾难数据丢失恢复、持续积压容量或全局停写generation切换。
