# CAP10-KAFKA-MEMORY-PROFILE：同机 Kafka 2GiB 资源合同

H2 正式窗口在 Kafka 原 1GiB 配额下触发既有 85% 内存守卫，原 FAIL 保留，H3 未运行。事后只读证据中的 Kafka heap 为 Xms256/Xmx512，OOM 和重启计数均为 0；退出后 working memory 下降不能改写当窗资源失败，也不能据此称发生了 OOM、内存泄漏或 API 业务故障。

新增显式 `mysql8-kafka2-v1` profile，在原同一主机内把 Kafka 配额改为 2GiB。它相对 `mysql8-v1` 只有 Kafka memory 不同：MySQL 仍为 8CPU/8GiB，Redis 为 2CPU/512MiB，Kafka 为 2CPU/2GiB，Elasticsearch 为 2CPU/1GiB。`baseline` 与 `mysql8-v1` 的定义保持原值，历史证据继续绑定原 profile。

Python 资源合同给出固定限额；Node 与 Go observer 的 metadata 白名单接受该精确名称，并继续拒绝私有配置与 metadata 错配或任意未知 profile。env、idle、resources 和 grade 沿用同一资源合同取得配额；85% 守卫本身不变，新分母必须与实际 cgroup limit 相符。没有新增任意内存参数，也没有让原 baseline→MySQL8 的资源执行器自动改变 Kafka。

实际配额变更是后续单独的、默认仅计划的私有动作：同 CID、镜像、卷、网络、端口、CPU 和 JVM heap，仅更新 Kafka memory 1GiB→2GiB；保持额外 swap allowance 1GiB，因此 Docker `MemorySwap` 从 2GiB→3GiB。它必须记录实际 before/after、boot、进程身份、完整四服务保护、host free 与 cgroup swap 使用、累计 memory.events 及差值；不能把历史计数写成 0，也不能推断未使用 swap 的性能影响。不重启 MySQL、不改 redo 或持久化、不删除数据，不采购新实例。

新 metadata 必须独立保存 Kafka 变更证明，原 MySQL/redo 证明及其 SHA 保留。新 profile、API/PID、工具、空载、candidate 和正式 freeze 重新绑定，不能把旧 1GiB profile 的空载或 H2 失败重标为新资源证据。真实 native 失败的旧正式组需要独立闭合与新组接续合同；本资源票不修改该规则，不把 H2 当作新组的一次成功。

本地直接验证：Python 资源合同 5 项 PASS；Node 现有 checker 39 项 PASS；observer 相关脚本 37 个顶层测试、92 个子例 `-race` PASS（包耗时 2.016 秒），同范围 vet PASS，并从固定源码交叉构建新 Linux/amd64 observer。测试未连接云端或真实 MySQL/Kafka，没有运行负载；API `c464c1fa4d153806fb44ac746312552655ebda63` 及业务源码不变。此票没有新 CI、实际配额更新成功或稳定 QPS 结论。
