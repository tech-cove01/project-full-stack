# GO-076a0 可选搜索运行环境

固定ES8.19.23单节点，内置CJK，无插件/SDK；Compose search profile默认不开启，9202仅loopback，独立命名卷，heap512MiB/container1GiB。Kafka与SQL/Redis保留原协议。本轮独立project`project-full-stack-go076`使用3332/6404/9099/9202，未停止其它核验或CRM容器。

`ES_URL`缺失或错误时cfg.Search.Ready=false，不阻止核心配置加载；地址仅loopbackHTTP/HTTPS根路径，无URL凭据/query/fragment，alias为单个小写受限名字，超时1—10秒。这里只检查本地配置，不付费/远端健康探测；实际API接入另提交。

已运行：config普通测试通过；Compose config --quiet通过；四依赖实际healthy。[实际ES版本](es-version.json)、[依赖镜像](dependency-images.txt)。[CJK两样例](cjk-samples.json)确认双字切分，例如“桂花/花小/小院/院手/手冲/冲咖/咖啡”；这是内置中日韩分析，不声称业务中文词典。

独立只读审查provider_wiring检查可选配置/错误脱敏、URL/index边界及Composeprofile，无阻断发现。当前资源配置提交不宣称搜索API、Kafka同步、浏览器或故障恢复已验收；这些分别交付证据。新增012迁移由同步子任务单独提交。
