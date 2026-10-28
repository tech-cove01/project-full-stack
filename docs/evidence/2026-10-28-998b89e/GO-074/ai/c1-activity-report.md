# GO-074c1 活动工具卡片库存与入口

起点`05f0baa52f14566566339786b10ef8d4193cb4ed`。现有Vue活动卡片读取`items[].stock/href`，后端公开活动工具未给这两项，导致库存空白、点击返回首页。本次只补`backend/internal/aitool/tool.go`的既有活动DTO与现有`review_integration_test.go`，不新增工具、SQL迁移、数据库或模型调用，不改Vue/其他工具分支。

已有`tb_seckill_voucher JOIN tb_voucher`查询增加读取`sv.stock/v.shop_id`，活动item新增字符串`stock/shopId`和`href=/shops/<shopId>`。原顶层字段与items原字段保留；公开券`v.status=1`、活动`publish_status='ACTIVE'`过滤不变。每轮重新读MySQL已提交库存，不引入缓存；该读数不是购买准入或Redis瞬时预留库存承诺。工具继续只读业务数据。

## 先失败后修复

专用MySQL3381/Redis6451、project `project-full-stack-go074-ai`，由root启动到真实healthy；运行时无API/worker。本测试只创建自己的随机大ID门店/券/秒杀活动，cleanup按精确ID删除，失败也执行，不修改既有商家/用户、不flush。

先只补断言，生产代码未改时真实执行原测试入口，结果[首次FAIL](c1-activity-initial.txt)：stock、shopId、href均为nil，库存1和更新0两个子组失败；公开/下架过滤两子组本来通过。随后修复DTO，再执行整个aitool包，[最终PASS](c1-activity-final.txt)：

| 子组 | 实测结果 |
| --- | --- |
| public_card_stock_and_large_string_ids | stock为字符串`1`；活动和门店ID均超过2^53且精确字符串；金额`9007199254740993`分仍为字符串；href为该真实夹具门店路径。 |
| same_service_reads_updated_zero_stock | 直接提交本活动MySQL stock由1→0，用同一Service再次查询，返回字符串`0`。 |
| unpublished_voucher_stays_hidden | 券status=0时没有活动卡片。 |
| offline_activity_stays_hidden | 券恢复公开后，活动OFFLINE仍无卡片。 |

复跑命令（backend目录）为`GO_INTEGRATION=1 go test -count=1 -v ./internal/aitool`；此前显式设置`MYSQL_HOST=127.0.0.1 MYSQL_PORT=3381 MYSQL_DATABASE=project_full_stack_go MYSQL_USER=fullstack`与本地演示库密码、`REDIS_HOST=127.0.0.1 REDIS_PORT=6451`。首次只运行`-run '^TestIntegrationReviewActivityTool$'`。

适用普通检查：[c1-activity-checks.txt](c1-activity-checks.txt)，`gofmt -l`为空，`go test -count=1 ./internal/aitool ./internal/aichat`、相同两包`go vet`与`go build`均PASS。这些普通测试没有启用真实Provider；本次实际付费调用为0。此前真实AI35项不重跑，因为Chat/Embedding、取消、来源与订单分支未改；后续本地页面差量由root协调，不将本报告当活动浏览器实跑证据。

代码/原始证据交root与非作者agent审读后独立commit。本报告是实现者自验，不提升票级独立核验状态；最终SHA由root记录。
