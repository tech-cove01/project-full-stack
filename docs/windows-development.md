# Windows 本地开发

本项目可以在 Windows 上开发：Java、Node.js 和 k6 在 Windows 原生运行，MySQL 8.4 与 Redis 7.4 由 Docker Desktop 的 Linux 容器提供。日常开发不需要维护 VMware / VirtualBox 里的手工 Linux 虚拟机。

推荐 Docker Desktop 使用 WSL 2 后端。WSL 2 仍使用轻量虚拟化和 Linux 内核，只是无需自行维护一台完整的开发虚拟机；无需为了本项目额外安装 Ubuntu 发行版。安装条件和后端设置以 [Docker Windows 文档](https://docs.docker.com/desktop/setup/install/windows-install/)及 [WSL 2 后端文档](https://docs.docker.com/desktop/features/wsl/)为准。

交易改造见[交易与一致性设计](transaction-design.md)，交付条件见[验收清单](acceptance.md)。本文只说明本地开发方式，不代表对应业务已通过验收。

## 2026-10-26 社区增补环境（待实现）

最新范围见 [后端亮点方案](backend-highlights-plan.md)。仍在 Windows 原生运行 Java/Node/k6；新增 Kafka 使用 Docker KRaft 单节点，建议独立 `community` profile，默认关闭社区时原交易与 AI 可启动。本文没有表示 Kafka/Canal 已安装或可用，实现 agent 不需要先整理旧虚拟机。

实现批次需提供：锁定 Kafka 镜像与 Boot 管理的客户端兼容验证、持久卷、健康检查、topic/consumer group 初始化、精确启动/停止命令和新环境复现。宿主 Java 访问用 `127.0.0.1:9092`，容器间访问用独立内部 listener（例如 `kafka:29092`）；正确设置 advertised listeners，避免首次连接成功、取到容器地址后失败。启动前检查端口和资源，不占用无关项目。

Canal 仅 H6 可选，单独 profile；必须实测所选版本与现有 MySQL 8.4 的 binlog/认证兼容、持久位点与恢复。不能为 CDC 删除或重建原 MySQL 数据卷。Kafka/Canal 的依赖版本、端口和真实健康检查结果由实现 agent 完成后追加，不能把下方 2026-10-24 记录当当前在线状态。

## 当前环境记录

以下为 2026-10-24 的只读检查，未安装工具、拉取镜像、启动或修改服务。

| 项目 | 当前结果 | 含义 |
| --- | --- | --- |
| Java / javac | 已安装，Java 24.0.1 | 项目编译目标为 Java 21；本机并非安装了 JDK 21。后续可统一到有安全更新的 JDK 21 LTS，当前无需因此阻塞开发 |
| Maven | 已安装，3.9.12 | 能调用并读取版本；不能据此认定真实服务已启动 |
| Node.js / npm | 已安装，24.15.0 / 11.12.1 | 满足前端声明的 Node.js `>=22.12.0`；安装依赖使用仓库锁文件 |
| Python | 已安装，3.13.0 | 可运行只依赖标准库的 HTTP 冒烟脚本 |
| Docker Desktop / Engine | 4.66.1 / 29.3.1；引擎只读访问成功 | context 为 `desktop-linux`，Server 为 Linux amd64，内核为 `5.15.167.4-microsoft-standard-WSL2` |
| Docker Compose | 已安装，5.1.0 | 使用 `docker compose` 命令；无需另装旧版 `docker-compose` |
| 本项目 MySQL / Redis 容器 | `docker compose ps` 无项目容器 | 待启动，尚未验证镜像是否已缓存及初始化结果 |
| Windows 原有 MySQL | `mysqld` 正在监听 3306 | 保留该服务，本项目 MySQL 建议改映射到 3307 |
| 3307 / 6379 / 8081 / 5173 | 检查时未发现监听 | 只是当时状态，启动前仍需复查 |
| WSL 状态命令 | 受限执行上下文返回 `E_ACCESSDENIED` | 属于读取限制；Docker Server 的 WSL 2 内核信息已经读取成功，不能据此断言 WSL 未安装或损坏 |
| k6 | 已安装 2.2.0（`C:\Program Files\k6\k6.exe`） | 新开终端若仍找不到命令，使用完整路径或重开 PowerShell |
| 真实服务联调 / 性能 / 故障恢复 | 尚未验收 | 健康检查、冒烟和压测需按下文执行并记录结果 |

依赖版本分别由 `backend/pom.xml`、`frontend/package-lock.json` 和 `compose.yaml` 管理。MySQL `8.4`、Redis `7.4-alpine` 是版本系列标签，重拉可能获得更新补丁；压测报告应记录实际镜像版本或 digest。

## 启动前检查

先打开 Docker Desktop，确认使用 Linux containers。以下命令在 PowerShell 中执行；路径有空格，使用 `Set-Location -LiteralPath`。换电脑后将路径改为实际克隆位置。

```powershell
Set-Location -LiteralPath 'D:\桌面\The ALL\agent\project\code\project-full-stack'
java -version
mvn -version
node --version
npm.cmd --version
python --version
docker version
docker compose version
wsl --version
wsl -l -v
Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue |
  Where-Object { $_.LocalPort -in @(3306, 3307, 6379, 8081, 5173) } |
  Select-Object LocalAddress, LocalPort, OwningProcess
```

`docker version` 应同时返回 Client 和 Server。若提示权限拒绝，先在用户自己的 PowerShell 或 Docker Desktop 检查访问；只有 Client 信息时不能视为引擎可用。不要为了排查本项目停止原有 MySQL 或无关容器。

以下启动步骤使用仓库内的本地开发默认账号。根目录 `.env` 只会由 Compose 自动读取，Spring Boot 不会自动读取它。若已有 `.env` 改过数据库名、用户名、密码等参数，需要将相同参数显式设置给后端；不要直接用示例文件覆盖已有 `.env`。

## 终端 1：数据库与缓存

当前 3306 已被原有 MySQL 占用，因此将本项目容器的宿主端口设为 3307，容器内部仍为 3306。环境变量只影响当前 PowerShell 及其启动的子进程，优先于 Compose `.env` 同名配置。

```powershell
Set-Location -LiteralPath 'D:\桌面\The ALL\agent\project\code\project-full-stack'
$env:MYSQL_PORT = '3307'
docker compose config --services
docker compose config --images
docker compose up -d mysql redis
docker compose ps
docker compose logs --tail 100 mysql redis
```

等待 MySQL 和 Redis 都显示 `healthy`。本地连接为 MySQL `127.0.0.1:3307`、Redis `127.0.0.1:6379`。如果 3307 或 6379 已被其他程序占用，应选空闲端口，并在后端终端设置同样的 `MYSQL_PORT` / `REDIS_PORT`；不要终止无关服务。

首次创建空数据库卷时会执行 `infra/mysql/001-schema.sql` 和 `002-demo-data.sql`。已有卷不会重新执行初始化脚本；表结构变化需备份后显式应用迁移。不要用 `docker compose down -v` 修复初始化或迁移问题，这会删除项目数据卷。

本项目 Redis 已配置 AOF `everysec`，这不等于零数据丢失。故障后的库存和订单一致性仍需按验收清单独立验证。

## 终端 2：后端

打开第二个 PowerShell，重新设置数据库端口。只在终端 1 设置环境变量，不能传到这个新终端。

```powershell
Set-Location -LiteralPath 'D:\桌面\The ALL\agent\project\code\project-full-stack'
$env:MYSQL_PORT = '3307'
$env:SPRING_PROFILES_ACTIVE = 'dev'
mvn -f .\backend\pom.xml spring-boot:run
```

后端默认监听 `127.0.0.1:8081`。保留此窗口查看日志；启动失败时检查 MySQL / Redis 是否健康、连接参数是否与 Compose 一致。`dev` 模式返回模拟验证码，并初始化演示活动的 Redis 数据，只适合本地开发。

## 终端 3：前端

打开第三个 PowerShell。使用 `npm.cmd`，可避免 Windows 对 `npm.ps1` 的脚本执行策略限制。

```powershell
Set-Location -LiteralPath 'D:\桌面\The ALL\agent\project\code\project-full-stack'
npm.cmd --prefix .\frontend ci
npm.cmd --prefix .\frontend run dev -- --port 5173 --strictPort
```

浏览器打开 <http://127.0.0.1:5173>。前端的 `/api` 请求会去掉前缀并代理到后端 `http://127.0.0.1:8081`。`--strictPort` 使端口占用时直接报错，避免误以为仍运行在 5173。依赖未变化时可跳过后续启动中的 `npm ci`。

## 健康检查与真实冒烟

容器后台运行后，可回终端 1 执行：

```powershell
Invoke-RestMethod -Uri 'http://127.0.0.1:8081/actuator/health'
python .\scripts\smoke.py --base-url 'http://127.0.0.1:8081'
```

健康接口应返回 `status: UP`，冒烟脚本成功时打印 `PASS`。健康检查只说明依赖连接等探测正常，不代替业务验收。

冒烟脚本会真实写入本地开发数据：登录固定店主 `13800000000`、店员 `13900000000`，再创建随机演示用户、抢购活动 `1001` 并消费一份库存。它检查门店范围、订单异步生成、跨用户查单限制和匿名访问限制。固定账号取码限流为 60 秒，重复运行应至少间隔 60 秒；库存不足、活动过期或数据被修改时，应检查测试数据，不能通过删除卷绕过问题。

不依赖真实 MySQL / Redis 的构建验证可单独执行：

```powershell
mvn -B -ntp -f .\backend\pom.xml verify
npm.cmd --prefix .\frontend run build
```

构建通过与真实 API 冒烟通过是两项独立证据，均需记录；当前文档没有宣称已执行上述启动与验收命令。

## Windows k6 负载实验

选择 Windows 原生 k6 即可，无需再装 JMeter 或整套监控平台。按 [k6 官方安装说明](https://grafana.com/docs/k6/latest/set-up/install-k6/)安装当前稳定版：

```powershell
winget install k6 --source winget
```

安装后重新打开 PowerShell，让 PATH 生效，再在真实服务就绪时运行：

```powershell
Set-Location -LiteralPath 'D:\桌面\The ALL\agent\project\code\project-full-stack'
k6 version
k6 run -e BASE_URL=http://127.0.0.1:8081 -e VUS=20 -e DURATION=30s .\scripts\load\shops.js
```

现有脚本只测热点店铺详情读取，默认 20 个虚拟用户、30 秒，输出吞吐、失败率、平均耗时、P95 / P99 等。脚本中的 P95 小于 500ms、失败率低于 1% 等是待验证目标，不是性能成绩。抢购并发、重复消息、重启恢复和最终库存检查按[验收清单](acceptance.md)增加独立场景。

若改用 Docker 运行 k6，容器中的 `127.0.0.1` 指向容器自身，应使用 `BASE_URL=http://host.docker.internal:8081`，并验证宿主端口可达；后端默认只监听 `127.0.0.1`，不能未经验证就假设容器一定可访问，必要时需在受控本地环境调整后端监听地址。当前推荐的 Windows 原生方式无需调整监听配置。[Docker 容器访问宿主说明](https://docs.docker.com/desktop/features/networking/networking-how-tos/)

本机同时运行应用与压测器会竞争 CPU、内存和 I/O。结果用于开发对比和发现瓶颈，不能直接当作生产容量。每次报告记录代码提交、硬件、实际软件版本、数据量、负载模型、耗时、吞吐、P95 / P99、业务错误、订单与库存结果。Linux 正式部署与另一台机器发压属于后续独立验收。

## AI 环境增补（P1-D 待实现）

业务 AI 方案见 [ai-design.md](ai-design.md)。首版通过后端调用远端 Chat/Embedding API，在应用内做小规模检索，知识与向量保存于项目 MySQL，会话使用现有 Redis。无需为了该方案整理旧虚拟机、安装 GPU 工具链、下载 Ollama 模型或启动 ES/Milvus。

实现后默认 `AI_ENABLED=false`，未配置模型也能启动交易。启用时在启动后端的 PowerShell 中设置设计文档规定的服务地址、模型、向量维度和 API Key；前端不持有提供商密钥。地址与 Key 按实际提供商及地域匹配，不把旧课程的 endpoint 或免费额度描述当作保证。

本节是目标环境说明，当前尚未安装 AI 依赖或验证模型调用；真实提供商与索引恢复按 [AI 验收](ai-acceptance.md) 单独记录。现有其他项目的向量数据库容器不作为默认依赖。

## 查看日志与停止

前后端分别在各自窗口按 `Ctrl+C` 停止。容器日志与停止操作在终端 1 执行：

```powershell
Set-Location -LiteralPath 'D:\桌面\The ALL\agent\project\code\project-full-stack'
$env:MYSQL_PORT = '3307'
docker compose logs --tail 100 mysql redis
docker compose stop mysql redis
docker compose ps -a
```

`stop` 保留容器和数据卷。下次继续使用同一项目目录、同一组连接参数执行 `docker compose up -d mysql redis`。本文命令仅操作本项目的两个服务，不清理 Docker 全局资源，也不更改原有 MySQL、虚拟机或无关容器。
