# GO-073-CAP 独立核验

2026-10-30，交付工具基线 `09b49211f81389fb4075396f318f8d4b48e30cf1`。本次独立源码审查、历史原始数据复算和新卷代表档现场复跑分别记账，不将实现者内部审读当外部实跑。

**结论：源码和原始数据复核未发现生产阻断缺陷或统计虚报；现场容量核验部分通过，multi100的长尾预算未通过，性能收尾尚未闭环。** 交易30与hot200新卷代表档通过，35交易再次积压；不能因原实现者multi100两轮通过而抹去本次独立失败。没有证据支持把整项目写成5000 QPS，也不能把当前档位称作Go或服务器理论上限。

## 源码与原始证据

- [并行消费者与诊断接口](production-review.md)：未发现新增生产阻断缺陷。每个 Worker 独立 consumer/游标/重试状态，默认1、实验4、配置上限8；订单事务、库存锁、审计/outbox、确认持久结果后ACK规则保持。诊断默认关闭，仅development/test数字loopback，使用独立受限mux。
- [Feed与013索引](feed-review.md)：公共计数装配移出RR快照后再借SQL池，权限/当前关注/个人点赞/游标仍在原快照；真实单连接回归确有旧FAIL→新PASS。相同完整SQL的候选扫描6400→200，仍有门店连接和排序；不称覆盖索引或HTTP提升13倍。
- [独立离线复算](offline-audit.md)、[可复跑脚本](offline-audit.py)：13329条已交交易请求逐项匹配公开用户池、exporter、SQL和Redis；原四轮读计数和分位与k6 native summary一致。原作者审计54正常阶段+1不可计分阶段、7130检查通过不等于54档均满足容量预算。
- 本核验者联网确认精确功能提交 `767c2fc8bdf1be08c5636143a71898eec25081a9` 的[CI](https://github.com/tech-cove01/project-full-stack/actions/runs/41016781664)四作业成功，[运行](ci-run.json)与[作业明细](ci-jobs.json)已保存。没有把文档基线09b的CI与767c混为一谈。

## 新卷代表实跑

现场证据根为 [qa-independent-6e6af1e78699](../../../evidence/2026-10-30-go073-cap/qa-independent-6e6af1e78699/)。按交接核对原CAP容器均已停止、固定端口未占用，再以显式全新随机卷替换本项目两个容器；原卷不删除。未停止CRM等其它服务，交易与读正式发压顺序执行。临时凭据在Git外受限ACL目录，不保存进程环境或完整Docker Config.Env。

复用实际固定API产物并重新核对干净源码HEAD、`go version -m`、精确health和SHA256：交易3db为 `ef29e9e3838b7ec8ffd1aca5b1a51848fbd8a020ca084228879481adc209bd69`；读767c为 `97a5165e48a74c98ee521eebda01f79d049c44938239e2bd4ed89fe38e25e20c`。这不是独立重新编译API。交易helper从09b工具checkout构建，应用源码未改；新增本轮证据不称干净工具工作树。

MySQL4CPU/2GiB、Redis2CPU/512MiB、API GOMAXPROCS4/GOMEMLIMIT1GiB/SQL池20，原鉴权和事务持久化不变。交易4消费者，负载器Go环境不设；读1消费者，负载器4/1GiB。新卷正常应用001–013迁移；交易25000真实合成会话，读为新建6400篇/32作者/32读者/每页20。机器、版本、容器、镜像、池、API身份及背景服务见各模式environment/api/containers记录。

### 交易

| 新阶段 | 稳定受理/最终逐单匹配 | P95/P99 ms | 末段SQL新增/同期受理；lag增量 | 判定 |
| --- | --- | --- | --- | --- |
| [30/s，180秒](../../../evidence/2026-10-30-go073-cap/qa-independent-6e6af1e78699/trade/trade30-hold-load/result.json) | 5401/5401 | 20.9826/30.5315 | 末分钟1794/1800；+7 | 原预算通过 |
| [35/s，45秒](../../../evidence/2026-10-30-go073-cap/qa-independent-6e6af1e78699/trade/trade35-neighbor-load/result.json) | 1576/1576 | 21.1676/31.1982 | 末14秒409/490；+81 | 持续成单不足 |

两档HTTP错误/dropped均0，含预热5702和1927笔最终按ID/用户匹配，SQL与Redis库存分别178/178、188/188，无重复有效订单、未解决review/补偿、未知回执或最终PEL/lag。30档末分钟比率0.99667且lag增长7在事前60预算内，不误写成独立复跑lag净增0。名义稳定末尾至首次观察收敛约2551/8301ms，包含观测分辨率，不是数据库精确commit时延。

30档符合既定本地承载判据；35档低HTTP延迟和最终排空不能掩盖窗口内积压。31–34未测，不能由此断言理论最大值30。

### Feed

| 新阶段 | 发起/成功/未知 | P95/P99 ms | 结果 |
| --- | --- | --- | --- |
| [hot200，180秒](../../../evidence/2026-10-30-go073-cap/qa-independent-6e6af1e78699/read/hot200-hold-load/result.json) | 36001/36001/0 | 26.0338/31.9083 | 完整通过，37完整/36有效CPU样本 |
| [multi100，原计划180秒](../../../evidence/2026-10-30-go073-cap/qa-independent-6e6af1e78699/read/multi100-hold-load/result.json) | 11988/11987/1 | 32.59979/1137.353972 | 约120秒触P99预算早停，25完整/24有效CPU样本 |

两档已分类的系统/HTTP/内容错误与dropped均0。multi未知的一次是只读请求被停止打断，未归为已成功或系统失败；它使完成覆盖不满足原预算。180秒名义分母算出的约66.6发起/秒只描述截断档，不是系统可持续66.6 QPS的边界。启动前1/s×5秒的5次内容探针通过，仅核夹具、观察器和分母，不列容量成绩。本次核验者每种代表档只实跑一轮，原实现者各两轮与新结果分别保留。

multi在前110秒采样未出现SQL/Redis连接池等待增长；约T+115秒SQL InUse达到20，SQL最终WaitCount增287、累计WaitDuration增141.141秒，Redis等待增160、累计24.172秒，随后恢复。该累计值是多请求并发等待之和。有效宿主CPU最高52.7%，API最高45.9%单逻辑核，GC总暂停只增约63.9ms，不能宣称CPU打满或秒级GC造成。

当前证据只支持瞬时排队，不能确定其唯一根因。诊断端点时间相对采样起点也突然迟约1.889秒；Docker/本机网络、调度停顿与应用放大仍需区分。源码中Cache全局publication门确实持有Redis写/删网络操作，所有Get/GetMany在L1命中前也取epoch门，存在跨key队头阻塞路径；GetMany冷miss也未合并。但没有本次负载内栈/操作分段计时，不能直接说本次由它触发，更不能删除顺序门来换数字，因为它保护旧加载与Invalidate次序。

因此没有继续向更高读档发压、没有改原预算、没有重试到通过再删失败，也未进行无依据的扩池/缓存/驱动改写。下一步应做同条件长尾诊断：在首次等待增加时抓负载内goroutine，分离SQL借连接/持连接、Redis RTT和publication排队时间；证据成立后再最小修复、必要回归和A/B。确切根因未查明，不把性能收尾标成全部通过。

## 这些QPS说明什么

本次hot是一位读者重复GET完整Feed第一页，对应20个公共计数key；multi为32个读者轮转，对应640个计数key。两种工作集均小于4096 L1上限；正文、关系、权限和个人点赞仍读取MySQL。这是访问分布验证，不是Redis单key极限，也不是关闭缓存的A/B，不能把200/100之比叫缓存收益。

实现者已验证的共享机可重复档为完整Feed hot200、multi100，各180秒两轮；500/s仅30秒探索通过。500长持触P95预算、400只有一次通过的失败都保留，旧3db结果不拼作新767c轮次。本人新卷代表档另列，不覆盖或改写历史数据。

用户参考简历的5000+位于点赞/收藏计数场景，而此轮没有测那个同口径接口。本项目点赞仍为MySQL事实优先事务，再派发持久事件。不能凭对方简历猜测其机器、时长、成功比例或持久化语义；也不能拿我们30笔持续落库直接对比对方某个缓存或准入接口。Redis官方[基准测试说明](https://redis.io/docs/latest/operate/oss_and_stack/management/optimization/benchmarks/)也要求考虑负载器、网络、数据和持久化条件；[k6开环模型](https://grafana.com/docs/k6/latest/using-k6/scenarios/concepts/open-vs-closed/)区分发起速率与实际并发，VU数不当QPS。

当前30笔/秒交易及百级完整Feed确实不是数千QPS成绩，不能包装成性能上限或高吞吐已达标。已交索引候选6400→200、批量32key/20轮SQL640→20、单连接饥饿修复、持久去向和恢复证据可分别用于解释工程效果，必须带各自实验范围。是否继续提速，应围绕同条件A/B而非预设简历数字。

## 可选的下一次性能改进

长尾诊断优先。随后可做一个低风险小任务：Feed `zone()` 每张卡片重新LoadLocation，固定20条页至少重复20次；已有旧profile发现明显累计分配成本。复用一次加载后的上海时区对象，不动SQL、个人状态或权限；同数据/负载/时间做A/B，并回归UTC/上海与同秒游标。旧累计alloc_space占比不能写成RSS节省或QPS提升比例，实际收益待新测；它不是本次秒级长尾的已证根因。

其次才分项量化SQL往返和库存行锁持有时间；正式对照如需隔离压测器或Linux部署，须另记录环境，不能与本次Windows/Docker数字直接计算代码收益。不开新业务模块、不预设5000达标，不为数字删除持久化/事务或缓存跨用户个人状态。

## 收尾与范围

[交易收尾](../../../evidence/2026-10-30-go073-cap/qa-independent-6e6af1e78699/trade/teardown.json)与[读收尾](../../../evidence/2026-10-30-go073-cap/qa-independent-6e6af1e78699/read/teardown.json)完成：交易精确撤销25001会话，读经正常logout注销32会话，移除本轮私有会话池/读token输入及随机只读观察账号；API核exe/PID/创建时刻/hash后Windows terminate退出15，四个本轮容器正常stop并保留新旧卷和SQL合成事实。Windows terminate不声称信号优雅关闭。正常PENDING订单事实保留，不为清理直接改状态/库存。

[fresh-audit.json](fresh-audit.json)独立核新逐单映射、原生指标、API/fixture字节hash、生成器环境和资源时点，逐档区分完整性通过与容量预算通过；[offline-audit.json](offline-audit.json)保留历史重算结果。原始大metrics仅在结束后无损gzip，[归档与收尾校验](../../../evidence/2026-10-30-go073-cap/qa-independent-6e6af1e78699/archive-and-cleanup-check.json)保留字节hash、回读一致性与8个新旧卷仍存在的核实。首次凭据形态预检将8个k6空分组MD5误识别为32位token，核对均为native summary的公开root_group.id后仅排除此已知字段，其余命中0；token池已撤销删除，不能冒称事后与全部原token逐值比较。STATUS、README及交接入口同步当前未闭环项。未重新调用真实付费模型，未重做既有业务页面或其它已核验功能。

[Git字节核对](git-byte-audit.json)确认本轮124份原始证据与暂存区blob逐字节相同。原始CLI空白及CRLF保持，手写文档/审计脚本另行通过diff空白检查，本地链接均有效。原先全量空白检查将原始日志CR当尾部空白，并未因此格式化或改写原始证据。
