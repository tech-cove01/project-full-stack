# GO-074a：商家订单、核销与退款浏览器矩阵

基线：`998b89e81bed42786d25d0cebbadb401fd506f70`。仅核验 FIN-05 剩余关键 B 端页面，依赖已核验 GO-020/021/030/031/032/033 的实际范围；不重复无关商铺、社区、搜索页面，不改 GO-076 服务或源码。本文件先记录执行计划，真实结果和独立报告随后追加；计划不表示通过。

技能：workspace-delivery 与 playwright-expert。真实 Vue/Go/MySQL/Redis 交互，不 mock 成功业务；角色/范围变化用明确的自有数据库夹具操作，交易创建、支付、核销、退款使用真实 HTTP 与实际 Stream worker。

## 独立问题与提交边界

- GO-074a1：工作台仍显示禁用的“订单与核销 / 公开规则 · 稍后开放”，实际路由已实现。先仅将两项恢复为对应 RouterLink，单独审查、验证与提交；浏览器脚本包含自然入口点击。
- 新发现的显示、撤权、错误/重试缺陷分别报告并独立修复，不与整轮矩阵或其它问题合并。
- 专项脚本与本报告/脱敏证据另行提交，实现者自测仍待独立核验。

## 专用环境与隐私

主实现者统一启动：API `8120`、Vue `5220`、MySQL `3380`、Redis `6450`，Compose project `project-full-stack-go074-merchant`。此前8088、3350/6420端口预检发现被其它任务占用，未启动本票服务，最终以本段及显式环境为准。不启动/停止容器或使用另一票服务。浏览器模块使用已固定的 Playwright Core 与本机 Chrome，环境显式指定。

只造本人合成账号、商家A/B、门店A1/A2/B1/B2及活动/订单。OWNER A、MANAGER A、STAFF A1、混合 MANAGER A+STAFF B1、OWNER B、独立消费者；B2保留为空店。64位ID与金额为字符串。所有完整手机号、登录Token、验证码、64位核销凭据仅在进程中使用；**不保存含鉴权的 trace/HAR**。记录白名单网络摘要（方法、去敏路径、状态、动作标签）和遮蔽核销输入/敏感字段的截图，错误只给安全步骤名，禁止把 Playwright 任意异常/请求正文落盘。

## 验收矩阵（执行前）

| 编号 | 场景 | 预期与交叉核对 |
| --- | --- | --- |
| B01 | 工作台自然入口 | OWNER从真实工作台点击进入订单/核销和公开规则，路由正确，无“稍后开放”假入口 |
| B02 | OWNER A | A1/A2订单可见、已支付行有退款按钮、可选两店核销、管理审计可见；B订单不出现 |
| B03 | MANAGER A | A1/A2可管理；真实A2核销与退款成功，与订单状态/审计一致 |
| B04 | STAFF A1 | 仅A1，退款按钮和管理审计隐藏；真实A1核销成功；A2/B筛选与退款/核销越权拒绝 |
| B05 | 混合角色 | A管理权限不升级B1员工角色；A行退款可见、B1行无退款，审计仅管理范围；B1可核销 |
| B06 | 跨店 URL/表单 | 浏览器真实伪造筛选URL与核销shopId/退款orderId；越权列表按既有契约返回200成功但data为空/total='0'，写请求403/404，订单、库存、审计均无非法变化 |
| B07 | Scope撤销、旧Token | 页打开后删除本人A1 scope，旧Token读列表为空/total='0'、核销403；重新加载不显示已撤范围/旧数据，仍为同一个Token |
| B08 | 成员停用、旧Token | 混合账号A成员停用后旧Token对A失权但B1员工范围继续有效；按钮/审计依实时范围收敛，拒绝写入无副作用 |
| B09 | 空态与错误/重试 | OWNER B筛B2为空；真实网络abort导致可见失败，恢复后实际重试读真实数据；不伪造成功JSON |
| B10 | 交易/审计结果 | 成功核销/退款各操作有唯一正确状态、操作者、门店及审计；重复核销不重复审计，退款库存回补等待真实补偿worker |
| B11 | 精确清理与worker门槛 | 本人Stream消息PEL=0、持久outbox DONE/无review并验证补偿后，才删本人行/消息/键；不XACK未知结果，不flush、不删group/topic/卷 |

## 夹具及清理原则

每个目标订单使用独立活动，经真实发布ready→准入→Stream落单→支付。保持拒绝对照订单为已支付，比较拒绝前后订单状态/版本、库存、审计/outbox条数；不可用HTTP200或toast替代数据库事实。旧Token不重新登录，撤权/停用都只改变自有成员/scope。

当前脚本计划10个独立已支付订单（A1五单、A2三单、B1两单），分别留出成功核销/退款及拒绝对照；B2无订单。错误恢复使用真实浏览器刷新重试，不把新增页内按钮作为本票前置。截图只含掩码输入和服务端遮罩手机号，不保存带鉴权 trace/HAR。

清理前收集本人 activity/order ID 与Stream messageId，校验消息的 userId/voucherId/orderId 三元归属；等待持久交易/活动 outbox 全部 DONE、本人PEL为空及补偿完成。只XDEL已ACK的本人Stream entry，精确删活动gate/stock/order/accepted、order reservation、本人event幂等键、登录session/code/throttle和自有SQL行；共享Stream/group及日序号保留。门槛未满足保留自有ID并写明失败，交主实现者受控恢复，不靠强行ACK或删数据消除问题。

## 实际执行与结果

GO-074a1 导航修复已由主实现者独立审查并提交 `0365e5dd46f725796cd1cbb4e8efd432027f4348`；[前端构建/类型](nav-build.txt)与[格式](nav-format.txt)通过。专项脚本 `scripts/go-merchant-orders-ui-smoke.mjs` 经非作者预审后实际运行，第三轮 **34项 PASS**，B01—B11在上述实际范围完成。首两轮为测试定位器失败，不修改产品核销、退款或权限实现；失败原始JSON/截图保留。

| 轮次 | 结果 | 原始记录 |
| --- | --- | --- |
| round-1 | 3项后定位器超时；10个已支付自有订单完整清理，PEL/未完成outbox/review均0 | [JSON](round-1.json)、[脱敏失败截图](round-1-failure-masked.png) |
| round-2 | 3项后定位器超时；增加安全错误类别/动作后精确定位，清理通过 | [JSON](round-2.json)、[脱敏失败截图](round-2-failure-masked.png) |
| round-3 | 34项 PASS；6次越权写均403、2次越权读均严格200空列表/total='0'；所有拒绝前后状态/版本/库存/审计/outbox计数一致 | [JSON与脱敏网络摘要](round-3.json) |

第一次脚本误把 Element Plus Select 的显示占位文字当作 input placeholder，尚未发送核销请求即超时；第二次改为 role=combobox，但当前非filterable Select 的只读input被组件隐藏，仍无法点击。核对已安装组件源代码后，只改POM为语义section内的可见Select wrapper，选项继续使用role/name定位；门店/状态两个固定顺序过滤控件的首个wrapper用法有注释说明。第三轮通过。这两项均为测试脚本错误，没有把首次失败覆盖成成功，也没有把脚本失败归为授权/交易产品缺陷。

实际证明：OWNER/MANAGER可分别在A1/A2核销及退款，STAFF仅核销A1且无退款/管理审计，混合账号在A有管理按钮但在B1无退款按钮、审计仅A；6次403覆盖跨商家退款、STAFF退款、伪造核销shopId、scope撤销后旧Token核销、混合B1退款以及A成员停用后的旧Token退款。撤销/停用后仍持原Token，`/user/me`身份有效而页面刷新已缩小范围。6个成功核销/退款动作各有唯一正确操作者/门店审计，重复核销无第二条审计；真实补偿完成后退款活动库存MySQL/Redis均回到2，其余订单活动均为1。

页面截图已检查：

- [OWNER](round-3-owner-masked.png)、[MANAGER](round-3-manager-masked.png)、[STAFF](round-3-staff-masked.png)
- [混合角色逐门店按钮](round-3-mixed-masked.png)、[scope撤销](round-3-scope-revoked-masked.png)、[成员停用后保留B1](round-3-member-disabled-masked.png)
- [真实网络失败后刷新恢复](round-3-other-owner-recovered-masked.png)

每轮均先确认10个本人消息已ACK、PEL=0、未完成outbox=0、未解决review=0和持久订单归属，才删除自有SQL/Redis/Stream entry；**没有通过XACK清理未知结果**。登录session/code/throttle、活动六类键、order reservation、本人comp:done幂等键均精确删除并核对；共享Stream/group/日序号保留。三轮 cleanup=true，没有残留本脚本活动或用户，服务由主实现者统一收尾。

实跑为上海浏览器时区；已有交易两时区证据继续复用，本票没有为相同服务端字符串显示重复全套交易页面。实际Go API健康版本为 `go074-qa2-worktree`，直连与Vue代理一致；最终交付SHA及二进制摘要由本票总报告绑定。Playwright Core 1.56.1和本机Chrome，未新增前端依赖。所有环境显式读取，未使用GO-076核验服务或CRM占用端口。

```powershell
# 主实现者准备的专用API/Vue/MySQL/Redis；密码仅进程环境，不写入结果
$env:API_BASE='http://127.0.0.1:8120'; $env:WEB_BASE='http://127.0.0.1:5220'
$env:MYSQL_HOST='127.0.0.1'; $env:MYSQL_PORT='3380'
$env:MYSQL_DATABASE='project_full_stack_go'; $env:MYSQL_USER='fullstack'
$env:REDIS_HOST='127.0.0.1'; $env:REDIS_PORT='6450'; $env:REDIS_DATABASE='0'
$env:UI_TIMEZONE='Asia/Shanghai'; $env:UI_EVIDENCE='<本目录>/round-N.json'
# 显式PLAYWRIGHT_MODULE为已安装playwright-core/index.mjs，CHROME_PATH为本机Chrome
node scripts/go-merchant-orders-ui-smoke.mjs
node --check scripts/go-merchant-orders-ui-smoke.mjs
```

限制：没有保存带鉴权trace/HAR，证据为安全网络元数据、断言结果与遮蔽截图；网络失败由传输abort注入，恢复通过真实页面reload，不要求新增页内按钮。仅本有限角色/范围/动作矩阵，非任意并发撤权证明、生产容量、持续压测或跨浏览器覆盖；旧有Go事务并发证据仍按原适用SHA使用。当前只代表自测通过待独立核验。
