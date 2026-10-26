# GO-021b / 活动接口与发布 relay 自测

依赖 GO-021a `07436b90dde53a2043527c19a08ae6b3034b4ab4` 和已交付 GO-020（独立核验另行进行）。本子提交交付四条商家活动 API、事务审计、relay 与应用生命周期组装；页面作为 GO-021c 单独提交。对应 P1-1、P1-6 发布权限子集、P1-7 活动事务回滚子集、MIG-GO-03 活动 DTO；不声明订单补偿、核销/退款已完成。

行为：列表逐条 SQL 限制当前成员/商家/具体门店范围，稳定降序分页、门店/状态筛选和字符串 total。创建白名单拒绝重复/额外身份字段，字符串 ID/分金额不经过浮点，带偏移整秒时间归一到 IANA 上海时区。规范 JSON 摘要消除 Java 分隔符拼接歧义；同键 8 并发返回同一活动，不同内容 409。

写事务用 READ COMMITTED，以 shop→merchant→member 当前共享锁授权，随后 voucher→库存行锁串行化；不依赖 GO-020 的普通 RR 读取。创建、ACTIVE、OFFLINE 各自与审计/outbox 原子提交。真实测试用 SQL 到达信号定位“等成员行锁时提交撤权”，原 Token 返回 403，三类成功数据均无新增。

PREPARE 在闭闸下准备 Redis，然后提交 ACTIVE/审计/OPEN 事件；独立 OPEN 读取 DB 当前状态/版本才能开。下架在短超时内同步关闸，再提交 OFFLINE/审计/CLOSE；关闭失败 503，DB 失败保留 CLOSED，重复同动作收敛。旧事件、缺库存及下架 phase 不能触发库存重建或重新开放。retry 不复活 DONE 事件，也不新增重新上架入口。

worker 每次 claim 一事件，claim/dispatch/finish 分别有 3/5/2 秒 deadline，Redis 操作最多 1 秒；应用取消后等待 worker 返回再关连接池，持久 PROCESSING 可在租约到期后接管。worker 与 HTTP 不共享请求 context。

## 实际验证

- [最终 Go 普通全套](go-test-final.txt)：PASS；`go vet ./...`、`gofmt -l .` PASS。
- [真实整轮](real-integration.txt)：活动初轮 8、outbox 5、gate 6、publication 5、认证 7、商家 11、商铺 7 主组 PASS。原始 GIN 路由日志保留。
- [最终活动 9 主组](real-activity-final.txt)：新增锁等待期间撤权后再次 PASS，其余初轮不变。审计/outbox 故障通过限定自有请求 context 的 GORM callback 注入，无新 DDL、无修改其他业务数据。
- [真实启动/关闭 6 场景](bootstrap-final.json)：PASS，正常信号退出 0、未强杀、端口可重绑；另有普通测试证明等 worker 退出后才关闭依赖。
- 两名实现者对彼此未实现的模块做只读交叉审查，并独立重跑普通 test/vet，未发现阻断。这不替代用户的独立核验。

最终测试二进制（忽略文件）`backend-go/bin/api-go021-final.exe`，revision `07436b9-go021-final-worktree`，SHA256 `5C56D3B1AB314D16CD0D563D52876F1E5DFB3AF65FF065CC79E8CC70122E0C05`。与随后提交 Go 源码一致，新增并发测试未改变生产二进制。

## 边界与后续

无新 SQL 迁移。legacy Java 请求摘要不匹配新摘要时安全 409，无 phase 的 gate 不自动开放；GO-071 必须明确排空/接管策略，不能声称无缝接管历史生产状态。Redis readiness 读取异常时列表保留 DB 状态、`ready=false`。失败下架不能自动修复回 OPEN；调用方应继续同动作下架。无 `/reconcile`，重建留 GO-033；交易受理/Stream 留 GO-030，故本票不宣称真实抢购或已售订单验证。

Linux race、远端 CI、持续负载和真实 Redis 断网下 CLOSE 超时窗口未执行；CLOSE 错类型/失败回滚是真实依赖验证，错误 Redis 连接重试恢复由闭池客户端验证，不混称为物理网络故障。
