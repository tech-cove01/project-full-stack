# GO-021 独立核验：活动发布、下架与可靠事件

日期：2026-10-26。结论：**GO-021在声明范围内通过，未发现阻断GO-030的应用缺陷。** 活动、发布worker/outbox、前端分别经独立只读审查；真实依赖与浏览器由核验者复跑，另补取消和轮询观察。此结论不包含正在开发的GO-030。

## 固定版本与无冲突核验

- 起点bb5d985d69bfb3d38d694b33ee934fc1499f647f；GO-021a为07436b90dde53a2043527c19a08ae6b3034b4ab4，GO-021b为b55be123059992b36256541b4259d06f26365358，GO-021c为1fcf37de56d0512060aeffdfefb1fe25e53a5a6d；核验终点e410c71d9d8dfeaf654e203e439fa3f871c9afd4。
- 独立分支review/go021-e410c71与worktree，固定到终点；审查者只读指定git show/diff。不读取后续工作区作为本票依据，不修改main文件、暂存区、进度页或其运行服务。
- 核验专用Compose项目project-full-stack-review-go021，独立MySQL3319/Redis6391/Go8093/Vite5185。空卷执行原001/002，再用既有runner应用003—005；未新增DDL、共享开发库或运行订单worker。
- 冻结Go二进制注入完整e410c71 revision，SHA256为fea0e5a258a15574e54d2f577e8c220ab948e036f1ad6ba60743f0a84c9e167d；[源代码核对](source-check.json)确认应用跟踪文件未变。额外探针不改变生产实现。

## 独立实际执行结果

| 检查 | 结果 | 证据/边界 |
| --- | --- | --- |
| go mod verify / go vet ./... / gofmt -l . | PASS | 模块校验通过，无vet/格式诊断 |
| go test -count=1 ./... | PASS | 冻结版全包普通测试；原始输出在本聊天工具记录 |
| 真实MySQL/Redis集成 | PASS，50主组 | [原始事件](integration.jsonl)：活动9、outbox5、gate6、publication5、auth7、merchant11、shop7；不计单位测试子组凑数 |
| 前端build / format:check | PASS | [构建](frontend-build.txt)、[格式](frontend-format.txt)，独立node_modules/输出与锁文件 |
| 启动/关闭脚本 | PASS，6场景 | [bootstrap](bootstrap.json)，Go身份、正常退出、重绑端口、缺配置/依赖拒绝/占端口 |
| UTC真实页面 | PASS，29项、cleanup=true | [UTC结果](ui-utc.json)、[下架截图](ui-utc-offline.png)；原28项加实际轮询发生的观察 |
| 上海真实页面 | PASS，29项、cleanup=true | [上海结果](ui-shanghai.json)、[混合角色截图](ui-shanghai-mixed.png) |
| 工作台真实回归 | PASS，24项、cleanup=true | [工作台](workspace.json)、[截图](workspace-mixed.png) |
| dispatch行锁等待期间取消/租约恢复 | PASS | [探针事件](cancellation-probe.jsonl)、[探针源码](review-cancel-test.go.txt)，具体窗口见下节 |
| 进程/测试容器收尾 | PASS | [汇总](summary.json)、[容器](containers-stopped.json)；Go exit0，无强杀，专用MySQL/Redis均Exited0，四端口关闭，卷保留 |
| Linux race / 远端CI | 未执行 | 未用Windows普通测试冒充Linux/远端结果 |

## 关键正确性审查

创建和变更事务使用READ COMMITTED，按shop→merchant→member当前共享锁授权，再锁voucher/库存行；事务内不回退到全局db。真实活动测试命中撤权先持锁、业务查询到达等待、撤权提交后403的窗口，并对照无新增券/审计/outbox。反向竞争、商家停用与降级竞争主要由同一锁协议静态支持，本次未逐一实测。

商家范围内的幂等键与规范JSON摘要绑定完整业务字段；8并发同键同内容只一活动，不同内容409。合法同商家管理用户共享键属于设计，跨商家隔离不依赖前端；本次未另跑多管理者共享键的专门实验。审计/outbox失败回滚与HTTP成功后丢响应重试分别验证，不能将它们等同精确COMMIT响应丢失。

outbox只认领BINARY精确匹配的三类活动事件，不消费订单/社区/未知事件；attempts与有效租约共同fence完成/重试，SKIP LOCKED可跳过已锁行。业务效果成功后才Done，失败持久退避，10次失败仍PENDING。PREPARE提交ACTIVE/审计/OPEN时Redis仍CLOSED，独立OPEN检查当前DB状态/版本；下架同步关闸后才提交OFFLINE。旧OPEN、重复PREPARE/OPEN不重置模拟消耗的库存，缺键不自动重建。

下架DB失败保留CLOSED，后续继续同动作收敛，不自动恢复OPEN；legacy无phase gate只允许保守关闭，新旧摘要不匹配安全409，正式旧状态接管仍留GO-071。没有将这些保守边界宣称为无缝兼容。

前端按每行shopId显示管理动作，STAFF只读；分页/筛选、失败清授权、字符串ID/BigInt分金额、上海时间和明确偏移均核对。真实丢响应后关闭/重开仍保留原key与冻结body，DB活动和创建审计各一条；离页销毁草稿不宣称跨页面持久恢复。库存97仅是模拟Redis消耗，不代表GO-030已成单。

## 补充的两个行为证据

原页面脚本先等任意行存在就离页，可能在新刷新尚未结束时通过“无后续请求”。核验者仅在独立探针中增强：等当前刷新完成、目标行显示待开放、观察一次真实自动GET，再离页等待3600ms（超过两个1500ms周期），确认无新活动请求；两时区各29项通过。[探针对照补丁](observed-poll.patch)为零上下文LF，可在独立拷贝用git apply --unidiff-zero复现。建议实现者后续将这个观察补入自测，不是产品返修或暂停GO-030的理由。

另以voucher更新锁阻塞真正的worker.Poll dispatch，GORM Row到达信号与100ms未完成确认正在等待；取消context后在3秒预算内返回错误，持久状态保持PROCESSING/attempt1，DB仍INITIALIZING且Redis四键未创建。释放锁并仅将自有租约受控设为过期后，再执行真实Poll，PREPARE与OPEN均Done、DB ACTIVE/version1、ACTIVE审计和OPEN事件各一、gate ready。没有把受控过期说成实际等待30秒，也没有把这一Poll探针说成整个进程在中途关闭的端到端实验。

复现该补充探针：在独立e410c71 worktree将review-cancel-test.go.txt复制到backend-go/internal/activitypub/review_cancel_test.go，设置专用依赖环境与GO_INTEGRATION=1，运行go test -count=1 -run TestReviewCancellationDuringLockedPublication ./internal/activitypub，随后只移除自建探针文件。实现者工作区未加此文件。

初版探针误用GORM Raw回调观察Scan，到达信号未触发而失败；[初版日志](cancellation-probe-hook-failure.jsonl)保留。改为Row回调后同一冻结生产代码通过，此失败属于核验探针，不记作应用故障。

## 未覆盖与收尾

未直接验证Done更新失败后的完整自动重投、COMMIT已持久但返回丢失的精确DB窗口、物理Redis断网下CLOSE超时、持续容量及多实例生产高可用；租约fence、重复投递与有界取消分别有证据，不能合并宣传为所有故障窗口均覆盖。

CI现有普通race及auth/shop/merchant真实集成步骤；本票新增activity/activitypub/outbox真实套件尚未显式接入CI。后续CI收敛时补入这三类并保留共享claim测试的互斥策略，本轮本地真实套件已独立通过，不阻断GO-030。

两个页面轮次及工作台夹具精确清理并注销session；临时Go探针复用精确ID/key清理，验证码节流按原TTL过期，不重置AUTO_INCREMENT。增强UI和取消探针未修改冻结应用；仅停止核验专用项目，未删除卷、动全局数据库时区或干预实现者服务。

GO-030按已有方案继续，后续独立验收Lua准入→Stream→DB成单、unknown outcome与ACK；GO-033前不开放库存重建。本报告单独提交在review分支，不合并到main、不替实现者修改正在维护的进度文档。
