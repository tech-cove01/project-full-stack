# CAP-06 独立核验：观测与有限诊断成立，原长尾未闭环

2026-10-30，核验交付 `a3c64b5361069ae4491b9de76654b5432ac9f9a8`，生产差量自 `c89da96` 起。使用隔离 worktree `review/cap06-a3c64b5`，不改实现者工作区、不覆盖原证据。

**结论：CAP-06A观测和CAP-06B有限三轮的实际交付可核验；没有证据宣布原秒级长尾已修，CAP-06C/D及CAP-07仍未实施。** 没有发现本批观测改动的P1/P2业务语义、资源无界或隐私泄漏问题。发现一项真实集成的CI覆盖遗漏，已按一个问题一commit修复；不是原长尾的产品修复。

## 独立审查与证据复核

- [代码审查](code-review.md)：默认关闭与非生产loopback限制、鉴权/完整Feed trace、GORM事务与原池接口、Redis hook、context/取消、计数门顺序、固定20阶段/32条记录/256 spans上界。未新建池、改SQL/TTL/业务查询或移除publication门。另独立运行7项无网络定向测试；不与主核验者统计相加。
- [证据复核](evidence-review.md)：原audit实际164项零失败，另独立复核58份声明及189份Git字节、三槽、三轮native统计、自然过期、slow ring去重、profile及收尾来源。
- [方法审查](method-review.md)：完成后发布慢trace的在途盲区、唯一恢复栈、额外观测开销、缺失的纯借池/服务器耗时，以及下一最小受控验证。

三轮multi100/s×180秒，分别18001/18001/18000完整成功，P95/P99为26.4453/43.7021、29.2595/40.4513、31.5160/40.9273ms，unknown、错误及dropped为0。自然冷填充SQL批次462/435/419，首轮39条新slow/1次抓栈，后两轮没有新slow。实际只复核在线分类器结果与native汇总，未保存逐响应和逐请求延迟，不能离线重判全部内容或重建分位。

唯一抓栈晚于触发样本所含最新已完慢请求约415ms，SQL InUse已经回到2；不能把恢复时点当排队峰值。阶段和全局池并非原子采样，嵌套阶段不能相加。三轮开启额外1Hz观测，没有复现旧约1.137秒P99；off/on短探针不证明零扰动。旧失败、publication/存储往返/宿主暂停等未知候选均保留，不能用本轮成功替换正式D组。

## 本轮独立运行的相关验证

原始输出见 [普通测试](ordinary-tests.jsonl)、[普通退出码与vet](ordinary-exits.json)、[真实回归](real-tests.jsonl)、[真实退出码](real-exits.json)、[统计与SKIP清单](test-summary.json)。通过项包含子项，普通与真实两次有重复，不相加为独立用例总数。

```text
# backend；不启用真实依赖：七包116个通过项、17个显式SKIP，exit0
GO_INTEGRATION=0 go test -count=1 -json ./internal/platform/taildiag ./internal/social ./internal/communitycache ./internal/auth ./internal/platform/config ./internal/platform/diagnostics ./internal/platform/app
go vet ./internal/platform/taildiag ./internal/social ./internal/communitycache ./internal/auth ./internal/platform/config ./internal/platform/diagnostics ./internal/platform/app

# 根目录，专用依赖端口：版本001–013应用成功，再跑一次全部skip
node scripts/migrate.mjs
node scripts/migrate.mjs

# backend；真实MySQL/Redis：三包47个通过项、1个专用容量测量SKIP，exit0
GO_INTEGRATION=1 go test -count=1 -p 1 -json ./internal/platform/taildiag ./internal/social ./internal/communitycache
```

新真实包装用例明确PASS，验证RR commit/rollback、归还连接、Redis命令/取消及原SQL池身份。其余Feed/cache回归不是全部在启用wrapper的Service上运行，不把两个范围合并声称所有业务都在观测开关下复跑。Windows本轮未运行race；无产品契约或页面变更，未重跑浏览器，没有执行第四次容量诊断或付费模型调用。

环境使用Go1.27.0 Windows/amd64、真实MySQL8.4/Redis7.4、基线compose加原资源限制及[独立卷/网段覆盖](qa-compose.yaml)。项目`fullstack-review-cap06-a3c64b5`，端口3462/6562、网段10.254.62.0/24预检无冲突，新自有卷从001/002初始化，API/Kafka/ES/AI未启动。选取的容器ID、镜像、挂载、资源与端口见[环境](environment.json)，不归档容器完整environment。测试后按不可变ID正常stop，两个容器exit0/OOMfalse、卷保留，无新会话凭据；[收尾](cleanup.json)。未停止CRM或他人服务，未flush/清库/删卷。

## 一问题一提交的修复

`e9d58bba29bba51e85401eee74d425df2bf6cc54`：在既有CI迁移后加入taildiag的`GO_INTEGRATION=1`真实race步骤。[缺口与验证](ci-coverage-fix.md)。原全包race明确SKIP此新用例，后续真实包清单也未覆盖，原四作业绿色不等于该新增测试已执行。修复只改CI和该问题说明，无业务源码变动，经独立审查。

在线核实交付精确`a3c64b5`的[41040093568](https://github.com/tech-cove01/project-full-stack/actions/runs/41040093568)四作业success；修复及本报告后最终SHA的Linux race/CI以对应运行结果为准，由主核验者在线核对后交付，不冒用旧运行或把预期当成已执行，不据CI绿色推断性能或根因。

## 下一步与项目完成边界

不再盲跑第四次完整诊断。沿既定CAP-06C，先用确定性barrier验证key A Redis写/删暂停时，独立预热key B读取、已进入等待者取消、旧加载/Invalidate代次顺序，并检查放行前是否能观察到在途等待；发现结构问题再给旧FAIL→新PASS和最小修复。该机制即使成立，也应与旧事故原因未知分别表达。若不成立或采证不足，先交结果决定后续范围，不能无上限堆观测或擅自标闭环。

功能层面，Go/Vue用户端、交易/B端、社区/Kafka、ES搜索和业务AI沿前批核验成立；本批是诊断工具交付，没有补出新的Feed卡片缓存。当前主要缺口是长尾处理、CAP-07公共内容分层/批量装配/真实同批singleflight及行为和正式量化验收。完成这些后再同步项目介绍/本地求职材料及演示讲解，不需增加新业务或中间件。算法和八股可以并行准备。
