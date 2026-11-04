# 开发与验证工具入口

应用代码在 `backend/` 与 `frontend/`；本目录放显式运行的辅助工具。当前任务和真实依赖的执行边界统一查阅根 [STATUS.md](../STATUS.md)。

| 类别 | 入口与职责 |
| --- | --- |
| 数据库迁移 | [migrate.mjs](migrate.mjs)，执行 `infra/mysql/migrations/` 中的版本 SQL |
| 业务演示与恢复 | [go-flow-demo.mjs](go-flow-demo.mjs)、[review-order.mjs](review-order.mjs) |
| 启动、接口与页面验证 | `go-*-smoke.*`；其中 [go-bootstrap-smoke.py](go-bootstrap-smoke.py) 也由 CI 使用 |
| 当前 CAP-10 链路 | [cap10-env.py](cap10-env.py) 管环境与进程，[cap10-like.mjs](cap10-like.mjs) 管夹具和对账，[cap10-run.py](cap10-run.py) 编排执行，[cap10-stage.py](cap10-stage.py) 管阶段，[cap10-grade.py](cap10-grade.py) 判定原始结果 |
| 当前观测与合同 | `cap10-downstream*.go`、`cap10-*-contract.py`、`cap10-*-check.*`；测量代码与其直接检查相邻维护 |
| PTS 工具 | [pts](pts/)，场景打包、JMeter 和平台交互 |
| 复用测量工具 | `cap09-*`、`capacity-*`、`go073-*` 与 [load](load/)；当前链路仍复用其中部分入口，保留原路径 |
| 通用脚本库 | [lib](lib/) |

已结束的 CAP-06–08 工具已移到 [archive/capacity-tools-cap06-cap08](../archive/capacity-tools-cap06-cap08/README.md)，旧路径与 SHA 可在其 manifest 查找。原始证据和历史命令仍保留，不因归档而重判。

带 `check` 或 `smoke` 的名字不代表没有外部副作用；各工具是否使用真实依赖，以实际参数和对应说明为准。当前 CAP-10/PTS 文件位置和内容保持，避免破坏已有工具哈希与包绑定。
