# CAP-06–CAP-08 历史工具源快照

本目录保留已结束的 CAP-06、CAP-07、CAP-08 容量诊断与核验工具，不是当前服务或 CAP-10 的运行入口。当前任务与执行边界以 [STATUS.md](../../STATUS.md) 为准。

## 归档范围

从源码提交 `fc36fff77b6a45bd98cd41565f75935aebf51c3d` 的 `scripts/` 原样移动 12 个文件，共 185,940 字节。文件内容未修改；全部文件仍放在同一个 `scripts/` 子目录，保留原有同目录模块、collector 与 tail 文件之间的关系。

| 历史阶段 | 保留文件 |
| --- | --- |
| CAP-06 | `cap06-env.py`、`cap06-stage.py`、`cap06-tail.py` |
| CAP-07 | `cap07-audit.py`、`cap07-env.py`、`cap07-stage.py` |
| CAP-08 | `cap08-audit.py`、`cap08-check.py`、`cap08-collector.py`、`cap08-env.py`、`cap08-policy.py`、`cap08-stage.py` |

[manifest.json](manifest.json) 逐项记录旧路径、新路径、字节数与 SHA-256，可用于核对归档前后的文件身份。

## 阅读与复核边界

这些文件是冻结的历史源快照，**不能从新目录直接运行**。脚本中原有仓库根目录推导、固定执行目录、证据路径和环境假设仍保持当时内容；归档没有将它们改造成新的可执行工具。若未来确需复现历史，应先核验对应原提交、原协议和独立授权，不修改历史失败或把旧环境恢复成当前环境。

原报告、原始证据、JSON、工具哈希及命令记录仍保留原路径与原字节。记录中的 `scripts/cap06-*`、`scripts/cap07-*`、`scripts/cap08-*` 表示当时路径，可通过本目录的 manifest 找到现有源快照。

本次未移动 CAP-09、`capacity-*`、`go073-*`、CAP-10、PTS、根 Compose 文件、Java 参考源或 CI。归档不运行服务、不操作数据卷、不产生新的容量结论。
