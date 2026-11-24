# WEB-017 同页工作区验收

本轮以用户本机启动的 Go/Vue 应用为视觉依据。展示首页继续使用浅紫主题，演示台沿用本机应用的浅底、白色卡片、墨绿色主操作和同源门店实拍。在线范围是两家示例门店与浏览器内业务数据。

## 从 CRM 核对的行为

[CRM 工作区](https://tech-cove01.github.io/project-crm/workspace/#overview) 的左栏使用同页哈希链接，`hashchange` 根据路径更新中间视图。直接点击概览、Agent 工作台、销售管道和客户会话，再后退，初次打开后没有额外文档请求，原侧栏节点一直存在。[CRM 路由实现](https://github.com/tech-cove01/project-crm/blob/gh-pages/workspace/app.js)

Full Stack 上一版实测抽查 6 个左栏入口，均触发文档导航。新版保留外壳，用哈希在交易、附近商铺、商家、社区、实验及项目资料中切换。第一次需要共享演示内容时会读取一次同站静态片段。面板以后保留在同一文档中。

## 本机验证

- 独立 Chromium 11/11。11 次模块切换期间，额外顶层文档请求为 0，iframe 为 0。侧栏保持同一个 DOM 节点，内容、面包屑、当前项及浏览器历史同步。
- 交易受理后可见 AC-0001，成单后订单号为 10000001。核销、重试和切换返回后仍是这一订单，库存 4 / 5，订单数 1。
- 探索附近抢券并支付后，现有按钮进入商家工作台，核销后可继续进入社区。切换保留订单、笔记和机制进度，刷新保留同标签的顾客业务和跟做订单。机制刷新恢复初始示例。
- 1440、390 与 320px 核对键盘、手机菜单、焦点和横向布局，切换模块时关闭旧引导或报告浮层。原自由业务及四条 `/demo/?scene=...` 深链通过。
- 工作区内设计可切换链路，实测可切 H3/H4 和原报告，保留原图并支持放大。50 项现有 Node 模型测试通过。

[改造前导航记录](baseline.json) · [本机 Chromium 结果](local-browser.json)

[桌面探索附近](explore-desktop.png) · [手机探索附近](explore-mobile.png) · [商家工作台](merchant-desktop.png)

## 线上发布

[新版演示台](https://tech-cove01.github.io/project-full-stack/workspace/) · [直接打开探索附近](https://tech-cove01.github.io/project-full-stack/workspace/#business) · [GitHub Pages 构建](https://github.com/tech-cove01/project-full-stack/actions/runs/45343358196)

发布版本为 [`b0ffdec`](https://github.com/tech-cove01/project-full-stack/commit/b0ffdec2d6a6fdd27bba82c91e3227fa7279f19f)。正式站正常加载导航、共享演示和资料脚本。独立线上验证 11 项均通过，其中 390px 键盘切换场景首次仍显示加载提示并超过 5 秒测试阈值，针对同一场景延长等待后复验通过。桌面 11 次切换无顶层文档请求，交易状态、H4 报告、5 个旧入口、桌面和窄屏实拍视觉检查通过。
