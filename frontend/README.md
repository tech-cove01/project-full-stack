# Vue 前端源码入口

[src/main.ts](src/main.ts) 启动应用，[src/App.vue](src/App.vue) 提供页面外壳，[src/router.ts](src/router.ts) 连接路由与业务页面。页面放在 `src/views/`，通过 [src/api.ts](src/api.ts) 调用 Go API；[src/types.ts](src/types.ts) 维护接口类型，[src/stores/auth.ts](src/stores/auth.ts) 管理身份状态。

| 业务域 | 页面入口 |
| --- | --- |
| 登录与商铺 | [LoginView](src/views/LoginView.vue)、[ExploreView](src/views/ExploreView.vue)、[ShopView](src/views/ShopView.vue) |
| 用户订单 | [MyOrdersView](src/views/MyOrdersView.vue)、[OrderView](src/views/OrderView.vue) |
| 商家工作台 | [MerchantView](src/views/MerchantView.vue)、[MerchantActivitiesView](src/views/MerchantActivitiesView.vue)、[MerchantOrdersView](src/views/MerchantOrdersView.vue) |
| 探店社区 | [FeedView](src/views/FeedView.vue)、[NoteView](src/views/NoteView.vue)、[NoteSearchView](src/views/NoteSearchView.vue) |
| 业务 AI 与知识 | [AssistantView](src/views/AssistantView.vue)、[PublicKnowledgeView](src/views/PublicKnowledgeView.vue)、[MerchantKnowledgeView](src/views/MerchantKnowledgeView.vue) |

从 router 找到页面，再追踪页面调用的 API 函数和后端模块。前端路由守卫与按钮控制负责交互，实际身份、门店权限和数据归属由后端再次校验。前后端调用关系见[源码讲解路线](../docs/source-walkthrough.md)。

本目录执行 `npm run dev` 启动开发页面，`npm run build` 完成类型检查与构建，`npm run format:check` 检查格式。依赖和环境配置见[开发说明](../docs/development.md)。
