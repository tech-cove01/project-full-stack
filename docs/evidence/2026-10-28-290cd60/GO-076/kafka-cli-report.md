# GO-076 Kafka容器内命令连接修复

原Compose只对宿主机发布`127.0.0.1:<GO_KAFKA_PORT>`。容器内CLI从localhost9092取得metadata后，会转连容器自己的127.0.0.1:9099，因未监听而失败。宿主机Go客户端此前真实同步测试通过；这不是ES或消费持久化失败。[首次命令与观察](kafka-cli-initial.txt)明确不是完整重定向raw日志，完整输出在工具记录。

新增仅Docker内部INTERNAL9094，inter-broker和容器健康/CLI使用它；外部PLAINTEXT监听9092及loopback映射不变，9094不对宿主机暴露。仅重建本票`project-full-stack-go076-kafka-1`，既有卷保留，未改其它运行中broker。[修复后的topic创建/describe](kafka-cli-final.txt)成功。

独立只读审查ai_eval分别解析默认GO_KAFKA_PORT9092和本票9099，确认内外地址与安全边界一致，无阻断发现。后续真实浏览器发布→三组索引与offset清理门槛同时证明宿主机与容器内客户端可用；该页面证据单独提交。
