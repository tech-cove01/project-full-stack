# CAP-10：独立完整LIKE审计

2026-11-03，小票 `AUDIT-V1b`。仅显式 `like-vector-v1` 使用新审计；旧SQL与30秒预算保留。新模式120秒仅限窗口外完整审计，不改变原生窗口、4秒采样或60秒恢复判据。

同一只读RR快照读取选定笔记的完整事实、计数、归属事件、投递状态、匹配收据、控制行与全局消费者失败。事件必须满足严格身份、四字段JSON类型、版本及受控奇偶规则；只有通过校验的十进制ASCII身份才能生成与生产Decode相同的canonical hash。收据保留实际projection/state字节分组，未知组、别名、错误状态和hash不能被正确数量掩盖。审计绑定原夹具文件SHA及nullable控制行，最大Outbox ID仅作诊断，不能作为发现游标。

真实生产Decode生成33个样例，其中32项可与MySQL存储后表示比较；重复JSON键只保留Go反例。实际MySQL32项身份/hash对照、12项收据反例和5源事件分成4组的聚合验证通过。首次生产EXPLAIN因ONLY_FULL_GROUP_BY报1055，已保留原件；最小修复将聚合标量放入派生表，再由外层生成JSON，不改变SQL模式或使用ANY_VALUE。随后的解析器因JSON字段顺序误拒正确4组结果，保留FAIL，仅对原raw离线修复解析，没有重发该查询。

三个实际EXPLAIN确认hash_values仅物化event_id与expected_hash，收据按完整PRIMARY(projection,event_id) eq_ref查找。源仍全扫，原始projection字节去重也完整扫描收据PRIMARY，不能宣称loose-index或按估算行数冒充实际基数。

在本机独立1CPU/4GiB容器、2GiB Buffer Pool、原1225500事件及三组收据上，仅一次完整RR审计耗时106077.0271ms，实际Exit0、无timeout、完整尾marker/COMMIT，符合120秒单次预算。它发现三组共3676500个hash不匹配，且该历史造数没有对应笔记/事实；原字节不修补，**业务对账不通过，也没有Redis或新QPS结论**。源/hash严格检查揭示了造数问题，不是Go业务链路发压失败。

真实会话退出、后置12张IT表空、原历史MAX/UUID/owner/配置守恒，自有MySQL正常停止且卷保留；原100容器/140卷/38网络及14个运行服务保护通过。完整104件manifest SHA256=`9701a337c197217d7a6050e395d5d0596a9465fcb5d34348abeb1d1d4475f90d`，前述87件与51件失败原件逐SHA保持。

10项Node新检查和39项原检查通过，源审与真实语义/计划/有限预算验证分别记录。单次106秒已表明现有setup60秒不能容纳反复全史扫描；中间阶段的轻量事实读取、已接受增量样本摘要绑定及仅一次最终完整审计仍需另票完成。当前不具备开机准入，实际状态见根[STATUS](../../../STATUS.md)。
