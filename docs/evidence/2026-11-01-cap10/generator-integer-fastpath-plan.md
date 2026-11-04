# CAP10-GEN-INTEGER-FASTPATH

在endpoint缓存独立提交后，本票只减少Groovy整数校验的临时分配。H2真实50ms违规未被GC日志归因；本票不承诺消除抖动或取得正式容量。

原integer对每个有效整数先toString、构造BigDecimal并stripTrailingZeros。标准Byte/Short/Integer/Long现在直接以long校验原0..9007199254740991范围；标准BigInteger使用一次初始化的同值常量比较。BigInteger子类、BigDecimal及其他Number仍使用原表达式；Float/Double、布尔、字符串等原边界不变。classify只复用一次version校验供valid与observedVersion使用，JSON解析、likes短路校验、全部输出字段与分类规则保持。

不改Map记录结构、128帧/持久ACK、token脱敏、wanted/version推进、ordinal、原始elapsed、50ms或park/stop/handoff，不增加线程/引擎。重生成公共示例JMX并更新实际工具身份，不把旧MD5、原FAIL或c464业务API CI冒作新工具证明。

直接回归覆盖所有标准整型边界、MAX_SAFE相邻值、BigDecimal的整数/小数/负零、Float/Double非有限值、其他Number与BigInteger子类fallback，以及原HTTP分类的完整字段和准备noop规则。旧原谓词与新实现做有界同输入比对；有限本机分配观察只报告该函数的ns/op与B/op，不能当QPS或云端GC因果证据。

本机8项聚焦检查、原38项合同及9项准备运行时检查、11项bundle检查全部退出0；示例JMX由原builder再生，差异检查通过，无HTTP/SQL/云或负载窗口。

有限Corretto8同JVM的ABBA观察，各20万次调用且返回计数完全相同：原谓词约261.0–262.1 B/op，新路径约52.0 B/op；对应约381.6–415.9 ns/op与157.1–251.9 ns/op。ThreadMXBean计入循环和Groovy dispatch，存在JIT及顺序差异，这不是统计基准或JMeter整体分配率。原类只改名后与新类同进程对照，输入原件与输出均保留；首次私有探针调用路径错误未执行，原错误也保留。它不证明PTS实际GC原因、50ms违规已消失或业务QPS提升。

独立审查待本票冻结后记录；仍须后续真实候选和两轮完整正式组同时通过原门槛。
