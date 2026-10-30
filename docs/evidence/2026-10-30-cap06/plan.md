# CAP-06 多读者长尾：事前诊断与验收计划

任务依据：根[STATUS](../../../STATUS.md)的CAP-06A—D。起点工具HEAD为`c89da96`（完整值由根实际记录），产品仍是767c修复后的Feed；上轮[独立核验](../../reviews/2026-10-30/go073-cap-09b4921/report.md)multi100约120秒因P99=1137ms早停，原失败不改写。本文件是执行前的范围，不是新实验通过报告。

## 目标、候选与选择

目标Feed要读当前关系/公开状态及本人点赞，同时从公共计数缓存装配一页20条。旧数据只显示约115秒SQL/Redis排队与诊断响应迟到，不能据低CPU直接认定缓存门、数据库或宿主为唯一根因。现候选包括publication全局门持Redis网络操作产生跨key等待、SQL多次往返/连接占用、Redis池/网络及共享宿主调度停顿。先增加有界请求分段与负载内栈，避免凭总池累计值扩池或删除顺序门。

取舍：复用现有loopback诊断与k6/资源监督器，不部署新平台。请求观测默认关闭，开启也只有固定stage名、有限聚合/slow ring，无用户/键/正文/SQL标签；SQL/Redis的单独“借连接时间”若无法从现有驱动可靠区分，明确标缺失，不用整条命令时间伪装。额外1Hz抓样与最多三次goroutine属于诊断开销，不计正式容量。

## 隔离环境与固定工作负载

- 新随机自有卷，项目固定`project-full-stack-cap-read`，MySQL3451/Redis6551/API8251/诊断8351。只在核对已移交且停止的原容器ID、标签和端口后，用显式fresh-volume override替换这两容器。旧卷与owned网络保留，不清库/flush/prune，不关闭CRM或他人服务。
- 私有runtime在Git仓库外，Windows显式owner ACL；随机observer密码、32个token输入及必要配置不打印。记录新容器不可变ID/镜像/挂载、API exe/PID/UTC创建时刻/干净构建/health和二进制SHA，后续切修复版只换API、夹具原字节保留。
- MySQL4CPU/2GiB、Redis2CPU/512MiB、API GOMAXPROCS4/GOMEMLIMIT1GiB/SQL池20/Redis池10、正常持久化和013索引保持。新合成夹具6400篇/32作者/32读者，每作者200篇、正文256B、每读者关注一作者；页长20。multi均匀轮转640计数key，hot为20key，Kafka/ES/AI关闭，响应`source=database`。
- 开环负载沿用真实鉴权HTTP与每页内容校验。正式180秒、预热10秒/25次每秒，pre256/max512VU；缓存TTL、抖动和自然过期保持。请求发出、native HTTP、终态分类、成功、错误、unknown、dropped和全/成功分位分列。

## CAP-06A 探针与观测说明

先1次/秒、5秒短探针核真实夹具/分类/SHOW与Redis访问/资源观察。需要比较应用观测开关开销时，用同一干净二进制、相同短探针协议分别启动关闭/开启的API；只报实际墙钟/分配/分段次数与范围，不用短探针声称容量或因果收益。开关名`GO_FEED_DIAGNOSTICS`，另需显式loopback `GO_DIAGNOSTICS_ADDR`。

原资源sampler仍5秒一次、原安全预算不变。诊断collector额外约1秒读取capacity：每poll记录计划与实际单调时刻、调度迟到/错过间隔、HTTP请求开始/结束/墙钟、响应内app时刻。各值时点不同，不能当原子快照；超时/失败样本单列，任一失败使collector退出非0，部分证据仍保留。

## CAP-06B 有限诊断（上限三轮）

启动前固定最多**3轮multi100/s、稳定180秒**诊断。`cap06-stage.py`以exclusive `diagnostic-slot-1/2/3.json`预约，失败启动也占名额；不能新tag无限重试。达到原SLO/资源预算就由原harness停止，完整失败保留。

额外collector触发条件预固定：SQL或Redis全局WaitCount有正增量、累计WaitDuration增量至少100ms，或新完成slow trace总长至少200ms。保存触发前最多20秒（另有最多21样本上限）与触发后最多10秒的公开时间序列；整个stable时间序列持续落NDJSON，进程结束会截断后窗并注明。

每轮goroutine **最多3次（包括失败尝试）**，最小间隔15秒；每次仅标准二进制goroutine profile，最多5MiB、3秒超时。capture前后核实本轮stable k6的PID/创建时刻/脚本/父子身份仍活；若抓取间退出，不称全段负载内。无CPU profile/native内存转储，不抓cmdline或环境。capacity响应2MiB上限，与32×256span的有界ring匹配。

按“现象→等待位置→候选→受控验证/排除”报告。三轮仍未复现或证据不能解释原事故，交原因未确认/已排除项/下一最小诊断，状态未闭环，不继续无限轮次，也不进入CAP-07抵消原失败。

## CAP-06C/D 修复与正式门槛

只有可验证证据支持才做最小修复，一问题一commit；保留授权/快照/游标/个人状态、缓存加载与失效顺序、代次隔离、取消和TTL。对已证机制做确定性旧FAIL→新PASS或受控故障验证，直接真实回归/gofmt/vet及精确SHA CI按STATUS执行。

关闭重型collector后，multi100/s×180秒连续一组两轮与hot200/s×180秒至少一轮；同参数采用相同轻量应用观测。两轮任一失败，不能挑后续成功补齐，先解释新证据/修复或环境差异，再开启新一组。正式判据保持：发起≥99%、成功≥99%、系统/HTTP各<1%、内容0、unknown0、dropped0，全/成功P95≤200ms/P99≤500ms。

安全预算保持：host内存88%、API RSS1.5GiB、k6 RSS2GiB、自有容器内存85%、host CPU85%连续3有效样本或连续3不完整观测即停。采样不能保证捕获瞬时峰，累计WaitDuration不是单请求时延；环境变动另列同源码对照，不包装为代码收益。

结束按[runbook](runbook.md)精确注销本轮会话/删observer/核原API身份停止/按不可变CID停止两容器、保留卷与SQL事实。中途失败保存脱敏progress，私有位置保留恢复，不先删除凭据阻断收尾。自测、内部审读与CI不代替外部独立核验。
