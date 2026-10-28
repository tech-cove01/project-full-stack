# 运行与验证

2026-10-26 Go 路线：接口表作为迁移契约继续复用。Go 工程基础位于 `backend/`，只交付下述已列出的能力，业务迁移进度见 [开发入口](README.md)。下面 Maven、JDK、Spring 配置和历史测试均指 Java 基线，不是当前 Go 开发要求，也不能作为 Go 通过记录。执行顺序和隔离切换见 [Go 总方案](implementation-plan.md)。

Windows 本机的具体端口、环境检查和三个 PowerShell 终端启动步骤见 [Windows 开发说明](windows-development.md)。后续改造任务见 [分阶段方案](implementation-plan.md)，交接入口见 [agent-handoff.md](agent-handoff.md)，目标验收见 [acceptance.md](acceptance.md)。本文接口表描述当前基线，不代表规划接口已经实现。

## Go 迁移环境与启动

工具链固定 Go `1.27.0`，Gin `1.11.0`、GORM `1.31.1`、GORM MySQL `1.6.0`、go-redis/v9 `9.17.0`；完整依赖与校验和见 `backend/go.mod` / `go.sum`。MySQL `8.4`、Redis `7.4-alpine` 沿用版本系列标签，证据另记实际镜像 digest。程序不自动读 `.env`，不执行 AutoMigrate或重置活动库存。当前API会运行订单Stream、交易/活动outbox及后台恢复任务；配置Kafka后运行社区relay及follow/feed消费者，ES配置就绪后另运行search消费者。连接必须使用隔离库，不可与另一API进程共用worker数据；重启会继续已有持久任务。

在根目录启动隔离依赖（不与 `compose.yaml` 混用）：

~~~powershell
docker compose -f compose.go.yaml up -d --wait mysql redis
docker compose -f compose.go.yaml ps
~~~

独立项目名 `project-full-stack-go`、卷 `go-mysql-data` / `go-redis-data`，默认端口 3308 / 6380。首次初始化复用 `infra/mysql/001-schema.sql` 和 `002-demo-data.sql`。启动应用前在根目录显式执行 `node scripts/migrate.mjs`，需本机 MySQL 客户端（可用 `MYSQL_BIN` 指定路径）；连接环境使用下方3308隔离配置。runner记录003—012的版本与校验和，重跑应跳过，不能靠重启容器或 AutoMigrate 代替。应用启动不迁移库。禁止把 Go 指向 Java 的数据或同时消费同一 Stream/outbox。

另一个 PowerShell 在根目录设置进程环境（示例是隔离本机开发值）：

~~~powershell
$env:GO_ENV = 'development'
$env:API_ADDR = '127.0.0.1:8082'
$env:MYSQL_HOST = '127.0.0.1'
$env:MYSQL_PORT = '3308'
$env:MYSQL_DATABASE = 'project_full_stack_go'
$env:MYSQL_USER = 'fullstack'
$env:MYSQL_PASSWORD = 'fullstack_go_dev_only'
$env:REDIS_HOST = '127.0.0.1'
$env:REDIS_PORT = '6380'
$env:REDIS_DATABASE = '0'
$env:REDIS_PASSWORD = ''
Set-Location backend
go run ./cmd/api
~~~

Go 默认监听 `127.0.0.1:8082`。MySQL host/port/database/user/password 和 Redis host/port 必须显式配置；缺配置或启动探测失败均非零退出。`GO_ENV` 取 development/test/production；默认 development，仅适用于本机，生产短信尚未接入。时区固定 `Asia/Shanghai`，不使用机器时区。可选 `GO_STARTUP_TIMEOUT=8s`、`GO_HEALTH_TIMEOUT=2s`、`GO_SHUTDOWN_TIMEOUT=10s`（正时长且不超过 1m）。Ctrl+C 会停止接收请求，在关闭期限内排空后关闭 MySQL/Redis 池；超期强制关闭并报错。

`GET /health/live` 返回进程存活，`GET /health/ready` 与 `/actuator/health` 实时探测两个依赖，失败返回 503；响应含 `service: project-full-stack-go`、`runtime: go`、构建 revision（未注入时 unknown）。健康不证明业务迁移已完成。

前端在 `frontend/` 执行 `npm.cmd run dev -- --port 5174 --strictPort`，默认代理 `http://127.0.0.1:8082`。可通过进程环境或 `frontend/.env.local` 的 `API_PROXY_TARGET` 覆盖；`VITE_API_BASE_URL` 默认 `/api`。先访问 `/api/actuator/health` 确认 Go 身份。回看 Java 时必须显式改目标为 `http://127.0.0.1:8081`。

在 `backend/` 中验证：

~~~powershell
go mod verify
gofmt -l .
go vet ./...
go test -count=1 ./...
go build -trimpath -o bin/api.exe ./cmd/api
python ../scripts/go-bootstrap-smoke.py --binary bin/api.exe --output ../bootstrap-result.json
~~~

最后一条需以上真实依赖环境。Linux CI 增加 `go test -race` 和真实依赖启动脚本；配置不等于远端 CI 已执行，实际结果见任务证据。停止应用用 Ctrl+C；停止依赖用 `docker compose -f compose.go.yaml stop --timeout 60 mysql redis`，保留卷并核对容器退出码。Git 回退不会撤销数据库变更，不用删卷回退。

GO-010 已实现 `POST /user/code`、`POST /user/login`、`GET /user/me`、`POST /user/logout`。`GO_ENV=development` 返回本机模拟验证码，生产未接短信时取码失败且不写 Redis。验证码 120 秒 / 每手机号 60 秒间隔，session 沿 Java 的 36000 分钟（25 天）滑动有效期，Redis key/hash 字段保持兼容。前端刷新通过 `/user/me` 恢复身份，没有独立 refresh 接口；authorization 使用原始 token 或 `Bearer <token>`。手机号正常号段与 Java 一致，仅修正正则中的逗号误匹配；未迁移业务返回 404。

真实身份测试（同样先显式设置隔离连接环境，`backend/` 中执行）：

~~~powershell
$env:GO_INTEGRATION = '1'
go test -count=1 -v ./internal/auth -run TestIntegrationAuth
~~~

测试复用 `001-schema.sql` 的 `tb_user`，只清理自身随机用户/key，不清空数据。真实 Vue 脚本在根目录 `node scripts/go-auth-ui-smoke.mjs`；需设置 `PLAYWRIGHT_MODULE`（已安装 Playwright 的模块路径或 file URL）、可选 `CHROME_PATH`、`WEB_BASE`（默认5174）、`API_BASE`（默认8082）、`UI_EVIDENCE`（JSON输出）。脚本创建随机演示用户、退出并撤销其 session，证据不含手机号/验证码/token。该页面脚本不证明商铺/交易已迁移。

GO-011 提供公开 `GET /shop/of/name?name=&typeId=&current=1&pageSize=10`、`/shop/of/type?typeId=...`（默认每页5条）、`/shop/{id}`、`/shop-type/list` 和 `/voucher/list/{shopId}`。名称为字面子串，可和分类组合，`pageSize` 上限50，稳定按ID升序及字符串total；空结果为 `[]`。商铺人均价是元，券金额是分，两者均字符串；时间保留无偏移的Shanghai ISO格式，前端显式按+08:00解释。坐标x/y查询暂返回400，未迁移详情缓存；旧已知商铺/分类/券写路由继续403。

真实商铺测试执行 `GO_INTEGRATION=1 go test -count=1 -v ./internal/shop -run TestIntegrationShop`（PowerShell先设置该环境变量）。`node scripts/go-shop-ui-smoke.mjs` 使用同一组UI环境变量，并需要显式MYSQL连接环境；创建53家大ID店铺、分类与券后精确清理，支持 `UI_TIMEZONE=UTC` / `Asia/Shanghai`。脚本通过浏览器离线和选择性网络abort验证失败/重试，成功响应均来自真实Go，不Mock数据。大ID夹具会推进隔离表AUTO_INCREMENT，即使清理也不重置；不要把该脚本指向旧库或共享数据环境。

GO-020 已提供受保护的 `GET /merchant/workspace`，返回当前身份的 `{merchants:[{id,name,role,shops:[{id,name,address}]}]}`。没有商家身份仍返回200空数组；匿名/过期登录401；MySQL失败503，不伪装为空权限。OWNER/MANAGER仅有本商家的门店，STAFF还需显式scope，当前商家/成员都必须ACTIVE。每个请求重新查DB，角色不缓存于token；停用成员/商家或撤回scope后，原token仍可 `/user/me`，但工作台授权立即缩减。

商家集成：在 `backend/` 显式设置隔离环境及 `GO_INTEGRATION=1` 后运行 `go test -count=1 -v ./internal/merchant -run TestIntegrationMerchant`。无新增SQL迁移。`merchant.NewAccess(tx)`复用事务句柄，但读取继承tx隔离级别；后续业务写操作须另外保证当前授权事实与变更边界一致，不能把RR普通查询当并发撤权保护。

根目录 `node scripts/go-merchant-ui-smoke.mjs` 运行真实Vue工作台验证，使用与商铺UI脚本相同的MYSQL/Playwright/Chrome/WEB_BASE/API_BASE/UI_EVIDENCE环境变量。脚本造3商家/5店/4用户，真实页面取码登录，沿原token验证混合身份、撤权/降级/停用、脏scope、错误重试与失效；只清理自身精确DB行并撤销发放session，验证码节流键按原60秒TTL到期。大ID夹具会推进对应自增序列。成功响应均来自真实Go；选择性网络abort仅用于失败注入。GO-021启用活动入口；订单/规则入口保持禁用。

GO-021 已提供 `GET /merchant/activities?current=1&pageSize=10&shopId=...&status=ACTIVE`（筛选可省略，pageSize上限50）、`POST /merchant/activities`（稳定Idempotency-Key）、`POST /merchant/activities/{id}/retry-publication` 和 `/offline`。创建只接受门店、标题/副标题/规则、字符串分金额、正整数库存及四个带偏移整秒时间，服务端做逐门店当前锁定授权；同键不同规范内容409。发布闭闸准备→提交ACTIVE/审计/OPEN→独立事件开闸；下架同步关闸成功才提交并返回。缺键不重建、OFFLINE不重新上架，无reconcile路由。

真实服务测试：先停API/worker，在 `backend/` 用 `GO_INTEGRATION=1 go test -count=1 -v ./internal/activity ./internal/outbox ./internal/activitypub`；真实消费测试只运行在无其他待认领事件的隔离库，包间用命名锁串行claim。之后启动API和Vite，根目录 `node scripts/go-activity-ui-smoke.mjs`，除已有UI环境外还需显式REDIS_HOST/REDIS_PORT；`BROWSER_TIMEZONE=UTC`或`Asia/Shanghai`。脚本真实提交后丢弃响应验证原键重试，按精确ID先清持久事件/业务，再删Redis键并确认0、注销session。不要对共享/正式库运行；大ID自增水位不回拨。phase/hash旧数据兼容与未执行项见[GO-021证据](evidence/2026-10-26-bb5d985/GO-021/report.md)。

## Go 服务启动（当前）

```sh
# 隔离环境：不要与 Java 基线共用 3307/6379
docker compose -f compose.go.yaml up -d mysql redis
$env:MYSQL_HOST='127.0.0.1'; $env:MYSQL_PORT='3308'
$env:MYSQL_DATABASE='project_full_stack_go'
$env:MYSQL_USER='fullstack'; $env:MYSQL_PASSWORD='fullstack_go_dev_only'
$env:REDIS_HOST='127.0.0.1'; $env:REDIS_PORT='6380'
node scripts/migrate.mjs

cd backend && go run ./cmd/api     # 默认 127.0.0.1:8082
npm --prefix frontend run dev       # 前端代理到 Go API
```

社区事件投递及消费投影需要 Kafka 时：`docker compose -f compose.go.yaml --profile kafka up -d kafka`，并设置 `KAFKA_BROKERS=127.0.0.1:9092`。先运行版本迁移（含009—011），再启API；两个消费者使用稳定的 `community.follow-v1` / `community.feed-v1` group，不与交易Stream共用协议。空broker配置同时停用社区relay和这两个消费者。

关注关系公开计数和Feed目录完整时使用投影，响应中的 `source=projection` 表示相应读模型参与；本人状态、内容及可见范围仍由DB事实决定。不完整、历史无事件或可选查询故障时 `source=database`，不会把缺失投影当作空数据。

在 `backend/` 用 `go run ./cmd/community-projector stats follow-v1` 查看收据/停车状态；`replay <projection> <topic> <partition> <offset>` 精确重放原失败记录；`rebuild follow-v1` 或 `rebuild feed-v1` 原子重建该环境的整张派生模型。命令不修改事实或Kafkaoffset，重放失败保持PARKED。命令与限制详见 [GO-072恢复说明](evidence/2026-10-28-8c3fb0e/GO-072/recovery-report.md)。

## 公开笔记搜索（GO-076）

搜索为可选能力。先停止本环境的 API/消费者，在仓库根目录启动并等待依赖健康，运行全部版本迁移（含012）；下面沿用 Go 默认端口，若已有其它实例，须整套改用独立 project/端口，不能共用 worker 数据。

```powershell
docker compose -f compose.go.yaml --profile kafka --profile search up -d --wait mysql redis kafka elasticsearch
# 先显式导出上节完整 MYSQL_*/REDIS_*，Go 不自动加载 .env
node scripts/migrate.mjs
$env:KAFKA_BROKERS='127.0.0.1:9092'
$env:ES_URL='http://127.0.0.1:9202'
$env:ES_INDEX='community-notes-v1'
$env:ES_TIMEOUT_SECONDS='3'
Push-Location backend
$env:GO_SEARCH_REBUILD_OFFLINE='1'
go run ./cmd/note-search rebuild
Remove-Item Env:GO_SEARCH_REBUILD_OFFLINE
go run ./cmd/note-search stats
go run ./cmd/api
Pop-Location
```

随后另开终端启动前端。主导航“搜索笔记”允许匿名访问；`GET /notes/search?q=咖啡&shopId=1&sort=recent&limit=10` 按标题/正文查询，`shopId` 可省略，排序可用 `relevance` 或 `recent`，limit为1—20。ID始终为字符串，同发布时间的ID按关键字字典序排序。分页只使用响应的 `nextCursor`，游标绑定原查询和一分钟有效期；进程重启或分页失效需从第一页重新搜索。MySQL复核当前公开状态/版本可能使页变短或为空；只要仍有游标即可继续，没有精确总数。

ES固定8.19.23，标题/正文使用内置CJK；不需要安装插件。`ES_URL` 为空或配置非法时仅关闭搜索，搜索请求503；实际ES不可达也不阻断核心健康检查、交易或普通商铺浏览。启用后新笔记经独立 `community.search-v1` 消费组同步；索引尚未重建时不能靠旧收据视为初始化成功。ES故障可能先形成持久PARKED记录再提交Kafka位置，lag=0不代表搜索已追平，必须同时检查恢复记录和文档版本。

恢复在 `backend/` 显式加载相同连接环境后执行 `go run ./cmd/note-search replay <topic> <partition> <offset>`，只重放原停车消息，不改内容或重置offset。全量重建须先停API/消费者并显式设置 `GO_SEARCH_REBUILD_OFFLINE=1`；命令创建新代次、完整写入/refresh后切换alias，保留旧代次/收据/失败历史。ES与SQL不是一个事务；切换alias后SQL提交失败需继续保持离线并重跑rebuild，使二者收敛。不宣传零停机重建。012只新增派生表，代码回退不自动撤销迁移或删除数据。

容器内Kafka只读CLI使用 `--bootstrap-server localhost:9094`；宿主机API仍使用 `127.0.0.1:${GO_KAFKA_PORT}`（默认9092）。9094是容器内部监听器，不映射宿主机，避免独立端口下CLI跟随错误的advertised地址。

真实自测（先停同环境API/消费者）：在 `backend/` 设置 `GO_INTEGRATION=1`，执行 `go test -count=1 -timeout 3m -v -run 'TestIntegration(PublicNoteSearch|SearchSync)$' ./internal/notesearch`。页面与物理停机脚本需要专用隔离环境，具体环境/清理门槛、两时区50项与物理停机17项原始记录见 [GO-076交接](evidence/2026-10-28-290cd60/GO-076/report.md)。本票自测不等于独立核验，最终HEAD的远端CI/Linux race与持续负载另验。

## Java 基线环境与启动（历史参考）

Java 源码已随基线冻结归档到 `archive/backend-java-20261026/`，下面的命令只用于查看或复现历史基线，不再是产品启动路径。

- JDK 21 是编译目标；兼容的较新 JDK 也可以通过 `--release 21` 构建。
- Node.js 24；前端依赖以 `frontend/package-lock.json` 为准。
- Maven 3.6.3+；Docker Compose v2。

先运行 `docker compose up -d mysql redis`，等 `docker compose ps` 显示两个服务 healthy 后，再运行 `mvn -f archive/backend-java-20261026/pom.xml spring-boot:run`。

本机原有 MySQL 已占用 3306；在启动 Compose 和后端的各个 PowerShell 窗口中都先执行 `$env:MYSQL_PORT = '3307'`，使项目容器映射与后端连接保持一致。仓库默认值仍是 3306；这个环境变量是本机端口覆盖，不会更改原有 MySQL 服务。

首次初始化将读取 `infra/mysql/001-schema.sql` 和 `002-demo-data.sql`。已有数据库卷不会再次执行这些脚本；新增表结构通过 `infra/mysql/migrations/` 下的版本化 SQL 和 `scripts/migrate.mjs` 显式应用。迁移脚本会在 `tb_schema_migration` 记录版本与校验和，重复执行会跳过已应用版本。

```sh
$env:MYSQL_PORT = '3307'
node scripts/migrate.mjs
```

默认数据库名为 `project_full_stack`，用户 `fullstack`，密码为 `.env.example` 中的本地开发值。

AI 助手默认关闭；关闭时交易不受影响。配置 `AI_ENABLED=true` 和 `AI_PROVIDER=stub` 可用确定性组件验证知识发布、检索、工具和 SSE；配置 `AI_PROVIDER=openai-compatible` 与 `AI_API_KEY/AI_BASE_URL/AI_CHAT_MODEL/AI_EMBEDDING_MODEL/AI_EMBEDDING_DIMENSIONS` 才调用真实模型。Key 只放在后端环境，不写入前端或仓库。后端使用 `MYSQL_HOST/PORT/DATABASE/USER/PASSWORD`、`REDIS_HOST/PORT/PASSWORD/DATABASE` 环境变量。根目录 `.env` 仅由 Compose 自动读取，后端进程不会自动读取它。

GO-075已接入真实兼容Provider。配置示例以`backend/.env.example`为准；百炼本轮实测`qwen3.8-flash`/`text-embedding-v4`、1536维、`AI_DISABLE_THINKING=true`、`AI_MIN_SCORE=0.5`，只发送合成公开FAQ。`AI_MAX_OUTPUT_TOKENS`限制单次输出，`AI_TOP_K/AI_MIN_SCORE`作用于公开检索与问答；`AI_CHAT_TIMEOUT_SECONDS`是Provider上限，仍受知识HTTP10秒、问答HTTP45秒及回合总期限约束。上游完整缓冲后，校验身份和知识，再发送单次answer；不声称上游token直传。AI配置缺失/错误不阻止核心API启动，远端失败在请求时返回明确失败；`ready`表示本地配置已就绪，`providerHealth=unchecked`不表示远端健康或质量已验收。

向量持久身份由兼容endpoint、Embedding模型和维度决定，不包含Key或Chat配置。切换向量空间后旧索引明确不可用，须在商家知识页显式重建，不会自动付费重建整库。重启复用持久向量，查询embedding另计；显式重建仍调用Provider。只读公开知识详情与编辑草稿不依赖模型可用。真实调用费用、取消后的未知结算和有限集范围见[GO-075证据](evidence/2026-10-28-fb37af5/GO-075/report.md)。

运行已有`node scripts/go-flow-demo.mjs all`或`ai`之前，在API进程显式设置`AI_ENABLED=true`、`AI_PROVIDER=stub`；默认关闭时这些AI演示应返回不可用。真实模型评测需同时显式开启`GO_INTEGRATION=1`及`GO_REAL_AI=1`，普通CI不调用付费Provider。复跑固定集与网络取消分别见[评测计划](evidence/2026-10-28-fb37af5/GO-075/eval-plan.md)、[生命周期计划](evidence/2026-10-28-fb37af5/GO-075/lifecycle-plan.md)，每次使用新的证据文件，Key从私有进程环境加载。

核心交易隔离冒烟：在仓库根目录提供专用MySQL/Redis环境、预编译API路径`GO_AI_ISOLATION_BINARY`和新的空输出目录`GO_AI_ISOLATION_EVIDENCE`后，执行`python scripts/go-ai-isolation-smoke.py`。该脚本以AI关闭、缺Key、不可达配置分别启动API，并运行实际发布→成单→支付→核销→审计；不调用付费模型，也不代表持续AI失败并发负载。正常关闭API并保留各轮原始结果，交易演示结束时退出精确的自有登录session。

应用默认使用 `dev`：返回模拟验证码并从演示数据库预热 Redis 活动库存与 GEO。预热使用 SETNX，不覆盖已有预约库存。部署时显式设置 `SPRING_PROFILES_ACTIVE=prod`、自己的连接参数和服务地址；真实短信服务尚未接入。

Docker 未运行时，可以进行后端单元测试和前端构建，真实 API 联调需要 MySQL、Redis 就绪。

## 关键接口

接口直接由后端提供，前端开发服务器将 `/api` 前缀去掉后代理到后端。

| 方法 | 后端路径 | 说明 |
| --- | --- | --- |
| POST | `/user/code?phone=...` | dev 下返回 `{code, mode:"development"}` |
| POST | `/user/login` | JSON `{phone,code}`，返回 Token |
| POST | `/user/logout` | 撤销当前 Token |
| GET | `/shop/of/name?current=1` | 分页店铺列表 |
| GET | `/shop/{id}` | 店铺详情 |
| GET | `/voucher/list/{shopId}` | 门店活动 |
| POST | `/voucher-order/seckill/{id}` | 受理抢购，返回字符串订单 ID；同用户重试返回原 ID |
| GET | `/voucher-order/{id}` | 本人订单详情，含 `orderStatus`、金额、期限；已支付单返回核销 token |
| GET | `/voucher-order?current=1` | 本人订单分页 |
| POST | `/voucher-order/{id}/pay` | 本人模拟支付；幂等 |
| POST | `/voucher-order/{id}/cancel` | 本人取消未支付单；取消后可重购 |
| GET | `/merchant/workspace` | 当前用户商家角色与授权门店 |
| POST | `/merchant/activities` | 店主发布活动，需 `Idempotency-Key` |
| GET | `/merchant/activities` | 授权范围活动分页与准备状态 |
| POST | `/merchant/activities/{id}/retry-publication` | 重试 Redis 准备/开闸 |
| POST | `/merchant/activities/{id}/offline` | 同步关闸并下架 |
| GET | `/merchant/orders` | 授权门店订单分页，不返回核销 token |
| POST | `/merchant/orders/redeem` | 店员/店主凭 `{shopId, token}` 核销 |
| POST | `/merchant/orders/{id}/refund` | 店主/管理者模拟退款 |
| GET | `/merchant/audits` | 店主/管理者查看操作审计 |
| GET | `/ai/capabilities` | AI 开关、provider 模式与就绪原因，不返回 Key |
| POST | `/ai/conversations` | 新建本人会话 |
| GET/DELETE | `/ai/conversations/{id}` | 本人历史/删除；他人 404 |
| POST | `/ai/conversations/{id}/messages` | 携带 authorization 的 POST，返回 SSE 阶段事件 |
| GET | `/ai/knowledge/{id}?version=` | 当前已发布公开来源 |
| GET/POST | `/merchant/knowledge?shopId=` | 门店公开 FAQ 列表/新建草稿 |
| PUT/POST | `/merchant/knowledge/{id}` 及 publish/disable/rebuild | 店主/管理者维护公开知识 |
| GET | `/actuator/health` | 健康状态 |

统一响应为 `{success,errorMsg?,data?,total?}`。`authorization` 请求头携带登录 Token；业务失败应读取 `success`，不能只判断 HTTP 200。订单 `state` 取 PROCESSING、CREATED 或 FAILED，业务订单状态另由 `orderStatus` 表示；数据库物理列仍为 `status`。

## Java 基线自动化验证（历史参考）

```sh
mvn -B -ntp -f backend/pom.xml verify
npm --prefix frontend ci
npm --prefix frontend run build
```

测试不依赖外部数据库或 Redis：Mockito 验证订单/缓存/鉴权路径，H2 验证商家和门店的真实查询范围。它们不验证 Redis Lua 在真实服务上的行为或 MySQL 的全部特性。

开发服务运行后：

```sh
python scripts/smoke.py
```

此脚本只适用于 dev，使用本地模拟验证码，创建一个随机演示用户和一笔订单，并检查店主/店员范围以及跨用户查单限制。固定演示账号每次取码需间隔 60 秒。

## 负载实验

安装 k6 后，在服务运行时执行：

```sh
k6 run scripts/load/shops.js
```

可用 `BASE_URL`、`VUS`、`DURATION` 和 `SLO_P95_MS` 调整实验。默认场景是小规模热点店铺查询，仅用于建立测量流程；脚本阈值是实验目标，不是已达成指标。

抢购负载需下一阶段准备多账号登录态、独立活动与初始库存，区分“多个用户争抢”和“一个用户重复请求”。实验后检查订单唯一性与最终库存，不能只看请求数。

每份报告至少记录：提交号、CPU/内存、JDK/Redis/MySQL 版本、数据量、并发模型、持续时间、吞吐、P95/P99、错误率、最终订单/库存、积压和故障恢复结果。

## 当前可靠性边界

- Caffeine 只在本实例更新后失效，其他实例最多保留短 TTL 旧值；跨实例广播和读写竞态仍需要单独验证。
- Redis AOF everysec 不是零丢失保证。当前 Lua 使用单实例键设计，未适配 Redis Cluster 哈希槽。
- 订单消费者在 5 次失败后保留死信证据；未知事务结果保留 PROCESSING，自动对账/人工重放页面待实现。
- 模拟登录和演示数据用于本地开发；支付、核销、退款和经营报表尚未形成完整业务闭环。
