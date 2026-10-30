# CPU初次采样区间修复

旧采样器先prime `cpu_percent(None)`，随后立刻取首值；极短区间可能被CPU时间取整放大。hot100首值API530.8%不能作稳态归因，原始数据保留。

新脚本保留raw值，标记API/host的 `cpu_interval_seconds/valid`，k6另外记录其自身prime区间；低于1秒的区间不计入host85%连续三样本预算，RSS/内存/身份/诊断/容器及缺观测护栏保持原值。配置记录采样器源码SHA256。Docker stats的CPU有自身采样区间，不能把parent priming标记解释成Docker观测失败。

Python AST与diff检查通过；cap_observe非作者只读审阅无阻断。真实 [hot150资源](hot-150-a-control/resources.ndjson)首个parent区间0.0025681秒、k6约0.0000225秒均为false，后六个约5秒区间为true，与设计一致；该轮7/7完整资源样本，HTTP4501/4501且原预算通过。报告只以有效CPU区间作归因，旧样本保守排除priming；其它原始资源与错误不会删除。
