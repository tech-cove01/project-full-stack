# CAP-08 内部非作者审查

本文件只记实施团队内部审查，不等于交付后的独立外部核验。

- `cap08_evidence` 对root的taildiag有界在途时钟/零值初始化、social等待/回退和app注入只读审查：未见业务/锁序/生命周期变更；指出requests混计shared任务，实施前已修为requests/shared_tasks分列并复审关闭。
- root对`cap08_cards`编写的shared/caller分段和受控测试审查：仍是app life+2s，mutex不跨I/O，新增trace覆盖最终发布，场景明确是BeforeRow屏障。`cap08_cards`再对root的实际HTTP观测测试、README/报告和wiring-result做非作者复核：实际工件10604B及SHA、337/336ms两年龄、释放前捕获和最终归零均一致，无阻断。
- `cap08_evidence` 审collector时指出失败窗口丢失、完整性口径、实际捕获间隔、邻近限档和redirect边界；工具作者在首次真实场景前修正。冻结collector `aa1ec0b291b7f278e4db4399a05bcbaa79d4f1932c56afba160ce257b2a5de8c` 再审通过，之后root实际执行场景1 PASS。最多32实际慢记录及parsed subset范围明确，未把静态准入代替真实采证。

一般真实回归、专用场景、工具纯检查、精确源码CI与现场负载分别按各自原始文件记账；没有把同一套自验重复包装成独立外部验收。stage/policy最终审查和后续现场结果另补，不提前声称完成。
