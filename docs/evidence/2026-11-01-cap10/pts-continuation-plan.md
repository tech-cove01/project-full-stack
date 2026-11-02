# CAP-10：保留原包的新场景准入命名空间

2026-11-03。原runtime已有初始/repair1/repair2包，初次packet命令按设计拒绝重复创建。下一次4096/9与dedicated-v1更新必须保留旧包/准入/上传登记，不能改旧文件或在付费运行期间临场改写验证函数。

本小票仅给只读`verify-scene`增加显式`--namespace profile-<12位小写hex>`。packet、binding、admission与API审计目录使用同一后缀，均在原ROOT之下；缺省仍用原文件名。新namespace必须与已登记环境的`CAP10_PTS_PACKET_FILE`和`CAP10_PTS_ADMISSION_FILE`精确一致。路径不接受任意目录、相对上跳、绝对路径或旧repair名称。

除了选择这组文件，不改变原verify_scene的完整源文件/manifest/JMX变量/MD5/私有配置/cohort/协议及实际Provider检查。仍需显式allow-live，只读一次GetOpenJMeterScene，不Start/Stop/调试；原四项source hash键保持。已存在的准入仍拒绝覆盖，任一失败只写本namespace的失败原件。

验证使用真实本地Node bundle和合成文件/只读Provider替身：新旧输出并存、默认旧路径不变、错误selector在Provider前拒绝、非法namespace不写文件，所有原反例继续通过。不发云请求或业务负载。

新的私有continuation制包器另行验证旧包原SHA和精确上传登记，先登记再写新包，使用新文件组并调用本原始验证函数；不再通过AST重写函数选择路径。其默认plan和未来原生Linux执行必须分开，不将离线packet准备写成真实场景准入。
