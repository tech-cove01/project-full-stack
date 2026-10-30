# CAP-03 读环境执行命令

以下为根 agent 顺序执行的准备命令，2026-10-30 本文件编写时尚未启动读容器、API、夹具或负载。必须等交易环境释放同一宿主资源，先核对独占端口与 Compose 标签。所有命令在仓库根运行，除明确的固定构建步骤外。原有环境和卷不删。

## 专用依赖、版本 SQL 和固定 API

```powershell
$repo = (Get-Location).Path
$env:GO_MYSQL_PORT='3451'
$env:GO_REDIS_PORT='6551'
$env:CAP_READ_SUBNET='10.253.51.0/24' # 本轮先核对Docker IPAM与host routes确认无重叠；复跑需重新检查
$env:MYSQL_HOST='127.0.0.1'
$env:MYSQL_PORT='3451'
$env:MYSQL_DATABASE='project_full_stack_go'
$env:MYSQL_USER='fullstack'
$env:MYSQL_PASSWORD='fullstack_go_dev_only' # compose.go.yaml 的本地合成环境账号
$env:REDIS_HOST='127.0.0.1'
$env:REDIS_PORT='6551'
$env:REDIS_DATABASE='0'
$env:REDIS_PASSWORD=''
$env:API_BASE='http://127.0.0.1:8251'
$env:GO_ENV='development'
$env:API_ADDR='127.0.0.1:8251'
$env:GO_DIAGNOSTICS_ADDR='127.0.0.1:8351'
$env:GOMAXPROCS='4'
$env:GOMEMLIMIT='1GiB'
$env:KAFKA_BROKERS=''
$env:ES_URL=''
$env:AI_ENABLED='false'

# 必须没有已有监听；若发现则先查所有者，不终止未知进程。
Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue |
  Where-Object LocalPort -In 3451,6551,8251,8351 |
  Select-Object LocalAddress,LocalPort,OwningProcess

# 第一次使用独占项目时确认该名称尚未被别人占用；复用自己的已建环境时查标签、端口和卷。
docker ps -a --filter label=com.docker.compose.project=project-full-stack-cap-read --format '{{.ID}} {{.Names}} {{.Status}} {{.Ports}}'
docker compose -p project-full-stack-cap-read -f compose.go.yaml -f scripts/load/compose.capacity.yaml -f scripts/load/compose.capacity-read.yaml up -d --wait mysql redis
node scripts/migrate.mjs
```

`MYSQL_BIN` 需要指向已安装的 mysql 客户端（或已在 PATH 中）；不要临时增装服务。依赖仅 MySQL/Redis，正常持久化保留。固定 API 沿用根 agent 已建立的干净源码 worktree 与二进制；若重建，记录该 worktree 完整 SHA、`git status --porcelain`、`go version -m <binary>` 及 SHA256，不能把当前有并行工具修改的仓库称为干净构建。

```powershell
# 将下面三个值填成根 agent 实际的固定构建，不用占位字符串执行。
$env:CAP_SOURCE_SHA='<干净构建完整40位SHA>'
$env:CAP_READ_EXPECTED_REVISION='<传给-X main.revision的精确值>'
$readApiBinary='<固定构建的API.exe绝对路径>'
$readParent=Join-Path $repo 'docs/evidence/2026-10-30-go073-cap/cap03'
New-Item -ItemType Directory -Path $readParent -Force | Out-Null

# 所有env在启动前已设置；Windows后台进程隐藏窗口并保存日志。
$readApi=Start-Process -FilePath $readApiBinary -WorkingDirectory $repo -WindowStyle Hidden -PassThru `
  -RedirectStandardOutput (Join-Path $readParent 'api.stdout.txt') `
  -RedirectStandardError (Join-Path $readParent 'api.stderr.txt')
Invoke-RestMethod "$env:API_BASE/health/ready"
Invoke-RestMethod 'http://127.0.0.1:8351/debug/capacity'
```

校验 health 的精确版本、service/status，诊断 PID 等于 `$readApi.Id`。根证据采集工具记录独占容器镜像/ID/CPU/内存、宿主/Docker 上限、API 连接池和二进制信息；不要将 `docker inspect` 全对象或进程环境输出到证据。MySQL 观察账号需显式配置 `CAP_READ_OBSERVER_USER`/`CAP_READ_OBSERVER_PASSWORD`，应使用能读 `performance_schema` 的专用只读账号或本地 root 观察账号；凭据只在进程环境中加载，不打印。P_S `statements_digest`、`events_statements_current` 与 SQL SELECT instrument 必须已启用；`run` 会验证并在失败时保留报告。

## 固定夹具与单档运行

```powershell
$env:CAP_READ_FIXTURE_DIR=Join-Path $readParent 'fixture-01' # 必须不存在
node scripts/go073-cap-read.mjs prepare
if ($LASTEXITCODE -ne 0) { throw 'read fixture failed; preserve manifest for finish' }
$env:CAP_READ_FIXTURE=Join-Path $env:CAP_READ_FIXTURE_DIR 'fixture.json'

# 观察凭据已从本地私有环境加载；不在命令/证据中展开其值。
if (!$env:CAP_READ_OBSERVER_USER -or !$env:CAP_READ_OBSERVER_PASSWORD) { throw 'load read-only P_S observer environment' }
$env:CAP_READ_MODE='hot' # multi 单独档位，数据与判据不变
$env:CAP_READ_RATE='25'
$env:CAP_READ_SECONDS='60'
$env:CAP_READ_WARM_SECONDS='10'
$env:CAP_READ_WARM_RATE='25'
$env:CAP_READ_PRE_VUS='64'
$env:CAP_READ_MAX_VUS='512'
$readStage=Join-Path $readParent 'hot-025-a' # 每次换新前缀
$env:CAP_READ_EVIDENCE="$readStage-load"
$readControl="$readStage-control"
$env:CAP_STOP_FILE=Join-Path $readControl 'stop.json'
python scripts/capacity-stage.py --mode read --control-dir $readControl `
  --api-pid $readApi.Id --diagnostics 'http://127.0.0.1:8351/debug/capacity' `
  --interval 5 --max-seconds 240 --stop-grace-seconds 90 `
  -- node scripts/go073-cap-read.mjs run
$readSupervisorExit=$LASTEXITCODE
Get-Content (Join-Path $readControl 'exit-status.json')
Get-Content (Join-Path $env:CAP_READ_EVIDENCE 'result.json')
```

控制目录和负载目录是新 sibling，supervisor 不提前建立负载目录。`CAP_STOP_FILE` 必须是 `<control-dir>/stop.json`。读预热与稳定期是两个 PID；supervisor 仅稳定期启动资源采样，预热仍保存 k6 summary。warm 检测到 stop 或阶段之间已有 stop 均拒绝启动 stable。

监督退出 0 只表示过程观测正常，不能代替 `result.json` 的 `meetsBudget`、正确性及访问增量复核。未见实际稳定 PID/完整资源样本、sampler 异常退出、超时或 stop 均保留失败。结果记录完整样本数和首末样本时间，需与实际负载窗口比对覆盖。资源采样在已验证的 k6 结束时写 done 停止，记录 `sampling_ended_after_generator_exit` 和时间；后续 helper 的排空/解析继续，不能将生成器消失误判为资源问题。资源 stop 由现有 harness 消费，监督器给它 90 秒保存/对账；仍不退出才只强制终止自己的 launcher 子树和已验证 k6，不动 API/容器/别人的进程。强制结束意味着本轮证据不完整。

先用 1 req/s、稳定 5 秒和预热 1 秒做内容/分母短探针，确认输出后再恢复上面的正式参数。正式两种模式分别 25→50→100→200→400，每档 30–60 秒；无资源/延迟/错误限制才提高，临近边界缩步。最高达标档延长 180–300 秒并复跑，增加 `--max-seconds` 覆盖总时长。每档读取结果并检查资源/访问增量后再启动下一档，禁止无条件循环压穿宿主。hot/multi 是不同 key 分布，不作为缓存优化提升的因果对照。

## 收尾与恢复

```powershell
# API 和专用依赖仍需可用；finish只注销manifest里的自身32会话并删其精确临时凭据文件。
node scripts/go073-cap-read.mjs finish
if ($LASTEXITCODE -ne 0) { throw 'preserve fixture credential location and retry finish before stopping API' }

# API退出由根 agent 按已记录的PID/创建时间确认后执行，勿按名称批量停止。
# 专用容器正常停止，保留卷、SQL夹具、原始失败与所有证据。
docker compose -p project-full-stack-cap-read -f compose.go.yaml -f scripts/load/compose.capacity.yaml -f scripts/load/compose.capacity-read.yaml stop -t 60 mysql redis
```

准备中途失败也应复用保留的 `fixture.json` 执行 `finish`，不能把未完成夹具当成功覆盖。SQL数据不清理，Redis不flush，卷不删除。若根固定构建/API已停，先按同一配置恢复自己的API再注销会话。

## 静态检查

已执行 `node --check scripts/go073-cap-read.mjs`、`node --check scripts/load/capacity-health.js`，以及 `python -c "import ast,pathlib; ast.parse(pathlib.Path('scripts/capacity-stage.py').read_text(encoding='utf-8'))"`、`python scripts/capacity-stage.py --help`，均退出 0。PowerShell Parser 静态解析本文件四个命令块，无语法错误；缺少 `CAP_READ_EVIDENCE`/`CAP_STOP_FILE` 的监督启动被 guard 以 exit 2 拒绝，确认没有创建控制目录或启动子 harness。`git diff --check` 通过。均无业务请求、SQL、容器或负载运行；这些结果不能当真实容量成绩。
