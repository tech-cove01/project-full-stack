# 当前进度与开发入口

更新：2026-10-27。用户已确定全面改用 Go 后端，前端保留 Vue 3/TypeScript，数据库保留 MySQL。唯一总路线见 [Go 迁移方案](implementation-plan.md)，实现者先读 [交接说明](agent-handoff.md)。

## 当前事实

- Java 参考基线为 `9f8c2b10839ef74556de0f55d19afc3ac215f9da`，由标签 `java-baseline-20261026` 保留；冻结后不再新增 Java 功能。
- `backend/` 已实现 GO-001 工程基础、GO-010 登录/恢复/退出、GO-011 商铺浏览、GO-020 逐门店权限/工作台、GO-021 活动发布/下架、GO-030 受理/异步成单/本人订单和 GO-031 模拟支付/取消/超时关单；隔离真实依赖与 Vue 页面证据见下方各票。核销/退款/Redis补偿、其他商家业务、AI 和社区尚未迁移/实现，不能称为全面迁移完成。
- Java 基线已有交易/B端/业务 AI，社区/Kafka/Feed 尚未实现。这些是 Go 的移植范围与新增范围，不是 Go 已完成功能。
- 最新 Java 修复提交 `e3d938d`、`9568206`、`9f8c2b1` 包含快照/AI 终态修复及窗口脚本，尚未在本聊天独立复核。标签是源码参考点，不是“全部验收通过”标签。
- 本机已观察到 `go1.27.0 windows/amd64` 可调用。数据库、Redis、Kafka、Go 依赖及 CI 在实施时重新验证，不能沿用旧环境记录推断当前健康。

## 产品与交付范围

本地生活交易 + 商家运营 + 探店社区 + 业务 AI，一个后端和一个前端贯通三条流程：

1. 商家发布活动 → 用户抢券/模拟支付 → 店员核销 → 店主审计；取消、超时、退款与恢复可验证。
2. 用户发探店笔记 → 他人关注/点赞 → 稳定翻页的关注动态；事件、缓存、计数可以恢复和测量。
3. 用户问公开规则/查本人订单 → 带出处或实时结果的答复；商家维护 FAQ，停用后旧知识失效。

前后端基础完整性包括登录/退出、商铺搜索分类、列表分页/加载更多、空态/错误/重试、按门店控制管理按钮。保留社区/Kafka/Feed 主要能力；Canal、ES、PostgreSQL/pgvector、压缩计数、分钟热榜、微服务集群不作为成型前置。

## 任务状态

已迁移到根目录 [STATUS.md](../STATUS.md)：工作流、任务状态表、门槛与复跑命令以那里为准。本文件保留产品范围、各票的详细交付记录与核验报告链接；小任务的范围与依赖见 [方案第 6 节](implementation-plan.md#6-里程碑与细粒度任务)。

### GO-001 / 工程、配置、健康与关闭

2026-10-26 独立核验已完成，覆盖HEAD d27a8ea：Go全套普通测试、vet/格式/模块校验、前端构建/格式、真实认证和商铺集成、启动关闭6场景、真实登录页面12项及UTC/上海商铺页面各14项均通过。可以继续GO-020；未执行Linux race/远端CI。完整结论、原始页面证据与GO-011-QA1修正见 [核验报告](reviews/2026-10-26/go-g1-d27a8ea/report.md)。下方各票仍保留实现者自测记录，不覆盖历史失败。

- 依赖：GO-000；开始 SHA `434679cf86aaa125f1f9a35fa695cc7050eed907`；交付 SHA `69ddd3ee001251b5a16ee252fbacf51130643630`。
- 交付行为：隔离 MySQL/Redis 启动 Go API；缺配置、依赖不可达或端口冲突明确失败；健康区分 live/ready 并返回 Go 身份；所有 HTTP 退出路径有界排空后关池。前端代理目标可配置，默认 Go 8082。
- 验收：MIG-GO-01；MIG-GO-02 的代理配置部分（业务与真实页面归 GO-010/011）。不涉及业务/SQL 变更、Lua、Stream/outbox 消费。
- 命令与证据：`go mod verify`、`gofmt -l .`、`go vet ./...`、`go test -count=1 ./...`、构建和 `go-bootstrap-smoke.py`；前端 build / Vite 配置 prettier。详见 [报告](evidence/2026-10-26-434679c/GO-001/report.md)。
- 限制：Linux race 和远端 CI 未执行；启动后真实依赖中断恢复尚未执行，ready 失效/超时由局部测试覆盖。独立静态审查不替代完整业务验收。

### GO-010 / 登录、身份恢复与退出

- 依赖：GO-001；开始 SHA `69ddd3ee001251b5a16ee252fbacf51130643630`；交付 SHA `cc0c6f920897b3ed65a4f080997a6c538fe0c2fa`。
- 交付行为：真实 Vue 经代理登录 Go、刷新 `/user/me` 恢复身份、退出撤销当前 session；失效请求 401 清理本地身份。验证码节流/单次消费、DB 手机号唯一、session 写入及续期原子化，大 ID 保持字符串。
- 验收：MIG-GO-02 身份子集、MIG-GO-03 字符串 ID / null省略 / 空数组 / total兼容子集；商铺、金额/业务时间、SSE不在本票。
- 验证：gofmt/vet/full go test PASS；真实 `GO_INTEGRATION=1 go test -count=1 -v ./internal/auth -run TestIntegrationAuth` 七组通过；真实 `go-auth-ui-smoke.mjs` 十二项通过。原始证据与命令见 [报告](evidence/2026-10-26-69ddd3e/GO-010/report.md)。
- 限制：Linux race / 远端 CI 未执行；手机号沿 Java 正常号段集合（去掉旧正则误收逗号），不扩大到前端接受的全部号段；无独立 refresh 路由。坏类型为 503，缺失/过期/损坏或无 TTL session 为 401；不把异常永久凭证修复为有效态。未知业务路由暂为 404。

### GO-011 / 商铺浏览、搜索分类与公开优惠

- 依赖：GO-010；开始 SHA `cc0c6f920897b3ed65a4f080997a6c538fe0c2fa`；交付 SHA `644b2c55ee73e1318f78c74ce8d24affef707295`。
- 交付行为：名称与分类在同一后端查询中组合、稳定分页及字符串 total，真实 Vue 53 家店完整翻页，独立分类/优惠重试，详情与公开活动展示。ACTIVE 且启用的券才公开，未开始/已结束/售罄仍展示；人均价为元，券金额为分，均不丢字符串精度。修复无偏移上海时间在 UTC 浏览器被错解析的问题。
- 验收：MIG-GO-02 浏览子集、MIG-GO-03 商铺/券 DTO 与金额/时间/空值/total 子集、PRODUCT-01 商铺子集；订单和商家列表分页仍归后续任务。
- 验证：gofmt/vet/full go test PASS；真实 MySQL 7 组、原版本 SQL 首次升级与校验和重跑 PASS；UTC 与上海浏览器各14项 PASS，登录页面回归12项 PASS；frontend build/prettier PASS。[报告和失败/最终证据](evidence/2026-10-26-cc0c6f9/GO-011/report.md)。
- 限制：`/shop/of/type` 带 x/y 明确400“附近坐标查询尚未迁移”；商铺详情直接读MySQL，L1/Redis缓存未迁移；5个公开GET已实现，HEAD未扩展；交易/商家/AI仍为404。已知旧商铺/券/分类写路由继续403。本票不证明持续容量，Linux race/远端CI未执行。

### GO-011-QA1 / UI夹具会话时区

- 依赖：GO-011；开始SHA `5c15358b287a39567df59d3becd52be54a28ee23`；单独fix提交SHA `514d0beef3de443be12f812faa3a7b3dd94d64d2`。
- 行为：seed自己的同一MySQL会话先 `SET time_zone='+08:00'`，再执行NOW和INSERT；不改变数据库全局时区、业务实现或精确ID清理。
- 验证：`node --check` PASS；受控初始UTC会话下旧seed窗口相对上海为-30600/-27000秒，当前实际seed为-1800/+1800秒，global前后均SYSTEM，清理剩余0；真实UTC浏览器14项PASS。[证据与边界](evidence/2026-10-26-5c15358/GO-011-QA1/report.md)。未重跑无关Go测试或Linux race/远端CI。

### GO-020 / 逐门店授权与商家工作台

- 依赖：已核验GO-010/011；开始SHA `514d0beef3de443be12f812faa3a7b3dd94d64d2`；交付SHA `8731642b5b37eb8fe7b2d32fc02f2adb6882ef5a`。
- 交付范围：仅 `GET /merchant/workspace` 和可复用的具体门店访问/管理权限、可见/可管理门店查询；不增加测试专用权限端点，不提前开放活动/订单/审计/FAQ操作。
- 授权：来自当前登录用户和实时MySQL，商家/成员ACTIVE；OWNER/MANAGER仅本商家，STAFF还须有效scope，跨商家脏scope不生效。可见范围与管理范围分离，角色不写入session；成员/商家停用或scope撤回后原token的授权立即缩减，登录身份可仍有效。
- 验收：P1-6权限基础子集、MIG-GO-03工作台DTO；具体发布/核销/退款等HTTP越权验收留对应业务票。真实DB服务层测试不写成这些未实现接口已通过。
- 无新DDL；`tb_shop`无status字段，本票不声明门店停用，只验证商家/成员状态、scope和归属。

- 结果：gofmt/vet/full普通Go测试、真实merchant集成11主组（含13组具体门店权限矩阵、tx句柄和真实闭池503）PASS；前端构建/文件格式PASS；真实Vue四账号24项PASS。独立只读审查者重复普通Go与真实11组并查看截图，无阻断。[命令、失败证据、边界](evidence/2026-10-26-514d0be/GO-020/report.md)。
- 后续边界：Access(tx)继承隔离级别，RR普通SELECT可能仍读旧快照，不是并发撤权屏障。GO-021/032的写操作必须使用锁定当前读或覆盖授权事实的条件更新；旧活动/订单页面的全局canManage及重试发布按钮需在启用对应业务时改成按每行shopId判断，不能沿用全局最高角色。

GO-020交付时已停止测试Go API/Vite与独立Go MySQL/Redis（Exited 0），卷和隔离库保留，原 `project-full-stack` 容器仍healthy。用户随后明确本票交其他agent核验并授权继续GO-021。附近坐标与详情缓存限制仍保留，不能据当前GET子集声称全部旧接口已接管。

### GO-021 / 活动发布、下架与交易 outbox

- 依赖：GO-020（实现已交付，用户独立核验另行进行）；开始 SHA `bb5d985d69bfb3d38d694b33ee934fc1499f647f`。按 GO-021a 门控/outbox 基础、GO-021b 活动接口/relay、GO-021c 页面贯通拆本地提交。GO-021a SHA `07436b90dde53a2043527c19a08ae6b3034b4ab4`。
- GO-021a 自测通过待核验：无新 DDL，四键 Lua 完整预校验、phase 下架屏障、不重建库存；只认领活动三事件、事务 outbox 和租约 fence。真实 outbox 5 主组、gate 6 主组通过。[本子提交证据](evidence/2026-10-26-bb5d985/GO-021/base-report.md)。接口/relay/页面另列后续子提交，不由本基础提交宣称完成。
- GO-021b 自测通过待核验：活动列表/创建/重试/下架四路由；当前锁定授权、并发同键幂等、审计/outbox 原子提交、提交后开闸和同步下架。应用 worker 使用独立生命周期并在关池前退出。最终真实活动 9 主组、publication 5 主组通过，全套普通 Go/vet/格式及启动关闭 6 场景 PASS。[行为、命令与限制](evidence/2026-10-26-bb5d985/GO-021/backend-report.md)。未开放订单或重建。
- GO-021b SHA `b55be123059992b36256541b4259d06f26365358`。GO-021c SHA `1fcf37de56d0512060aeffdfefb1fe25e53a5a6d`，页面自测通过待核验：逐行门店管理、分页/筛选、精确金额/北京时间、丢响应原键重试、卸载轮询停止；仅启用活动入口。UTC/上海最终各28项、工作台回归24项、最终构建/格式 PASS。[整票证据、失败轮次、环境与限制](evidence/2026-10-26-bb5d985/GO-021/report.md)。

GO-021测试应用及隔离依赖已停止，卷保留，原项目容器未变更；详情见证据。下一票GO-030受理→Stream→成单；用户另行核验GO-020/021。GO-033前仍无重建入口。Java旧摘要/无phase gate不自动接管开放，正式切换需GO-071排空/接管方案；Linux race、远端CI未执行。

### GO-030 / 受理、异步成单与本人订单

- 开始 SHA `e410c71d9d8dfeaf654e203e439fa3f871c9afd4`，依赖 GO-021；用户将此前批次交其他agent核验并授权继续。本票仍为自测通过待核验，不沿用其他票结论。[完整交付、命令与限制](evidence/2026-10-26-e410c71/GO-030/report.md)。
- 四个功能提交：准入/ID `f86a97fc563d526dba5c74c9e62f76d6167d3da6`；DB持久结果/本人API `0e5f5913d2c548f96fc7f2858c6e7f1bc008cba5`；Stream消费/生命周期 `871773efe0693ec6c05ae29ee84736146beab82c`；用户页面 `a7e45fd6ab9d990dfb47defe8c28e6431daffa53`。文档与环境收尾另提交，不改写历史。
- CI单独提交 `c87cd195a40710e948fe8eff0a294414b0ec8735`，在版本迁移后/启动API前串行执行三个订单包真实依赖/race测试；本地YAML解析通过，Linux race与远端CI尚未实际执行。[CI边界](evidence/2026-10-26-e410c71/GO-030/ci-report.md)。
- 已贯通真实Vue抢券→稳定受理ID→Stream→MySQL订单/库存/审计/outbox→本人查询/分页；响应丢失后即使活动下架仍可找回同ID。受理不代表成单，PROCESSING无伪快照；详情越权404清空数据，金额/ID字符串、上海时间、分页/空态/错误重试保持精度。
- 自测：准入9/ID4/恢复1、落单12主组（另4绑定竞争子组）、消费8主组通过；提交前回滚/提交后ACK前中断有受控context时间线。普通Go/verify/gofmt/vet、前端构建/格式、启动关闭6场景、UTC/上海页面各30项及商铺诊断回归14项通过。两份真实竞态初始FAIL和Vite失联/商铺分类超时及诊断证据均保留；分类首次超时根因未证实。
- 200人抢100库存得到100唯一受理/100单，无负库存，只是短突发正确性；每轮102个真实worker消息清理前pending0。SQL分页/PROCESSING查询夹具不算真实建单。未知持久结果不ACK、不回补；明确拒绝先持久化事件，本票仍保留PENDING补偿，支付/取消/超时/核销/退款与完整review/补偿/重建未开放。
- API/Vite与隔离Go依赖已停止，MySQL/Redis均Exited0，卷保留，原项目容器healthy；[收尾](evidence/2026-10-26-e410c71/GO-030/cleanup-final.json)。Linux race、远端CI、OS强杀、物理断库、Redis重启/灾难恢复和持续负载未执行。下一票GO-031按支付、取消、超时小功能继续，统一券→库存→订单锁序；GO-033前不开放重建。

### GO-031 / 模拟支付、主动取消与超时关单

- 开始SHA `0072c867ab1f34d7219e815d04779c651c55d9d0`，依赖GO-030。GO-031自测通过待独立核验；[完整交付与边界](evidence/2026-10-26-0072c86/GO-031/report.md)。GO-030已由核验者完成本票范围核验及QA修复，见[独立报告](reviews/2026-10-26/go030-0072c86/report.md)，不代表GO-031已验收。
- 四功能提交：模拟支付 `a5125d6ef8c7189e024a58dc2dd0fbc49cc78c47`；取消/过期事务 `6701e62a5d53d4132c4e9b5f81debdd82c6e2ae4`；DB超时扫描与重启验证 `8f82fa51f22926225d836d3cb8838aff66cfee02`；页面/延迟确认修复 `a9662cfd393cf434bec5b4b9b6b252845e843b2f`。CI单独提交 `43eb901e6bc63033ea291691f253559b69e3857a`；文档收尾另提交，保留历史。
- 支付按旧单DB期限、锁后当前时间和状态判断，256位随机凭证仅本人已付详情可见。取消/SYSTEM超时将DB库存、状态、审计及版本事件同事务只变一次；全程券→库存→订单锁序，审计/事件/库存异常全回滚。恢复事件仍PENDING，Redis资格/库存及accepted映射释放留GO-033，不宣称取消后已可立刻重买。
- 真实支付8/取消11/扫描SQL6/实际scanner→生命周期3主组通过；20支付/20取消、支付/取消及支付/过期竞争有DB对账。API在10秒旧期限前完整退出，离线跨期限，改1h配置重启仍关旧单；18项PASS、两进程exit0，另启动关闭6场景PASS。无ZSet依赖。
- 普通Go/verify/gofmt/vet、前端构建/格式、UTC/上海生命周期页面各25项、GO-030真实落单12/Stream8及原页面30项回归通过。真实行锁复现延迟支付确认后旧错误遮凭证的初始FAIL，修复后原断言通过；scanner夹具同值UPDATE误报及各轮日志也保留。并发均为正确性实验，不是持续容量。
- API/Vite与隔离Go依赖已停、卷保留、原项目容器healthy；[收尾](evidence/2026-10-26-0072c86/GO-031/cleanup-final.json)。Linux race/远端CI、OS强杀、物理断库、Redis重启及持续负载未执行；核销/退款/审计留GO-032，完整补偿/review/重建留GO-033。

### GO-032 / 核销、退款与审计

- 依赖：GO-031；开始 SHA `fa1fa50409567b75d06f1b89344b3eb68bf1d927`。四个功能提交：列表与审计 `8d5b655cf277ae2f39eb02aa73bf665a978e9a64`；核销 `b5439fd57364bceaecda1b1988c48658b7418f81`；退款 `1b292691de017b4e313a256baac0b37ce5fb3e76`；前端分页与字段对齐 `f0d9c55c7dc4a23de5a8b3eab85f32d71ccd0919`。
- 交付行为：商家订单列表按可见门店范围分页，支持 `shopId/status/orderId`，手机号服务端掩码；审计列表按管理范围分页，STAFF 的跨商家 scope 不并入。核销以 token 为凭证但仍按当前 membership 授权，未知 token 与他人门店 token 同为 404；锁序 `catalog→inventory→order`，状态更新同时校验核销期，重复核销返回 `alreadyRedeemed`。退款仅 OWNER/MANAGER，要求已支付且未过核销结束时间，DB 库存同事务回补一次，事件 `releaseEligibility=false`。前端两个列表改为服务端分页并把 `MerchantOrder` 字段对齐为 `phone/status`。
- 验收：接近 P1-C/P1-5/P1-6 的业务子集；本票不覆盖重建与补偿。
- 验证：`gofmt -l` 干净、`go vet ./...`、`go test -count=1 ./...` 全绿（含新包 `merchantorder`）；前端 `vue-tsc`、生产构建、prettier 通过。
- 限制：真实 MySQL 并发核销/退款、库存对账与浏览器页面均未执行，留独立核验；Redis 资格与库存回补仍 PENDING，归 GO-033；Linux race、远端 CI 未执行。

### GO-033 / 补偿、复核与重建

- 依赖：GO-030/031/032；开始 SHA `3519a08`（文档收尾后的工作区）。四个功能提交：补偿 worker `5fc0879848eb684847a31dffb4b27cf9064cc194`；持久复核与 ACK 顺序 `ac67d416955058bb57e1dc242fe32b9820c90f14`；重放命令 `9b00b9ae2fee4958c44e07c01c40de0b02317dec`；显式重建 `c279cf806e9f8d0f85587dcd869c0bff831ce253`。
- 交付行为：`ORDER_CHANGED` 由独立 worker 认领并幂等应用到 Redis，脚本先校验标记类型、库存键存在/类型/可解析、`stock+1<=total_stock` 与资格键类型，全部通过才写标记与效果；缺键、错类型、越界停车 `NEEDS_REVIEW`，不再静默跳过。消费者对未知结果不 ACK，超过 5 次先写 `tb_order_review` 再 ACK，写不进去保持 pending；建单或持久拒绝后置 `RESOLVED`。`cmd/review list|replay` 先认领（`OPEN→REQUEUED`）再 XADD，失败回退 `OPEN`。重建接口先关闸 N+1，再核对 Stream 在途、未决 review、待补偿 outbox（全部锁定读，`READ_COMMITTED`），清零后按锁定行在 N+2 重建，旧 OPEN 事件因版本不匹配被拒。
- 验收：接近 P1-C/P1-5 的恢复子集与交易对账不变式；不覆盖持续负载。
- 验证：`gofmt -l` 干净、`go vet ./...`、`go test -count=1 ./...`、`go build ./...` 全部通过（新增 `ordercomp`、`orderreview`、`cmd/review`）。
- 限制：真实 MySQL/Redis 的补偿缺键、DB 停机跨阈值、重放二次失败、重建停写窗口均未执行，留独立核验；Redis 重建的并发窗口证据未采集；Linux race、远端 CI 未执行。

### GO-040 / FAQ、分块与索引版本

- 依赖：GO-033；开始 SHA `58c2830`。两个功能提交：provider 抽象与 stub `2a0cc977dec947db68cf3ae77c0f44587626bd70`；FAQ 版本与分块发布 `21e843d6a14ac68b2bad6242db7784b0491da526`。
- 交付行为：`ai.Provider` 定义 Embedding 边界，stub 用确定性词法向量并自报 stub，不冒充真实模型。商家 FAQ 草稿按文档真实门店授权；发布把正文冻结成不可变版本，写入分块与向量，再推进 `published_version` 与 `epoch`；嵌入在事务外计算，第二个事务重读正文/版本/epoch，期间被修改则拒绝写入。停用保留版本历史但列表与公开读取都隐藏；未知、草稿、停用对匿名调用者统一 404。分块按段落聚合，超长段落按句切分且不超过 400 字。
- 验收：AI 验收的 FAQ 子集与 MIG-GO-03 的大 ID/字符串金额子集；检索与生成不在本票。
- 验证：`gofmt -l` 干净、`go vet ./...`、`go test -count=1 ./...`、`go build ./...` 全部通过（新增 `ai`、`aiknowledge`）。
- 限制：真实 MySQL 的发布/停用与公开读取未执行；真实模型嵌入、检索与 SSE 属 GO-041；`tb_ai_chunk.embedding` 目前只写 stub 向量，切换真实模型需要重建索引；Linux race/远端 CI 未执行。

### GO-041 / 检索、本人工具与 SSE

- 依赖：GO-040；开始 SHA `2fecd3e`。三个功能提交：检索 `85a5ef7a3ac9bf82f8187b89ee4c8ff1bb8285b0`；本人只读工具 `536fe097d40852a3c2f50490d6c29454cf88ce88`；SSE 会话与生成 `ec89df711a2663ac70d1a8dd206476d9959db5a8`。
- 交付行为：`GET /ai/search` 只检索 `published_version` 指向的版本，停用/被替换文档不可见，模型或维度不匹配的分块跳过，0.18 以下分值丢弃，同分按 chunk id 稳定排序。`aitool` 只用登录身份查本人订单、公开活动与门店，未知订单与他人订单同一回复，卡片不含 token/手机号。会话为 Redis 24 小时、最多 40 条，追加与存在性检查在同一脚本内；回合依次发出 `meta → status(tool|retrieving) → [tool_result] → [sources] → status(generating) → answer → done`，错误以 `error` 事件给出且不泄露内部文本。
- 验收：AI 验收的检索子集、只读工具子集与 SSE 协议子集；去重/预算/截止属 GO-042。
- 验证：`gofmt -l` 干净、`go vet ./...`、`go test -count=1 ./...`（20 个包）、`go build ./...` 全部通过（新增 `aichat`、`aitool`）。
- 限制：真实 Redis/MySQL 的会话回合、工具查询与 SSE 客户端行为未执行；stub 生成只证明协议，不代表真实模型质量；历史消息暂不进入提示词（多轮记忆属 GO-042）；Linux race/远端 CI 未执行。

### GO-042 / 去重、预算与终态保护

- 依赖：GO-041；开始 SHA `86999e8`。提交：去重与终态 `eacd5a3e33d27510183cbb6726f5353318c1ef8e`；限频与日预算 `c638912a6f1313be20435b4aa977029b59489e8b`；端到端集成测试 `515973d35787f76820f3d1a5951b339a4a31d559`。
- 交付行为：一个 `clientMessageId` 只能认领一次（NEW/IN_PROGRESS/CACHED/CONFLICT 单脚本判定），完成态重放不再调用模型，失败删除认领以便重试，同一编号换内容直接冲突。会话租约 90 秒、令牌化释放；所有出口（工具、无证据、检索、生成、收尾）统一检查截止时间、租约归属与会话存在，删除会话或被接管后不再推送或落库。分钟与日上限在租约之前检查并分别返回 429。最近 6 条历史进入提示词。
- 验收：AI 验收的生命周期子集（去重/预算/取消/终态）与 P1-D 失效保护子集。
- 验证：真实 Redis/MySQL 集成通过 —— 认领四态、重放载荷、清理、租约归属、分钟与日上限、以及“发布 FAQ → 检索 → 生成 → 历史 → 重放 → 删除会话”整轮；`gofmt/vet/go test/go build` 全绿。
- 限制：真实模型质量未评测（stub 只证明协议）；SSE 客户端断连与上游不可取消只做了资源释放，未测真实慢调用；Linux race/远端 CI 未执行。

### GO-050 / 笔记发布与详情

- 依赖：GO-033；开始 SHA `db612ea`。提交：后端与迁移 `147c70a9ef690201057d1701fdf9ece93fa83994`；页面 `f12a04deeef1ca44913f94b4ed1142b2d1e044cd`。
- 交付行为：迁移 006 为 `tb_blog` 增加 `status/publish_version`，新增 `tb_blog_like`（每对一条、active+version）、为 `tb_follow` 补 active/version 与唯一对、建立独立 `tb_community_outbox`。`POST /notes` 只接受门店、标题、正文与 http(s) 图片，作者取自会话，门店必须存在；`GET /notes/:id` 公开且只返回 `PUBLISHED`，草稿与不存在的 id 同为 404。页面 `/notes/new` 与 `/notes/:id` 共用 `NoteView`。
- 验收：PRODUCT-01 笔记子集；点赞/关注/Feed/事件属 GO-051—053。
- 验证：真实 MySQL 集成（发布、读回、未知门店拒绝）PASS；`gofmt/vet/go test/go build` 与前端类型检查/构建/prettier 通过。
- 限制：仅发布即公开，无草稿/审核流；列表、搜索与 Feed 未做；`tb_blog.liked/comments` 仍是被动计数，位图投影属 GO-062；Linux race/远端 CI 未执行。

### GO-051 / 关注与 DB 基线 Feed

- 依赖：GO-050；开始 SHA `7c23cc4`。提交：后端 `e2c1474495d381fa1cdbad0fd34e98ff6810369e`；页面 `76abdd7d42792ca44aba411c1705afd983d34db3`。
- 交付行为：`PUT /authors/:id/follow` 为期望状态写入，重复当前状态不改版本，真实变化版本 +1 并与社区 outbox 同事务；关注自己或不存在作者被拒绝。`GET /authors/:id/relationship` 返回本人关系与公开关注数；`GET /feed/notes` 用 `(create_time,id)` 复合游标，先读关注作者再按索引倒序取一页。
- 验收：PRODUCT-02 关注/Feed 子集；Kafka 投影与点赞属 GO-052/053。
- 验证：真实 MySQL 集成覆盖幂等（重复关注版本不变）、每真实变化一条事件、同秒三条笔记的游标翻页无重复、取关后为空；`EXPLAIN` 显示 JOIN 写法 `Using temporary; Using filesort`，两步写法为 `Backward index scan` 且命中 `idx_blog_feed`。`gofmt/vet/go test/go build` 与前端类型检查/构建/prettier 通过。
- 限制：Feed 只有第一页与"加载更多"，无定位跳页；关注数实时 COUNT，未接 Redis 计数投影；关注列表、作者主页未做；社区 outbox 尚无投递者（GO-052）；Linux race/远端 CI 未执行。

### GO-052 / 社区 outbox → Kafka

- 依赖：GO-051；开始 SHA `f6dc4f1`。提交：relay 与配置 `db77e58346fbcf359fbc3e8e26c45271e8997c4e`；真实 broker 测试 `2f920ca3bb92f9c22e3d5cacf96f9a27fef88c6d`。
- 交付行为：`compose.go.yaml` 增加 `kafka` profile（KRaft 单节点，不 advertise 0.0.0.0，`log.dirs` 指向命名卷）。`KAFKA_BROKERS` 为空时 relay 不启动、进程照常运行。relay 以 30 秒租约认领 `tb_community_outbox`，`attempts` 作为 fence；broker 确认后写 `DONE`，失败按 1→2→4…→256 秒退避，超过 8 次停车 `NEEDS_REVIEW`；失去租约的 worker 得到 `ErrLeaseLost` 而不是覆盖新结果。消息以 `aggregate_id` 为 key，信封含 `eventId/eventType/aggregateType/aggregateId/version/payload`，并带 `eventType/eventId/version` header。
- 验收：事件可靠性子集（至少一次）；消费端幂等与 Feed 投影属 GO-053/060。
- 验证：真实 Kafka 集成 PASS —— 发送后可从 topic 读回、信封与 header 正确、状态 `DONE`、完成后不再重复投递、空队列返回无工作；`127.0.0.1:1` 不可达时保持 `PENDING`、`attempts=1`、`next_attempt_at` 在未来。`gofmt/vet/go test ./...`（23 包）全绿。本机 broker 用后已 `stop`，卷与镜像保留，复跑用 `docker compose -f compose.go.yaml --profile kafka up -d kafka`。
- 限制：至少一次语义，消费者必须按 `eventId` 去重；没有死信重放的运维命令；未接真实消费端（投影在后续票）；Linux race/远端 CI 未执行。

### GO-053 / 期望状态点赞

- 依赖：GO-050/052；开始 SHA `f4eac0f`。提交：后端 `b7e9717ce8af381315087721dd174b5fb91e4aa8`；页面 `3e99ce98d4ceccb3a214260101335dbac4ac25f9`。
- 交付行为：`PUT /notes/:id/like` 以期望状态写入；先 `FOR UPDATE` 锁笔记行，再改 `tb_blog_like` 与 `tb_blog.liked` 计数，重复状态不改版本、不发事件；计数用 `GREATEST(0, liked+?)` 保底，每次真实变化写一条 `LIKE_CHANGED` 到 `tb_community_outbox`。`GET /notes/:id/like` 返回本人状态、版本与公开计数；未发布与不存在的笔记同为 404。笔记详情页提供点赞按钮并同步计数。
- 验收：社区互动子集；位图计数与重建属 GO-062。
- 验证：真实 MySQL 集成通过 —— like/unlike 幂等（版本与计数不变）、10 并发点赞收敛为 1 条 active 与 1 条事件、未知笔记拒绝；`gofmt/vet/go test ./...`、前端类型检查/构建/prettier 全绿。
- 限制：计数仍为 `tb_blog.liked` 单列，未接 Redis 位图；无点赞列表；Linux race/远端 CI 未执行。

### GO-060 / 页卡片批量装配与计数缓存

- 依赖：GO-051/053；开始 SHA `b1c778a`。提交：批量装配 `e67e28081973821a01c5cee0dba7fe15c5a4f419`；计数缓存 `f521831acfadb27cce19f65c7876c64816b0cd2f`。
- 交付行为：Feed 一次 JOIN 取门店名、一次 `IN` 查本人点赞，卡片带 `shopId/shopName/likedByMe`；计数缓存键 `community:counts:<id>`，页面用一次 `MGET` 加一次 `IN` 回源，详情走单键 singleflight，TTL 60 秒加 0–15% 抖动，点赞后删除键。缓存读写失败只降级到数据库，不影响请求。
- 验收：G6 的批量装配与页卡片计数子集；热点保护、位图计数与重建属 GO-061/062。
- 验证：真实 Redis/MySQL 通过 —— 冷读回源、直接改库后仍命中缓存、失效后重建、20 并发读一致、批量读忽略未知笔记；Feed 集成测试断言相邻卡片不串门店/点赞字段。`gofmt/vet/go test ./...` 与 `go build` 全绿。
- 限制：只缓存 `liked/comments`，不含关注数；没有热点 key 保护；位图与重建未做；Linux race/远端 CI 未执行。

### GO-061 / GO-062 / 热点保护、位图计数与重建

- 依赖：GO-060；开始 SHA `27e046c`。提交：热点本地层 `031d97cca906267c55e561eefd966064f53ddc97`；位图与重建 `54fc5e8a1fef779e55f3c7701f508cbcf6d58be4`。
- 交付行为：计数缓存前加 2 秒进程内副本（上限 4096 条），失效时两层同时清除，热点笔记不再每次访问 Redis；点赞位图以平台用户 id 为偏移，事务提交后置位、状态读取优先位图，超过 32 位偏移的 id 跳过；位图无 TTL，因为它是可重建投影而非缓存；`cmd/recount <noteId>` 用一个流水线从 `tb_blog_like` 重建位图并核对计数，不一致以非零退出报错。
- 验收：G6 的热点与位图子集；对账以 MySQL 为准，不以位图为准。
- 验证：真实 Redis/MySQL 通过 —— 删除 Redis 键后本地层命中、过期后回源重建；位图幂等置位、取消、重建替换旧位、越界 id 跳过；点赞后位图与状态读取一致；`gofmt/vet/go test ./...`（24 包）与 `go build` 全绿。
- 限制：`tb_blog.liked` 与位图是并行计数，尚无统一对账入口；重建只支持单笔记；Linux race/远端 CI 未执行。

原 Java 增量开发的 30—50 小时估计已失效，不能用于 Go 重写。GO-010/011 首次真实贯通后按实际移植速度、剩余交易/AI边界与联调量重新估算。不要把模型生成代码时间当成可投递项目交付时间。

### GO-072 / Kafka消费与两个查询投影

本票已按持久投影、停车/重放/重建、查询接入、消费者和贯通验证分别提交，自测通过待独立核验；状态仅在根STATUS维护。[完整SHA/命令/失败与限制](evidence/2026-10-28-8c3fb0e/GO-072/report.md)。

启用Kafka时，独立follow/feed组按eventId去重与业务版本应用关注图、公开笔记作者目录。原始失败持久保存后才允许提交offset，CLI可精确重放或原子重建派生模型。实际Relationship/Feed使用完整投影，否则回退DB；本人状态及当前关注/公开条件始终受事实守护。目录采用pull，不宣传每用户fan-out或强一致。

真实Kafka6主组、仓储9/恢复5、查询20子组、HTTP→outbox→Kafka双组→查询及应用启动关闭6场景通过。新增009—011版本SQL须先正常迁移；现有事实不改、新投影可回退。GO-072未执行远端CI/Linux race或持续量化，上一轮核验的绿色CI不替代本票结果；本票无前端源码改动。

## 文档与目录

`project-full-stack/` 是正式仓库，旁边 `references/` 是参考资料，不进入构建。当前 `backend/` 仍是 Java；迁移期间可临时使用 `backend/`，验收后正式收敛到 `backend/`，最终不保留两套活跃后端。前端、SQL、Lua 和 HTTP 验收尽量复用。

| 要做什么 | 入口 |
| --- | --- |
| 技术栈、迁移任务、隔离切换、截图能力落点 | [implementation-plan.md](implementation-plan.md) |
| 交给实现 agent、单任务提交/复核规则 | [agent-handoff.md](agent-handoff.md) |
| 交易状态、权限、库存与恢复契约 | [transaction-design.md](transaction-design.md)、[acceptance.md](acceptance.md) |
| 社区/缓存/事件业务要求与验收 ID | [backend-highlights-plan.md](backend-highlights-plan.md)、[backend-highlights-acceptance.md](backend-highlights-acceptance.md) |
| AI 业务范围与验收 | [ai-design.md](ai-design.md)、[ai-acceptance.md](ai-acceptance.md) |
| Windows 环境、接口表和历史启动命令 | [windows-development.md](windows-development.md)、[development.md](development.md) |
| 历史 Java 实测与独立核验 | [evidence](evidence/)、[reviews](reviews/) |

旧报告保留原日期/SHA/语言，不能批量改成 Go。H0—H6 是业务需求/历史报告引用编号，当前执行顺序统一使用 G0—G7 / GO-*，不同时维护两条路线。已有已通过的业务场景转为 Go 回归，不继续按旧计划补 Java。

## 四条亮点与量化边界

本项目定位是一页简历里的一段后端/全栈项目，配合 RAG 实习和另一个 Agent 项目。目标是三条后端 + 一条 AI，以下是完成验收后的写作方向，不是当前完成声明：

1. 高并发交易：Redis Lua 准入、Stream 异步落单、MySQL 条件更新/唯一约束、事务 outbox、持久复核与库存恢复。
2. Feed 查询与缓存：页 ID/公开卡片/计数拆分，批量读取和缺失项批量回源，本地有界缓存 + Redis，singleflight、复合游标、TTL 抖动与轻量热点保护。
3. 可靠社区事件：关系变更与 outbox 同事务，Kafka 至少一次投递，关系版本/幂等消费、两类业务投影、积压恢复与重建。
4. 业务 AI：公开规则 RAG 带来源，本人订单只读查询，知识版本失效、隔离会话、原子去重与有界预算。

Java 历史 20 VU × 10 次、200 账号抢 100 库存得到 100 单、无重复/超卖，是短突发正确性证据。约 0.243 秒的 821.97 req/s 和混合成功/售罄 P95 29.39ms 不是持续容量，更不是 Go 成绩。截图里的 QPS、内存节省与延迟降幅不能沿用。

Go 重新记录稳定负载 RPS/P95/P99、DB 查询数、cold miss 加载次数、成功受理至成单耗时、投影延迟及故障恢复时间，报告硬件/版本/数据量/分布/时长。语言与运行时会影响性能，不能简单认为换 Go 就提高 QPS。

完成标准是三流程可复现、关键权限/竞争/故障通过、真实负载证据和干净环境启动，README 与实物一致。达到后以项目讲解、算法和面试准备为主，不继续为技术栈名单叠组件。
