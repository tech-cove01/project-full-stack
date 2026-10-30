# CAP07 独立核验代表档

核验者可只复跑最终 B 的 **multi100/s×180秒一档**，不必重跑本批全部探索或诊断。它是新 QA cohort 的代表档，不能补齐实现者本组 B1 失败，不能据一轮宣布重复容量档通过。实跑前先取得固定 read 项目/端口的明确移交；本文件没有授权停止未知服务。

`cap07-env.py` 的 ROOT/runtime 与 `cap07-stage.py` 的 slot 固定绑定本批，现已消费并完成清理；**不要直接重跑、删除slot、覆盖旧证据、改脚本放宽guard或把ROOT改成任意路径**。新 QA 使用既有通用 `go073-cap-read.mjs prepare/run/finish` 与 `capacity-stage.py`，在独立review目录、私有runtime及全新自有卷落证。`cap07-audit.py` 固定本批A夹具与ROOT，也不能拿来冒充新QA数据审计。

## 独立准备

先按[原QA交接的新自有卷规则](../2026-10-30-go073-cap/qa-handoff.md)和[本批runbook](runbook.md)核对：移交的两只完整64位CID、compose project/service、已停止、loopback端口全空、原挂载、旧卷和自有network/subnet。只复用该文的**fresh-volume与身份保护流程**，不要沿用旧API SHA、旧env/token、trade参数或旧代表档计划。

- project仍为 `project-full-stack-cap-read`，端口仍3451/6551/8251/8351。新随机volume override必须挂到 `/var/lib/mysql`、`/data`，旧新卷保留；只对明确移交且已停止的两容器force-recreate，启动后重新保存新CID/挂载/资源，不能仅换`-p`假装新卷。
- 新证据目录例如 `docs/reviews/<核验日期>/cap07-<随机nonce>`；私有runtime在所有Git仓库外，例如 sibling `.runtime-fullstack-cap07-qa-<nonce>`。二者必须不存在，Windows runtime核owner ACL。`TEMP/TMP`指向新runtime；本轮新observer随机账号/密码只在私有env，由自有MySQL socket root经stdin建立，只授本库与performance_schema SELECT。
- MySQL4CPU/2GiB、Redis2CPU/512MiB、API GOMAXPROCS4/GOMEMLIMIT1GiB、SQL池20/Redis池10、workers1、持久化、migration001–013保持；Kafka/ES/AI关闭，heavy Feed diagnostics=false。6400篇/32作者/32读者、正文256B、页20、multi640key不改，工具/harness脚本取最终交付工具SHA并记录hash。
- B固定 `30b6571227b5508d0f04bc8c4cfc1bcfbc313275`。独立clean checkout构建，exe置于checkout外；保存源码/构建前后git状态、Go版本、构建参数、`go version -m`及exe SHA256。revision为`capacity-<完整SHA>`，VCS匹配且modified=false。新QA的 **API来源与新fixture来源都为B30b**，不能复制本批A来源manifest、改旧manifest或声明为原A夹具。

以下关键命令从最终工具checkout根执行，先将占位路径替换成核验者自己新建的对象。私有env已按上述条件创建，不使用本批已经删除的env文件；不打印整份配置或进程环境。

```powershell
$qaRepo = (Get-Location).Path
$qaEvidence = '<新review证据目录绝对路径>'
$qaRuntime = '<本轮新私有runtime绝对路径>'
$qaPrivateEnv = '<该runtime内的新env.private.json>'
$qaBinary = '<已验真固定B的exe绝对路径>'
$qaBSource = '30b6571227b5508d0f04bc8c4cfc1bcfbc313275'
if ([IO.Path]::GetDirectoryName((Get-Item -LiteralPath $qaPrivateEnv).FullName) -ne (Get-Item -LiteralPath $qaRuntime).FullName) {
  throw 'private env must belong to this new QA runtime'
}
$qaConfig = Get-Content -LiteralPath $qaPrivateEnv -Raw | ConvertFrom-Json -AsHashtable
if ($qaConfig.ContainsKey('CAP_READ_FIXTURE')) { throw 'new QA env must not reuse a prior fixture' }
foreach ($entry in $qaConfig.GetEnumerator()) {
  [Environment]::SetEnvironmentVariable([string]$entry.Key, [string]$entry.Value, 'Process')
}
$env:CAP_SOURCE_SHA = $qaBSource
$env:CAP_READ_EXPECTED_REVISION = "capacity-$qaBSource"
$env:CAP_READ_FIXTURE_SOURCE_SHA = $qaBSource
$env:GO_FEED_DIAGNOSTICS = 'false'
$env:TEMP = $qaRuntime
$env:TMP = $qaRuntime
$qaApi = Start-Process -FilePath $qaBinary -WorkingDirectory $qaRepo -WindowStyle Hidden -PassThru `
  -RedirectStandardOutput (Join-Path $qaEvidence 'api.stdout.txt') `
  -RedirectStandardError (Join-Path $qaEvidence 'api.stderr.txt')
```

启动依赖前核四端口全空；启动API前核8251/8351无监听，3451/6551则必须为身份已验且健康的新自有依赖。启动后保存API exe/hash/PID/UTC创建时刻及精确health version，确认diagnostics PID等于新API、cards.coalescing=true。凭据不进入命令参数/日志。P_S required consumers/instrument必须真实可读，缺失不补0；`prepare/run`仍有固定项目/端口/健康检查。

## 新夹具与单代表档

```powershell
$env:CAP_READ_FIXTURE_DIR = Join-Path $qaEvidence 'fixture' # 必须不存在
node scripts/go073-cap-read.mjs prepare
if ($LASTEXITCODE -ne 0) { throw 'preserve partial new fixture and private recovery input' }
$env:CAP_READ_FIXTURE = Join-Path $env:CAP_READ_FIXTURE_DIR 'fixture.json'
# 保存新manifest字节SHA256，核READY/32 readers/6400 notes/sourceSha=B/revision=capacity-B。
$qaStage = Join-Path $qaEvidence 'b-multi100-180'
$qaControl = "$qaStage-control"
foreach ($path in @("$qaStage-load", $qaControl, "$qaStage-capacity-before.json", "$qaStage-capacity-after.json")) {
  if (Test-Path -LiteralPath $path) { throw 'preserve prior attempt; this stage must be new' }
}
$env:CAP_READ_EVIDENCE = "$qaStage-load"
$env:CAP_STOP_FILE = Join-Path $qaControl 'stop.json'
$env:CAP_READ_MODE = 'multi'
$env:CAP_READ_RATE = '100'
$env:CAP_READ_SECONDS = '180'
$env:CAP_READ_WARM_SECONDS = '10'
$env:CAP_READ_WARM_RATE = '25'
$env:CAP_READ_PRE_VUS = '256'
$env:CAP_READ_MAX_VUS = '512'
$qaBefore = Invoke-RestMethod 'http://127.0.0.1:8351/debug/capacity'
if ($qaBefore.pid -ne $qaApi.Id -or !$qaBefore.cards.coalescing) { throw 'API identity/cards mismatch' }
$qaBefore | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath "$qaStage-capacity-before.json" -Encoding utf8
python -B -X utf8 scripts/capacity-stage.py --mode read --control-dir $qaControl `
  --api-pid $qaApi.Id --diagnostics 'http://127.0.0.1:8351/debug/capacity' `
  --interval 5 --max-seconds 360 --stop-grace-seconds 90 `
  -- node scripts/go073-cap-read.mjs run
$qaSupervisorExit = $LASTEXITCODE
$qaAfter = Invoke-RestMethod 'http://127.0.0.1:8351/debug/capacity'
if ($qaAfter.pid -ne $qaApi.Id) { throw 'preserve missing/mismatched final counters' }
$qaAfter | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath "$qaStage-capacity-after.json" -Encoding utf8
```

上述review/fixture/stage/load/control/快照路径必须是新路径；只运行一次，不在同tag或换tag盲重试。只有一个监督/负载任务，期间不并行编译、Go测试或额外探针。资源预算、请求deadline、判据保持原值；达到预算即停，保留全部早停、unknown、错误和采集失败。wrapper exit与`*-load/result.json.meetsBudget`分开读取；核计划180秒分母、native HTTP/终态/成功/unknown/drop、全/成功原生P95/P99及采样覆盖，不合并分位。

before/after cards单调counter差的范围是**预热前至wrapper收尾后，包含预热、稳定负载与后台工作**，不能标成稳态精确loader量；gauge只列前后/采样最大观察值，不作次数或保证峰值。独立列content_sql_batches/rows/bytes、flight与错误/拒绝；bytes为DB title/content/原images的UTF8字节。原native访问快照窗的Com_select/Com_stmt_execute分别报告、不相加、不替代loader，不据热内容零回源称Feed零DB。若最终snapshot失败，保留缺失并限制结论，不能伪造0。

## 精确收尾与核验交付

确认wrapper和本轮node/k6/sampler均已收敛后，API与依赖保持可用：

```powershell
node scripts/go073-cap-read.mjs finish
if ($LASTEXITCODE -ne 0) { throw 'preserve private recovery files before stopping API/dependencies' }
```

沿[runbook收尾顺序](runbook.md)核本轮新manifest的32次正常logout、私有输入移除及Redis session EXISTS0（仅私有内存读取本轮token，不放redis-cli命令参数或公开输出）；经自有容器socket root删除本轮observer并核0。再按保存的exe/hash/PID/UTC创建身份停止新API，Windows terminate如实记，不称Go优雅退出；按**本轮启动后保存并再次核实的新完整CID**停止两依赖，核exited/exit0/OOMfalse/挂载未变及所有旧新卷仍在。不能调用固定本批ROOT的cap07-env finish清理QA，也不按名称全杀、down-v/prune/flush/删SQL事实。失败保留progress与私有恢复位置，不先清凭据。

QA报告分别给最终工具SHA、固定B构建hash/metadata、新fixture来源/manifest SHA、环境/背景负载差异、代表档全部原生判据和loader范围、停止/清理证据；本轮新QA结果与实现者A/B、内部审查、CI分别记账。它可验证代表档正确性与当前观测，不把一次通过或不同宿主数字写成已证明性能提升，也不覆盖本批B1失败及旧长尾未知风险。
