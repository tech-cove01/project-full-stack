# CAP-10 AUDIT-V1a：完整审计预算合同

本票仅为离线准备观测配置，不构成新模式已具备发压准入或取得业务性能成绩的声明。

## 固定配置

Node 通过纯合同 `scripts/load/cap10-observer-policy.mjs` 读取 `CAP10_OBSERVER_MODE`，只接受以下两个固定组合：

| 环境变量 | `mode` | `fullAuditTimeoutMs` |
| --- | --- | --- |
| 缺省或 `full-history-v1` | `full-history-v1` | 30000 |
| `like-vector-v1` | `like-vector-v1` | 120000 |

显式空值、`null`、未知模式和非字符串拒绝；不新增可任意指定预算的环境变量。合同的 `scope` 固定为 `full-snapshot-audit-only`。旧数据缺少模式字段时仍表示旧完整历史审计，不能据新上下文重标为新模式。

## 接入范围

`cap10-like.mjs` 只有 `snapshot()` 的一项完整、只读 RR SQL 调用接收 `fullAuditTimeoutMs`。该函数用于发压前后、规范化前后、独立检查和最终对账的完整事实审计；其 SQL、事务、收据检查及结果判定均保持原样。快照和新的运行报告记录完整 `observerPolicy` 对象，使增大审计预算可见。

其它 SQL 的 30000ms 默认值、规范化自己的 60 秒截止时间、Go 主窗总预算 4 秒/读取 3 秒/5 秒采样间隔、60/180 秒原生主窗和原 60 秒下游恢复门槛都不变。较长的最终审计不代表下游在 60 秒内恢复，不得据此改写失败。该配置不能代替尚未交付的新 Go 向量观测与 grade 接线。

新 helper 必须加入 runner 的字面量工具 SHA 清单。纯检查覆盖固定组合及非法配置，并执行从生产源码提取的 `snapshot()`，证明只有完整快照 SQL 收到该预算，报告声明匹配实际调用。

## 后续收据 SQL 的研究边界

本票不修改生产 SQL。下一项候选形状是先过滤选定笔记的 LIKE outbox `o`，再与收据表中实际存在的 `DISTINCT projection` 集合 `p` 组合，最后按 `(projection,event_id)` 主键查找收据 `r`。投影集合不能硬编码成三个已知名称，否则会隐藏未知投影；字符集与 BINARY 精确比对、未知/缺失/非法状态必须保留现有校验语义。

须等待独立 P3 对实际表定义、EXPLAIN、语义反例及固定预算的真实结果后，才决定是否接入；SQL 形状建议不构成性能收益证明。
