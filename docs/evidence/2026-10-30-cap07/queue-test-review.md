# CAP-07A3 排队回归同步修正

内部非作者审查指出，观察到第二 caller 的 Redis 首查尚不足以证明共享任务已经入队；二查也可能重复关闭同一测试 channel。生产实现未改。

测试改用 sync.Once 保护 hook，确认 2 flight / 1 active 后才取消 caller；取消后共享任务仍在，Close 后全部 admission 归零且内容 SQL 仍仅一次。定向 `go test -count=1 -run '^TestActiveQueueAndWholeLoadDeadline$' ./internal/communitycards`、对应 vet 和 diff 检查通过。此前全套普通与真实相关回归保留在 phase2；此处只补直接相关验证，不把旧测试通过当成已验证排队屏障。
