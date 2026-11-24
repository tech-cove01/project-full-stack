# WEB-015 展示改版验收

2026-11-24。本机使用 GitHub 项目子路径 `/project-full-stack/`。实测浏览器为 Chromium 158，桌面宽 1440px，窄屏宽 390px 和 320px。GitHub Pages 版本已发布，待用户线上验收。

## 预览

[展示首页](https://tech-cove01.github.io/project-full-stack/) · [交易演示台](https://tech-cove01.github.io/project-full-stack/workspace/) · [设计详解](https://tech-cove01.github.io/project-full-stack/details.html) · [点赞实测](https://tech-cove01.github.io/project-full-stack/performance.html)

线上发布入口。需要本地复查时见[静态站说明](../../../showcase/README.md)。

## 业务结果

从首页点击「打开演示台」直接进入街角咖啡梧桐路店的同一张 ¥19 购 ¥30 示例券。工作台的第一步无需先选择实验或关闭提示。需要指引时，可以查看、关闭或重新打开说明。

| 操作 | 受理编号 | 订单编号 | 显示状态 | 剩余库存 | 已生成订单数 |
| --- | --- | --- | --- | --- | --- |
| 初始 | 未受理 | 未生成 | 待抢券 | 5 / 5 | 0 |
| 抢券并受理 | AC-0001 | 未生成 | 已受理 | 4 / 5 | 0 |
| 查询成单结果 | AC-0001 | 10000001 | 待支付 | 4 / 5 | 1 |
| 模拟支付 | AC-0001 | 10000001 | 待核销 | 4 / 5 | 1 |
| 授权店员核销 | AC-0001 | 10000001 | 已核销 | 4 / 5 | 1 |
| 重试同一请求 | AC-0001 | 10000001 | 已核销 | 4 / 5 | 1 |

工作台是浏览器内的合成案例。点击查询逐步展示后台成单的结果，支付、核销和库存变更仅影响示例。历史点赞吞吐和延迟单独来自实测报告。

## 验证结果

- **独立浏览器核对 18/18。** 跑过三种宽度的首屏、完整交易、设计页和实测页。覆盖键盘操作、提示关闭和重开、刷新恢复、交易台重置与旧自由业务相互隔离、四个原机制入口、设计锚点和架构切换，以及禁用 JavaScript 时的正文和报告。
- **最后一次交易台复验 3/3。** 320px 的券卡标题保持整行，抢券按钮能直接操作。桌面和两种窄屏的交易、刷新与重试结果一致。
- **历史报告核验。** H3/H4 四张原 PNG 与改版前 SHA-256 一致。原图在首屏出现，可以切换、按原尺寸查看，用 Escape 关闭后焦点回到触发链接。历史口径保留两轮各 180 秒，3008.33 成功变更 QPS，P95 62/55 ms、P99 102/94 ms。概览图包含准备阶段。
- **规则与静态检查。** 原有 Node 演示规则测试 50/50 通过，101 个本地资源或锚链接通过检查。格式与差异空白检查通过。

仓库根目录复跑规则测试（PowerShell）：

```powershell
$checks = @(Get-ChildItem -Path 'scripts/showcase*.test.mjs' -File | Select-Object -ExpandProperty FullName)
node --test $checks
```

### GitHub Pages 发布

2026-11-24 用户要求直接在线验收。发布内容按正常快进推到 `main`，发布提交为 [`92e9914`](https://github.com/tech-cove01/project-full-stack/commit/92e9914eec4d95fec7cfd62578a47216e6b9cd56)。[成功部署](https://github.com/tech-cove01/project-full-stack/actions/runs/45325941158)完成后，从正式地址访问首页、演示台、设计详解和实测页均为 HTTP 200。

线上 Chromium 验证共 18 项通过，涵盖 1440px 桌面、390px 与 320px 手机、原自由业务与四条机制场景、架构和报告切换、报告放大与焦点恢复，以及禁用 JavaScript 后的阅读。公开站 15 个 HTML、脚本、样式和图像文件与待验本地版本字节一致。线上抢券到核销、再重试的受理编号 AC-0001、订单号 10000001、剩余库存 4 / 5 与订单数 1 均与本机验收一致。

首次发布及同一运行的重试均在部署读取产物时失败。打包已成功，部署却未找到刚上传的 github-pages 产物。将打包和部署拆分，部署通过 needs 等待打包完成后成功。见[第一次发布日志](https://github.com/tech-cove01/project-full-stack/actions/runs/45325561507)、[GitHub Pages 工作流说明](https://docs.github.com/en/pages/getting-started-with-github-pages/using-custom-workflows-with-github-pages)及[同类问题记录](https://github.com/actions/deploy-pages/issues/451)。

## 截图

[桌面展示首页](home-1440.png) · [手机展示首页](home-390.png) · [320px 首次操作](workspace-start-320.png) · [核销及重试回执](workspace-redeemed-desktop.png) · [桌面报告首屏](performance-1440.png)
