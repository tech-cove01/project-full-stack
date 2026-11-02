# CAP-10：合并已锁定 Claim 行的更新

2026-11-03，CAP-10-CLAIM-POINT。提交与远端状态见根 [STATUS](../../../STATUS.md)。

## 问题与行为

ClaimBatch 已在一个 RC 事务中通过活跃索引锁定合格行，但逐行发送 lease 或停车 UPDATE。原实现已经只提交一次。本票减少这些已锁定行的 SQL 往返，不据此声称减少提交次数或 fsync。

保留原有有序选择、尝试次数、字节前缀、首条超大事件单独前进，以及截断后尾部不变的规则。先按原循环得到 parked、claimed 两组；每组单行沿用原 SQL，2–64 行使用有界 UNION ALL 参数驱动，ID 显式转 UNSIGNED、attempts 转 SIGNED，再 STRAIGHT_JOIN 目标 PRIMARY。每行仍核自己的 attempts；RowsAffected 必须等于该组唯一输入数。任一组出错或失配，整个事务回滚，成功提交后才返回事件；提交错误仍保留原不确定性。

一个 lease UPDATE 中所有行共享该语句的 NOW(3)。这与原逐行语句起点有区别；等待仍可能消耗 lease，不能保证提交后还剩完整 30 秒。实际点查计划也不代表不存在索引等待或死锁。调用方的同步 Kafka ACK、完成 fence 和重放规则继续生效。

## 真实隔离验证

实际 UPDATE EXPLAIN 覆盖 N2/32/64、lease/park 两种语句。小夹具使用实际 Go driver 的 InterpolateParams=false/true，大历史使用自有 root 本地客户端的 text/server-prepared 路径，合计 24 个计划；目标均为 PRIMARY const/eq_ref。大历史只做 EXPLAIN。uint64 最大值仅做绑定 CAST，没有插入共享 AUTO_INCREMENT 表。

真实功能回归 10 个顶层用例通过，覆盖合格/未来/过期/锁定行、RC 下非匹配 live owner、8 路唯一领取、字节边界与超大首条、取消、64 行上限和未选尾部。新增跨组用例先写停车组，再触发实际 MySQL1054 或第二组短 fence，核整批状态、attempt、lease、错误文本回滚。

同一程序对旧、新算法各测普通32行和16停车+16领取，两种驱动模式共8个有限样本。专用会话在调用前后读取实际计数，准备、检查和清理在计数区间之外：

| 场景 | 路径 | Com_update | Com_update_multi | prepare/execute/close 各自次数：false / true | COMMIT |
| --- | --- | ---: | ---: | ---: | ---: |
| 32领取 | 旧 | 32 | 0 | 33 / 0 | 1 |
| 32领取 | 新 | 0 | 1 | 2 / 0 | 1 |
| 16停车+16领取 | 旧 | 32 | 0 | 33 / 0 | 1 |
| 16停车+16领取 | 新 | 0 | 2 | 3 / 0 | 1 |

每个样本另有 SELECT1、rollback0。首轮计数工具只读取 Com_update，漏掉多表语法的 Com_update_multi，导致4个新路径子例 FAIL；原始失败和源冻结保留。按 [MySQL8.4 计数定义](https://dev.mysql.com/doc/refman/8.4/en/server-status-variables.html)修正测试工具后，独立 namespace 只重测1顶层/8子例，全部通过，并在断言前保存全部样本。10个已通过功能用例没有重复执行。

本机验证使用1CPU/4GiB容器、2GiB buffer pool，整个研究累计935条自有事件。接续经历重启、新 AUTO_INCREMENT ID 和缓存变化，不跨次拼延迟倍率。上述是 SQL 正确性、计划及有限组件计数证据，没有 API QPS、接口延迟或5000稳定容量结论。

业务源码与真实 overlay 测试版本逐字一致；最终相关包普通测试39顶层、vet和差异检查通过。独立测试库12表清空、会话0，自有四CID正常停止、五卷保留，原100容器/140卷/38网络保护比对通过。首轮档案 SHA256 为 `a95dbdf838e2f23bede29e72cc7f04f6d603e40bfb85b254938e8c2716c68bac`；完整224文件档案为 `a11690003d63fbd5a2935512e5acce7c0fc1153d60f0a9f4a43971e3c88ca370`。

回退恢复原逐行 Claim 更新即可，不涉及表迁移或数据重建。
