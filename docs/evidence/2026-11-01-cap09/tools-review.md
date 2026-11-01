# CAP-09A 工具内部交叉审查

本页只记录实施团队内部审查，不代表新容量或外部验收通过。

root对非root作者的共享stage/resources/Feedhelper与纯契约逐项审查：新行为须显式CAP_COHORT=cap09，旧trade/read默认保留；新project/六个端口及4服务全CID/挂载/限额核验，缺采样不补0。完整Feed逐页核标题/256B正文/图片/门店/时间及nextCursor，原有ID/本人状态/计数保持。root实际运行5项纯检查通过；作者另报旧CAP08的12项纯检查与Node语法检查通过。未把纯检查算HTTP/负载。

共享工具作者作为非作者审查root新cap09-env与执行协议：namespace、四CID、私有ACL、源码/进程身份、phase切换、logout/observer删除/精确停服务留卷和时间/名额/公平条件一致。审查发现中断时warm进程与已登记phase私有input可能遗漏，root已补为warm/stable身份均核、注销后仅精确删除本runtime/tmp下登记input和空parent，再经原审查者复审无阻断。NoRedirect、IPv4、ESyellow/green和k6+Node组合RSS范围也已同步。

以上完成时仅编译了独立干净baseline应用、执行语法/纯检查，尚未创建新依赖、API或负载。实际新卷、Kafka/ES/三组低速canary、变更/幂等/恢复/停止及完整业务对账由后续原始证据单列。

首次四依赖均健康后，Kafka镜像两匿名卷触发原严格单卷断言，停止于迁移/observer/API/负载之前。原compose输出、preparation与recovery选取身份保留。root独立核4CID、数据卷及匿名卷labels；工具作者补resources的Kafka全部3个volume精确匹配、拒绝额外bind/tmpfs及其它错挂反例，root同步env。六项纯检查通过。该作者再对root固定检查点resume作非作者复审：prep hash与四CID/挂载完全一致、禁止重复compose/换卷，仅继续迁移/observer，无阻断。此适配是工具环境修复，不计业务优化迭代或吞吐收益。
09A真实探针后补齐两链路原生offer时钟：共享纯函数核k6 scenario.startTime、配置duration、wrapper覆盖、正常退出且无external stop；保留旧wrapper时间，早停/缺summary不补native成绩。Feed作者与点赞作者双向审查，root复跑7项common、26项like、15项grade纯检查通过；grade含窗外分母、跨end积压、恢复真实finish、内部采样间隙、零事件Feed、固定来源和名额反例。45秒末15秒斜率仅作描述。

root执行器由点赞审查者独立复审，另9项纯convergence反例通过；必须done+可信监督终态、无同PID/create存活或warm/stable marker活进程才解锁。正常observer先退可接受；异常/Ctrl+C清零通过标志并请求原监督有界停止，缺终态保留锁。预期停止须自身精确stop原因、资源完整、真实事实/observer收敛，始终不计吞吐通过。root另6项纯收敛检查通过。无native之前所有工具冻结，后续按每档hash核对。

09C采集SQL修复经非作者审查闭环：同静态数据的真实EXPLAIN证明outbox utf8mb4 event_id与receipt ascii_bin隐式比较使r仅用projection索引前缀；新ON转换外侧o并加BINARY精确guard后，r完整PK eq_ref/每行1。保持LEFT JOIN缺失计数、水位/fixture范围、4秒总采集预算/5秒间隔/三连续失败保护。old/new同RR全量follow均[missing0,APPLIED0,IGNORED2415]，单note三组全等，尾空格/非ASCII损失guard反例正确。旧单组2232.882ms→新12.788ms仅采样SQL一次idle对照；不计业务吞吐收益。helper原plan为rscan→o unique lookup，不做无关修改。原like-diagnostic-1及其缺summary/整体失败不回填重跑。

Like最终同条件对照旗标：生产只有Feed端点启动tail request观察，故root把阶段gate限定为Feed diagnostic才true；Like diagnostic和其余所有purpose均强制false，并在command记录feedTailEnabled。非作者核这两行未改变任何名额、早停、收敛或原始工件；旧Like诊断1原true/旧hash/整体失败保持，新诊断2与最终20档可按同样tailfalse旗标比较。AST解析通过，该采集开关校正不改变应用代码。

20/s诊断在native前被helper的warm<=target保护正确拒绝，runner已由非作者审查仅修为非probe warm=min(25,target)、probe保持1；>=25场景不变，AST通过。该诊断失败与slot2保留，不补跑、不转名额。热点before/after仅用于相同20/s/hot/tailfalse/indexedsampler/warm20场景，main先前无完整有效baseline不报相对优化比例。

Warm/steady政策修正：仅stable保留原200/500ms对象、abort与delay，warm照常输出延迟与全部其它护栏。27项实际options纯检查与非作者审查通过，原hot201warm的SLO失败保持、不补45秒；sampler未启动时get缺字段判资源false、防secondary KeyError亦经独立审查。runner工作文件在读取时再次消失，已复核源码由Git blob保全，尚不代表最终容量执行完成。
