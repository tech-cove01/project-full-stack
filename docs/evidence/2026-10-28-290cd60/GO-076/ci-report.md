# GO-076 持续集成覆盖

现有community-kafka作业增加固定ES8.19.23、loopback9202端口映射、512MiBheap/1GiB上限和yellow健康检查。新增ES_URL/ES_INDEX后，在原真实broker序列后执行notesearch，保持`-race -count=1 -p 1`；迁移runner自动包含012。无付费模型配置、无新SDK或业务数据库。

实际验证：PyYAML解析通过；断言固定镜像/ES环境/notesearch命令；独立ai_eval用拒绝重复键解析器复核，作业结构与依赖顺序通过。Compose中的宿主机自定义端口不套到CIKafka；CI维持localhost9092，其地址无容器内/外错配。

本机普通Go全套及真实搜索/同步分别通过，原始证据在本票目录。此提交只表示CI配置已准备；本票远端GitHubActions/Linux race尚未执行，旧绿色CI不替代新代码。
