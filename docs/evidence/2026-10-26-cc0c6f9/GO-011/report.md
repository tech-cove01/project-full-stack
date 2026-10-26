# GO-011 商铺浏览与优惠自测

日期：2026-10-26（Asia/Shanghai）。状态：自测通过待核验。依赖 GO-010；开始 SHA `cc0c6f920897b3ed65a4f080997a6c538fe0c2fa`，交付 SHA `644b2c55ee73e1318f78c74ce8d24affef707295`。

范围：五个公开 GET、名称与分类同 SQL 交集、稳定分页/字符串total、商铺/分类/公开券DTO，Vue分类/搜索/加载更多/独立重试，Shanghai业务时间在不同浏览器时区的解释。未迁移附近坐标查询（x/y明确400）、商铺详情缓存、HEAD、交易/B端/AI；已知旧商铺/券/分类写路由仍403。PRODUCT-01仅覆盖商铺子集，不包含订单/商家列表。

## 环境与版本 SQL

环境沿 GO-001/010：Windows amd64 / Go1.27.0 / Node24.15.0，真实 MySQL3308/Redis6380、Go8082/Vite5174、Playwright+本机Chrome。独立Compose项目及数据卷；没有修改Java服务或数据，没有订单worker/预热库存。

`MYSQL_*` 按 `backend-go/.env.example` 显式配置，在根目录执行 `node scripts/migrate.mjs`：复用既有003-trade/004-ai/005-order-review；[首次执行](migrate-first.txt)全部apply，[再次执行](migrate-repeat.txt)按相同checksum全部skip。没有新增/修改DDL，没有AutoMigrate。SQL回退与Git分开：此批复用的迁移已升级隔离库，revert代码不撤销迁移，回看Java也应保留兼容列/表，不删除卷。

## 验证与原始证据

| 命令/场景 | 结果 | 原始证据 |
| --- | --- | --- |
| gofmt / `go vet ./...` | PASS，无差异/诊断 | 本地执行与独立复核 |
| `go test -count=1 ./...`（默认不启用真实依赖） | PASS | [初轮单测](tests-unit-initial.txt) |
| `GO_INTEGRATION=1 go test -count=1 -v ./internal/shop -run TestIntegrationShop` | PASS，7组 | [真实MySQL](tests-integration-initial.txt) |
| `GO_INTEGRATION=1 go test -count=1 ./...`（最终含auth+shop真实集成） | PASS | [最终全套](tests-final-all.txt) |
| `npm.cmd run build` / 四文件Prettier | PASS（修复后最终版本） | [build](frontend-build-final.txt)、[format](frontend-format-final.txt) |
| 真实Vue / Shanghai初稿 | PASS，12项；未含后续UTC窗口 | [初轮](ui-initial.json) |
| 真实Vue / UTC，sale窗口±30min（修复前） | FAIL，精确cleanup完成 | [原始失败](ui-timezone-failure.json)、[独立Node复现](timezone-initial-failure.txt) |
| 最终真实Vue / UTC及Shanghai | PASS，各14项 | [UTC](ui-final-utc.json)、[Shanghai](ui-final-shanghai.json)，各自同名png |
| 最终Go+前端身份回归 | PASS，12项 | [auth回归](auth-regression.json) |

最终 Go binary SHA256：`D01F99B437DC193EE3EADD40303716BF7A8ECBB14FC2D32947DDBAEB98ADCA82`；UI使用的binary与冻结后再次构建的 `api-go011-final.exe` 字节哈希相同。构建revision明确为 `cc0c6f9-go011-worktree`，不冒充已提交SHA。

真实MySQL七组验证：53个>2^53的店铺完整分页、确定ID顺序/无重漏；中文名称与分类交集；`%/_/!` 字面转义与注入文本；缺失详情/NULL省略/空串保留/元金额/Shanghai无偏移ISO；分类sort相同按id；券只允许v.status=1且publication ACTIVE，普通无秒杀行/INITIALIZING/OFFLINE不公开，售罄/未来/过期仍显示；分页极值/非法/坐标参数明确处理。另含金额>2^53及signed actual_value正确字符串。仅精确删除测试自己创建的53店/3分类/9券，无DDL、Redis写、flush/truncate或sequence重置。

UI每轮另建53店/2类/7券，真实六页无重漏；未提交输入不污染续页；组合分类回第一页；空结果；离线续页失败保留已有结果、恢复后重试同页；分类/优惠选择性网络abort后独立重试，成功响应仍来自真实Go/DB；活动可见性、元与分、不同浏览器时区、无未捕获JS异常。每轮精确清理通过。夹具的大ID会推进隔离shop/type/voucher自增序列，清理后不会恢复旧计数器。

## 失败、修复与复核边界

首次前端签名调整让未改动的AssistantView旧调用编译失败，保留[原始输出](frontend-build-initial-failure.txt)，已保留api.shops既有调用方式并增加可选过滤参数。此前build/format通过结果分别保留为before-timezone-fix文件，不覆盖失败或冒充最终代码成绩。

独立审查发现旧页面用浏览器本地时区解析Shanghai无偏移时间；UTC浏览器对销售窗口判断偏8h。root用真实Go与±30min窗口复现失败后才修复。`parseBusinessTime`仅给无offset的日期时间加+08:00，带Z/显式offset保留；最终UTC/Shanghai真实浏览器均通过。独立只读agent复核实际生产helper，在三时区24断言通过，并独立执行mod verify/vet/full Go测试/真实MySQL七组，无剩余阻断；其未重复跑浏览器或迁移命令。

Linux race和远端Actions未执行；CI仅新增配置（显式SQL初始化、现有checksum runner两遍、auth/shop真实集成race）。没有持续负载/缓存命中/交易结果或Go性能提升声明。完整三业务流程、恢复窗口、AI与社区仍待后续小任务。

复现见[运行步骤](../../../development.md#go-迁移环境与启动)。UI脚本需现有Playwright模块、Chrome、显式隔离MYSQL和Go/Vite地址。停止应用/前端使用各自Ctrl+C；停止隔离依赖使用 `docker compose -f compose.go.yaml stop mysql redis`，保留卷与未知数据。

收尾实际执行：Ctrl+C 停止本轮Go API（日志HTTP requests drained）与Vite；`docker compose -f compose.go.yaml -p project-full-stack-go stop mysql redis` 后仅两个Go测试容器exited。只读检查原 `project-full-stack` MySQL/Redis仍healthy。没有删卷或删除未知数据。三个GO任务的功能提交分别独立，后续文档提交仅补全交付SHA、限制和此环境交接。
