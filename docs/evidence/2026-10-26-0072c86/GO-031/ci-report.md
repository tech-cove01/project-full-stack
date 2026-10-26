# GO-031e / 超时扫描集成进入 CI

在已有订单真实依赖步骤中增加 `./internal/ordertimeout`，保留 `GO_INTEGRATION=1` 与包串行 `-p 1`。该步骤位于版本迁移之后、构建/启动API之前，避免测试用扫描器触及同时运行的应用夹具。新增 Pay/Cancel 测试由既有 `./internal/order` 步骤自动纳入。

```sh
go test -race -count=1 -p 1 -v ./internal/orderadmission ./internal/order ./internal/orderstream ./internal/ordertimeout
```

本地 PyYAML 全文件解析 PASS，见 [静态结果](ci-yaml-check.txt)。Windows 普通 Go 全套、真实支付8/取消11/扫描SQL6/实际生命周期联调3主组分别有独立证据，见整票报告；普通模式跳过集成不会被写成集成已执行。

未运行 Linux race、远端 GitHub Actions 或持续负载；本票仅记录配置补齐及本地已执行结果。
