import { createRouter, createWebHistory } from 'vue-router'
import { TOKEN_KEY } from './api'

const router = createRouter({
  history: createWebHistory(),
  scrollBehavior: () => ({ top: 0 }),
  routes: [
    { path: '/', name: 'explore', component: () => import('./views/ExploreView.vue') },
    { path: '/login', name: 'login', component: () => import('./views/LoginView.vue') },
    { path: '/shops/:id', name: 'shop', component: () => import('./views/ShopView.vue') },
    {
      path: '/orders',
      name: 'my-orders',
      component: () => import('./views/MyOrdersView.vue'),
      meta: { requiresAuth: true },
    },
    {
      path: '/orders/:id',
      name: 'order',
      component: () => import('./views/OrderView.vue'),
      meta: { requiresAuth: true },
    },
    {
      path: '/merchant',
      name: 'merchant',
      component: () => import('./views/MerchantView.vue'),
      meta: { requiresAuth: true },
    },
    {
      path: '/merchant/activities',
      name: 'merchant-activities',
      component: () => import('./views/MerchantActivitiesView.vue'),
      meta: { requiresAuth: true },
    },
    {
      path: '/merchant/orders',
      name: 'merchant-orders',
      component: () => import('./views/MerchantOrdersView.vue'),
      meta: { requiresAuth: true },
    },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})

router.beforeEach((to) => {
  if (to.meta.requiresAuth && !localStorage.getItem(TOKEN_KEY)) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
})

export default router
