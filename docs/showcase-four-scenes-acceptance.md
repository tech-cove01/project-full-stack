# 四条亮点展示与互动演示：操作和自验

2026-11-10。依据STATUS中的11月9日待办，同时修改展示首页、设计详解、实测入口和互动演示。四条顺序统一，保留浅紫风格、原始报告及顾客/商家/社区自由体验。本批交付后停在**可访问版本待用户验收**，用户尚未确认体验结果。

## 逐条体验

| 入口 | 正常流程 | 关键异常/对比与预期结果 |
| --- | --- | --- |
| [交易场景](https://tech-cove01.github.io/project-full-stack/demo/?scene=trade)；[对应设计](../showcase/details.html#trade) | 抢券并受理 → 重试同一请求 → 消费成单 → 重复投递 → 确认消息。受理ID不变、库存只预占一次、唯一订单数1。 | 重置 → 抢券 → 模拟结果待核对 → 确认消息被阻止 → 保存待核对任务 → 确认消息 → 核对并恢复，OPEN变RESOLVED。另：取消 → 应用补偿 → 再抢券 → 投递旧补偿，新归属和库存4保持。 |
| [Feed场景](https://tech-cove01.github.io/project-full-stack/demo/?scene=feed)；[对应设计](../showcase/details.html#feed) | 读取 → 完成回源 → 再读，来源变为进程内缓存；只清进程内缓存 → 再读，来源为Redis。A点赞并读取 → 切B读取，公共正文复用但B未点赞。 | 清空两级缓存 → 同批请求 → 取消一个等待者 → 加入另一缺失批次 → 完成两次回源，其余请求完成、运行上限1。另：冷批次加载中修改版本 → 完成，旧内容拒绝；读到新版后隐藏 → 再读，旧缓存不能返回隐藏内容。 |
| [事件与搜索场景](https://tech-cove01.github.io/project-full-stack/demo/?scene=events)；[对应设计](../showcase/details.html#events) | 发布示例笔记 → 事实与Outbox出现、尚无索引 → 投递最新 → 搜索可见。编辑标题保存新版本，再投递和查询。 | 重复事件不重复收据；连存两版本先投新再投旧，索引不回退；隐藏后旧索引候选被事实过滤。故障：发布 → 索引不可用 → 投递停车 → 恢复索引 → 普通投递仍拒绝 → 人工重放后搜索恢复。 |
| [点赞场景](https://tech-cove01.github.io/project-full-stack/demo/?scene=likes)；[对应设计](../showcase/details.html#likes) | 点赞/取消 → 当前事实、成功变更和Outbox变化；重试同状态不新增事件。发布/消费后唯一收据与变更数一致，积压为0。 | 生成互动 → 暂停消费 → 处理本组，业务已提交而待收据积压；恢复本身不清空，显式处理后核对。对比同一组12次操作：逐条和最多4条示例批量，事实与收据相同、处理轮数不同。真实QPS请另看[原实测](evidence/2026-11-01-cap10/cloud-like-final-report.md)。 |

展示[首页](https://tech-cove01.github.io/project-full-stack/)和[详解](https://tech-cove01.github.io/project-full-stack/details.html)均按此顺序提供四个独立体验链接，不共用默认交易起点。演示顶部可切换场景、重置当前场景，并返回对应设计和公开证据；原[自由体验](https://tech-cove01.github.io/project-full-stack/demo/)继续保留。刷新保留深链选择、恢复机制场景初始状态；通用业务仍按当前标签页保存原进度。

## 实际验证记录

- `node --test scripts/showcase-demo.test.mjs scripts/showcase-scene-*.test.mjs`：29项通过，含21项新场景模型验证、8项原业务验证。
- Chrome真实浏览器从首页四条入口逐一进入，1440/390宽共8条场景路径全部通过；按上表实际执行正常/异常动作，验证状态、计数和版本变化。深链刷新、返回浏览历史、重置和返回对应详解通过，无JS异常和横向溢出。
- 原顾客抢券/支付、店员错误凭证与核销、门店越权、店主审计/退款、社区发布搜索点赞、取消重买、刷新恢复、重置和存储禁用降级，在1440/768/390宽回归通过。
- 三展示页1440/768/390共9组回归通过；63处本地引用/锚点有效，原四张报告图SHA不变。实测首图桌面top355.47px、宽1198px，手机仍在开头显示，原报告切换和原尺寸查看保持。
- 新深链首次弹窗会说明当前场景起点，不再统一提示从抢券开始；访客中性文案保留。

### 审查发现与修复

事件场景初版恢复服务后普通投递可处理停车事件，现已增加人工重放守卫并补回归；索引不可用时不会返回旧搜索结果。整合时场景按钮同步重绘会使target脱离DOM，现使用事件composedPath隔离，避免交易取消按钮误入原订单逻辑。均在实际操作后验证修复，未放宽规则。

交易、事件与点赞由非作者交叉审查通过；Feed模型与两层接线由主代理/非作者核对。最终整合独审核对四条同名同序、深链、证据、引导、重置、无求职口吻及实际截图，PASS。

## 截图与范围

操作后截图见本目录下 `evidence/2026-11-10-showcase-four-scenes/`，桌面和手机各四张，展示示例状态而非真实后端测量。过程计数、轮数与队列都明确标为浏览器模拟，不作为QPS或真实SQL计数；没有启动后端、压测或云机，也未修改简历或原实测数据。

| 场景 | 桌面截图 | 手机截图 |
| --- | --- | --- |
| 交易 | [待核对恢复后的唯一订单](evidence/2026-11-10-showcase-four-scenes/trade-1440.png) | [手机流程](evidence/2026-11-10-showcase-four-scenes/trade-390.png) |
| Feed | [版本与可见性复核](evidence/2026-11-10-showcase-four-scenes/feed-1440.png) | [手机流程](evidence/2026-11-10-showcase-four-scenes/feed-390.png) |
| 事件与搜索 | [人工重放后搜索恢复](evidence/2026-11-10-showcase-four-scenes/events-1440.png) | [手机流程](evidence/2026-11-10-showcase-four-scenes/events-390.png) |
| 点赞 | [收据核对和同组对照](evidence/2026-11-10-showcase-four-scenes/likes-1440.png) | [手机流程](evidence/2026-11-10-showcase-four-scenes/likes-390.png) |

## 已部署，待用户验收

发布源码提交 `d478fbf1e748b3f7f74d451bc765932cd82258d9` 的[Pages任务](https://github.com/tech-cove01/project-full-stack/actions/runs/42744176100)实际completed/success。从线上展示首页逐一进入四场景，1440/390宽共8条完整路径再次执行正常与关键异常、重置、深链刷新和返回设计，均PASS、无JS异常。原始检查记录见[本地记录](evidence/2026-11-10-showcase-four-scenes/local-browser-result.json)与[线上记录](evidence/2026-11-10-showcase-four-scenes/online-browser-result.json)。当前交付等待用户实际体验，未写作用户验收通过。

本批不覆盖穷举故障、跨实例缓存、真实Kafka/ES现场故障或新的负载测试，这些均非本次静态展示任务。部署回执和最终状态统一见STATUS；满足本批标准后等待用户验收。
