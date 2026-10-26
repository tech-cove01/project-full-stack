# GO-030e / 真实订单集成进入 CI

在已有 Linux Go job 的版本 SQL 迁移、活动集成之后、构建/启动 API 之前，增加三个订单包的真实 MySQL/Redis 测试。明确 `GO_INTEGRATION=1`，避免普通 `go test -race ./...` 跳过集成后被误认为已覆盖；`-p 1` 串行执行包，测试期间没有应用消费者消费共享 stream.orders。

```sh
go test -race -count=1 -p 1 -v ./internal/orderadmission ./internal/order ./internal/orderstream
```

本次保留此前核验 agent 新增的活动 CI 步骤，仅追加订单步骤。PyYAML `safe_load` 本地解析整个工作流通过，步骤配置已核对；原始静态解析输出见 [ci-yaml-check.txt](ci-yaml-check.txt)。新增三个包的 Windows 普通测试及真实依赖结果分别见 [全套验证](go-validation-final.txt)、[准入](admission-real.txt)、[落单](order-real-final.txt)、[消费](stream-window-final.txt)。

本次未执行 Linux race 或远端 GitHub Actions；这里只证明 CI 已配置及本地非 race 结果，不声称该远端步骤已经运行成功。不因声明流水线而覆盖现有未执行边界。
