# 历史归档

当前应用代码在 [backend](../backend/README.md) 和 [frontend](../frontend/README.md)。这里集中保留迁移参考、旧构建、已结束阶段的工具和文档，便于核查来源与历史。

| 目录 | 内容与用途 |
| --- | --- |
| [backend-java-20261026](backend-java-20261026/) | 冻结 Java 参考源码；现有 Maven CI 仍验证它，不作为 Go 应用的运行依赖 |
| [windows-builds-20261026](windows-builds-20261026/README.md) | 原 `backend/bin/` 中已跟踪的 14 个旧 Windows 构建；清单保留旧路径、大小、SHA 和 Git blob |
| [capacity-tools-cap06-cap08](capacity-tools-cap06-cap08/README.md) | CAP-06–08 的 12 个冻结工具源；原字节与同目录关系保留，不能从新目录直接当当前工具运行 |
| [docs](docs/) | 状态、交接与 Go 交付记录的完整历史快照；各页标明截止时间和当前入口 |

当前任务只看根 [STATUS.md](../STATUS.md)，原始证据仍在 `docs/evidence/` 与 `docs/reviews/`。归档文档保留原结论，调整了相对链接；二进制和冻结脚本的内容保持不变，路径映射见各目录 manifest。

移动文件不会缩小 Git 历史或下载体积。本次不删除历史、不重写提交，也不迁移本机凭据、未跟踪构建或私有运行材料。
