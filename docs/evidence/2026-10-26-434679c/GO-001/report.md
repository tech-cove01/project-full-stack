# GO-001 自测记录

日期：2026-10-26（Asia/Shanghai）。状态：自测通过待核验。

- 开始 SHA：`434679cf86aaa125f1f9a35fa695cc7050eed907`；交付 SHA：`69ddd3ee001251b5a16ee252fbacf51130643630`。
- 范围：Go 配置/连接池/健康/HTTP 生命周期，隔离 Compose，Vite 代理，Linux CI 配置。未新增/修改 DDL，应用不执行迁移；隔离空库仅复用首次初始化 SQL。没有迁移业务、开 worker、清库存或操作 Java 实例。
- 环境：Windows amd64 / Go 1.27.0，i9-13900H / 20 逻辑核 / 约 31.6 GiB RAM；Docker Engine 29.3.1、Compose 5.1.0。API 随机 loopback 端口，真实隔离 MySQL 3308 / Redis 6380，单实例。
- 镜像：MySQL `mysql@sha256:76f0358884cb92d9a086d24e5671c38048d3fef40f2588703d73eb55392d6b69`；Redis `redis@sha256:57e4255cc971b1c8d6f8a6ca283ac01bc3ecdd914273ef6bfaede87aaecd6541`。

## 命令与实际结果

在根目录 `docker compose -f compose.go.yaml up -d --wait mysql redis`，两个独立容器 healthy。环境按 `backend-go/.env.example` 显式注入，原 Java project / 3307 / 6379 保留。

| 命令/行为 | 实际结果 | 证据 |
| --- | --- | --- |
| `go mod verify` | PASS，all modules verified | go.mod / go.sum 精确版本与校验和 |
| `gofmt -l .` / `go vet ./...` | PASS，无格式差异/诊断 | 源码与本地执行输出 |
| `go test -count=1 ./...` | PASS，含缺配置/时区/DSN/ready取消与超时/JSON异常/正常排空/超期强关/Accept故障排空 | [最终输出](tests-final.txt) |
| `go build -trimpath -ldflags '-X main.revision=434679c-go001-worktree' -o bin/api-final.exe ./cmd/api` | PASS | 二进制未入库；revision 明确标记待提交工作区 |
| `python scripts/go-bootstrap-smoke.py --binary backend-go/bin/api-final.exe --output docs/evidence/2026-10-26-434679c/GO-001/bootstrap-final.json` | PASS，6/6 | [最终进程与HTTP证据](bootstrap-final.json) |
| `npm.cmd run build` / `npx.cmd prettier --check vite.config.ts`（frontend） | PASS，类型检查和生产构建通过 | 本地命令执行，未作业务页面验收 |

六场景为：真实依赖启动、三个健康接口 Go 身份、Windows CTRL_BREAK 后 exit 0 且端口可重绑；缺 MYSQL_PASSWORD；缺 MYSQL_PORT；MySQL 拒绝连接；Redis 拒绝连接；API 端口占用。后五场景均非零退出，无强杀冒充优雅关闭，无密码泄露。脚本不写业务数据或 Redis key。

## 失败与审查边界

首次并行编译期间 agent 的 app 测试在 1 秒内未进入 loopback handler；失败输出保留在本目录，不以最终 PASS 覆盖。排查/修订说明见相应证据。独立预审还发现 Serve 异常退出时可能先关池的问题，已改为所有退出路径统一排空，并加 listener 永久 Accept 失败窗口回归。

[初版 binary 的启动证据](bootstrap-initial.json) 与最终源码构建的证据分别保留。一次在 binary 尚未构建时调用脚本，参数校验明确报“不存在预构建文件”，未启动进程，不计入六场景。

Linux race / 远端 GitHub Actions、启动后的真实 MySQL/Redis 中断恢复、长期连接/负载与业务流程均未执行。本任务不声明吞吐、业务迁移完成或全面验收。现有 Java 测试/数字不作 Go 成绩。

独立只读 agent 对本票源码、配置、脚本和证据复查并独立执行 verify/vet/test/gofmt 与真实启动 6/6，无阻断发现。其普通权限 Docker pipe 访问被拒，故容器/卷归属由实现者的 Docker 输出确认；未独立执行 Linux race/远端 CI 或启动后依赖中断。最终 binary SHA256：`FAD073CF5DAB12E104873712600862B58D3BD937B429CC02C70ABD8484211523`。

## 复现与回退

按 [Go 启动步骤](../../../development.md#go-迁移环境与启动) 设置环境后执行以上命令。停止应用用 Ctrl+C，停止隔离依赖用 `docker compose -f compose.go.yaml stop mysql redis`，保留数据卷。Git revert 不撤销 SQL；本票无表结构变更。若调试 Java，前端显式设置 `API_PROXY_TARGET=http://127.0.0.1:8081`，禁止把 Go 连接到旧数据库/Redis。
