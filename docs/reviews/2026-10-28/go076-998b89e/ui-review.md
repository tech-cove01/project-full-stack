# GO-076 前端与浏览器独立核验

日期：2026-10-28，固定基线`998b89e81bed42786d25d0cebbadb401fd506f70`。核验在独立worktree和父核验者准备的专用环境完成，没有修改应用源码、正式脚本、依赖清单、main索引或其他agent的文件，没有启动/停止API/Vite/容器或重建索引。

## 源码审读

- `NoteSearchView.vue:32/64`：编辑条件取消旧请求、递增generation并清结果/游标；成功、失败与finally都核对当前代次及abort，旧响应不会恢复上一查询内容。
- `load()`的400翻页分支清旧结果/游标并保留已提交条件，从第一页重搜；普通分页传输失败保留已展示结果和原cursor。短页/空页只要还有游标就允许继续。
- `watch(route.query)`恢复URL条件并拒绝重复数组参数、非法sort；大ID只按规范十进制字符串比较，不转Number。
- 标题与摘要使用Vue文本插值，没有`v-html`；缩略图只选http(s)。页面未把ES索引、offset等运行信息暴露给用户。
- `api.ts:237`的`searchNotes`显式传`sessionToken=null`；公开静态路由在`/notes/:id`前，详情页仍可匿名打开。
- `go-note-search-ui-smoke.mjs`只将网络abort/暂停作为故障，不伪造成功JSON；脚本以本轮合成ID清理SQL/ES并先等outbox、三组收据和broker位点。
- 物理故障脚本硬锁旧GO076项目名、端口和容器标签；本次由父核验者制作固定到新专用环境的本地副本，不放宽正式脚本去停止未知容器。

本次静态审读未发现确定P1/P2；这不是所有输入、浏览器或生产容量的证明。

## 构建和真实浏览器

按现有锁文件执行`npm --prefix frontend ci`、`build`（含vue-tsc）、`format:check`全部通过；日志为[安装](frontend-ci-initial.txt)、[构建](frontend-build-initial.txt)、[格式](frontend-format-initial.txt)。两个已交付浏览器/故障脚本`node --check`通过。没有安装全局工具或新增应用依赖。

浏览器复用忽略目录中的Playwright-core及本机Chrome，明确路径：

- `project-full-stack/backend/bin/go076-tools/node_modules/playwright-core/index.mjs`
- `C:/Program Files/Google/Chrome/Application/chrome.exe`

本轮实际API8116、Vite5216、MySQL3350、Redis6420、Kafka9114、ES9214，alias=`review-notes-v1`，Kafka容器=`project-full-stack-review076-kafka-1`。AI关闭，无付费模型或真实用户会话。

| 独立复跑 | 结果 | 证据 |
| --- | --- | --- |
| 上海时区原脚本 | 25项PASS，cleanup=true，30.642秒 | [JSON](ui-shanghai-initial.json)、[截图](ui-shanghai-initial-results.png)、[trace](ui-shanghai-initial-trace.zip)、[命令](ui-shanghai-initial.txt) |
| UTC时区原脚本 | 25项PASS，cleanup=true，28.402秒 | [JSON](ui-utc-initial.json)、[截图](ui-utc-initial-results.png)、[trace](ui-utc-initial-trace.zip)、[命令](ui-utc-initial.txt) |
| 新增公开边界差量 | 2类行为、4项断言PASS，4.278秒 | [JSON](ui-public-boundary-module-fixed.json)、[截图](ui-public-boundary-module-fixed-results.png)、[trace](ui-public-boundary-module-fixed-trace.zip)、[执行元数据](ui-public-boundary-module-fixed-command.json) |

两时区覆盖真实13篇发布→outbox→Kafka三组→ES→匿名Vue、两页独立SQL顺序/无重漏、大于2^53的ID、门店筛选、URL刷新返回、中文正文、转义和真实详情、空态、无效条件清理、迟到请求取消、只关闭本人真实PIT后的400/重搜200、首次与翻页网络失败及真实重试。上海全页截图已人工查看：布局正常，大ID完整，标题`<b>`和正文`<img ...>`显示为文字，未生成对应DOM或执行事件。

新增差量使用全新匿名浏览器上下文，仅写入固定合成localStorage canary，不读取用户浏览器配置：

1. 在storage存在canary时，实际公开搜索请求仍无Authorization/Cookie且返回真实200。
2. 重复q参数URL显示格式错误、不发搜索/不保留旧结果，改成单关键词后实际搜索200且URL规范化。

差量不创建任何SQL/ES业务夹具，不应把其`cleanup`缺省误判为业务清理失败。两轮原脚本分别精确清理自有数据，清理前确认三组各自对分区0提交到log end，报告`partitions=3`指3条group-partition记录，不是3个不同Kafka分区。

## 保留失败与范围

差量第一次未进入浏览器：本地执行wrapper将Playwright路径的父级层数算错，发生`ERR_MODULE_NOT_FOUND`；[原失败日志](ui-public-boundary-initial.txt)、[元数据](ui-public-boundary-command.json)保留。修正执行器路径后同一差量源码通过，未修改产品或将失败轮覆盖为PASS。

差量源码只放在外层本地`.reviews/go076-998b89e/ui-public-boundary.mjs`，未改已交付脚本；SHA-256和三份结果hash见[汇总](ui-review-summary.json)。本阶段完成后已通知父方再进行物理ES故障，未与故障演练并行建夹具。

本次结果仅是固定版本、Chrome、本地真实依赖的页面正确性与恢复验证，不代替GO-073持续负载/检索质量，亦不自动验收GO-074的其它B端/真实AI页面。
