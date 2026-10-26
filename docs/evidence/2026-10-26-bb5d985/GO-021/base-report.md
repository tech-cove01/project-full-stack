# GO-021a / 活动门控与交易 outbox 基础自测

开始 SHA：`bb5d985d69bfb3d38d694b33ee934fc1499f647f`。2026-10-26，Windows Go 1.27.0，已有隔离 MySQL 8.4 / Redis 7.4（3308/6380），无新 DDL。

本子提交交付可独立测试的 gate 与 outbox repository，尚未组装活动 HTTP 或后台 relay。其余 GO-021 工作区文件不包含在本提交。

- `PREPARE` / `OPEN` 在首次写前检查参数、四键类型和完整性；现有库存绝不覆盖，缺键不重建。键命名、PREPARE 参数顺序保留；OPEN 在原 gate 后追加 stock/begin/end 三键做完整检查。
- gate 增加 `phase=PREPARED/OPEN/OFFLINE`；下架失败窗口内同版本的 OPEN/PREPARE 也不能重开。无 phase 的 Java gate 不自动接管开闸，只能安全下架；正式接管策略留 GO-071。
- outbox 与调用方 tx 同事务；重复事件核对完整语义，只认领精确 ACTIVITY 三类事件，订单/社区/未知事件不受影响。claim 后 attempts 和有效租约共同 fence 完成/重试；Redis 等持续失败保持 PENDING，退避最多 60 秒。

实际运行：`go test -count=1 ./...`、`go vet ./...`、`gofmt -l .` 通过；`GO_INTEGRATION=1 go test -count=1 -v ./internal/activity ./internal/outbox ./internal/activitypub ./internal/auth ./internal/merchant ./internal/shop` 通过，其中本提交 outbox 5 主组、gate 6 主组（含四键错类型子组）。测试使用随机精确 ID/键并清理，未 FLUSH 或删卷。完整整批原始日志在本票收尾证据中保存。

覆盖：事务回滚、重复事件冲突、白名单、过期租约接管、旧 owner 无法完成/重试、SKIP LOCKED、10 次失败仍 PENDING、四键错类型/坏参数无部分写、扣库存后重投、缺键不恢复、同版本下架屏障、legacy 保守策略。

状态为自测通过待用户独立核验。未执行 Linux race、远端 CI 或持续负载；不声明活动发布页面或订单流程已完成。
