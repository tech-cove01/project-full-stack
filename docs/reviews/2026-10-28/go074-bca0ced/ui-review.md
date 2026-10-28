# GO-074 前端独立核验

2026-10-28。固定起点 `bca0ced`，在独立 worktree 和专用 MySQL3360 / Redis6430 / API8130 / Vue5230 验证，Kafka/ES 关闭。AI 明确启用 stub，Key 为空，未启动观察器、读取用户浏览器会话或调用付费模型。服务由父核验者统一启停；本审查者没有操作其他 agent 的应用或服务。

## 源码与修复

`998b89e..bca0ced` 的 Vue 差量与 Go DTO 对齐：工作台接通已存在的订单/核销和公开规则路由；入口仍要求登录，退款按钮按订单门店对应角色决定；PUBLIC / SHOP_PUBLIC 来源标签和版本详情路由一致。门店工具使用真实 name/address，href 不符合本店 ID 时回退至 `/shops/<字符串ID>`，不再假设评分；活动库存、金额和大 ID 保持字符串。未发现这三个 Vue 差量中的确定阻断问题。

父核验者另发现范围内发布入口及路由复用问题，本轮分别验证：

- QA1：主导航补已实现的发布笔记页。匿名不显示入口，直接访问 `/notes/new` 跳登录且带原路径；实际合成身份登录后经主导航可见发布表单和按钮。
- QA2：详情请求暂停时点击发布入口，Vue 复用 NoteView，旧请求 abort 后 finally 跳过 loading 重置，导致发布页一直显示 skeleton。先保留真实失败，再仅在 compose 分支补 `loading.value = false`；父核验者独立审查后分笔提交，未把它与导航或 CI 合并。

## 实跑结果

| 范围 | 独立结果 | 原始证据 |
| --- | --- | --- |
| 原商家角色矩阵 | 34项 PASS，35.392秒；cleanup=true | [JSON](merchant-initial.json)、[执行](merchant-initial.txt) |
| 原工具卡片专项 | 23项 PASS，6.487秒；cleanup=true | [JSON](tools-initial.json)、[执行](tools-initial.txt) |
| 发布导航/路由复用，修复前 | QA1两项通过，QA2确定失败；cleanup=true | [失败JSON](note-nav-before-fix.json)、[失败截图](note-nav-before-fix-failure.png) |
| 相同脚本，修复后 | 3项 PASS，4.077秒；cleanup=true | [JSON](note-nav-after-fix.json)、[恢复截图](note-nav-after-fix-pending-detail-recovered.png) |
| stub两scope来源/详情差量 | 6项 PASS，7.736秒；cleanup=true | [JSON](source-scope-stub-initial.json)、[来源截图](source-scope-stub-initial-sources.png)、[PUBLIC详情](source-scope-stub-initial-detail-public.png)、[SHOP_PUBLIC详情](source-scope-stub-initial-detail-shop_public.png) |

商家矩阵真实发布并支付10笔独立订单，覆盖 OWNER / MANAGER / STAFF / 混合角色、逐店退款/核销、撤scope/停成员后旧 Token 与刷新、审计和补偿。6次越权写403、2次越权读200空页均对比订单版本/库存/审计/outbox无非法变化。等待自有持久结果、outbox完成及PEL为空再清自己的数据，没有 XACK 未知结果。

工具专项通过真实 Vue/SSE 执行 Go 只读工具：同一会话新消息读取库存3，经正常准入和 Stream 持久成单后读取字符串库存2；SQL/Redis库存同为2。大ID详情导航、当前价格、实际门店地址、未知ID拒绝均通过。只问确定性工具分支，不以 stub 结果充当模型质量。

QA2使用规范十进制详情URL，暂停其网络请求而不提供成功JSON，再通过真实导航切换 compose；不需要写入笔记。修复前 `/notes/new` 的 skeletonCount=1、formCount=0；修复后表单出现且无skeleton。该差量只建实际合成登录用户并精确清理，不修改现有笔记。

两scope差量先通过真实商家API发布合成FAQ，再为平台scope复制其已发布快照/向量作为SQL夹具，不伪造HTTP或SSE。真实 stub 检索返回两来源，Vue均显示v1及对应中文标签；实际点击两个链接，新页路径、标题、正文、scope和版本全部核对。只验证页面/来源协议，不新增真实Provider评测。

## 构建、截图及清理

初轮 `npm ci`、build（含vue-tsc）、format及两份非付费专项脚本 `node --check` 全通过，见 [命令结果](frontend-checks-initial.json)。QA1/QA2更改后重跑 [build](frontend-build-after-qa.txt) 通过；初次 [format](frontend-format-after-qa.txt) 只报新增 App 导航长行，定点运行已有 Prettier 后 [format复跑](frontend-format-after-qa-fixed.txt) 通过，保留原始失败，不重复无关构建。

人工查看了QA2失败/恢复、来源两scope、工具库存3→2、混合角色截图，1440与1360宽截图的新增导航均未溢出，表单恢复、来源标签及库存变化可读。带鉴权 trace/HAR 未保存；截图输入均遮罩，详情图只含公开合成FAQ。

所有轮次 cleanup=true，来源差量等待自有 AI lease 归零，删本人会话/登录、SQL FAQ/快照/向量/审计及合成商家用户，精确删本人AI键、验证码和session；没有 flush、删共享group或卷。浏览器均已关闭，已通知父方可做最终环境检查。

差量源码仅存外层本地 `.reviews/go074-bca0ced/note-nav.mjs` 和 `source-scope.mjs`，原脚本保持不变；结果与脚本SHA-256见 [汇总](ui-summary.json)。执行命令、退出码和耗时见 [公共命令账本](commands.jsonl)。本审查者未自行commit或push，QA1/QA2及CI改动由父方分别提交。

本次覆盖Chrome/上海时区和上述有限流程，不重跑付费模型，不代替持续负载、跨浏览器或任意并发撤权验证。修复前失败只证明具体复用状态缺陷，不增加虚构场景或吞吐指标。
