# GO-030a / Redis 准入与稳定受理 ID

开始 SHA `e410c71d9d8dfeaf654e203e439fa3f871c9afd4`；依赖已交付 GO-021（用户另行独立核验）。本小功能只交付准入组件，不注册 HTTP、不启动消费者或支付功能。

复用原八个 KEYS、七个 ARGV 顺序和业务返回码；Go 内部返回 `{code,stringID}`，避免 Lua double 和返回旧 ID 后二次读取的竞争。新准入先完整预检所有键/整数/销售窗/phase/version/候选预约与 Stream 上限，再扣库存、写用户标记/预约/受理映射和 XADD。缺时间不使用 fallback 补开，已有预约 ID 不覆盖。已有本人受理可在售罄、下架或销售结束后找回，既不再扣库存也不追加消息。

ID 沿用 Java 上海 wall-clock 高位与 `icr:order:yyyy:MM:dd` 日序列命名，Go 整数计算保持 signed BIGINT 精度；脚本校验序列类型、无 TTL、非负规范值及 32 位上限，不重置/回绕旧序列。时区兼容表达不等于 UTC 事件时间。Redis 序列丢失后的核对/切换策略属于 GO-033/071，不能据此宣称灾难后保证不碰撞。

自测：gofmt、普通测试、vet PASS；[真实 Redis 日志](admission-real.txt) 中准入 9 主组、ID 4 主组、accepted 恢复 1 主组 PASS。含 8 类错类型零部分变更及修复、坏参数/别名键、MaxInt64 精确返回、TTL、同用户 20 请求一新受理和 19 原 ID、200 用户抢 100 库存得到 100 受理/100 售罄、缺键/错值/关闭/结束及 Stream ID 耗尽。并发是短突发正确性，不是持续吞吐成绩。

测试使用私有 Stream/计数器和随机精确 ID/键，全部清理，不消费/删除生产 `stream.orders` 或回拨真实序列。未执行 Linux race、远端 CI、Redis 崩溃/数据丢失或持续负载。本子功能自测通过待独立核验；数据库落单、消费者和页面分别后续小提交。
