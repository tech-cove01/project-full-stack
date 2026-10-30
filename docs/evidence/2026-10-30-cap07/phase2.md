# CAP-07A3 同批公共内容回源合并

`phase2-checks.json` / `phase2-related-tests.txt` 记录自有真实 MySQL/Redis 六包回归、全包普通测试和对应 vet。FC-01—07 的功能自验已通过；FC-08 的精确源码 Linux CI/race 及容量协议另行记录，尚不代表外部独立验收。

真实 HTTP 冷突发采用 SQL 进入/释放屏障：8 个 caller，确认 7 个已加入同一 flight 后取消首 caller，首 caller 得到 context.Canceled，其余 7 个 HTTP 成功；1 flight、7 join、1 内容 SQL/2 行，随后的热请求没有新增内容 SQL。它证明实际业务合并和取消隔离，不是持续 QPS。完整投影/精确缺一条投影的 HTTP 结果分别使用 projection/database source，公共片段复用且页序、授权卡片保持。

共享 key 是排序去重后的 ID/version 缺失集合；不同/部分重叠 batch 允许独立回源。worker 从应用生命周期派生 2s deadline，首请求/其他等待者仅取消自身等待；64 flight、8 活动 loader、1024 miss caller 上限保留。短 mutex 只保护 map/准入和发布结果，不持有做网络操作；Close 排除新增工作后取消并等待 caller/worker 收敛。每个响应复制 map/Images。普通受控测试核对反序/重复集合合并、不同批次、队列超时、全部等待者退出及晚 v1 只回填 v1 键、不覆盖先完成的 v2。

真实组件回归核坏 JSON、images:[null]、错误 Redis 类型和超大 payload 的重建；只对自有 key 设置短 PEXPIRE，观测真实自然过期后重建，生产 TTL 不改；独立断开的 Redis client 降级真实 SQL，完整结果保留；版本缺失或真实 MySQL 查询错误均不回填空缓存。慢写用专属命名 Redis 连接在 pipeline 前插入自有空 key 的 BLPOP，CLIENT LIST 确认服务端确实阻塞后 Close，任务有界收敛；清理先等待该连接消失，再精确删 key/核 EXISTS，避免迟到 SET。

CAP-06V 的旧 FAIL→新 PASS 和计数失效/冷计数单连接回归仍保留。旧秒级长尾归因未知；本次内容缓存正确性通过不覆盖旧容量失败。无全站页面/付费 Provider 重跑，没有新增编辑/下架 API 或个性化整页缓存。
