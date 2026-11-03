# CAP-10：已接受样本的事实摘要

2026-11-03，小票 `OBS-SAMPLE-DIGEST`。完整历史审计实测106秒后，中间阶段需要轻读事实，并证明它与增量观察器读到的是同一组关系；相同事件总数不足以证明这一点。

仅新模式的已接受样本在 `sample.sql` 输出三项：`digestContract=like-vector-digest-v1`、`factVectorSHA256`、`controlsSHA256`。从原RR事务已验证的事实和控制行计算，实际SQL提交、Kafka完整性及原预算检查全部成功，并且原Accept成功后才写入本行。失败、取消、旧epoch或已消费proposal没有可借用的摘要；诊断SQLView和baseline/旧模式不增加这组三字段。

事实摘要沿原夹具配对顺序，按该键序将 `{noteId,userId,present,version,active}` 组成无空白、无换行的JSON数组后计算SHA256。控制摘要复用原Go结构体与sql.NullString的三组控制行hash，以及固定follow/feed/search紧凑JSON数组的外层hash，保留Go JSON的键序、大小写、HTML/行分隔符转义；不能直接hash Node原controlRows JSON。该hash只绑定样本身份，不能替代独立完整终审。

没有新增SQL；hash CPU和分配计入原采样预算。原4秒总、3秒读、5秒节奏和native恢复60秒不变。7项新顶层/11子例检查、包含原检查的36顶层/89子例race、vet及Linux构建通过，三方独审通过；原Go函数另产四组独立golden供Node后续对照。检查覆盖同总量换配对、SQL提交回执错误、Kafka缺组/重复/未初始化/迟到、deadline、旧epoch及发布后事实变化，不能把这些内存边界检查称为真实Kafka或容量验证。

Node轻读、setup收敛绑定及一次完整终审由后续小票接线；本票无SQL执行、无压力或新QPS结论。实际状态见根[STATUS](../../../STATUS.md)。
