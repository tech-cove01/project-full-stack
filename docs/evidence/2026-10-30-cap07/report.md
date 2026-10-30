# CAP-06V / CAP-07 交付与未通过项

2026-10-30。实际 Feed 公共卡片 L1/Redis 缓存、批量内容装配、同进程同缺失 batch 的 singleflight 已实施并通过功能自验，固定 B 的 Linux CI/race 通过。**本轮容量验收未通过：B1 长尾失败，不能用 B2 成功补齐，CAP-07 整票和最终项目收尾尚未完成。** 原秒级长尾仍未归因；本批没有新增业务、付费 Provider 测量或第四轮完整诊断。

## 已实现与验证

| 小功能 | 提交与证据 |
| --- | --- |
| CAP-06V 已证结构缺陷 | `f3d99ee43dfb422a0cba525de93e9266f07c4ee5`；[同用例旧 FAIL→新 PASS](controlled/report.md)。全局 publication 门改为4096固定分片，同 key 失效代次保持，碰撞仍会等待；不等于历史事故归因 |
| CAP-07A2 实际 Feed 缓存 | `4d60b0201ee93ac43cb94cfd50e869ddb6c8ea3d`；[真实 HTTP 冷1批2行、热0内容SQL](phase1.md)，当前授权/公开版本/店名/个人点赞/游标仍由事实目录决定；事务结束后借池；版本/Busy至多一次新RR完整回退 |
| CAP-07A3 同批合并 | `47938143b7c429618c30163675557bc90e6ac1a7`；[真实8 HTTP/首caller取消/7成功、1flight7join1SQL2行](phase2.md)，版本晚归、坏值、过期、断开client、真实慢pipeline关闭及所有上限/复制验证 |
| 排队测试修正、固定B | `30b6571227b5508d0f04bc8c4cfc1bcfbc313275`；[明确观察共享队列后取消](queue-test-review.md)，生产代码同A3；[精确SHA CI四项成功](https://github.com/tech-cove01/project-full-stack/actions/runs/41046300775)，包括Linux全包race及真实组件/HTTP回归 |
| CAP-07B 原始对照与收尾 | `980d01556951d9ae3da60dc9589b83d018cd62b8`；[native复算](audit-final.json)、[213份输入散列](raw-manifest.json)、[213份Git字节全部匹配](git-byte-audit.json) |

无额外 schema 迁移，保留001—013、原公开计数语义及TTL。新组件只存 ID/version 对应的标题/正文/图片，L1 2048条/8MiB编码载荷、单卡8KiB、Redis60—69s、64 flight/8 loader/1024 miss caller、共享2s/Redis250ms上限；超大合法正文完整返回但不缓存。没有个性化整页缓存、逐key跨batch全局合并、动态hotkey或详情singleflight的新接入。

[普通全包、六包真实回归与vet](phase2-checks.json)通过。[内部非作者审查](internal-review.md)分别审实现、测试和工具；不等于用户外部独立验收。CI的frontend成功单列，不冒称本地全站浏览器或真实AI复测。

## 全部预定对照

固定新6400篇/32作者/32读者夹具，正文256B、页20、multi100/s×180秒，预热10秒/25/s，heavy diagnostics关闭、5秒资源采样。A为受控修复后无正文缓存的f3d99ee；B为最终30b6571，两份clean detached构建/exe hash均冻结。详见[完整容量记录](harness-report.md)。

| 轮次 | 发起 / 成功 / unknown | P95 / P99 ms | 原预算 |
| --- | --- | --- | --- |
| A1 | 18001 / 18001 / 0 | 28.2026 / 36.3832 | 通过 |
| B1 | 5788 / 5787 / 1 | 40.82359 / 1449.571716 | 约58秒原生早停，失败 |
| A2 | 18001 / 18001 / 0 | 29.7205 / 45.3673 | 通过 |
| B2 | 18001 / 18001 / 0 | 29.8148 / 39.8796 | 通过 |

四轮内容/系统/HTTP错误与dropped均0，无资源stop。B1的unknown、P99和全计划180秒分母下发起比例仍使其失败。A2虽P99通过，也有max789.2475ms和池等待，不能说A完全无长尾。6阶段（含两个只作接线的短probe）[复算](audit-final.json)工件/协议算术错误0，`finalBProtocolPassed=false`。未保存逐响应时延分布，分位来自各轮native summary；不合并分位，也不把汇总核对称离线重建。

B1/B2独立内容SQL分别151批/1192行和437批/2005行，口径含预热至收尾；不同初始L1状态及TTL成本不能写成稳态收益。A无组件计数为null，不当0。全实例SQL重叠计数与内容loader分开。当前没有一致HTTP改善，也没有可重复通过的B100档，不能宣称容量提高或最高QPS。因B1已失败，后续阶梯、邻近、最高档持有及hot200均按既定门控**未执行**，不是通过。

## 未知风险与下一步

B1末采样/最终端点可见SQL与Redis等待增长和内容回源排队，loader无错误/拒绝、状态在界内；A2也有池等待。现有观测不能区分纯池等待、存储执行和宿主停顿，更不能证明新缓存或原计数门是本次触发根因。CAP-06V仅证明被屏障暂停的写/删存在跨分片隔离问题及其修复，旧1.137s事件、本轮B1 1.45s现象与新缓存效果分别保留。

下一步交独立agent审功能及失败材料，可按[新卷代表档](representative-rerun.md)只复跑固定B100×180一档；新QA一次通过不补实现者旧失败。后续定位应从已留失败窗口和读链路出发确定最小范围，不盲扩池/改TTL、不换tag重开本组或补成功轮。容量通过前不更新求职吞吐成绩或宣布项目最终完成。

## 清理

[cleanup](cleanup.json)记录32次正常logout、Redis会话剩0、observer删除核验、四个API进程均按身份停止，最后API14328停止不冒称Go优雅退出；两精确CID均exited/exit0/OOMfalse，旧新卷和SQL事实保留。6份read私有env及token输入回收；受控测试依赖在负载前停止留卷，其[唯一私有env也已精确删除](controlled/private-input-cleanup.json)。未修改或停止CRM等其它服务，未清理未知目录或卷。
