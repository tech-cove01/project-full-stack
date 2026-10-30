# CAP-08 内部非作者审查

本文件只记实施团队内部审查，不等于交付后的独立外部核验。

- `cap08_evidence` 对root的taildiag有界在途时钟/零值初始化、social等待/回退和app注入只读审查：未见业务/锁序/生命周期变更；指出requests混计shared任务，实施前已修为requests/shared_tasks分列并复审关闭。
- root对`cap08_cards`编写的shared/caller分段和受控测试审查：仍是app life+2s，mutex不跨I/O，新增trace覆盖最终发布，场景明确是BeforeRow屏障。`cap08_cards`再对root的实际HTTP观测测试、README/报告和wiring-result做非作者复核：实际工件10604B及SHA、337/336ms两年龄、释放前捕获和最终归零均一致，无阻断。
- `cap08_evidence` 审collector时指出失败窗口丢失、完整性口径、实际捕获间隔、邻近限档和redirect边界；工具作者在首次真实场景前修正。冻结collector `aa1ec0b291b7f278e4db4399a05bcbaa79d4f1932c56afba160ce257b2a5de8c` 再审通过，之后root实际执行场景1 PASS。最多32实际慢记录及parsed subset范围明确，未把静态准入代替真实采证。

`cap08_evidence`随后对冻结的stage/policy/check/lifecycle与plan/runbook最终只读复审通过：邻近仅真实SLO失败、采集完整性与treated公开证明、同SHA正式首失败终止、orphan/失败名额不重置、子句柄身份/监督终态和中断保留锁、4小时保守计时与旧cohort互拒均无新增阻断。root实际执行10项纯工具测试方法通过，未把它计作HTTP/SQL/负载。

一般真实回归、专用场景、工具纯检查、精确源码CI与现场负载分别按各自原始文件记账；没有把同一套自验重复包装成独立外部验收。

## 已完成现场结果与终态补审

- `cap08_evidence` 对有效诊断1的原生计数、182次1Hz覆盖、在途年龄/慢记录及原5秒监督只读复核，形成[准入意见](diagnostic-report.md)。它只支持not-reproduced准入，没有证明业务机制，不支持信息重复的诊断2。
- 正式2失败后，`cap08_evidence` 只读既有native/访问快照/5秒末窗与清理记录，形成[失败审读](failure-final.md)：原生unknown200/dropped57独立成立，另时基一致性拒绝；不把复合counterResets或低资源采样写成重启/根因结论。未新增HTTP、服务或负载。
- 工具作者在全部负载结束后仅补policy/check两文件的晚期中断守卫；root复跑11项纯检查，`cap08_cards`非作者再审/纯检查11项通过，无阻断。实际三个stage的true/null工件分类不变，应用与测量链路未变，不补轮。详情见[独立小修](tool-guard-review.md)。
- `cap08_cards` 对root的report、代表档说明、STATUS/handoff顶部及failure-final只读审查，并直接核对三轮native/stage/control、访问时间差与cleanup：数字、缺失口径、旧B1未知及停止边界一致，无阻断。明确P99合格不抵消unknown/drop，正式失败后没有继续load的授权。
- root完整审查非root作者的只读`cap08-audit.py`，要求30个工具hash严格对历史Git516，禁用当前文件替代历史匹配；作者收紧后冻结，root执行`--check`通过，3stage errors为空、整组false。formal2原result缺失保持、native独立复算失败、访问计数无效均一致，未访问服务或追加负载。
- 最终Git字节核对发现仅08A早期派生`failure-window.json`因先于`-text`入库而保留LF，原件CRLF未变。`cap08_evidence`独立核staged/working/manifest hash一致、仅1025处CRLF差且JSON相同、暂存仅目标与说明；root追加单独小提交，重新批量核131份raw加manifest共132份/3490228字节全部匹配，见[字节审计](git-byte-audit.json)。没有为格式检查改写原生日志。
- `cap08_cards`在audit与manifest落地后再核主要最终文档链接均存在，继续无口径/授权阻断；Git字节审计作为另一个限定范围的离线结果，不替代外部容量验收。

这是实施团队内部交叉审查。最终交付后的外部独立核验尚未完成，不能把这些复核或精确CI写成外部容量验收通过。
