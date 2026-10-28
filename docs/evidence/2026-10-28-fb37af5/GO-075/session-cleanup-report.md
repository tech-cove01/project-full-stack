# GO-075-QA1 演示登录会话清理

独立脚本问题：已有`go-flow-demo.mjs`删除自有SQL用户，但没有撤销本轮登录session。GO-075三种配置演练前两轮留下12个合成演示session（[数量证据](session-cleanup-initial.txt)），可能存活25天。业务交易断言通过不代表夹具清理完整，此处单独修复，不把原结果改写为无残留。

修复在finally中，使用本进程sessions Map保存的精确Token，先通过实际logout撤销，再删除SQL夹具；logout网络/业务失败使cleanup失败。Token不输出到日志/报告。未扩其它Redis运行键清理。

专用Redis6403是本票新建卷，仅用于这些合成测试；前两轮明确属于本脚本的12个演示session已按精确扫描出的key和nickName对照删除，未触碰其它实例：[补救数量](session-cleanup-remediation.txt)。修复后三种配置实际交易各9项通过、正常关闭，且自有演示session为0：[归零](session-cleanup-final.txt)、[重跑](isolation-script-session-fixed/isolation-final.json)。

独立只读审查ai_eval确认只撤销本进程Token、发生在SQL清理之前、失败可见且不泄露Token；Node语法检查通过。本问题只提交演示清理与对应证据；隔离脚本与票级收尾另提交。
