# CAP-04 索引后读链路的实际剖析

固定 API3db/PID45532、SQL pool20、GOMAXPROCS4/GOMEMLIMIT1GiB、同6400篇夹具与013索引不变。以下仅定位本配置下的限制，不修改Go、池、驱动参数或预算；正式稳定档另以CAP-03最后180秒两轮为准。

## 原始受限档与诊断轮

首次hot400/pre64因一次dropped触原drop0阈值中止，已分类请求全成功且HTTP分位达标；宿主有86.6%单区间CPU波动。它不能证明服务端最大容量。独立400/pre256带CPU profile/SQL采证诊断通过，随后相同400/pre256无profile正式复跑也通过；原失败和诊断都保留，不把诊断通过当容量，也不声称preVU是唯一原因。

hot800/pre512正式30秒档约12秒触P95阈值早停：9400 started、9371完成分类且成功、unknown29、dropped0，P95/P99=201.561/371.354ms。有效CPU采样峰API206.2%单核、MySQL276.52%、k6103.6%、host79.3%；并非仅API CPU满四核。采样中SQL in-use20，首末WaitCount增4140、累计WaitDuration增287.44秒。整体时间序列和原始计数在 `cap03/post-hot-800-pre512-a-*`，不把原计划30秒分母的比值当实际持续吞吐。

随后独立同档带profile的诊断也按原P95阈值早停，P95/P99=256.032/437.291ms，9387完成分类全成功、unknown14、drop0。SQL采样等待增6028/452.50秒，host有效区间峰83.8%。这轮有额外观察开销，不能拿它包装原始成绩。完整采证见 [post-index-hot800-profiled/manifest.json](post-index-hot800-profiled/manifest.json)，所有sections available，SQL锁等待只是瞬时快照，空快照不能证明全程无争用。参数化SQL在digest有盲区；执行次数采用各阶段SHOW GLOBAL STATUS/Execute摘要，Com_select与Com_stmt_execute不相加，缺字段不补零。

## 真正CPU与负载内goroutine证据

800诊断CPU profile实际10秒、15.08 CPU秒样本，cum WSASend40.38%、mysql.writePacket32.49%，更多调用在GORM读取/事务和网络写路径。400诊断也有类似分布。Windows native I/O栈与Docker/本机TCP条件有关，这些调用占比本身不能证明某一驱动、系统调用、锁或CPU是唯一根因；没有据此改协议或调pool。

800 CPU profile采集区间为06:17:45.914–06:17:55.931 UTC，stable命令窗口06:17:40.803–06:17:54.395 UTC（k6实际spawn为40.879），两者重叠约8.48秒，最后约1.54秒已在进程结束后，不能声称整个profile都在业务负载内。collector默认在CPU profile后抓goroutine，因此该文件是退出后的栈，不伪称峰值。

为补齐负载内栈，独立800/15秒诊断（不计容量）在stable进程spawn后4/6.5/9秒抓runtime和二进制goroutine，见 [post-index-hot800-stacks/capture-times.json](post-index-hot800-stacks/capture-times.json)。三个点goroutines572/616/685、SQL in-use13/20/20。T+9秒profile中102条栈在`database/sql.(*DB).conn`，18条在MySQL读包路径；这直接支持固定pool20前存在连接获取排队。profile总686条goroutine中大量是空闲HTTP读连接，不能把它们全称为活跃请求并发。CPU/锁/连接状态共同支持本配置的I/O与连接占用限制，但不能证明提高pool就会变快。

该15秒诊断12001完成分类全成功、drop0、P95/P99=136.012/309.435ms；同时宿主单区间CPU91.1%，未连续三样本达到85%停止条件。它与原30秒/带CPU profile诊断的差异必须保留，说明短窗和共享宿主扰动，不把一次诊断通过称为可持续800或确定理论上限。后续hot700也早停，宿主有效区间峰95%，进一步限制跨轮比较的外推。

## 时区候选的实证与本轮决定

400诊断CPU里`time/tzdata.loadFromEmbeddedTZData` cum5.51%，不是当前CPU主限的充分证据。另在无load时抓了API自启动以来的累计alloc_space，[作用域](post-index-hot400-profiled/allocs-scope.json)与profile均保留：pprof原单位总6552.65MB累计采样分配中，social.zone/time.LoadLocation cum1541.06MB/23.52%。这是累计分配而非RSS、不是400窗口增量，也不能转换为CPU或吞吐提升比例。

root已明确本轮保留固定API3db，不追加时区缓存或驱动调优。只记录该成本为后续候选，完成013后的实际边界和两模式各180秒两轮；若共享宿主/生成器先受限，报告本配置共同部署观测边界。
