# 项目静态展示站

纯 HTML / CSS / JavaScript，GitHub Pages 只发布本目录，不依赖 Go API、数据库或已释放的 ECS。

- `index.html`：以街角咖啡到店券介绍交易、架构与完整产品能力，主按钮直达同一案例。
- `workspace/`：同页演示工作区。默认跟走一笔到店交易。左栏在探索附近、商家工作台、探店社区、四个机制实验、设计和实测之间切换。保留 `#business`、`#merchant`、`#community` 和 `#recovery` 等深链与浏览历史。导航往返保持已打开模块状态，刷新时自由业务与到店订单沿用浏览器会话存储，机制实验按原规则恢复初始样例。
- `details.html`：订单状态、异常分支、三条架构链路、四项机制与源码。
- `performance.html`：页面开头为两轮原始 PTS 报告及发压监控，支持原尺寸查看，后面是正式主窗与组件优化。
- `demo/`：自由业务体验，可切换顾客、商家和社区，体验取消、退款、发布、搜索与点赞。
- `demo/?scene=trade|feed|events|likes`：四个原机制场景，支持异常操作、步骤引导和场景重置。刷新后恢复机制场景的初始示例。
- `styles.css`：用户指定的浅紫主题、响应式布局与减少动效支持。
- `script.js`：移动导航、架构与轮次切换、原图放大；禁用 JS 后正文和导航仍可读。

维护内容须以仓库的业务实现与正式报告为依据。公开报告ID已由用户明确授权，四张 PNG 保留原字节；其它私有资源ID、密钥、PDF与本地简历材料不得加入。截图仅显示时刻与时间轴，无具体日期。数据截图不可用美化或生成图片替换。

从仓库根目录运行 `python -m http.server 8767 --bind 127.0.0.1 --directory showcase`，打开 `http://127.0.0.1:8767/`。站内路径均为相对路径，也支持 GitHub 项目子路径。

发布由 `../.github/workflows/pages.yml` 完成，Pages 使用 GitHub Actions 来源；修改本目录并正常推送 main 后自动部署。维护时检查首页到交易台、旧演示入口、窄屏导航、架构与报告切换、截图关闭与焦点返回。回退使用追加 revert 提交。

交易台和自由业务使用虚构门店与示例数据，复用 `demo/model.js` 的业务模型。两处各自保存在当前标签页的 `sessionStorage`，重置只清理对应示例。受理、成单、支付与履约都在浏览器内模拟。规则测试从仓库根运行 `node --test scripts/showcase-demo.test.mjs`。

四场景直接验证为 `node --test scripts/showcase-scene-*.test.mjs`，完整操作与验收见仓库 `docs/showcase-four-scenes-acceptance.md`。模拟的回源、收据、积压和处理轮数仅解释机制，真实3000+成功QPS引用原H3/H4及明确条件，禁止把动画或模拟计数冒称压测。

设计参考用户打磨后的 CRM 展示站的信息层次，页面业务和实现独立。规格、审查和交付状态见仓库文档，本站不承载内部工作记录。
