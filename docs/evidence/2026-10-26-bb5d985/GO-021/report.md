# GO-021 / 活动发布与下架交付自测

2026-10-26；开始 SHA `bb5d985d69bfb3d38d694b33ee934fc1499f647f`。用户明确 GO-020 交其他 agent 核验并授权继续，本票未把 GO-020 状态改为已验收。状态：自测通过待独立核验。

按可测试边界分三步提交：GO-021a 门控/outbox `07436b90dde53a2043527c19a08ae6b3034b4ab4`；GO-021b 接口/relay `b55be123059992b36256541b4259d06f26365358`；GO-021c 页面与真实浏览器贯通（完整 SHA 由收尾交接回填）。生产 Go 无新依赖/DDL，沿用 SQL003—005；无库存重建入口。

## 实物与验证

- [门控与 outbox](base-report.md)：只认领活动事件、租约 fence、Lua 完整预检、version/phase 拒绝旧 OPEN、现有库存不重置。
- [活动接口与 relay](backend-report.md)：GET 列表、POST 创建、retry-publication、offline；事务当前读授权、并发幂等、同步关闸、提交后开闸及有界生命周期。[最终活动 9 组](real-activity-final.txt)、[其余真实整轮](real-integration.txt)、[普通 Go 全套](go-test-final.txt)、[启动/关闭 6 场景](bootstrap-final.json) 均 PASS。
- 页面只启用工作台的活动管理，发布默认可管理门店；逐行 shopId 决定重试/下架，STAFF 只读。真实分页与门店/状态筛选、空态/错误/重试、大 ID/金额、北京时间输入、固定发布请求键与不可变内容、卸载轮询停止均实现。单笔金额和 ID 不从中间拆行。
- [最终 UTC 浏览器](ui-utc-frozen.json) 28 项、[最终上海浏览器](ui-shanghai-frozen.json) 28 项 PASS；[工作台回归](workspace-regression.json) 24 项 PASS。真实成功流均经 Vue→Vite→Go→MySQL/Redis，无 mock 成功数据。
- [最终前端构建](frontend-build-final.txt)、[最终格式检查](frontend-format-final.txt) PASS，`node --check` 两个 UI 脚本 PASS，`git diff --check` PASS。

浏览器断言包括：OWNER-A + STAFF-B 只看 A1/A2/B1，STAFF-B 行无管理按钮、B2 不可见；两页共 14 条无重复；STAFF 商家排序在前时发布仍默认 A1；UTC 浏览器默认北京时间准确；大于 2^53 的分金额原样提交且四个时间含 +08:00；真实提交成功后仅丢弃响应，关闭/重开弹窗仍用相同请求键/内容返回同活动，DB 只一活动/创建审计；后台最终 ACTIVE 并完成 PREPARE/OPEN；把 Redis 库存改为 97 后原键重放/重试保持 97；ACTIVE 缺库存重试不补键；下架后重投真实旧 OPEN 仍 CLOSED/version2/OFFLINE；成员停用后原 Token 仍登录，但活动范围和按钮缩减、写请求 403；请求失败清空旧列表/权限，重试恢复；离页后不再发轮询请求。

“库存 97”是明确模拟的 Redis 消耗，不声称 GO-030 订单已受理/落库。导航测试真实证明 timer 停止，在途请求取消由 `onBeforeUnmount` 的读写 controller abort 静态审查支持，未写成浏览器网络取消窗口已覆盖。原请求状态在同页关闭/重开保留，离页销毁；未实现跨页面/跨账号持久化发布草稿。

## 环境、复跑与清理

Windows Go1.27.0、Node24.15.0、Chrome155/Playwright；隔离 Compose `project-full-stack-go`，MySQL8.4:3308 / Redis7.4:6380，API8082、Vite5174。命令与环境入口见 [运行说明](../../../development.md#go-迁移环境与启动)。二进制 revision/hash 见后端报告，GO-021b 之后仅前端/脚本/文档变化。

在显式 MYSQL_*、REDIS_* 配置下，无其他 Go worker/API 时执行真实 Go 集成；三个 claim 测试包共享 MySQL advisory lock，防同轮测试互相认领。真实 UI 需要启动 Go API（含 worker）和 Vite，然后在根目录运行 `node scripts/go-activity-ui-smoke.mjs`，设置 PLAYWRIGHT_MODULE、CHROME_PATH、UI_EVIDENCE、BROWSER_TIMEZONE=UTC 或 Asia/Shanghai。成功夹具从 SQL 到 Redis 都只清理其精确 ID/键：先删除持久事件/业务行并等待 worker 行锁释放，再删四类 Redis 键并检查 EXISTS=0，发放 session 全部 logout。大 ID 会推进隔离表自增水位，不回拨；验证码节流键按原 TTL 到期。

[最终 UTC 下架图](ui-utc-frozen-offline.png)、[最终上海混合角色图](ui-shanghai-frozen-mixed.png) 已人工查看，表格/分页/中文筛选/长值可读。[环境停止记录](environment-stop.json) 保存最终 API/Vite 已退出、Go 隔离容器 Exited0、原项目容器状态；卷保留。

## 保留的失败证据与边界

- `ui-utc-initial.json` / `ui-utc-selectors.json`：测试点击非筛选型 ElSelect 的隐藏输入层，超时；改点可见外框。夹具清理均完成。
- `ui-utc-flow.json`：测试在真实请求尚未完成时检查返回活动；加等待真实提交及按钮解除 loading。截图保留当时状态。
- [中途 Vite/route 异常](ui-proxy-interruption.json)：plain-pipe Vite 退出导致 ECONNRESET；用 PTY 运行并捕获 route 错误，避免输出请求头。该轮临时 session 已注销，按已核实精确 ID 手动清理，后续轮次全自动清理通过。无原始凭据进入提交。
- 收尾第一次停止依赖时 MySQL 非 OOM 退出 137，日志仅观察到收到 SHUTDOWN，原因未完全确定；保留 [首次容器状态](mysql-stop-initial-state.json) 和 [首次停止记录](environment-stop-initial.json)。用原卷重新启动健康，[四表 CHECK TABLE](mysql-recovery-check.txt) 均 OK，临时 UI 用户/活动和待处理活动事件均 0；随后以 `stop --timeout 60` 再停止，最终状态另记，不覆盖首次失败。
- `ui-utc-proxy.json`、`ui-shanghai.json` 是修正脚本后的 28 项 PASS；`*-final.json` 是中文筛选/长值显示微修后的 28 项 PASS；`*-frozen.json` 是最终精确清理顺序加固后的冻结脚本，各 28 项 PASS。旧失败/成功记录均未覆盖。

未执行 Linux race、远端 CI、持续负载、真实物理断网 CLOSE 超时；未实现 Stream 成单/模拟支付/核销/退款/审计查询。legacy Java hash/无 phase gate 的安全接管、失败下架只允许继续关闭、缺键不自动重建等边界见后端报告。下一票 GO-030；GO-033 前继续禁止重建写入口。
