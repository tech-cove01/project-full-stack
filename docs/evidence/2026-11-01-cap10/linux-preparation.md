# CAP-10 Linux 执行准备（尚未连接主机）

依据本批环境选择说明、用户可准备云机的追问及已审最小方案追加；本机checkpoint为ed9f91a7bd28f46e1ae0782c768870b9fef3eee8。此处只有离线工具适配，真实Linux准入/容量结果均未产生，不计生产性能优化，也不重新发放预算。

- 同一批根保留Windows71份原件；Linux实测固定写linux/，平台必须与Python/Node/Go实际OS匹配，不能任意覆盖路径。
- Linux环境创建前必须有一次environment-transition.json：用户提供的主机/授权引用、switchCount1、原env/idle/cleanup/manifest/handoff哈希、S/H/优化/主负载已用0、8/2/840s/一优化及10800s最大有效工作预算。原handoff已保守计4200s，离线Linux适配还须加账。
- 无电池服务器明确AC/saver为null及sysfs依据；实际采boot_id、UTC、MONOTONIC、BOOTTIME和不确定度。逐5秒及首尾拒boot变化、suspend/壁钟差超过0.25s+不确定度；不假称读取Linux journal或排除全部hypervisor暂停。MySQL Uptime原门槛保持。
- 初始API/四依赖/发压器仍同机、同六端口和原配额/池/TTL/持久化。新会话、新卷；私有目录0700、配置0600，先登记再写秘密，TMPDIR明确指向私有tmp。
- Windows事件查询仍只末窗一次；中途轻量检查只读已有snapshot数值。power异常先停止/锁存，晚建control也补发停止，不以恢复洗掉失败。
- 原生主窗60/180、源/平台/transition绑定、候选与同批名额不变。只扣terminal stage已证loadWait区间，裁剪/合并后计一次；不回扣原4200s，不为换环境重新开始3小时。
- 主机实际到位后先核归属/架构/工具/资源/时钟与健康，完成该环境必要一次准入，再用余下S1=50/s×60启动。不会先发压后补说明，也不重跑Feed/hot或追加核验容量轮。

已完成离线检查：34Like、21runner、34策略、15平台纯反例；五个真实CLI导入/help smoke通过；Go观察器Windows/Linux amd64均编译成功但未执行。Linux环境、实际脚本接线与负载仍须新机验证，纯检查/交叉编译不能代替该准入。

静态网页材料独立见showcase-capture-plan.md，只规划现有采样可支持的图表和真实业务截图，不生成未测成绩或新负载。常见云压测PTS主要提供发压编排/报表，当前被测系统仍需运行环境；本批已有k6与深度对账，普通Linux云机能更直接复用全部工具。
