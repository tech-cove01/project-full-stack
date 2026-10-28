# GO-076 公开笔记全文搜索

起点`290cd60358e0467f43caef4ddd647704bdbcfac6`；进行中。依赖GO-050/052笔记事实和投递、GO-072事件契约（待独立核验的状态不提升）。验收按总方案第9节GO-076及FIN-01/04/05/06，不扩大为商铺搜索、向量搜索或推荐。

小功能：a公开笔记可搜索（固定ES资源/mapping/CJK、初始化重建、API、门店过滤、明确排序分页和Vue入口）；b新笔记Kafka同步（独立search-v1组、版本与持久失败重放）；c真实停机/重投/重建与页面故障提示。实现较大时按必要独立行为再细分，小功能适用自测和非作者审查后commit，独立缺陷另提交。

ES固定`docker.elastic.co/elasticsearch/elasticsearch:8.19.23`，Go stdlib net/http无额外SDK。标题/正文用内置`cjk`分析器，中文双字切分行为用独立样例实测，不宣称业务词典/智能中文分词。公开字段白名单，MySQL事实决定最终输出，ES失败明确503而非LIKE替代。

分页计划使用PIT和search_after，相关度或最新发布明确排序，ID字符串保持精度。滞后索引命中须经当前MySQL公开状态与版本复核，允许短页，不返回假精确总数；游标取原始候选末项以便继续。非法/过期游标明确要求重新检索。

重建计划生成独立物理代次并原子切换别名，数据库control锁与增量索引应用串行；旧收据/offset不清空，失败保留原别名。保留旧代次供核对，不自动删除未知索引。只有真实交错证据通过后才说明具体恢复边界，不宣传零停机。

专用Compose project`project-full-stack-go076`：MySQL3332、Redis6404、Kafka9099、ES9202；API8086/Vite5176用于本票页面。其它核验agent正在用3340/6410/9104，不复用或停止其服务。真实模型关闭，不读取/发送私密材料，不付费调用。

依据：[固定版本](https://www.elastic.co/guide/en/elasticsearch/reference/8.19/release-notes-8.19.23.html)、[CJK内置分析器](https://www.elastic.co/guide/en/elasticsearch/reference/8.19/analysis-lang-analyzer.html#cjk-analyzer)、[分页](https://www.elastic.co/guide/en/elasticsearch/reference/8.19/paginate-search-results.html)、[版本化索引](https://www.elastic.co/guide/en/elasticsearch/reference/8.19/docs-index_.html)、[原子别名操作](https://www.elastic.co/guide/en/elasticsearch/reference/8.19/aliases.html)。

尚未运行本票真实依赖或页面验证，最终记录实际失败/成功、SHA、命令、清理及未执行项。按sql-pro检查迁移/事务、按playwright-expert做直接相关页面验证，不增加无关指标或Mock业务。
