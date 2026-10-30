# 专用读网络启动修复

首次启动在创建read网络时因Docker默认地址池耗尽失败，[原日志](compose-start.txt)保留，尚无read API或业务负载。没有删除其它网络或更改全局Docker地址池。

实现者核对30个已有Docker IPAM子网和38个Windows IPv4非默认路由，选用不重叠的 `10.253.51.0/24`；[预检](network-preflight.json)与[选择](network-selection.json)仅保留本次CIDR、数量和结果，无关路由细节留在仓库外。新增[独立override](../../../../scripts/load/compose.capacity-read.yaml)要求显式 `CAP_READ_SUBNET`，仅在带明确 `-p project-full-stack-cap-read` 的本次命令中使用。[重试日志](compose-start-explicit-subnet.txt)记录两依赖健康；启动/停止命令已同步[运行说明](../read-runbook.md)。

cap_trade对override、明确项目名和作用域作非作者只读审阅，无阻断；这不是其独立重跑网络或容量。复跑必须重新核对新宿主路由，不能把本机CIDR当成全局默认。原数据卷、其它服务及全局Docker设置均保留。
