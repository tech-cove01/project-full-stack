# GO-020 / QA1 独立核验

日期：2026-10-26。结论：**GO-020在只读工作台与授权查询范围内通过，QA1会话时区修复通过；未发现阻断下一轮开发的应用缺陷。** 后续写业务权限与并发撤权仍须随对应业务票核验，不能由本票结论外推。

## 固定版本与协作隔离

- QA1：514d0beef3de443be12f812faa3a7b3dd94d64d2；GO-020：8731642b5b37eb8fe7b2d32fc02f2adb6882ef5a；交接终点：bb5d985d69bfb3d38d694b33ee934fc1499f647f。
- 从bb5d985导出独立源码快照，所有构建/测试/页面均基于该快照，不跟随实现agent后续工作区变化。两个只读审查者仅使用指定git show/diff审查。
- [快照完整性](snapshot-check.json)逐文件对照git归档，测试后所有跟踪文件仍与bb5d985一致。增强探针保存在快照外，附录补丁为零上下文格式，可在独立拷贝中用git apply --unidiff-zero复现。
- 专用Compose项目project-full-stack-review-go020，MySQL3318、Redis6390、Go8092、Vite5184；独立数据卷与首次001-schema/002-demo-data初始化。本票所需商家/成员/scope表已在001定义，未运行后续交易迁移或触碰开发库。
- 未修改原main工作区、暂存区、进度页或运行服务。报告单独在review/go020-bb5d985分支/worktree提交，供实现者查看后同步进度，避免与其下一票争用文件。
- Go二进制注入完整bb5d985 revision，SHA256：173fec49417147e0d880f24e4e74bb22c734fda0f40649b37dcb2736a401ec0c。

## 独立实际执行

| 检查 | 结果 | 证据与边界 |
| --- | --- | --- |
| go mod verify / go vet ./... / gofmt -l . | PASS | 模块校验通过，vet与格式无诊断 |
| go test -count=1 ./... | PASS | 冻结源码全包普通测试，不冒充race |
| GO_INTEGRATION=1 GIN_MODE=release go test -count=1 -json -run TestIntegrationMerchant ./internal/merchant | PASS，11主组 | [原始JSON事件](integration.jsonl)，真实独立MySQL/Redis，组内断言/精确清理均通过 |
| npm ci离线安装后npm run build / npm run format:check | PASS | 独立前端依赖与输出，不共用开发者node_modules |
| 原版go-merchant-ui-smoke.mjs | PASS，24项，cleanup=true | [结果](ui-original.json)、[截图](ui-original-mixed.png)；直连与代理revision均为bb5d985 |
| 增强稳定渲染断言的独立页面探针 | PASS，28项，cleanup=true | [结果](ui-steady.json)、[截图](ui-steady-mixed.png)、[相对原脚本的补丁](steady-assertions.patch)；未修改原工程或冻结应用 |
| QA1从UTC会话执行冻结脚本的实际seed前缀 | PASS | [会话证据](qa1-session.json)：初始+00:00，原样SET后+08:00，NOW-UTC=28800秒，全局设置未改变 |
| 应用/前端停止与夹具清理 | PASS | [进程汇总](summary.json)，Go exit0、无强杀；两轮UI仅按自身ID清理并注销session |
| 核验专用容器收尾 | PASS | [容器记录](containers-stopped.json)，仅专用项目两容器Exited(0)，卷保留；四个核验端口均关闭 |
| Linux race / 远端CI | 未执行 | 静态核对CI配置，不算执行通过 |

11组真实集成包含：混合角色/大ID/空成员，visible与managed范围分离，13组具体门店允许/拒绝，伪造query/header/session角色无效，原token撤scope，门店改归属，成员停用/降级，商家停用但登录仍有效，调用方事务句柄，单SQL/取消/真实闭池503，退出后401。没有为了测授权增设HTTP后门；具体门店Access服务层验证与只读HTTP范围分开记录。

## 审查结论

工作台单SQL约束ACTIVE商家/成员并按门店实际merchant关联；STAFF scope不能跨商家，零门店成员保留空数组。MembershipForShop、ManagedShopIDs按目标门店/商家裁决，A的OWNER不提升B的STAFF。身份来自服务端session，角色不缓存到token；数据库错误返回503而非伪装空权限。

前端按每个merchant.role显示角色和范围；刷新开始/失败清空旧workspace，旧请求取消后不写回；401沿现有流程清身份。真实混合角色截图已人工查看，角色和门店范围可读，未开放业务入口保持禁用。

**一项非阻断的测试加强建议：** 原UI脚本refresh只等响应，随后用旧节点detached证明降级，但页面在加载开始就清空旧workspace，因此该断言可能命中加载暂态。独立增强探针补了加载骨架消失后再判断：撤成员只剩B1、降级后B区data-role=STAFF且B1保留/B2消失、STAFF最终页面只有A1、MANAGER最终页面只有A两店；28项实际通过。建议实现者把对应断言作为独立小修补入测试脚本，此项不构成应用返修或暂停下一轮的理由。

QA1是在同一seed SQL会话先SET time_zone再NOW，修复位置正确。本轮独立重跑实际seed前缀的UTC→+08转换，不重造53店或将其称为完整商铺时区回归；原G1与实现者已有相关页面证据，本次应用业务时间未变。

## 后续写操作的门槛

Access(tx)使用调用方句柄，但普通SELECT仍遵守事务隔离级别，RR可能读旧快照。只读跨请求撤权生效与“写操作与撤权并发”不是同一保证。GO-021/032等写入必须在实际事务里使用合适的当前锁定读或覆盖授权事实和具体资源的条件写，并验证竞争窗口。不能仅调用NewAccess(tx)就把发布/核销/退款越权标通过。

本轮没有核验正在开发的GO-021、业务写入、活动/订单页面、库存/Stream/outbox恢复、AI或社区。它们仍按各票自测、独立commit和核验推进；没有新增与之冲突的改造任务。

## 环境与证据限制

独立npm缓存最初缺包导致两次离线安装失败；只读复制现有包缓存到核验目录后，以锁文件离线安装成功。没有修改开发工程依赖或把安装失败计作应用回归。Go/前端普通检查输出保留在本聊天工具记录，关键真实集成和页面原始材料随本报告保存。

核验原版与增强探针均使用自有数据库和随机夹具；精确ID清理不重置AUTO_INCREMENT，节流key按原60秒TTL到期。未删除数据卷、改全局数据库时区或停止实现者服务。报告提交不合并到main，也不代替实现者后续进度更新。
