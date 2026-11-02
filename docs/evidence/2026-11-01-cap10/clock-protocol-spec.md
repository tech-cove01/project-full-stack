# CAP-10-PTS-CLOCK：独立时钟采样与持久化 ACK

本票只调整测量协议，不改 Go 业务代码、真实 HTTP 计时、原生窗口或容量通过标准。此前所有失败原件保持不变；不会据此重判旧窗口，也没有预写性能收益。

## 问题与取舍

当前 Groovy 将带 `serverUnixMs` 的每个响应同时当作时钟样本。批次必须先完成校验、journal 写入和 fsync 才返回 ACK；把这些排队时间绑定到时钟 RTT 会混淆两个职责。离线模型和回环探针已保留成功及失败，最高尖峰尚不能单一归因于 GC、状态表扩容或云端环境。

选择独立、已认证的 `GET /clock`。它只返回固定运行身份和当前宿主 UTC，不取得 handler/store 的记录锁，不 tick、不更新资源或心跳、不写 journal。持久化批次仍使用原 seq、SHA、重试、fsync 与 ACK，安全 control、共同起始屏障、资源监督、15 秒心跳和最终全记录核验继续原规则。

## 显式身份与兼容

- 新生成 bundle 的 manifest 与 JMX 变量 `cap10.clockProtocol` 显式为 `dedicated-v1`；packet、私有 config、binding、admission、phase 及 collector 必须同值。
- 原 `sourceSHA256` 四个源文件键保持不变，更新的是对应字节 hash；新字段另行绑定。不会覆盖旧 packet/admission/JMX。
- 缺少 `clockProtocol` 永远表示旧 `embedded-v1`，保留旧响应采样规则和旧 identity 形状；显式 null、空值、未知枚举或两端模式不一致均拒绝。新生成/冻结 packet 不能靠删除字段冒充旧包。
- 新客户端先取得并严格校验 control 身份，完成专用时钟校准后才注册私有分片；后续 reportLoop 继续定期检查冻结 anchor。旧模式沿用原路径。

## 时钟接口与客户端规则

`/clock` 使用现有 Bearer 认证，并要求 `X-CAP10-Run-ID` 与 collector 的不可变 runId 相符。只接受 GET，响应固定为 `version`、`clockProtocol`、`runId`、`phase`、`serverUnixMs`。错误认证、方法、运行身份或模式不成为有效样本，也不通过记录锁伪装成快速成功。

新模式只在 `/clock` 上执行原时钟判断：RTT 推导不确定度 `RTT/2+1 <= 50ms`、本机墙钟与单调钟差不超过 50ms、冻结前选择更小不确定度、冻结后继续用原区间一致性判断漂移。原生 HTTP 开始时间仍由冻结的单调 anchor 推导，不夹到计划时间、不放宽 50ms 发起迟到限制。

新模式下 `/control`、`/batch`、`/register` 和 `/complete` 中的 `serverUnixMs` 保留为原始元数据，不能再校准或修改时钟 anchor。迟 ACK 不能提前确认业务结果或删除未 ACK 的队列前缀。原重试与超时没有取消。

## 最小验收与限制

1. 持有实际记录锁并阻塞一个真实 batch/fsync 时，已认证 `/clock` 仍能完成；clock 前后不能修改 journal/hash、facts、资源或心跳。
2. 错误 token/runId/method/protocol、显式 null、缺失或漂移身份拒绝；旧缺字段保持 embedded 行为。
3. RTT 超过 98ms、墙钟跳变和冻结 anchor 漂移继续拒绝；迟 ACK 本身不再成为新模式时钟错误，仍只按原 durable ACK 规则处理。
4. 新 bundle/JMX/manifest/config/packet/binding/admission/phase 的协议一致，任一删除/错值不得取得新冻结准入；原四键 source map 保持。
5. 九客户端本地探针分别报告 clock RTT 和 durable ACK RTT，使用实际生产 handler/listener 与真实 fsync。它只证明本地协议，不代表真实 PTS 九引擎、180 秒业务负载或 5000 QPS。

`/clock` 仍共享监听队列、Python GIL 和宿主 CPU；严重 GC、网络或调度停顿仍可能超过原时钟预算，此时继续失败。协议改动不能代替新资源 namespace 的真实空载、PTS 网络/资源准入、事实对账和正式业务窗口。
