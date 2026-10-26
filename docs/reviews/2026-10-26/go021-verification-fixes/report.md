# GO-021 独立核验后的验证修复

结论：未发现 GO-021 业务阻断；补齐 CI 与页面测试的证据缺口。修改仅涉及一个工作流和两个测试脚本，不修改应用、DDL、Lua 或接口。

## 独立提交

- `91b51c26afacdb1787871d40d6cd6a9310d2c624`：GO-021-QA1，版本 SQL 迁移之后、API 启动之前，串行执行 activity/activitypub/outbox 的真实依赖测试；Linux 工作流保留 `-race`，新增 `-p 1` 与显式 `GO_INTEGRATION=1`。
- `7a42c0d00329ddb82d66feb7e5f958b4400b0bf4`：GO-021-QA2，等待页面加载完毕后核对当前角色与准确门店 ID；活动页观察到真实自动 GET 后再离页，经过 3600ms 检查没有继续请求。
- 修复追加于隔离分支 `fix/go021-verification`，基线为 `e410c71d9d8dfeaf654e203e439fa3f871c9afd4`。应用运行文件复用该冻结基线的 Go 二进制和 Vue 源码；不把 GO-030 未核验修改混入本轮成绩。

## 验证与证据

- CI YAML 解析通过，确认真实测试位于迁移之后、API 启动之前。Windows 本机执行 `GO_INTEGRATION=1 go test -count=1 -p 1 -json ./internal/activity ./internal/activitypub ./internal/outbox`：活动 9、门控 6、发布 5、outbox 5，共 25 个真实集成主子组通过，无 fail；同次执行的普通测试也通过。原始结果见 [JSONL](ci-packages-native.jsonl)。
- 隔离已有数据库的迁移检查及再次重跑均通过，均为校验后 skip；本轮不声称重新验证空库首次升级，见 [第一次](migrate-first.txt)、[第二次](migrate-repeat.txt)。
- UTC 活动页面 [29 项](activity-utc.json)、上海活动页面 [29 项](activity-shanghai.json)、商家页面 [28 项](merchant.json) 均通过，三个脚本均报告精确夹具清理成功。总结果见 [summary](summary.json)。
- 两脚本 `node --check`、`git diff --check` 通过；另一个只读审查者复核最终 diff，无阻断问题。
- 仅启用独立 compose 项目 `project-full-stack-review-go021`：MySQL 3319、Redis 6391、Go 8093、Vite 5185。Go 正常退出；Vite 通过 Windows CTRL_BREAK 退出；未强制终止。测试容器已停止，卷保留，不触碰开发服务。

## 失败记录与边界

首次 UTC 运行的 Node 原生进程以 `3221226505` 退出，未写出页面 JSON，退出前完成多少检查未知；底层 stderr 未保留，根因未确定。随后使用相同脚本重跑，三组均通过。初轮 summary 被成功重跑覆盖；[初轮记录](process-exit-initial.json) 明确标注为从工具实际 stdout 恢复，不作为原始文件冒充。该次遗留夹具先按唯一前缀、精确用户/商家/门店 ID 核实，再清理 SQL、活动键及对应会话；[清理记录](first-run-cleanup.json) 证明剩余行与精确键均为 0，不记录手机号、token 或凭据。

Linux race 与远端 GitHub Actions 未实际执行；本机无 race 的结果不替代它们。测试证明离页后不再发起后续轮询，不额外声称验证了所有在途请求的网络取消。

## 迁移进度交接

截至本次核验：GO-001/010/011/020/021 已通过相应独立核验，GO-030 正在另一个实现现场分步提交，尚未作为整票验收。订单支付/取消/超时、核销/退款/审计、pending/review/补偿/重建与业务 AI 仍待迁移。社区/Kafka/Feed/缓存计数属于后续新增建设；持续压测与 Go 正式目录切换尚未完成。

本报告不修改实现者正在维护的 `docs/README.md`；实现者下一次交接时应把 GO-020/021 状态同步为相应已核验范围，保留 Linux race/远端 CI 限制。不能沿用 Java 压测数值作为 Go 结果。
