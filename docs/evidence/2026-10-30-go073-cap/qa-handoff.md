# GO-073-CAP 外部独立QA复跑

**实现者自测及收尾完成，外部QA按根 agent 明确移交的环境复跑。** 本文不授权停止任何未知API/容器。QA需全新自有卷，代表稳定/相邻受限档及正确性即可，不要求重跑全部准备失败、profile或探索；结果与实现者自测、内部复算和CI分别记账。

## 固定源码与环境

- 工具checkout为根 agent 最终交付完整SHA，先记录git状态；交易历史对照API为 `3db296dd0beedd6f85d06cf5479d707b0498ce7e`；新读API为 `767c2fc8bdf1be08c5636143a71898eec25081a9`、SHA256 `97a5165e48a74c98ee521eebda01f79d049c44938239e2bd4ed89fe38e25e20c`，见[干净构建](cap03/build-767c2fc/build.json)。分别核干净构建和实际hash，读不能再默认用3db。可验证现成固定产物，需重建时才另开干净worktree，`-X main.revision`与该模式实际SHA匹配；不借用hash或把当前脏工作树称为固定API。helper从最终工具checkout构建，不能误用旧测量器。
- MySQL4CPU/2GiB、Redis2CPU/512MiB，API GOMAXPROCS4/GOMEMLIMIT1GiB/SQL池20；真实持久化、鉴权、事务、审计/outbox保留，AI/Kafka/ES关闭。记录新宿主、镜像、背景负载与数据量；不同条件不硬套原机数字。
- **负载器分模式：trade主对照/持有使用GOMAXPROCS/GOMEMLIMIT未设；read沿用对应原阶段的4/1GiB。** API启动后只在trade shell清这两项，不能清read。保存supervisor的generator_runtime_environment佐证。

| 模式 | 工具固定Compose项目 | MySQL/Redis/API/诊断 |
| --- | --- | --- |
| trade | project-full-stack-cap-trade | 3450/6550/8250/8350；k6控制8260 |
| read | project-full-stack-cap-read | 3451/6551/8251/8351 |

## 新自有卷：不能只换-p

优先独立机器/Docker上下文使用相同固定项目和端口，创建全新卷。若同宿主，先取得移交，确认相关监听全部关闭；已有两个容器必须核project/service标签、已停止和旧卷身份，不明归属就退出，不终止未知进程。脚本硬编码这些值，不能换成`-p qa`或删护栏。

同宿主可保持固定项目，使用显式新volume名覆盖，旧卷不删。只有确认已移交的已停止mysql/redis才可用下列force-recreate替换；若原容器身份也要保存，改用独立Docker上下文，不重命名绕标签。下例在交付仓库根执行，先设`$qaMode`为trade或read：

```powershell
$qaRepo=(Get-Location).Path
$qaNonce=[guid]::NewGuid().ToString('N').Substring(0,12)
$qaMode='trade'
$qaProject="project-full-stack-cap-$qaMode"
$qaRuntime=Join-Path (Split-Path $qaRepo -Parent) ".runtime-fullstack-cap-qa-$qaNonce"
$qaEvidence=Join-Path $qaRepo "docs/evidence/2026-10-30-go073-cap/qa-$qaNonce"
New-Item -ItemType Directory -Path $qaRuntime,$qaEvidence -ErrorAction Stop | Out-Null
$env:TEMP=$qaRuntime; $env:TMP=$qaRuntime
$qaMysqlVolume="$qaProject-qa-$qaNonce-mysql"
$qaRedisVolume="$qaProject-qa-$qaNonce-redis"
$qaOverride=Join-Path $qaRuntime 'fresh-volumes.yaml'
@"
volumes:
  go-mysql-data:
    name: $qaMysqlVolume
  go-redis-data:
    name: $qaRedisVolume
"@ | Set-Content -LiteralPath $qaOverride -Encoding utf8
$env:GO_MYSQL_PORT=if($qaMode -eq 'trade'){'3450'}else{'3451'}
$env:GO_REDIS_PORT=if($qaMode -eq 'trade'){'6550'}else{'6551'}
$qaCompose=@('-p',$qaProject,'-f','compose.go.yaml','-f','scripts/load/compose.capacity.yaml')
if($qaMode -eq 'read') {
  if(!$env:CAP_READ_SUBNET){throw 'set an inspected non-overlapping private subnet'}
  $qaCompose+=@('-f','scripts/load/compose.capacity-read.yaml')
}
$qaCompose+=@('-f',$qaOverride)
docker compose @qaCompose config --quiet
if($LASTEXITCODE -ne 0){throw 'configuration failed'}
# 上述归属/已停止/无监听核对、两个新卷名不存在之后才执行。
docker compose @qaCompose up -d --wait --force-recreate mysql redis
if($LASTEXITCODE -ne 0){throw 'preserve startup failure; no benchmark'}
```

启动前inspect两个随机新卷名，任何已存在即换新nonce；read还须实查Docker IPAM和host非default路由，不盲复制旧CIDR。启动后核容器labels/不可变ID/固定loopback端口及挂载：`/var/lib/mysql`、`/data`必须是本轮新卷名，而非默认旧卷；记录旧卷仍保留。只保存选定字段，不dump Config.Env。禁止down -v/prune/flush或删旧数据/他人网络。

runtime须在所有Git仓库外，核Windows目录ACL，不把0600当独占证明；凭据仅合成且私有。证据放本轮`$qaEvidence`，因为settle还固定要求此CAP证据根；不要指向实现者旧stage。

## 启动与最少复跑

只沿用[读runbook](read-runbook.md)的环境、API隐藏启动/health/PID校验及prepare/run/finish入口；**不执行其中默认compose up/stop，始终使用本文件含fresh-volume override的$qaCompose**。将其`$readParent`赋值替换为`$readParent=$qaEvidence`，所有fixture/stage均取本轮新目录，不用原cap03路径；API启动后设`$qaApi=$readApi`供下方PID参数。trade环境改为上表端口、ORDER_STREAM_WORKERS4，read固定1。MYSQL/Redis显式指向新卷环境，记录API exe/PID/create_time与精确health版本，不能只按名称认领。

若只核最终能力，正常迁移至013即可。若要独立复现索引收益，先把001–012原SQL逐字节复制到runtime专用目录，MIGRATIONS_DIR指向它正常迁移，收齐before后再恢复完整迁移目录正常应用013并重跑应skip；不先应用再删索引/账本伪造before。数据库统计、Go SQL、池和数据不同时变化；新建API worktree与测量工具工作树分开。

read同fixture换API时，CAP_SOURCE_SHA设当前新API、CAP_READ_EXPECTED_REVISION设精确capacity-SHA，另设CAP_READ_FIXTURE_SOURCE_SHA为原manifest.sourceSha；保留原expectedRevision和全部manifest字节。run/finish须落双source/revision/manifest hash。fresh prepare则两source必须相同，不能修改旧manifest来通过新版本校验。

read观察账号需只读本库和performance_schema。可在窗口外用自有容器socket root创建随机用户名/hex密码，通过stdin传CREATE USER/GRANT SELECT；容器命令为 `sh -c 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql -uroot --default-character-set=utf8mb4'`，让它读取容器自身环境而非展开root密码。将SQL和错误仅保留私有目录，不公开含密码的语句；加载CAP_READ_OBSERVER_USER/PASSWORD后实测SHOW与required instrument/consumer，缺列不补0。

- **trade：** 以最终[helper说明](../../../backend/cmd/capacity-trade/README.md)设置私有CAP_POOL_FILE、API_BASE/CAP_API_VERSION、新CAP_OUTPUT_DIR，窗口外prepare25000真实会话，Windows经capacity-trade-launch.py做新预检；然后代表30/s×180秒两轮、邻近35/s×45秒。需要核并行因果再补同3db的W1/W4各25/s×45秒，不必重跑所有旧档。默认30档预热10秒、pre20/max60VU；35档按原参数。每轮新活动、唯一pair、充足库存。
- **read：** prepare同6400篇/32作者/32读者/页20数据，以1/s×5秒核内容；代表hot200/s及multi100/s各180秒两轮，分别补最近已测受限的hot300/multi200名义180秒档，触预算即停。原四轮结果见[总报告](report.md)，不是要求不同宿主必须得到同数字。**旧3db的hot300两轮不能混入767c，也不将600/650短档当目标。** 需要核索引同档效果才补150/175或175/200的before/after。沿用每轮warm10秒/25/s、pre256/max512VU、timeout与read负载器4/1GiB；不能用VU数称吞吐。

所有阶段使用[capacity-stage.py](../../../scripts/capacity-stage.py)：trade入口为`python scripts/capacity-trade-launch.py --helper <本轮helper> run`，read为`node scripts/go073-cap-read.mjs run`。load/control必须为新sibling目录、父目录先存在；CAP_STOP_FILE精确为control/stop.json，不能提前建load。以下从最终checkout构建helper并演示trade持有，`$qaApi=$readApi`必须来自上节自己启动并验证的API：

```powershell
$qaHelper=Join-Path $qaRuntime 'capacity-trade.exe'
Push-Location (Join-Path $qaRepo 'backend')
try {
  go build -o $qaHelper ./cmd/capacity-trade
  if($LASTEXITCODE -ne 0){throw 'final-checkout helper build failed'}
} finally { Pop-Location }
$env:CAP_RATE='30'; $env:CAP_WARM_SECONDS='10'; $env:CAP_SECONDS='180'
$env:CAP_PRE_VUS='20'; $env:CAP_MAX_VUS='60'
$qaStage=Join-Path $qaEvidence 'trade-rate30-hold1'
$env:CAP_OUTPUT_DIR="$qaStage-load"
$qaControl="$qaStage-control"
$env:CAP_STOP_FILE=Join-Path $qaControl 'stop.json'
python scripts/capacity-stage.py --mode trade --control-dir $qaControl `
  --api-pid $qaApi.Id --diagnostics 'http://127.0.0.1:8350/debug/capacity' `
  --interval 5 --max-seconds 600 --stop-grace-seconds 150 `
  -- python scripts/capacity-trade-launch.py --helper $qaHelper run
```

read的对应入口/环境与stable-only PID监督直接使用runbook，按最终持有配置设置CAP_READ_*。正式负载不并行；逐档看结果再继续，达到固定预算就停，不为复现原数字强撑或事后放宽。

本轮cap03/run-stage.ps1和finish-and-cleanup.py固定原证据路径、PID/CID/runtime，**不要用于fresh QA**；使用上面的通用harness/finish入口，按自己保存的新身份清理。本轮internal-evidence-audit.py只离线复核已归档原cohort，允许从其它checkout读取公开证据；不能把其通过当QA新夹具已实跑。

离线复核原读档案可执行以下只读命令（`--check-only`不会覆盖已交审计JSON）；应得到54正常阶段、1不可计分、7130 checks、0失败/结构错误/待结束。它不请求API、不需要已撤销的旧凭据，也不产生fresh QA成绩。

```powershell
python -X utf8 docs/evidence/2026-10-30-go073-cap/cap03/internal-evidence-audit.py --check-only `
  --selected-api-source-sha 767c2fc8bdf1be08c5636143a71898eec25081a9 `
  --selected-hold hold-fix-hot-200-180-a --selected-hold hold-fix-hot-200-180-b `
  --selected-hold hold-fix-multi-100-180-a --selected-hold hold-fix-multi-100-180-b
```

## 必核结果及结束

监督exit0不是容量通过。分别核目标/实际发起、HTTP/终态/成功/unknown/dropped、全部与成功分位、原计划秒数、完整180秒及重复；旧Rate推已分类只适用于保存的旧插桩，unknown不能补0。CPU有效间隔≥1秒、首样raw保留，写样本首末/缺失及Go pool wait累计差范围。SHOW Com_select/Execute交叠不相加，SQL/Redis是全实例，不冒充缓存回源数。

trade额外核exporter/公开pool index/fixture/完整SQL ID-user-status/Redis受理/库存/review/拒绝/补偿/PEL/lag，区分预末SQL观察下界与最终同批持久化。停止回执缺口须查完整持久去向，不用缺日志判丢单；不明去向不能用accounted-stop豁免。旧cohort若会在下轮过15分钟，先在窗口外用本轮已对账stage经正常owner cancel并等outbox/review/Stream排空，不能直接改库存/时限或跳过等待。

结束时trade先新输出目录logout并精确删本轮私有pool；read在API/依赖仍可用时finish，核32会话及凭据清理。API仅按保存的exe/PID/create_time终止，Windows terminate如实记，不冒称优雅信号退出。仅停止自己已移交的两个容器，保留新旧卷。gzip只归档本轮已结束文件，核原字节/压缩字节/SHA与解压一致，失败和未知不改写。

QA报告给完整工具SHA、trade3db/read767c的各自干净API来源、013/新卷/资源条件、代表档实际分母与通过/受限/不可测、stock与持久去向、有效资源覆盖和收尾。不同机器先遇宿主/负载器限制就报共同部署观测边界，不能称理论服务端最大，也不把实现者内部复算当外部独立实跑。
