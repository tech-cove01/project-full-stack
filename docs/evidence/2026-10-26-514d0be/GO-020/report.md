# GO-020 逐门店授权与商家工作台

2026-10-26（Asia/Shanghai），自测通过待核验。开始SHA `514d0beef3de443be12f812faa3a7b3dd94d64d2`；GO-011-QA1已在此前独立fix提交。GO-020交付SHA `8731642b5b37eb8fe7b2d32fc02f2adb6882ef5a`；后续文档提交仅补全SHA，不改变已测应用源码。

交付：仅开放 `GET /merchant/workspace`，角色/门店信息来自当前登录身份和实时MySQL；新增可复用Access具体门店/管理权限及可见/可管理范围。一个LEFT JOIN读取工作台，保留有效但零门店的成员；ID字符串、空merchants/shops数组，与Vue契约兼容。OWNER/MANAGER仅本商家，STAFF仅scope，脏scope不能跨商家。角色不写进token，不把同账号A的OWNER权限提升到B。数据库错误保留503，身份失效401，普通登录无商家返回200空工作台。

前端展示逐商家的角色和逐门店范围，刷新开始/失败清空旧授权，可重试/处理空态；尚未开放业务入口禁用，不展示假操作。保留公开店铺链接。未实现商家/员工授权管理写接口或额外业务端点，没有新DDL、Redis worker、Stream/outbox消费、库存重建或SQL迁移。

## 环境与执行

沿用Go独立MySQL3308/Redis6380、库project_full_stack_go；Go8082/Vite5174，Windows amd64/Go1.27.0/Node24.15.0，Playwright+本机Chrome。原Java项目服务与数据未操作。环境变量按 `backend-go/.env.example` 显式设置，SQL版本003—005已存在，不重跑无关迁移。

| 命令/验收 | 实际结果 | 原始证据 |
| --- | --- | --- |
| `gofmt -l .` / `go vet ./...` | PASS，无差异/诊断 | root与独立审查者分别执行 |
| `go test -count=1 ./...` | PASS，全包普通测试 | [完整输出](tests-unit-final.txt) |
| `GO_INTEGRATION=1 GIN_MODE=release go test -count=1 -v ./internal/merchant -run TestIntegrationMerchant` | PASS，11主组 | [真实集成输出](tests-integration-initial.txt) |
| `npm.cmd run build` / `npx.cmd prettier --check src/views/MerchantView.vue` | PASS | [build](frontend-build-final.txt)、[format](frontend-format-final.txt) |
| `node scripts/go-merchant-ui-smoke.mjs` | PASS，4真实账号24项，精确cleanup=true | [最终冻结binary的UI](ui-frozen.json)、[截图](ui-frozen-mixed.png) |
| P1-6 / MIG-GO-03 子集 | PASS，限本票HTTP与Access边界 | 以下矩阵与限制 |
| Linux race / 远端Actions | 未执行 | 仅增加merchant真实集成race的CI配置 |

最终冻结binary SHA256 `3328A852E59AD8B0D637519E2FBD65D600FD7B56A1DF30BA3AA122F845B1A8F6`，注入revision `514d0be-go020-worktree`。最后一处生产源变化仅澄清RR读取的注释，独立Go/真实集成复核发生在其后，root再构建冻结binary并重跑UI24项。二进制不入库，不冒充已提交revision。

## 真实权限与持久行为

集成11组（真实MySQL/Redis、6个真实取码登录session）：

1. OWNER-A/STAFF-B混合、MANAGER-A、STAFF-A、普通用户、零scope与无店商家；大ID、顺序、空数组。
2. 可见A1/A2/B1，管理仅A1/A2，不因A角色管理B。
3. 13组具体门店访问/管理允许和拒绝、未知门店；脏scope不能跨实际merchant。
4. query/header伪造user/merchant/role，以及session追加role字段均不能授权。
5. scope撤回沿原token即时缩减。
6. 门店改归属使旧scope失效。
7. 成员停用或OWNER降级STAFF沿原token缩减，`/user/me`仍有效。
8. 商家停用移除全部授权，登录身份仍有效。
9. tx内停用成员未提交，NewAccess(tx)拒绝，而全局db仍允许，rollback恢复；证明使用调用方句柄。
10. 工作台一次SQL；取消ctx及独立已关闭MySQL池导致真实错误，HTTP503无SQL/敏感诊断、不返回空授权。
11. 退出后原token工作台401。

这些具体门店/管理操作是Access服务层的真实DB验证；没有为了测试增设HTTP权限探针。活动发布、核销、退款、审计及FAQ等HTTP越权尚待相应业务票，不能把本票说成完整P1-6通过。`tb_shop`没有status字段，本票不声称门店停用，只测商家/成员状态、scope及归属。

集成仅精确清理自身DB行与Redis keys，清理后COUNT=0/EXISTS=0有断言，不执行DDL/flush/truncate或重置序列。大ID夹具会推进隔离表AUTO_INCREMENT；这是隔离环境的已知副作用。

## 真实页面、失败与审查

UI通过真实Vue取码登录4账号，访问Go工作台；确认混合角色的A1/A2/B1与逐商家范围、未开放入口禁用、伪造query无效。修改自己的fixture成员状态/scope/角色/商家状态，点击刷新沿原token查看范围缩减，同时 `/user/me`200；验证零scope空态、降级后移除B2、脏scope无效、普通账号空态、网络abort后清旧授权并独立重试，最后撤销session触发401跳登录。成功响应均来自真实Go/DB，只有错误路径注入网络abort，不Mock业务数据。

[初轮UI](ui-initial.json)24项通过；目视截图发现标签正处于进入动画，保留[渲染排查](ui-render-check.json)及截图。截图调用改为 `animations: disabled` 后角色80/68px清晰可读，无应用样式缺陷，保留[稳定截图版本](ui-final.json)和最终冻结binary证据。UI按自身精确ID清理DB并注销发放session，取码节流key保留原60秒自过期；不会清空Redis。

首次agent普通sandbox中既有app loopback测试被Windows socket权限拒绝，[原始失败](tests-agent-sandbox-failure.txt)保留。root和独立审查者在各自有效上下文的同版完整普通测试均PASS；不将上下文权限错误记为应用回归，也不覆盖失败证据。

独立只读审查者完整检查本批源码、测试、CI、脚本、工作区和未跟踪文件，独立重跑gofmt/vet/full普通Go与真实merchant11组、diff检查，均PASS，无清理错误；人工查看最终截图并静态核对24项报告，未自行重跑浏览器。未发现阻断项；QA1的同会话SET也静态核对正确。

## 限制与后续

Access(tx)继承事务隔离，RR普通SELECT可能沿旧快照；本票是跨请求当前授权查询与只读工作台，不是写事务的并发撤权屏障。GO-021/032等写操作必须增加锁定当前读或覆盖授权事实的条件更新，不能仅调用NewAccess(tx)就宣称解决并发停用。旧活动/订单页面仍有全局canManage与重试发布按钮问题，在对应业务启用时按每行shopId修复，本票没有声称验收这些页面。

无持续负载/交易/AI/社区结果，也未重跑用户刚已核验且本次未变的G1重型实验。回退采用本票revert，无数据库结构迁移可撤销；现有SQL与用户数据兼容，不删除卷回退。

收尾实际停止Go API/Vite；Go日志确认HTTP requests drained。只停止project-full-stack-go的两个依赖，均Exited(0)，卷保留，原project-full-stack容器healthy：[环境证据](environment-stop.json)以JSON字符串原样保留CLI输出（含行末空白）。复现命令和UI环境参数见[运行文档](../../../development.md#go-迁移环境与启动)。
