# CAP-08 固定工具操作与恢复边界

先读[协议](plan.md)与根[STATUS](../../../STATUS.md)。本文件是可执行模板，编写时未启动依赖/API/负载，也未创建真实名额或budget；root明确选择的每一步才执行。所有命令从project-full-stack根执行，替换占位路径，私有配置只加载、不打印。

## 时间与非负载名额

预算起点按root保守决定固定，历史初始HEAD另在起点记录。只运行一次，不覆盖预算文件：

```powershell
python -B -X utf8 scripts/cap08-policy.py init-budget --started-at '2026-10-30T13:10:00+00:00' --source-sha 1de173517a54c3be46748779437b592f7bcdbc58
python -B -X utf8 scripts/cap08-policy.py budget
```

每个受控场景/修复先写公开prediction JSON，只含机制预测和证据路径，不含用户/token/key/SQL参数。`evidence`为仓库docs内实际存在文件；不允许私有env路径。第1controlled模板：

```json
{
  "task": "CAP-08",
  "kind": "controlled",
  "prediction": "暂停共享内容SQL时，cards.cold_sql的active/oldest增长；另一个batch仅在cards.slot_wait等待",
  "differentObservation": "若延迟主要在其他边界，目标stage不增长；采证缺失不能当排除",
  "evidence": ["docs/evidence/2026-10-30-cap08/failure-window.md"]
}
```

```powershell
python -B -X utf8 scripts/cap08-policy.py reserve --kind controlled --prediction '<本轮controlled prediction JSON>' --tag wiring-1
# 之后才由root运行已经审查的精确Go受控测试命令。
```

第2controlled还需`observedCandidate:true`和已出现证据，不依次注入所有猜测。修复reserve使用`--kind repair`、prediction.kind为repair；最多2次。相同确定性用例旧FAIL→新PASS回归保留原slot，不重新算新场景。失败、启动失败、采集失败不删slot，不换tag重置。

## 新依赖与API身份

只读核移交的两只完整64位CID、compose project/service、已停止、loopback端口、挂载、自有network ID/subnet；该project仍为`project-full-stack-cap-read`。依赖启动前四口3451/6551/8251/8351必须空；只对明确移交且已停的两容器force-recreate，新随机CAP08命名卷、旧新卷全部保留。不要输出全部Docker inspect的Config.Env。

```powershell
python -B -X utf8 scripts/cap08-env.py prepare-deps --previous-mysql-id '<64位已验mysql CID>' --previous-redis-id '<64位已验redis CID>' --subnet '<已验private /24>' --mysql-bin '<本地mysql可执行路径>'
```

prepare输出仅有private env路径和卷名；中途失败读公开preparation/progress/migrate-exit与私有恢复位置，不盲重跑或删凭据。将输出env路径赋给`$cap08Environment`。独立clean detached checkout构建诊断源码，link revision为`capacity-<完整SHA>`，exe放checkout外；保存构建前后状态、go version -m、VCS revision/modified=false、SHA256。不能从主工作区未提交代码假装clean。

```powershell
$cap08Environment = '<本轮private env绝对路径>'
$cap08Source = '<本次clean完整应用SHA>'
$cap08Binary = '<已验诊断exe绝对路径>'
python -B -X utf8 scripts/cap08-env.py start-api --environment $cap08Environment --binary $cap08Binary --source-sha $cap08Source --label diag1 --phase diagnostic --tail
```

API启动前仅8251/8351无监听，3451/6551应为身份已验且健康的新自有依赖。`--phase diagnostic`必须显式`--tail`；formal必须不带tail。新cohort ROOT、runtime、observer、phase metadata及active lock与06/07互拒。每次start后把其输出env更新`$cap08Environment`；首次API创建新6400/32合成夹具：

```powershell
python -B -X utf8 scripts/cap08-env.py prepare-fixture --environment $cap08Environment --api-metadata docs/evidence/2026-10-30-cap08/api-diag1.json
```

再次更新为fixture输出的新env，后续API切换必须传最近包含fixture的env。夹具原始sourceSha与API当前sourceSha分别保存，不能篡改旧manifest或复用已撤销token。工具记录实际bytes hash及当前工具Git SHA，不把未提交脚本假装已冻结。

## 受控collector与诊断

Go受控测试仅在root显式设置`CAP08_CONTROL_EVIDENCE`时调用下面的collector；测试自身创建并持有真实SQL/队列barrier、loopback临时端点。collector首轮立即采样，命中100ms年龄或全池等待正增后抓一次，成功或失败捕获后即返回，不人为等满2s：

```powershell
python -B -X utf8 scripts/cap08-collector.py --url 'http://127.0.0.1:<受控端口>/debug/capacity' --output '<新的受控capture目录>' --max-seconds 2 --max-captures 1 --stop-after-capture --expected-pid '<Go测试PID>'
```

`manifest.json.captures[]`含available/file/truncated/trigger_sample_sequence/reasons；`timeline.ndjson`保存原始阶段快照，`goroutine-1.txt`最多1MiB。必须由Go测试在collector返回时仍证明barrier未释放，并核目标stage与trigger；collector自己不声称知道barrier状态。默认测试不生成真实采证文件，root保留明确开关下的原始输出。

其中timeline是已标注的parsed subset：原stage/pool/cards/runtime字段保持，full slow_traces移除，只留visible sequence/latest sequence；实际HTTP字节SHA在fetch观测里，完整response未保存。慢payload只在`slow-records.json`保留已观察total_ns最高32条（sequence作tie），窗口引用未保留序列时没有payload，明确缺失，不称逐响应重建。失败fetch同样记录起止/耗时/status/缺失，URL禁止redirect；正式phase无collector。

实际diagnostic prediction同样含task/kind/prediction/differentObservation/evidence，并增加`wiringEvidence`为已成功受控场景的公开结果。第二diagnostic还必须：

```json
{
  "change": {
    "type": "candidate",
    "baselineTag": "diag-1",
    "description": "仅一项已出现证据支持的受控变化",
    "oneChangeOnly": true
  }
}
```

```powershell
python -B -X utf8 scripts/cap08-stage.py --environment $cap08Environment --api-metadata docs/evidence/2026-10-30-cap08/api-diag1.json --source-sha $cap08Source --kind diagnostic --tag diag-1 --prediction '<公开prediction JSON>' --mode multi --rate 100 --seconds 180
```

此CLI只起一轮，网络/依赖验证前已独占slot/command，真实启动失败也占诊断名额。原supervisor5s采样和原native分类器不改；collector最多1Hz/3次1MiB文本栈/相邻15s，待本轮stable-process出现后采样，done-file或最长360s退出。collector只HTTP、不做进程枚举，真实k6/API身份由既有supervisor和前后身份验证承担，记录不原子/迟到边界。不能把恢复栈排除以前阻塞，也不把采集失败当未重现。

## 修复后的正式gate与一组稳定性

没有可证缺陷不改业务。每次修复保留必要回归、原受控用例回归/因果对照与精确源码CI；最终应用源码固定。root根据真实证据独占写`formal-gate.json`，模板字段如下，不把布尔声明当实际证明：

```json
{
  "task": "CAP-08", "kind": "formal-gate",
  "sourceSha": "<最终40位应用SHA>",
  "at": "<实际UTC时刻>", "workCompletedAt": "<必要工作实际在限内完成的UTC时刻>",
  "relatedChecksPassed": true, "exactSourceCIPassed": true,
  "provenIssuesHandled": true, "workCompletedWithinBudget": true,
  "diagnosticOutcome": "not-reproduced",
  "unresolvedReproducedFailure": false,
  "evidence": ["<实际公开诊断/回归/CI证据路径>"]
}
```

`diagnosticOutcome`只可not-reproduced或treated；not-reproduced要求全部实际诊断原生判据与完整采集均过：stable创建至首poll、末poll至done、最大poll起点间隙均≤2.5s，无失败/overflow/字段缺口/截断栈。只有一个成功poll不算完整180秒。treated不因一次采集缺口永久阻断独立证明的处理，但必须至少一个诊断实际stage采样，并提供`treatmentEvidence`路径；不能只有gate主张。同类失败重现仍未知/无可验证处置或已证问题未处理，不写成功gate、不进入正式组。工具不替代root判断证据是否支持声明。

treated证明JSON需`task=CAP-08`、`kind=treatment-proof`、`mechanismObserved=true`、mechanism说明、`method=old-fail-new-pass|same-source-causal`及`siteEvidence`实际现场公共证据路径。`baseline`/`treated`各写sourceSha、configuration公共配置文件路径、result（分别FAIL/PASS）及实际证据路径list；treated.sourceSha为最终应用SHA。old-fail-new-pass要求公开配置字节相同；same-source-causal要求同sourceSha及`oneChangeOnly=true`。没有已证机制时不提前创建treated gate。

停止当前精确API，再以最终clean构建`--phase formal`启动（不带tail），更新env。API切换使用`cap08-env.py stop-api --api-metadata <当前metadata>`，不按进程名停止；保留依赖、fixture和旧卷，不清缓存伪造热命中。

```powershell
python -B -X utf8 scripts/cap08-stage.py --environment $cap08Environment --api-metadata '<最终formal API metadata>' --source-sha $cap08Source --kind formal --tag formal-1 --gate docs/evidence/2026-10-30-cap08/formal-gate.json --mode multi --rate 100 --seconds 180
```

检查第一轮原native/result/control/cards与tool稳定性后，完整通过才以新tag做第二multi100×180；再完整通过才做第三hot200×180。同最终SHA与相同配置；每轮slot独立且首次失败立即结束本组，不补轮、不换tag、不改变窗口或阈值。collector在formal不启动。工具计算的cards端点差含预热/后台，不冒充稳态，gauge不作次数。

可选余量只在正式三轮全过后使用：`--kind stair`依次150/200/300/400×45；首限停止，至多一次`neighbor`严格括区内且只对可测SLO失败；最高完整通过>100才`--kind hold`该档180秒两轮，第一失败即结束可选组。最高100及hot均复用正式组。不新增第二组诊断/修复或降档补持有，无同档A对照只写B实测档。

## 活动锁、收尾与交付

`cap08-stage-active.private.json`记owner与wrapper/collector创建身份。中断、wrapper缺supervisor终态、collector仍存活时保留，不删锁以启动下一轮；先核本轮PID/创建时刻/exe及原stage/harness/k6/sampler是否已收敛，再选择精确恢复操作。slots与失败工件永不删除。正常完成需wrapper已退出、control done/exit终态、已启动sampler退出、stage-exit完成后才解锁。脚本不是按名称全杀工具。

完成预定路径或达到停止条件后，保持API/依赖可用，确认全部stage任务已收敛：

```powershell
python -B -X utf8 scripts/cap08-env.py finish --environment $cap08Environment --api-metadata '<当前精确API metadata>'
```

核cleanup/progress：32正常logout/Redis EXISTS0、observer删除核0、API精确PID/创建/exe/hash停止、两精确CID exited/exit0/OOMfalse、挂载及旧新卷保留；私有env按登记精确删除，失败先保留progress/恢复输入，不递归清理。Windows terminate不称Go优雅退出。无flush/prune/down-v/删SQL事实、不停其它服务。

最后报告保留每个失败、实际source/tool/binary/fixture hash、所有阶段命令/原生/资源/loader口径和等待时间，状态分列功能已核验、已证机制/修复、旧B1未知、正式是否通过及可选高档边界。仅新卷单代表multi100×180交独立核验，不复跑全部探索；预算用完或组失败即交未通过并结束，不自动续CAP09。
