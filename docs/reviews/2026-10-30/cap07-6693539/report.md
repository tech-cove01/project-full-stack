# CAP-06V / CAP-07 独立核验：功能成立，容量验收未通过

2026-10-30，交付 `6693539ae3715cc28f1b52b7d1583a713659ca4e`，审查差量自 `52cd5c4`。使用隔离worktree `review/cap07-6693539`，未覆盖实现者工作区、旧失败或原始证据。

**结论：CAP-06V已证分片隔离修复、CAP-07公共卡片缓存/实际Feed批量装配/同批回源合并及功能边界通过本轮独立核验，未发现需要追加生产修复的P1/P2。CAP-07性能整组未通过，不能标整票或项目最终性能收尾完成。** 原事故仍未归因；本轮不再重复容量负载来尝试取一轮成功覆盖失败，也没有新增功能或中间件。

## 源码与规格

- [卡片组件审查](cards-review.md)：只缓存版本化公共标题/正文/图片；规范化缺失batch、赢家二次检查、一次批量内容SQL；同进程同batch合并，不承诺部分重叠逐key唯一。共享加载派生应用生命周期、有界排队/超时，首caller取消只结束自身等待；Close取消并等待注册工作，返回对象/图片有复制。64个flight、8个活动loader、1024个miss caller及L1条数/编码字节预算落实。
- [Feed与计数门审查](feed-review.md)：app真正注入卡片缓存，窄目录在RR内保留当前关系/公开状态/版本/门店/本人点赞/游标；事务归还连接后才访问内容和计数缓存。缺版本/Busy至多一次新RR全字段回退，不递归缓存或拼空卡。计数门按4096个固定分片保序，碰撞仍共享，不宣称任意不同key绝对独立。
- [原始证据复核](evidence-review.md)：独立跨工件算术、来源/字节及清理核对，未发现确定的统计虚报；三类结论分别记录：已证结构缺陷及修复、旧事故原因未知、缓存功能和实际A/B表现。

本次交付的Feed已在完整事实快照结束后才借池装配，未发现旧嵌套借池路径仍存在；这不撤销767c前已真实复现和修复的历史缺陷。B1的SQL20/20、cards active8/8说明当时存在等待，不能直接证明死锁或指定唯一原因。共享loader不继承某一HTTP的trace；同步Redis回填仍占用活动槽且有250ms子超时，属于需分段证据才能归因的成本，不能由库名推断性能收益或先改为无界异步写。

## 独立的新卷真实验证

Go1.27.0 Windows/amd64，项目 `fullstack-review-cap07-6693539`，MySQL3463/Redis6563回环端口、预检无重叠网段10.254.63.0/24。新命名卷使用既有compose和资源限制（MySQL4CPU/2GiB、Redis2CPU/512MiB），001/002初始化，版本001—013迁移两次均成功。选取的镜像/不可变CID/挂载/端口见[环境](environment.json)，覆盖见[qa-compose](qa-compose.yaml)。未启动独立API进程、Kafka/ES/真实AI；HTTP范围是Go测试中的真实依赖handler调用，不冒称浏览器、独立压测服务或k6。

```text
# backend；普通六包70个通过项、28个显式SKIP，vet exit0
GO_INTEGRATION=0 go test -count=1 -json ./internal/communitycards ./internal/communitycache ./internal/social ./internal/platform/app ./internal/platform/diagnostics ./internal/platform/taildiag
go vet ./internal/communitycards ./internal/communitycache ./internal/social ./internal/platform/app ./internal/platform/diagnostics ./internal/platform/taildiag

# 根目录；新依赖端口3463/6563
node scripts/migrate.mjs
node scripts/migrate.mjs

# backend；真实六包112个通过项、1个专用容量测量SKIP，exit0
GO_INTEGRATION=1 go test -count=1 -p 1 -json ./internal/communitycards ./internal/communitycache ./internal/social ./internal/note ./internal/platform/taildiag ./internal/platform/app
```

通过项含子项，普通与真实两次及子审查有重复，不相加为独立用例数。完整[普通输出](ordinary-tests.jsonl)、[普通/vet退出码](ordinary-exits.json)、[真实输出](real-tests.jsonl)、[真实退出码](real-exits.json)、[统计与SKIP清单](test-summary.json)保留。Windows本轮未运行race。

关键实际通过行为：

- CAP-06V SET/DEL均进入barrier，放行前独立热B返回、同key/碰撞分片等待者独立取消，旧加载/失效代次顺序正确。[SET](controlled/set-control.json)、[DEL](controlled/del-control.json)及同目录在途栈；纳秒计时为0是分辨率边界，不写零耗时。本轮复跑新实现PASS，旧实现仅counts.go overlay的FAIL来源/同用例证据另外核对，不冒称本轮重跑旧版。
- 真实Feed冷读一次内容SQL/两行，热读新增内容SQL为0；单SQL连接不嵌套借池，两用户个人点赞隔离、同秒游标/取关/公开版本及投影完整性保持。
- 真实8 HTTP冷突发加入同flight后取消首caller，其余7成功，1flight/7join/1内容SQL两行；本轮通过对应断言，属于受控合并行为，不是持续QPS。
- 真实SQL版本竞争触发一次完整新快照回退，数据库失败明确失败且不写空；真实Redis坏值/错误类型/自然过期/独立client断开及受控慢pipeline下Close收敛。测试故障只作用自有键/连接，不是全局停机或生产TTL变化。

完成后仅按本轮两个完整CID正常stop，均exit0/OOMfalse，新旧卷保留，[收尾](cleanup.json)。随机测试身份和键由测试清理；没有创建k6 cohort、observer或私有容量env，未停止CRM或其它服务，未flush/删卷。

## Linux CI与来源

在线确认交付精确 `6693539` 的[41049174082](https://github.com/tech-cove01/project-full-stack/actions/runs/41049174082)四作业success；[运行元数据](ci-delivery.json)、[作业/步骤](ci-delivery-jobs.json)及[相关真实用例PASS行](ci-selected-tests.txt)归档。固定B `30b6571227b5508d0f04bc8c4cfc1bcfbc313275` 的[41046300775](https://github.com/tech-cove01/project-full-stack/actions/runs/41046300775)也已在线确认success。

`30b6571..6693539` 的backend/frontend/infra/CI无差量，后续工具差量仅cap07-audit.py；交付CI包含Linux全包race及真实communitycards/social/计数门测试。这里不冒称本轮Windows执行了race，也不将前端CI当成新浏览器交互验证；本报告/状态后续文档提交按其实际范围区分，不冒用旧CI称性能通过。

## A/B核验：保留B1失败，没有一致HTTP改善

以下是实现者原始实验的独立离线复核，**本轮未复跑容量，也未创建新代表档**。原始逐响应体/逐请求延迟未保存，分位为native汇总交叉核对，不能重建分布。213份冻结原始工件与Git字节/来源一致，不合并分位或挑轮次。

| 阶段 | 发起 / 成功 / unknown | 全部与成功P95 / P99 ms | 实际结论 |
| --- | --- | --- | --- |
| A1 | 18001 / 18001 / 0 | 28.2026 / 36.3832 | 完整180秒，通过 |
| B1 | 5788 / 5787 / 1 | 40.82359 / 1449.571716 | 约58秒阈值早停，失败 |
| A2 | 18001 / 18001 / 0 | 29.7205 / 45.3673 | 完整180秒，通过；最大789ms，不等于所有请求低于500ms |
| B2 | 18001 / 18001 / 0 | 29.8148 / 39.8796 | 完整180秒，通过，不能补掉B1 |

B1为原生stable exit99、wrapper/harness失败，无资源stop或强制终止；已分类内容/系统/HTTP错误均0仍不抵消unknown1和P99超预算。A2也有池等待，不能把等待全部归因B。B1/B2内容SQL151批/1192行与437批/2005行的范围含预热到收尾；A没有该组件计数，null不能填0，不能从时长不同的访问量计算缓存减少百分比。B2未观察到flight join与受控突发已验证合并不矛盾。

两个B100必须都通过的既定门槛未满足；后续阶梯/邻近/最高档持有/hot均未执行。本组不支持更高成功容量、一致HTTP改善或CAP-07C通过。旧事故未归因与本轮B1实际失败继续单列，不由CI、正确性通过或收尾完成覆盖。

## 项目现状与剩余范围

功能层面已有完整用户/商铺、交易闭环、逐门店B端、社区/Kafka、ES搜索和业务AI；本次补齐的实际Feed缓存机制也已独立核验。没有因本轮发现而需要再补的业务模块，当前不再扩大功能或中间件。

还未完成的是当前约定的稳定性能验收，以及基于最终结果的项目介绍/本地求职素材、演示与源码讲解收尾。可以据实讲解已核验的分层、批量、同批合并、版本/个人字段隔离、取消与降级；受控冷/热SQL和8→1合并数据必须注明场景，不能写成持续HTTP容量提升。

若继续处理性能，先针对已归档失败窗口区分事实事务、共享loader排队/内容SQL/回填及依赖或宿主等待，再对证实的缺陷做最小修复；不先盲调池/TTL、叠缓存或无上限重跑。没有证实原因就明确未知，本报告不新授权额外诊断轮或预设收益，也不将旧问题重新设为新增功能开工前置。按现判据B1仍未通过，最终性能收尾不能标完成；算法、八股、源码熟悉和简历机制部分可以并行。
