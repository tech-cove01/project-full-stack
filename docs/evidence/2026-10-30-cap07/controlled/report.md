# CAP-06V 单次受控验证

2026-10-30，原秒级长尾仍未归因；按用户决定带该未知风险推进 CAP-07。本批是屏障控制的正确性验证，不是第四轮容量诊断，也不代表 CAP-06D 通过。

同一测试分别暂停自有 A 的 Redis SET/DEL。确认进入后，在释放前采集阶段活动数与有界 goroutine 栈，验证热 B、同 key 等待者取消、同分片碰撞等待者取消，以及旧 SET 与 Invalidate 的代次顺序。进入、等待、释放都有屏障和超时；未用 sleep 猜测并发。

- `before-source.json` 固定 52cd5c4015d2ac4b745f1bea9006e8dba4e359e9 的 counts.go 字节；Go overlay 只替换该源码。同一真实测试旧实现 SET/DEL 均 FAIL：热 B 在 A 放行前达到 300ms 截止期限。
- 最小修复把一个全局 publication 门和代次改为固定 4096 个惰性分片，逐 ID 获取代次，不持多个门。相同 key 的发布/失效顺序保留；分片碰撞仍可能互相等待。没有扩池、改 SQL、TTL、计数语义或增加动态 key 锁表。
- 新实现 SET/DEL 均 PASS；B 在 A 放行前返回，两个等待者独立取消，最终读取为新事实。见 `after-test.txt` 和 before/after 子目录原始记录。纳秒耗时为时钟分辨率下的观测，零值不表示零成本。
- 真实 MySQL 3452 / Redis 6552 的 communitycache、social、taildiag 回归及对应 vet 均 exit 0，见 `related-checks.json`。专用旧测量用例按其开关显式跳过；没有重跑页面、付费模型或容量。
- 非作者内部审查由 cap07_delivery 完成，未发现阻断项；独立外部核验仍待交付。

栈采集发生在 A 尚未释放且 publication 等待活动数至少 2 时，未截断；阶段为直接缓存调用，Feed/鉴权阶段没有请求。SQL/Redis 包装只支持所述采样范围，没有独立纯池等待分解。它证明跨分片隔离的结构缺陷及修复，不证明历史 1.137s P99 的触发原因，也不能用后续卡片缓存结果覆盖历史失败。

复跑：在独立迁移好的 MySQL/Redis 上设置 GO_INTEGRATION=1 和相应配置，运行 `go test -count=1 -run '^TestIntegrationCountPublicationIsolation$' -v ./internal/communitycache`；CAP06V_EVIDENCE 指向新的空输出目录才会写采证文件。原始失败记录保留，不覆盖重试。
