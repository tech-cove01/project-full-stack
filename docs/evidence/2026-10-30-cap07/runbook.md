# CAP07 固定环境操作

本文件的命令是后续可执行流程，编写时未启依赖/API/负载。先读 [plan](plan.md)，由根agent确认 CAP06V 真实回归完成、3452/6552停止并释放共享宿主资源。所有命令从 `project-full-stack` 根执行；这里只示路径/变量名，私有配置内容不输出。

## 只读交接后准备

先只读检查 `docs/evidence/2026-10-30-cap06/environment.json` 与其cleanup；再用 `docker inspect` 仅选择两只已移交容器的完整ID、project/service、state、loopback端口、volumes，并选取自有network的ID/labels/subnet。不要输出全部inspect的Config.Env。旧报告CID只作候选，当前完整CID必须重新核实；若不是已停止的同一自有容器，先停止操作。

```powershell
$previousMysqlId = '<本次重新核实的64位完整mysql CID>'
$previousRedisId = '<本次重新核实的64位完整redis CID>'
$retainedReadSubnet = '<已核实自有network的private /24 subnet>'
python -B -X utf8 scripts/cap07-env.py prepare-deps --previous-mysql-id $previousMysqlId --previous-redis-id $previousRedisId --subnet $retainedReadSubnet
```

此动作只force-recreate两只已停自有容器，创建CAP07随机新卷、private ACL runtime、migration001–013和只读observer，不启动API/k6。成功输出仅有新卷和private环境文件路径。将路径保存在 `$cap07Environment`；不要 `Get-Content` 或导出整份环境。中途失败先读公开preparation/migrate-exit/progress，不盲目再运行prepare，也不删private文件或旧卷。

## 固定构建、夹具与A/B切换

A固定为06V已证修复后且无cards的 `f3d99ee43dfb422a0cba525de93e9266f07c4ee5`；B必须是所有cards功能提交完成后的clean commit。构建在对应clean checkout完成，固定linker version为 `capacity-<完整SHA>`。应使用detached clean Git worktree保留Go VCS元数据；单纯git archive没有 `.git`，不能满足生命周期要求的 `vcs.revision`/`vcs.modified=false`。root记录 SHA、build命令与exe/hash，不以当前脏工作区假冒固定版本。按工具输出赋值，变量中的路径不含配置内容。

```powershell
$cap07Environment = '<prepare-deps输出的env.private.json绝对路径>'
$aSha = 'f3d99ee43dfb422a0cba525de93e9266f07c4ee5'
$bSha = '<最终B的40位完整SHA>'
$aBinary = '<A的独立不可变exe绝对路径>'
$bBinary = '<最终B的独立不可变exe绝对路径>'
python -B -X utf8 scripts/cap07-env.py start-api --environment $cap07Environment --binary $aBinary --source-sha $aSha --variant A --label a1
```

将start-api输出的private环境文件赋给 `$cap07Environment`。首次A启动后创建且仅创建一次新fixture：

```powershell
python -B -X utf8 scripts/cap07-env.py prepare-fixture --environment $cap07Environment --api-metadata docs/evidence/2026-10-30-cap07/api-a1.json
```

将fixture输出的private环境文件再次赋给 `$cap07Environment`。该私有env指向本轮新manifest与32个新token，manifest原始fixtureSHA保留。后续所有切换都把**最近一个带fixture的env**传给start-api，将其新输出更新同一变量，不复用CAP06 env或令牌。

```powershell
python -B -X utf8 scripts/cap07-stage.py --environment $cap07Environment --api-metadata docs/evidence/2026-10-30-cap07/api-a1.json --variant A --kind probe --tag a-probe --mode multi --rate 10 --seconds 10
python -B -X utf8 scripts/cap07-stage.py --environment $cap07Environment --api-metadata docs/evidence/2026-10-30-cap07/api-a1.json --variant A --kind comparison --tag a1-multi100 --mode multi --rate 100 --seconds 180
python -B -X utf8 scripts/cap07-env.py stop-api --api-metadata docs/evidence/2026-10-30-cap07/api-a1.json
python -B -X utf8 scripts/cap07-env.py start-api --environment $cap07Environment --binary $bBinary --source-sha $bSha --variant B --label b1
```

更新 `$cap07Environment` 后做B唯一短probe及B1 comparison；随后同样只停当前记录API，切换A标签a2做A2 comparison，再切换最终B标签b2做B2 comparison。每轮执行前确认宿主无其它本批负载。所有stage都写独占slot/command、before/after capacity、loader delta与exit；另保留原load/control目录。命令失败也读取原始result、资源stop和loader错误，不能仅凭wrapper退出码分类。

## B阶梯与持有

四轮对照完整尝试且B1/B2完整通过后，用当前b2 API：

```powershell
python -B -X utf8 scripts/cap07-stage.py --environment $cap07Environment --api-metadata docs/evidence/2026-10-30-cap07/api-b2.json --variant B --kind stair --tag b-stair150 --mode multi --rate 150 --seconds 45
```

检查结果后才手动选择下一项，顺序200/300/400、均45秒。首失败停止上探；只有容量SLO失败且有明确通过/失败相邻档时，最多一次 `--kind neighbor --rate <严格位于括区内> --seconds 45`。资源/监督停止不做邻近缩步。禁止另加500/1000/5000或新tag无限重试。

最高完整通过档若大于100，用 `--kind hold --rate <最高通过档> --seconds 180` 做两轮，分别唯一tag；若最高100，B1/B2已合并覆盖，不再补同档轮。最后用当前同SHA B：

```powershell
python -B -X utf8 scripts/cap07-stage.py --environment $cap07Environment --api-metadata docs/evidence/2026-10-30-cap07/api-b2.json --variant B --kind hot --tag b-hot200 --mode hot --rate 200 --seconds 180
```

stage私有exclusive lock阻止同runtime并发。若owner崩溃留下 `cap07-stage-active.private.json`，先核其PID/当前进程创建身份、已有k6/helper/原wrapper状态并保存恢复说明，确认所有自有负载已结束后才精确处理该文件；不能按进程名全杀或删slot。任何源码/环境修正导致需要新组时，先由根更新协议与证据安排，现有文件保留，不绕过guard。

## 正常收尾

保持当前API和依赖可用，确认全部stage已退出后：

```powershell
python -B -X utf8 scripts/cap07-env.py finish --environment $cap07Environment --api-metadata docs/evidence/2026-10-30-cap07/api-b2.json
```

检查cleanup-progress/cleanup：32正常logout、Redis EXISTS0、observer0、精确API身份停止、两精确CID exited/exit0/OOMfalse、挂载及旧新卷仍在。finish只删除登记的本轮env文件和正常logout删掉的本轮input，不prune/down-v/rm卷/清SQL事实。不将Windows terminate称为Go优雅退出。若中断保留private凭据和部分progress，按已完成步骤恢复，不能盲重跑或清空目录。
