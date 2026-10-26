# GO-011-QA1 会话时区修正

开始 SHA：`5c15358b287a39567df59d3becd52be54a28ee23`。日期2026-10-26（Asia/Shanghai），自测通过待核验；非阻断问题来自独立G1核验。此票单独 `fix(GO-011-QA1)` 提交，不混入GO-020。

完整交付SHA：`514d0beef3de443be12f812faa3a7b3dd94d64d2`。

只改UI脚本seed的一行：同一个 `sql()` 调用先 `SET time_zone='+08:00'`，再开启事务及执行NOW/INSERT。没有修改应用业务时间、全局DB设置或清理逻辑，无新DDL。

验证：

- `node --check scripts/go-shop-ui-smoke.mjs` PASS。
- [真实MySQL受控seed证据](utc-seed.json)：从当前脚本提取实际fixture/seed/精确ID清理；CLI `--init-command="SET time_zone='+00:00'"` 令每个连接初始为UTC。去掉新增SET作负对照，窗口相对上海为-30600/-27000秒；当前原样seed执行后会话+08:00、NOW-UTC=28800秒、窗口-1800/+1800秒。两轮全局时区前后均SYSTEM，只读检查，精确清理后剩余fixture行0。
- [真实UTC浏览器回归](ui-utc.json)：14项PASS且cleanup=true，Go8082/Vite5174/独立MySQL3308和Redis6380，revision `5c15358-qa1-worktree`。此轮SQL默认仍是Compose既有设置；默认UTC的因果验证由上一项受控CLI完成，没有把浏览器UTC与数据库UTC混淆。

复现环境与脚本参数见 `docs/development.md`。UI命令 `UI_TIMEZONE=UTC node scripts/go-shop-ui-smoke.mjs`（PowerShell先设置环境变量），需要显式隔离MYSQL、Playwright/Chrome路径及运行中的Go/Vite。控制实验仅给每次CLI连接设置会话时区，使用脚本原53店/2分类/7券精确ID清理，不改全局设置。

此票未改Go/前端业务代码，不重跑此前已核验的无关套件；Linux race/远端CI仍未执行。回退只影响测试脚本，无数据库结构变化。大ID夹具按既有脚本可能推进自增序列，不重置计数器。
