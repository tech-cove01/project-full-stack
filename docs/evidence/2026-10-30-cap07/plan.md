# CAP07 执行前协议

依据根 [STATUS](../../../STATUS.md) 与 [公共内容缓存设计](../../feed-card-cache-design.md)。本文件定义范围、固定参数与限次，尚不代表已执行或通过。先完成一次 CAP06V 受控核验与已证缺陷修复；原秒级尾延迟即使仍无法归因，也保留风险后直接做 CAP07，不另开第 4 轮完整诊断或单独 CAP06D。

## 固定环境与身份

- project 固定 `project-full-stack-cap-read`，仅自有 MySQL3451、Redis6551、API8251、diagnostics8351；等待 CAP06V 的 3452/6552 环境释放宿主资源后再启依赖/负载。
- 重新核对移交的两只 **64 位完整 CID**、compose project/service 标签、loopback 端口与已停止状态；保留现有自有 network，显式验证其 private `/24` subnet。新随机 CAP07 命名卷替换这两容器，旧卷保留，不复用 CAP06 fixture/token/observer/cleanup。
- MySQL4CPU/2GiB、Redis2CPU/512MiB，API GOMAXPROCS4/GOMEMLIMIT1GiB、SQL池20/Redis池10、ORDER_STREAM_WORKERS1；持久化不改，migration001–013。Kafka、ES、AI关闭。
- 新合成夹具 6400 篇、32 作者、32 读者，每作者200篇、正文256B、每读者关注一作者，页长20。multi轮转640个计数/内容key，hot20；`source=database` 保持，内容缓存不改变事实源标识。
- 私有 runtime 固定 sibling `.runtime-fullstack-cap07-<12hex>`，CAP07 bookkeeping 与 observer 独立。`cap07-env.py` 薄适配复用 CAP06 已审生命周期；cap06默认行为保留，两 cohort 的 runtime/path/key 不互认。公开证据不导出环境值、token 或 observer 密码。

## A/B 与有限阶梯

A 固定为 CAP06V 已提交且尚无cards/social改动的 `f3d99ee43dfb422a0cba525de93e9266f07c4ee5`；B 为最终带跨 caller 合并实现的固定 clean SHA。A/B 都保留同一已证计数门修复及相关观测修复。每只 exe 记录 `go version -m`、SHA256、PID/UTC创建时间与 health version；不得在同路径覆盖正在使用的 exe。B 初版无跨 caller 合并只做功能回归，不能充当最终B。

| 顺序 | 变体/阶段 | 稳态负载 | 次数与上限 |
| --- | --- | --- | --- |
| 准备 | A/B短探针 | multi≤25/s、≤15秒，预热1秒 | 每变体至多1次，全部保留 |
| 对照 | A1 → B1 → A2 → B2 | multi100/s×180秒 | A/B各2轮；B两轮可复用最终07C的100档 |
| 探索 | 最终B有限阶梯 | multi150/200/300/400/s，各45秒 | 最多4档；前档完整通过才进入下一档，首失败即停上探 |
| 缩步 | 最终B首限邻近 | 最高已通过与首失败档之间，multi×45秒 | 最多1档；资源/监督停止不作为可缩步的服务容量括区 |
| 持有 | 最终B最高完整通过档 | multi×180秒 | 同最终SHA两轮；最高100时与B1/B2合并 |
| 热点 | 最终B | hot200/s×180秒 | 1轮，最后执行 |

正式阶段统一预热10秒/25/s、preAllocatedVUs256/maxVUs512，heavy Feed diagnostics固定false；sampler仍5秒一次，无额外高频抓栈。每次 CLI 只启动一轮，不自动上探、重试或覆盖。stage/slot 独占创建，启动失败也保留名额。A1/B1/A2/B2固定交错；使用同一fixture manifest，API版本与fixture原始版本分别落证。

两个B100对照任一未通过，不上探。一个持有组任一失败，完整报告失败，不能拿额外成功轮补齐。若源码或环境必须修正，先记录差异和新协议，不沿原阶段名隐藏重跑。API切换只停止已记录PID+创建时刻+exe/hash，依赖和fixture保持；不清空缓存伪造热命中。正式结果包含运行开始、自然TTL过期与热点期间的实际成本，冷/热/过期语义另由定点功能证据解释。

本协议A/B在100档作同条件比较。B更高档未经同档A对照，**只能报告B实测上界，不能据此声称容量提高**；100档效果可报告无一致提升。所有旧失败、早停和未归因秒级尾风险继续保留，不因缓存通过倒推旧事故已修。

## 判据与独立计数

沿用原判据：发起≥99%、成功≥99%、系统/HTTP错误各<1%、内容错误0、unknown0、dropped0，全请求与成功请求各P95≤200ms/P99≤500ms。早停时 nominal-second 分母仍是计划全时长。wrapper退出码只代表监督结果；必须同时读取 `*-load/result.json`、control stop/exit-status 与 cards证据。

安全预算不变：host内存88%、API RSS1.5GiB、k6 RSS2GiB、自有容器内存85%；host CPU85%连续3个有效样本，或连续3次不完整观测即停。共享宿主资源触线是共享环境限制，不能写成服务独占容量；先处理现有停止原因，再决定下一步，不盲目换tag重跑。

`/debug/capacity.cards` 直接公开低基数 Snapshot，B必须 `coalescing=true`。独立记录 requests、L1/Redis hits、miss_items、loads/flight、实际内容SQL批次/行数/字节、错误/超大不缓存、拒绝、过期、驱逐与有界状态gauge。SQL batches含真实尝试及失败；rows为实际DB行；bytes为DB行title/content/原images的UTF8字节，不是wire或RSS。hits/misses按输入去重Ref item计数。

`cap07-stage.py` 在预热前和wrapper收尾后各取一次完整capacity，并独立计算单调counter差；gauge只列前后，不将gauge差写成次数/峰值。范围包含预热、稳态与后台工作，端点为顺序快照，**不冒充稳态精确loader计数**；原5秒资源样本保留过程变化与自然过期。A无cards组件时独立loader数据为null，不能当0 SQL；全实例`Com_select`与`Com_stmt_execute`重叠、含观察开销，分别报告，不相加，不代替内容loader计数。内容热命中0次loader SQL也不代表Feed0DB。

## 收尾与交付

正常注销本轮32会话并Redis EXISTS=0；删本轮observer并核其不存在；核API身份后停止；按两只不可变CID停止并核exit0/OOMfalse；卷、SQL事实及全部失败证据保留。私有凭据只在成功完成对应步骤后精确删除，失败保留progress与恢复材料，不递归清理目录。见 [runbook](runbook.md)。工具提交、功能提交与最终证据按小问题分开，由根agent提交并交独立核验。
