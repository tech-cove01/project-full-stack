# GO-074c2 门店工具卡片契约

既有Go门店只读工具返回id/name/address，Vue却读取没有返回的area/score，链接缺省时回首页。仅修shops卡片：显示实际name/address，缺省链接按字符串id进入现有 `/shops/:id`；已有同id站内href仍可保留。不新增评分契约、后端工具或模型调用。

根agent非作者审读接口字段、路由、字符串ID和安全站内href回退通过；[前端构建/类型](shops-build.txt)、[格式](shops-format.txt)均PASS。本小修独立提交，随后由真实GoAITool工具页面专项补大ID/地址/点击详情验证，另交运行结果，不把本次静态构建当浏览器通过。实现者自测仍待独立核验。
