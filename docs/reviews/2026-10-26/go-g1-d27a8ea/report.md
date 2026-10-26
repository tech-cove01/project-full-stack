# Go G0/G1 独立核验

日期：2026-10-26。结论：**GO-001、GO-010、GO-011 在各自声明的交付范围内通过核验，可继续 GO-020。** 没有发现阻断下一票的应用缺陷；一项非阻断的UI夹具时区修正留给实现者单独提交。Linux race / 远端CI未执行，不记通过。

## 版本与范围

- 总基线：434679cf86aaa125f1f9a35fa695cc7050eed907；核验HEAD：d27a8ead5280853dc88cbf597bfac13638ee659e，开始工作区干净。
- GO-001：69ddd3ee001251b5a16ee252fbacf51130643630；GO-010：cc0c6f920897b3ed65a4f080997a6c538fe0c2fa；GO-011：644b2c55ee73e1318f78c74ce8d24affef707295；最后d27a8ea只补交接。
- 检查已提交差量及工作区/暂存/新文件，核验时无实现者未提交变更。两个独立只读审查者分别检查认证/生命周期与商铺/前端，未修改应用。
- 本次重建二进制注入完整d27a8ea revision，SHA256为7191672ECDAC354F8C67656176D5033B6FBD9D6DE1978FE39F4A9446959BE429；二进制不入库。
- 真实依赖为Go专用MySQL3308/Redis6380，数据库project_full_stack_go；Go8082、Vite5174。MySQL确认已有003—005迁移记录，本轮未执行新DDL或重跑迁移。

## 实际执行

| 检查 | 结果 | 边界/证据 |
| --- | --- | --- |
| go mod verify / go vet ./... / gofmt -l . | PASS | 模块校验通过，vet与格式无诊断 |
| go test -count=1 ./... | PASS | 全部Go包；默认不启用真实集成 |
| GO_INTEGRATION=1 go test -count=1 -v ./internal/auth ./internal/shop | PASS | 真实MySQL/Redis；认证7主组、商铺7主组，另有wrongtype子组；不是mock数据库 |
| npm.cmd run build / npm.cmd run format:check | PASS | 类型检查、生产构建、全前端格式检查 |
| 冻结二进制启动/关闭脚本 | PASS，6/6 | [bootstrap](attempt-02/bootstrap.json)，三个健康接口识别Go、正常关闭/端口重绑；缺配置、依赖拒绝及占端口明确失败 |
| 真实Vue登录/恢复/退出/失效 | PASS，12项 | [auth-ui](attempt-02/auth-ui.json)，[截图](attempt-02/auth-ui.png)；代理与直连都指向相同Go revision |
| 真实Vue商铺与活动，UTC浏览器 | PASS，14项 | [UTC](attempt-02/shop-ui-utc.json)，[截图](attempt-02/shop-ui-utc.png)，cleanup=true |
| 真实Vue商铺与活动，上海浏览器 | PASS，14项 | [上海](attempt-02/shop-ui-shanghai.json)，[截图](attempt-02/shop-ui-shanghai.png)，cleanup=true |
| 结束应用与前端 | PASS | [汇总](attempt-02/summary.json)，Go exit0、Vite收到Windows中断退出；无强制终止 |
| Linux race / GitHub Actions | 未执行 | 已静态核对配置；本机测试不能替代远端结果 |

认证覆盖验证码单次消费/节流、12并发同码仅一成功、16并发建号唯一、大ID字符串、异常无TTL/损坏session、错误类型503、正常失效401，以及刷新/退出不复活。精确logout窗口没有hook，结论结合Lua只续期、不重建hash的静态审查，不宣传为精确窗口故障实验。

商铺覆盖53家大ID店稳定完整翻页、名称/分类交集、字面通配符和注入文本、NULL/空串/空数组/total、元与分、券可见性、错误与独立重试。两种浏览器时区都实际点击页面，成功响应来自Go及真实库，离线/abort仅用于注入失败。UTC截图已人工查看。当前不支持的附近坐标/详情缓存没有计入已完成范围。

原始普通Go测试与集成输出保留于本聊天工具执行记录；可复用实现者[认证报告](../../../evidence/2026-10-26-69ddd3e/GO-010/report.md)和[商铺报告](../../../evidence/2026-10-26-cc0c6f9/GO-011/report.md)中的逐项源码与原始输出核对场景，不能将自测报告等同本轮执行。本文件记录的是核验者独立重跑结论。

## 一项非阻断修正：GO-011-QA1

位置：scripts/go-shop-ui-smoke.mjs:51—60。脚本用MySQL会话NOW()生成“当前时间±30分钟”的DATETIME，但seed只执行SET NAMES，没有SET time_zone；Go的数据库会话固定为+08:00。如果MySQL默认会话是UTC，夹具会写入比上海时间早8小时的值，脚本可能把正常页面误报为时区错误。

本轮同一MySQL连接分别SET time_zone为+00:00和+08:00，只读NOW()实际相差8小时；没有修改全局设置，也没有把整个UI脚本在默认UTC数据库上重跑。此项是条件明确的测试可移植性问题，不否认当前Compose上海时区下的两轮PASS，不是应用时区缺陷。

修复：seed同一次SQL会话先显式SET time_zone='+08:00'，再执行NOW()及INSERT；清理逻辑保持按精确ID。单独提交fix(GO-011-QA1)，可在CLI会话默认UTC条件下确认造数时间与应用上海口径一致。不要为此更改全局数据库时区或重写业务时间实现。

## 失败与环境边界

普通权限运行新二进制时，bootstrap健康场景报告MySQL startup check失败；[失败JSON](bootstrap.json)和[失败汇总](summary.json)保留。相同配置的Go真实集成和MySQL客户端可访问，同SHA二进制在获准的执行上下文复跑后6/6及页面全部通过。未读取底层驱动诊断，不把这一执行上下文差异直接判断为应用/数据库故障。

Docker普通上下文的命名管道访问受限；隔离启动操作被中断后，确认3308/6380已监听，才继续真实测试。最终通过获准的Docker操作确认Go项目归属并停止这两个容器，均Exited(0)，卷保留。原project-full-stack的3307/6379容器仍healthy，未停旧服务/改旧数据。

应用与Vite均已关闭；集成/商铺UI夹具按精确ID清理，UI创建的随机演示用户保留在隔离库，未flush/truncate或重置AUTO_INCREMENT。本轮未测试持续负载、启动后真实依赖中断、交易worker、B端权限或AI。

## 给实现者的下一票

可以继续GO-020，不重复开发已通过的G0/G1。GO-011-QA1单独小提交，不混入业务迁移。

GO-020交付真实Vue商家工作台及逐门店权限。至少核验：普通用户无商家权限；OWNER/MANAGER/STAFF访问差异；OWNER-A / STAFF-B混合角色；跨商家资源拒绝；成员/门店停用后旧登录token不能继续授权；身份、角色和门店范围取服务端事实。查询先按授权范围过滤再分页，不能仅前端隐藏按钮或全局合并角色。

接口复用既有merchant/workspace契约，ID保持字符串，缺权限有明确页面。自测通过后独立commit并交完整SHA、命令与真实依赖证据，再按GO-021推进活动，不把未实现交易/AI/社区标完成。
