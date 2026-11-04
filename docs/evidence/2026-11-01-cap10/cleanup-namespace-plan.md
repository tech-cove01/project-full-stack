# CAP-10-CLEANUP-NAMESPACE

正常finish会删除夹具凭据、上传目录和登记环境文件，但刻意保留登记索引及清理回执。旧阶段的cleanup-progress.json、cleanup.json及PTS清理计划/结果必须保留；新夹具不能覆盖它们，也不能因旧阶段已完成而跳过本轮注销。

本票为cap10-env.py finish与cap10-pts-prepare.py cleanup-packet新增可选的显式12位十六进制cleanup namespace。指定时仅将本次progress、result及PTS清理计划/结果写入ROOT/cleanup-<nonce>/，原metadata、history、ROOT、夹具选择、注销/会话归零、账号删除、API及四依赖停止、保卷和精确登记文件清理规则不变。默认不指定时保留原路径、排他与失败规则。

新目录排他创建并记录本进程PID/birth及环境文件path/SHA。env finish调用的packet子进程仅可在同PID或直接父PID/birth、同环境原字节与明确env-finish owner均匹配时加入；未知owner、PID复用、非法namespace或已有独立namespace拒绝。失败目录保留，不清锁、不自动重试。该目录不是负载或资源准入，不能创建slot或容量结论。

7项直接纯检查通过，覆盖真实finish到packet cleanup调用链、真实父子进程身份握手、旧四回执字节保持、默认旧排他、重复namespace、输入环境改动和未知/复用owner拒绝；原制包器204项检查及3次本地Node制包通过。它们使用临时本机文件和替代服务边界，不连接数据库、Redis、Kafka、API、PTS或云；真实云端finish仍待下次明确执行。首次sandbox临时目录ACL拒绝，以及测试夹具误用排他save覆盖owner文件的失败保留；仅调整本机检查运行权限及夹具写法，未放宽生产保护。
