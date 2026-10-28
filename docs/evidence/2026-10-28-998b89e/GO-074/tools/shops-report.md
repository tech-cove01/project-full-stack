# GO-074c2：门店工具卡片与既有DTO一致

起点 `05f0baa52f14566566339786b10ef8d4193cb4ed`。后端 `aitool.shop` 的既有 items DTO 仅有字符串id、name、address；Vue原来读取不存在的area/score，默认href又回到首页。该独立修复只改 `AssistantView.vue` shops分支：显示name/address，缺href时链接到对应 `/shops/:id`，保留同id有效站内href的兼容。没有修改活动分支、工具路由或后端契约，没有发明评分。

已执行前端构建（含vue-tsc）及全前端格式检查，均PASS：[构建](shops-build.txt)、[格式](shops-format.txt)。来源/模型等其它卡片不随此提交改写。真实AITool页面与大ID详情点击随后由独立工具脚本取证，不把类型/格式通过当作浏览器验收，也不重新调用真实模型。

状态：主实现者独立审查后已提交 `13d5f49c787740f9bf2b83b9c843fb4ce3d6e1c1`。随后真实工具页面、大ID详情点击与交易后库存刷新已完成专项自测，结果另见 [report.md](report.md)，不重写本独立源代码提交。
