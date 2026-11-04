# Go 后端源码入口

[cmd/api/main.go](cmd/api/main.go) 是正式 API 入口；[internal/platform/app](internal/platform/app/) 负责组装路由、连接池和后台任务，并管理进程关闭。业务模块放在 `internal/`，每个模块的 HTTP、服务、存储及相关测试就近维护。

| 业务域 | 模块 |
| --- | --- |
| 登录、商铺与权限 | [auth](internal/auth/)、[shop](internal/shop/)、[merchant](internal/merchant/) |
| 活动与订单 | [activity](internal/activity/)、[activitypub](internal/activitypub/)、[orderadmission](internal/orderadmission/)、[order](internal/order/)、[merchantorder](internal/merchantorder/) |
| 交易后台处理 | [orderstream](internal/orderstream/)、[ordertimeout](internal/ordertimeout/)、[ordercomp](internal/ordercomp/)、[orderreview](internal/orderreview/)、[outbox](internal/outbox/) |
| 社区事实与读侧 | [note](internal/note/)、[social](internal/social/)、[communitycards](internal/communitycards/)、[communitycache](internal/communitycache/)、[notesearch](internal/notesearch/) |
| 社区事件链路 | [communityrelay](internal/communityrelay/)、[communityconsumer](internal/communityconsumer/)、[communityprojection](internal/communityprojection/) |
| 业务 AI | [ai](internal/ai/)、[aiknowledge](internal/aiknowledge/)、[aitool](internal/aitool/)、[aichat](internal/aichat/) |
| 公共设施与组装 | [platform](internal/platform/) |

交易 `outbox` 与社区 `communityrelay` 分别处理各自的事件；路由和后台任务通过 app 组装，业务模块不负责启动整个应用。阅读一项功能时，按 HTTP → 服务/事务 → 存储或事件 → 后台处理的顺序查找。

完整调用链、前端对应页面及设计取舍见[源码讲解路线](../docs/source-walkthrough.md)。建库、版本 SQL、环境配置与启动命令见[开发说明](../docs/development.md#go-迁移环境与启动)。`cmd/` 中其余命令是独立管理或测量入口，不随 API 自动运行。

`bin/` 是本机构建输出目录，当前程序从源码重新构建。旧的已跟踪 Windows 构建已移至[历史归档](../archive/windows-builds-20261026/README.md)。
