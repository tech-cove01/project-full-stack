# CAP-06：真实存储包装测试的 CI 覆盖修复

问题：交付 `a3c64b5361069ae4491b9de76654b5432ac9f9a8` 新增的 `TestIntegrationObservedStorageTransactions` 需要 `GO_INTEGRATION=1`。原 CI 的全包 race 步骤未设置该开关；随后真实依赖步骤又未列入 `internal/platform/taildiag`，因此这项真实包装测试仍被跳过。交付的四作业 CI 成功是真实结果，但不能据此声称它已跑该测试。

修复：只在既有 backend-go 作业、隔离 MySQL/Redis 已就绪并完成版本迁移之后，增加 `GO_INTEGRATION=1 go test -race -count=1 -v ./internal/platform/taildiag`。沿用原依赖、凭据和版本，不更改业务代码或新建测试环境。

本地验证：新自有卷、专用回环端口 3462/6562 上执行版本迁移两次均通过；`GO_INTEGRATION=1 go test -count=1 -p 1 -json ./internal/platform/taildiag ./internal/social ./internal/communitycache` 三包通过，包含该测试。普通七包及对应 vet 通过。精确命令、原始输出、环境和收尾见同目录总核验报告；Windows 本地未运行 race，Linux race 以修复后精确 SHA 的远端 CI 为准。

该修复只补持续验证的遗漏，不是原 Feed 长尾的业务修复，不满足 CAP-06C/D 或 CAP-07 前置。
