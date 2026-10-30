# CAP-06 实际命令入口

脚本已准备，本文编写时未由harness agent启动依赖/API/负载；执行结果由根agent另落证。所有命令在仓库根执行，明确路径不得照抄占位值。新脚本只属于本轮，旧CAP-03的固定PID/cohort收尾脚本不能套用。

## 1. 新卷依赖与观察账号

先重新核实既有read项目两容器已移交、Exited、固定3451/6551/8251/8351无监听，读取完整64位CID与现有owned网络CIDR；不从名字自动推所有权。`prepare-deps`会再二次核ID/标签/端口、使用全新随机卷保留旧卷和网络、正常版本迁移、经容器socket root建立仅SELECT的随机observer；runtime owner ACL和私有配置在Git外。

```powershell
python -X utf8 scripts/cap06-env.py prepare-deps `
  --subnet '<本次核实的现有read网段，例如10.253.51.0/24>' `
  --previous-mysql-id '<明确已移交的完整64位mysql容器ID>' `
  --previous-redis-id '<明确已移交的完整64位redis容器ID>' `
  --mysql-bin 'D:/MySQL/Install/bin/mysql.exe'
```

终端只输出私有配置**路径**、新卷名和已准备状态。把输出的`privateEnvironmentFile`路径保存为`$cap06Private`，不打印文件内容。失败保存preparation/compose/migration记录及私有文件，不能用同目录盲覆盖重跑。此步骤不启动API或发业务负载。

## 2. 固定干净API与新夹具

观测代码提交后从完整SHA建立干净worktree构建，传`-trimpath -ldflags '-X main.revision=capacity-<完整SHA>'`；保存实际Go build info和SHA。`start-api`拒绝`vcs.modified=true`，核新容器ID/端口，隐藏启动并保存元数据/日志，再核精确health/diagnostic PID。

```powershell
$cap06Private='<prepare-deps输出的Git外env.private.json绝对路径>'
$cap06Source='<实际干净API的完整40位SHA>'
$cap06Binary='<该干净构建的cap-api.exe绝对路径>'
python -X utf8 scripts/cap06-env.py start-api --environment $cap06Private `
  --binary $cap06Binary --source-sha $cap06Source --label baseline --tail
```

输出新的`env-baseline.private.json`路径。当前开启`GO_FEED_DIAGNOSTICS=true`，默认关闭的API可另选新label省略`--tail`；同一API切换必须先核身份正常停止，不并行复用端口。启动后用下面命令一次准备新6400篇/32作者/32真实登录读者，旧SQL/会话不复用。

```powershell
$cap06Private='<start-api输出的env-baseline.private.json绝对路径>'
$cap06Api='docs/evidence/2026-10-30-cap06/api-baseline.json'
python -X utf8 scripts/cap06-env.py prepare-fixture --environment $cap06Private --api-metadata $cap06Api
```

输出`env-baseline-fixture.private.json`，后续stage使用这份配置。公开manifest只保存来源/期望页/ID等合成事实，token仅在私有tmp输入。未来换修复API时仍用这份含fixture的配置作`start-api`输入，新env自动保留原fixture source，不能重写原manifest。

## 3. 短探针及最多三轮诊断

```powershell
$cap06Private='<prepare-fixture输出的env-baseline-fixture.private.json绝对路径>'
python -X utf8 scripts/cap06-stage.py --environment $cap06Private --api-metadata $cap06Api `
  --kind probe --tag baseline-probe --mode multi --rate 1 --seconds 5

# 根核探针分类/资源/取消及无凭据输出后再启动；每次换未用tag。
python -X utf8 scripts/cap06-stage.py --environment $cap06Private --api-metadata $cap06Api `
  --kind diagnostic --tag baseline-diag-1 --mode multi --rate 100 --seconds 180
```

diagnostic严格multi100/180，最多三个exclusive slot，不因失败删slot或换目录绕限制。每轮原harness输出`<tag>-load`、资源/停止监督`<tag>-control`、额外触发采证`<tag>-diagnostic`。读取capacity结果、native summary、监督/资源及collector manifest分别判断，诊断轮不计正式成绩。约1秒额外轮询与profile有开销；collector任何poll失败退出非0，原partial证据保留，不变成0失败。

触发后`goroutine-N.pprof`可在负载结束后用同实际API二进制执行`go tool pprof -top`，不要同时编译/profile重活干扰正式load。保留触发前后time series、observer fetch起止/时延和app时刻，不以sampler row.at代替所有观测真实时点。

## 4. 修复后正式组与换API

定位证据支持的修复完成并直接回归/提交后，使用新干净构建。在无active本轮stable k6时精确停止旧API；不停止或改依赖/夹具。`stop-api`只认记录的exe/PID/UTC创建时间/hash，不按名字杀进程。

```powershell
python -X utf8 scripts/cap06-env.py stop-api --api-metadata $cap06Api
python -X utf8 scripts/cap06-env.py start-api --environment $cap06Private `
  --binary '<新干净API绝对路径>' --source-sha '<新完整40位SHA>' --label fixed --tail
```

用新输出私有env和`api-fixed.json`运行`--kind formal`，此时只有原5秒安全sampler、无额外collector/重型profile。同组multi100/180两轮加hot200/180至少一轮，tag分别唯一。正式代码与轻量观测保持一致，原TTL/自然过期不改；一轮失败则该组失败，不挑下一轮凑组。

```powershell
$cap06Private='<start-api fixed输出的新env-fixed.private.json>'
$cap06Api='docs/evidence/2026-10-30-cap06/api-fixed.json'
python -X utf8 scripts/cap06-stage.py --environment $cap06Private --api-metadata $cap06Api `
  --kind formal --tag fixed-multi-a --mode multi --rate 100 --seconds 180
```

## 5. 精确收尾

先确认全部本轮load/supervisor结束，再用**当前仍运行**的API元数据与其fixture env执行finish。它先核新容器不可变ID、API exe/PID/UTC/hash/health，拒绝本票记录的stable k6仍存活；在任何变更前落progress。正常HTTP logout32并精确Redis EXISTS核0、删除私有输入、删除本轮observer、再次核API身份终止、按确切CID stop两容器并核exit0/OOMfalse及卷保留，最后只删已记账的精确私有env文件，不递归删除runtime/事实/卷。

```powershell
python -X utf8 scripts/cap06-env.py finish --environment $cap06Private --api-metadata $cap06Api
```

Windows terminate如实记录，不称Go信号优雅关闭。中途失败保存complete=false和已完成步骤，先分析该progress，再选择明确恢复步骤，不盲重跑或提前删凭据。其它服务/网络/旧卷不处理。

静态验证：三脚本AST及`--help`、纯trigger计数边界、diff空白检查已通过。非作者审读由cap06_review完成，prepare先获无阻断；collector缺样/窗口与cleanup独立会话验证建议已落实。这些静态结果不是新的环境/容量成绩。
