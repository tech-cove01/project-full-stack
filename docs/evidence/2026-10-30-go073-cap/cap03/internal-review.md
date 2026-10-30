# CAP-03 内部审读与离线交叉复算

2026-10-30。最终先运行 [auditor](internal-evidence-audit.py) 的 `--check-only`，明确指定 API 源码 `767c2fc8bdf1be08c5636143a71898eec25081a9` 和下表四个目录；确认收齐后，再去掉该开关独占生成 [internal-evidence-audit.json](internal-evidence-audit.json)。**54 个可复算阶段＋1 个不可计分中止，共 7130 项检查，0 失败、0 读取/格式错误、0 pending。** 54 个阶段包含原始受限与诊断轮，不表示它们全部达到业务预算。

复算核对 native/terminal/started/success、未知数与错误分母、导出的数值分位、退出/停止状态与预算、SQL/Redis 原快照差、CPU 有效区间及来源链。分位是与原生导出值交叉核对，没有从未保存的逐响应样本重新计算；legacy threshold 布尔不作通过依据。审计 JSON 的 SHA256 为 `c4ec564a62a309ed75b18eec13f83e97582225655a3c31c95264ee3c1c6b4c43`，生成后未修改。

## 最终仅选择新 API 的四轮

| 阶段目录（均稳定180秒） | 模式/目标请求率 | started＝native＝terminal＝success | P95/P99 ms |
| --- | --- | --- | --- |
| [hold-fix-hot-200-180-a](hold-fix-hot-200-180-a-load/result.json) | hot / 200/s | 36001 | 25.377 / 43.451 |
| [hold-fix-hot-200-180-b](hold-fix-hot-200-180-b-load/result.json) | hot / 200/s | 36000 | 25.371 / 68.021 |
| [hold-fix-multi-100-180-a](hold-fix-multi-100-180-a-load/result.json) | multi / 100/s | 18000 | 89.152 / 188.785 |
| [hold-fix-multi-100-180-b](hold-fix-multi-100-180-b-load/result.json) | multi / 100/s | 18001 | 17.553 / 21.873 |

四轮 unknown、dropped、系统/HTTP/内容错误均0，数值与退出状态重算预算通过。每种模式的两轮速率相同，不以旧 API、短诊断或不同速率拼轮。API source/revision、command→元数据字节散列/PID→[干净构建](build-767c2fc/build.json)均匹配；[原夹具](fixture-01/fixture.json)仍为3db来源，实际原字节散列 `7e3a9a9019c7fffc35dbbffd1c66f88ca65db2808faee1d263017698e2501754` 未变，未用夹具 SHA 代替 API SHA。

## 资源覆盖与非零等待

四轮均 **37/37 完整样本，parent 与 k6 各36个有效CPU区间**；首个极短 priming CPU 值保留在 raw，仅不参与CPU归因。以下峰值为采样值，API/k6 单逻辑核100%，不与宿主或Docker口径相加。

| 阶段（按上表顺序） | API/k6/宿主 CPU峰 % | 宿主内存峰 % | 首末采样起点跨度 s |
| --- | --- | --- | --- |
| hot A | 86.5 / 32.5 / 50.0 | 76.4 | 180.032 |
| hot B | 86.2 / 31.9 / 61.7 | 77.2 | 180.029 |
| multi A | 67.2 / 31.2 / 80.0 | 76.9 | 180.029 |
| multi B | 46.2 / 20.3 / 42.7 | 76.6 | 180.022 |

| 阶段 | SQL等待：次数 / 累计s | Redis池等待：次数 / 累计s | GC次数 / 累计暂停ms |
| --- | --- | --- | --- |
| hot A | 232 / 8.499 | 89 / 1.666 | 483 / 92.613 |
| hot B | 334 / 31.971 | 194 / 26.982 | 481 / 239.638 |
| multi A | 136 / 3.056 | 40 / 0.684 | 250 / 36.673 |
| multi B | 0 / 0 | 0 / 0 | 250 / 18.365 |

以上差分取首末完整采样的实际计数，Redis timeouts均0。等待时间是并发累计值，不是单请求时延；资格通过不等于没有池等待。四轮均无资源 stop 或观察到的预算命中，5秒采样仍可能遗漏短峰，不能证明任何瞬时资源都未受限。

首点在k6 spawn后约1.173/1.079/0.984/0.970秒，最大采样间隔约5.003/5.003/5.005/5.001秒；最后采样起点早于stable命令结束约1.628/0.283/1.540/0.359秒。各 section 顺序采集，最后一次约1.30–2.23秒的采样工作会部分跨过命令退出，按采样总耗时估计分别跨约0.329/1.952/0.114/0.939秒。因此这不是原子快照、逐请求全覆盖或完整退出后服务端排空观测；end收尾记录不补齐覆盖缺口。

## 不可计分中止与边界

[post-fix-multi-500-pre512-a 的原始中止](post-fix-multi-500-pre512-a-load/unscored-failure.json)单独保留：Node退出 `3221226505 / 0xC0000409`，原因未知。auditor核对原exit-status字节SHA、非0退出、无force/resource stop、无stable前置 `before.json`、无stable进程/summary/result、sampler未启动及资源样本0，才将其列为unscored；业务计数为 `null`，既不补零也不当作一次已测业务失败，不能进入最终选择。另一次重试的成功不覆盖该中止。

本说明为**内部源码审读与同批原始文件的离线交叉复算**，不是外部独立验收、现场二进制重新核验或生产容量认证。旧失败、宿主波动、观察成本和参数化SQL digest盲区仍保留；SQL/Redis全局差含后台/观察器，Com_select与Execute不相加，公共计数回源次数仍未知。本文件及审计生成未发送业务、SQL或负载，也未改raw、服务或已生成JSON。
