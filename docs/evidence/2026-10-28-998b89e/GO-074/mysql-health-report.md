# GO-074-QA0 首次MySQL就绪门槛

全新merchant容器 `up --wait` 后立即迁移第一次失败（helper只保留结构化失败，原始node诊断未持久，不能仅凭该次诊断确定原因）；稍后显式重跑003—012成功，保留 [第二次迁移](merchant-migration-second.txt)。

随后在另一套全新AI容器逐次比较原socket `mysqladmin ping` 与新TCP业务用户查询 `tb_shop`，实际捕获旧probe成功、新probe尚失败，见 [26次观察](mysql-health-precheck.json)。两个独立CLI探针顺序执行，不能当原子时间快照，但已直接证明旧socket ping不足以作为实际连接/表结构就绪门槛。没有延迟/假造MySQL成功响应。

Compose健康改为TCP、实际业务用户/数据库读取基础表，覆盖初次临时socket server以及尚未导入基础schema的窗口。版本迁移仍由runner显式执行，应用不启动AutoMigrate；没有改变MySQL版本、端口或数据。仅重建本票两个MySQL容器配置，命名卷保留，其它项目服务不动。新probe和Compose重建后的结果另存health-final.json。

独立问题独立提交，非作者审查及初始化原始记录由整票交接引用；不把这项修复冒称GO-076独立核验结论。
