# CAP-08 工具：缺少异常终态字段必须判为不完整

独立核验交付`2c1ef04b7b1d1ae10d98767e18e6c12ff697166c`时，发现两个策略谓词使用`stage.get('failedStartOrException') is None`，缺字段与正常显式null被视为同一情况。当前writer明确保存该字段；不完整记录应当拒绝进入下一正式阶段或构成受限容量括区。

新增公开工件行为反例：其余条件满足，仅删异常终态字段，`passed`和`measured_slo_failure`均错误返回true。旧代码下两个子项实际FAIL，见[原输出](guard-before.txt)及[退出码](guard-before-exit.json)。修复只把两处读取改为显式索引，沿用既有KeyError处理返回false；正常null、真正异常及未退出行为保持。全部12项纯检查PASS，见[输出](guard-after.txt)及[退出码](guard-after-exit.json)，另经非作者独立审查和纯检查。

三轮实际工件均有显式null。复核diagnostic-1/formal-1仍passed=true、formal-2仍false；本批没有可构成受限括区的阶段。此修复不改Go应用、原516测量工具历史、原始结果、阈值或名额，也不重跑容量、消除unknown/dropped或把正式组改通过。
