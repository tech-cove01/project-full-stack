# CAP-08 工具终态守卫补充

正式第2轮失败并停止全部新负载后，只补一个独立工具问题：`passed` 与 `measured_slo_failure` 原先没有同时要求 wrapper 已退出、且 stage 没有异常。晚期 `KeyboardInterrupt` 可能发生在计数与工具 hash 已完成之后，留下成功字段与异常字段并存的记录。它不能用于放行下一轮或形成容量括区。

仅修改 `scripts/cap08-policy.py`、`scripts/cap08-check.py`，增加 `wrapperExited is True`、`failedStartOrException is None` 判定及晚期中断/未退出反例。作者报告同一反例在旧策略有4个预期失败；root重新执行 [11项纯检查](tool-guard-check.txt)，全部通过，`git diff --check`通过。非作者 `cap08_cards` 另行只读审查及纯检查11项通过，无阻断，确认实际三轮显式true/null因而分类不变。缺失异常字段仍按None处理的schema边界已披露，当前writer与实际工件均显式保存该字段。

本批实际 diagnostic-1、formal-1、formal-2 的 stage 均为 wrapperExited=true、failedStartOrException=null、toolBytesStable=true。新增守卫不改变任何已测结果；formal-2仍为失败。本修复没有重跑正式组、增加诊断、修改 stage/collector/Node/k6/sampler 或应用，不计为B1业务机制修复。

实际测量时工具来源保持 `516d5342af95d51adf8f6ec9a8c3af321fefb4fa`，应用固定 `46ddefc2a16d41e4bb01a3d20d08bdfcaeff465f`。后续审计按 command 保存的实际脚本hash与历史Git字节核对；不能拿补丁后的 policy 字节替代原测量工具，或倒写旧工件。
