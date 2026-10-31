# 有限回查交叉审查

本页记录实施团队内部只读交叉审查，不冒充新容量复跑或新一轮业务验收；业务结论沿用此前已完成的独立核验。

root从Windows System按5个EventRecordID逐项重读原生UTC、provider、eventId、Message与全部选取PowerFields，与host-events.json完全一致。事件是有范围的选取投影，不是完整EVTX；Docker日志覆盖与缺失也按原记录保留。

`cap08_harness`非作者复核root的cross-run-check.json、report/scope和另一作者的cross-run-analysis，独立读45份旧源文件核size/SHA及五轮native/时基算术，零差异。`cap08_evidence`非作者核executor-analysis及k6 v2.2.0官方取消/阈值/动态VU源码，语义一致。

审查指出root初稿两处归因超出证据，已收紧并复审：200个unknown不能全部逐条归因abort；native最大值仅覆盖整个stage，不能绑定到电源事件的精确时刻。两名非作者对修改后的两轮只读边界、Lid强关联而非VM已证暂停、旧B1未知、功能版收尾但容量仍失败的结论均无阻断。

`cap08_cards`最后对STATUS/handoff与report/scope/review再次作非作者审查，无阻断；106个本地链接存在，3 blob/29291字节及新负载0的审计口径一致，确认应用、工具和旧CAP-06/07/08证据没有差量。

本次未运行Go测试、编译、SQL/HTTP探针、依赖/API或k6。只有旧工件算术/hash、原生事件重读、源码/文档审读及交付链接/字节检查；没有把这些包装成新性能成绩。
