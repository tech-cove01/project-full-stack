param(
  [Parameter(Mandatory=$true)][string]$EnvironmentFile,
  [Parameter(Mandatory=$true)][ValidateSet('hot','multi')][string]$Mode,
  [Parameter(Mandatory=$true)][ValidateRange(1,20000)][int]$Rate,
  [Parameter(Mandatory=$true)][ValidateRange(1,600)][int]$Seconds,
  [Parameter(Mandatory=$true)][ValidatePattern('^[a-z0-9][a-z0-9-]{1,60}$')][string]$Tag,
  [ValidateRange(1,60)][int]$WarmSeconds=10,
  [ValidateRange(1,4096)][int]$PreVUs=64,
  [ValidateRange(1,4096)][int]$MaxVUs=512
)
$ErrorActionPreference='Stop'
$settings=Get-Content -LiteralPath $EnvironmentFile -Raw -Encoding utf8 | ConvertFrom-Json -AsHashtable
foreach($entry in $settings.GetEnumerator()) {
  [Environment]::SetEnvironmentVariable($entry.Key,[string]$entry.Value,'Process')
}
if($env:MYSQL_PORT -ne '3451' -or $env:REDIS_PORT -ne '6551' -or $env:ORDER_STREAM_WORKERS -ne '1') { throw 'dedicated read environment required' }
$api=Get-Content -LiteralPath (Join-Path $PSScriptRoot 'api.json') -Raw -Encoding utf8 | ConvertFrom-Json
$stage=Join-Path $PSScriptRoot $Tag
$env:CAP_READ_MODE=$Mode
$env:CAP_READ_RATE=[string]$Rate
$env:CAP_READ_SECONDS=[string]$Seconds
$env:CAP_READ_WARM_SECONDS=[string]$WarmSeconds
$env:CAP_READ_WARM_RATE=[string][Math]::Min(25,$Rate)
$env:CAP_READ_PRE_VUS=[string]$PreVUs
$env:CAP_READ_MAX_VUS=[string]$MaxVUs
$env:CAP_READ_EVIDENCE="$stage-load"
$control="$stage-control"
$env:CAP_STOP_FILE=Join-Path $control 'stop.json'
$commandPath="$stage-command.json"
if(Test-Path -LiteralPath $commandPath) { throw 'new stage tag required; preserve previous evidence' }
@{at=(Get-Date).ToUniversalTime().ToString('o');mode=$Mode;rate=$Rate;seconds=$Seconds;warmSeconds=$WarmSeconds;warmRate=[Math]::Min(25,$Rate);preVUs=$PreVUs;maxVUs=$MaxVUs;apiPid=$api.pid;sourceSha=$settings.CAP_SOURCE_SHA;workingDirectory=(Get-Location).Path;command='python -X utf8 scripts/capacity-stage.py --mode read --control-dir <stage>-control --api-pid <api.pid> --diagnostics http://127.0.0.1:8351/debug/capacity --interval 5 --max-seconds <seconds+warm+120> --stop-grace-seconds 90 -- node scripts/go073-cap-read.mjs run';secretsExported=$false} | ConvertTo-Json | Set-Content -LiteralPath $commandPath -Encoding utf8
& python -X utf8 scripts/capacity-stage.py --mode read --control-dir $control --api-pid $api.pid --diagnostics http://127.0.0.1:8351/debug/capacity --interval 5 --max-seconds ($Seconds+$WarmSeconds+120) --stop-grace-seconds 90 -- node scripts/go073-cap-read.mjs run
exit $LASTEXITCODE
