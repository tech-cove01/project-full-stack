# 本机演示与录屏指南

按功能冻结版本 `0f185a558acf70ffcf60d05882d66db5a6b946db` 的页面、初始化数据和运行入口校对。目标是约 8 分钟讲清“商家发布 → 顾客消费 → 门店履约 → 内容与业务助手”。本文是操作与讲解材料，本次没有启动录屏环境或生成视频；产品核验和量化结论见 [STATUS](../STATUS.md)。源码讲解见 [调用链路线](source-walkthrough.md)。

## 录制前准备

使用新的本机 Compose 项目和四个数据卷，保留录制中产生的数据。以下示例项目名为 `project-full-stack-recording`，端口为 MySQL 3440、Redis 6540、Kafka 9140、ES 9240、API 8240、Vue 5240。运行前检查端口和项目是否已被使用；若已有同名项目，确认归属后继续原场景，或为新录制整套换名换端口。

```powershell
# 仓库根目录：只查看状态
git rev-parse HEAD
git status --short
docker compose -p project-full-stack-recording -f compose.go.yaml --profile kafka --profile search ps -a
docker volume ls --filter label=com.docker.compose.project=project-full-stack-recording
Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue |
  Where-Object { $_.LocalPort -in 3440,6540,9140,9240,8240,5240 } |
  Select-Object LocalAddress,LocalPort,OwningProcess
```

终端 A：在仓库根目录设置环境、准备依赖，最后启动 API。Go 程序不自动读取 `.env`；这些是本机合成数据环境的开发配置。

```powershell
$env:GO_MYSQL_PORT='3440'
$env:GO_REDIS_PORT='6540'
$env:GO_KAFKA_PORT='9140'
$env:GO_ES_PORT='9240'
$env:GO_ENV='development'
$env:MYSQL_HOST='127.0.0.1'
$env:MYSQL_PORT='3440'
$env:MYSQL_DATABASE='project_full_stack_go'
$env:MYSQL_USER='fullstack'
$env:MYSQL_PASSWORD='fullstack_go_dev_only'
$env:REDIS_HOST='127.0.0.1'
$env:REDIS_PORT='6540'
$env:REDIS_DATABASE='0'
$env:REDIS_PASSWORD=''
$env:API_ADDR='127.0.0.1:8240'
$env:KAFKA_BROKERS='127.0.0.1:9140'
$env:KAFKA_TOPIC='community.events'
$env:ES_URL='http://127.0.0.1:9240'
$env:ES_INDEX='recording-notes-v1'
$env:AI_ENABLED='true'
$env:AI_PROVIDER='stub'
$env:AI_API_KEY=''

docker compose -p project-full-stack-recording -f compose.go.yaml --profile kafka --profile search up -d --wait mysql redis kafka elasticsearch
if ($LASTEXITCODE -ne 0) { throw '隔离依赖未就绪' }
node scripts/migrate.mjs
if ($LASTEXITCODE -ne 0) { throw '版本迁移失败' }
Push-Location backend
try {
  $env:GO_SEARCH_REBUILD_OFFLINE='1'
  go run ./cmd/note-search rebuild
  if ($LASTEXITCODE -ne 0) { throw '离线索引初始化失败' }
} finally {
  Remove-Item Env:GO_SEARCH_REBUILD_OFFLINE -ErrorAction SilentlyContinue
  Pop-Location
}
# 完成离线重建后才启动唯一的 API/消费者
Push-Location backend
try { go run ./cmd/api } finally { Pop-Location }
```

需要本机已安装的 Go、Node、Docker Compose 和 MySQL 客户端；迁移客户端路径可用 `MYSQL_BIN` 指定。首次空卷由 Compose 执行 `001-schema.sql`、`002-demo-data.sql`，迁移再执行 003–012。上述离线重建仅在这个环境的 API/消费者尚未启动时运行；已启动的环境应先停 API，再显式重建。详见 [运行说明](development.md#公开笔记搜索go-076)。

终端 B：仓库根目录启动前端，代理明确指向录制 API。

```powershell
$env:API_PROXY_TARGET='http://127.0.0.1:8240'
npm.cmd --prefix frontend ci
if ($LASTEXITCODE -ne 0) { throw '前端依赖安装失败' }
npm.cmd --prefix frontend run dev -- --port 5240 --strictPort
```

访问 `http://127.0.0.1:5240/api/health/ready`，确认 `service=project-full-stack-go`、`runtime=go`、`status=UP`，再打开首页。`go run` 未注入版本时 health 的 version 为 `unknown`，录制记录应保存实际 Git SHA。health 仅证明 MySQL/Redis 就绪，搜索还需发布笔记后实际查到。

## 角色与数据

首次空卷中的角色来自 [演示数据](../infra/mysql/002-demo-data.sql)，仅用于本地演示，登录页会自动填入开发验证码。

| 浏览器会话 | 合成账号 | 角色与入口 |
| --- | --- | --- |
| 店主窗口 | `13800000000` | 商家 1 的 OWNER，管理街角咖啡（1）与青禾食堂（2）；`/merchant` |
| 店员窗口 | `13900000000` | 商家 1 的 STAFF，仅授权街角咖啡（1）；`/merchant` |
| 顾客窗口 | `13600000000` | 全新环境首次登录自动创建普通用户；`/`、`/orders`、`/assistant` |

使用三个不同浏览器配置文件，或不同浏览器隔离登录；同一个配置文件的多个标签、同一浏览器的多个无痕窗口都会共享各自会话，不能据此隔离三个角色。演示仅展示这些实际角色，不把未演示的 MANAGER 或撤权竞争说成这次录屏已验证。

准备下面两组门店 1 活动参数，标题加本次录制后缀。录前在店主窗口 `/merchant/activities` 创建 B，顾客购买并模拟支付后保留未核销状态；A 在录制中现场新建发布：

| 字段 | 活动 A：到店核销 | 活动 B：未核销退款 |
| --- | --- | --- |
| 购买价 / 面值 | 19.00 / 30.00 元 | 9.90 / 20.00 元 |
| 总库存 | 5 | 5 |
| 销售与核销开始 | 当天北京时间，早于当前时间约 5 分钟 | 同左 |
| 销售结束 | 当前时间后 1 天 | 同左 |
| 核销结束 | 销售结束后 1 天 | 同左 |

每次发布都等待新活动显示“已发布”，并在顾客门店页确认可购买。原始优惠券 1001/1002 经迁移可能显示 ACTIVE，但没有本次发布建立的 Redis gate，不能用它们代替录制用的新活动。发布后的价格、库存和起止时间不能直接修改；重录需要新的合法活动。准备完 B 后让顾客退出登录，录制时再展示开发验证码登录；距最后一次获取该号码验证码至少间隔 60 秒，避免触发发码节流。

店主在 `/merchant/knowledge` 为门店 1 建立并发布公开规则：标题“录屏核销与退款规则”，正文“核销之后不可退款，核销之前可以联系门店处理。未支付订单可以取消；退款后不可再次购买同一活动。”确认已发布版本。AI 使用 stub 时页面明确显示演示模式，问答展示的是确定性协议与真实只读业务数据；真实模型质量引用 [GO-075/074 的已有证据](../STATUS.md)，本次无需重新付费调用。

## 约 8 分钟录屏顺序

| 时间 | 操作与可观察结果 | 建议讲解 |
| --- | --- | --- |
| 0:00–0:40 | 首页，顾客 `/login` 获取本地开发验证码并登录，搜索/分类进入 `/shops/1` | “用户浏览、优惠消费和门店运营由同一个 Go 后端贯通，验证码为本机开发模式。” |
| 0:40–1:30 | 店主展示 `/merchant` 的两店范围，再到活动管理现场发布 A；等已发布 | “只有当前管理身份可发布，异步开闸完成后才能购买。” |
| 1:30–2:30 | 顾客刷新门店页并购买 A；若显示“处理中”则等待，直到待支付，点击“模拟支付” | “接口先返回受理 ID，异步任务完成数据库事务后才成为真实订单。快速消费时可能直接看到待支付；支付是本机模拟。” |
| 2:30–3:20 | 顾客复制凭证；店员工作台只显示门店 1，再到 `/merchant/orders` 选该店核销 | “核销在事务中检查当前门店授权、订单状态和凭证；店员发布和退款权限受限。” |
| 3:20–4:00 | 店主退款录前已支付但未核销的 B，再看订单状态与审计 | “退款与核销使用不同订单演示；已核销订单不能再退款。业务变更和审计同事务写入。” |
| 4:00–5:20 | 顾客 `/notes/new` 发布门店 1 笔记，标题带唯一词；店主打开笔记点赞并关注作者，再看 `/feed`；`/notes/search` 查唯一词 | “关注动态和搜索有各自投影，MySQL 仍是事实源。搜索要等异步处理与 refresh 可见后再展示。” |
| 5:20–6:40 | 顾客 `/assistant` 选择门店 1；问“核销之后可以退款吗？”并点来源；问“我的订单”，点订单卡片 | “公开规则带版本和可点击来源；订单卡片由当前身份查询。当前录屏使用页面标明的 stub。” |
| 6:40–7:20 | 助手问“活动 `<本轮活动A的ID>`”，查看库存并点门店入口；切回订单与商家审计收束 | “工具只有读取能力。库存是查询时已提交的快照，购买仍以准入结果为准。” |
| 7:20–8:00 | 展示 [独立核验汇总](reviews/2026-10-29/go073-399705a/report.md) 的指定负载与恢复范围 | “5 次/秒、180 秒的稳定批次 901/901 成单；缓存与事件恢复另有真实依赖证据。这里不推断极限容量。” |

订单/审计都在 `/merchant/orders` 中操作，没有独立 `/merchant/audits` 页面。活动工具需要明确数字 ID；仅问“当前活动库存”不会命中该工具。核销凭证是有效操作凭据，录屏时只展示必要片段，复制和粘贴过程可切镜头；不要打开含授权头的 Network 面板或本地配置。FAQ 按已发布正文和版本讲，不把助手文字当成实际交易授权。

## 现场卡住时

- 活动“待开放”：先刷新活动与授权；检查这次新活动的起止时间和发布状态，使用管理者现有“重试发布”入口，不手工写 Redis 库存。
- 订单仍处理中：保留当前订单 ID，稍后重新查询；不另起购买请求或直接改 SQL 把它变成成功。
- 搜索没查到：等待后从第一页重新查询唯一词，确认查询门店与笔记一致；不能把“已发布”直接讲成“ES 已同步”。
- AI 不可用或没来源：检查当前进程的 stub 配置、门店选择和 FAQ 发布版本；未命中就如实展示，不临时换成真实 Provider 或自动重建付费向量。

`scripts/go-flow-demo.mjs all` 和 UI smoke 都会清理自有夹具，不要用它们给录屏留数据，也不要在正在录制的库上运行核验脚本。无需在视频中重新演练物理停机或压测，引用已核验记录即可。

## 停止与重录

先在前端与 API 所在终端分别 Ctrl+C，确认端口释放；再在保留上述 Compose 端口环境的终端 A 执行：

```powershell
docker compose -p project-full-stack-recording -f compose.go.yaml --profile kafka --profile search stop --timeout 60 mysql redis kafka elasticsearch
docker compose -p project-full-stack-recording -f compose.go.yaml --profile kafka --profile search ps -a
```

检查退出状态，不把强制停止写成优雅退出。卷和录制数据保留；不运行 `down -v`、全局 prune、flush 或重置 Kafka 位点。重录时原订单状态不会自动重置：可复用环境并新建活动/笔记，或另建整套独立项目。保存录制日期、源码 SHA、服务/端口、所用模式和视频文件路径；不要把本地账号配置或完整操作凭据提交进仓库。
