<script setup lang="ts">
import { defineAsyncComponent, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import { ElButton, ElMessage } from 'element-plus'
import 'element-plus/es/components/button/style/css'
import 'element-plus/es/components/message/style/css'
import { api, errorMessage, isShowcase, LAST_ORDER_KEY, TOKEN_KEY } from './api'
import { useAuthStore } from './stores/auth'

const auth = useAuthStore()
const DemoToolbar =
  import.meta.env.MODE === 'showcase'
    ? defineAsyncComponent(() => import('./showcase/DemoToolbar.vue'))
    : null
const route = useRoute()
const router = useRouter()
const lastOrder = ref(localStorage.getItem(LAST_ORDER_KEY) || '')
const loggingOut = ref(false)
const menuOpen = ref(false)
const menuButton = ref<HTMLButtonElement | null>(null)
watch(
  () => route.fullPath,
  () => {
    menuOpen.value = false
  },
)

function closeMenuOnEscape() {
  if (!menuOpen.value) return
  menuOpen.value = false
  menuButton.value?.focus()
}

function updateLastOrder() {
  lastOrder.value = localStorage.getItem(LAST_ORDER_KEY) || ''
}
function syncDemoTab(event: StorageEvent) {
  if (event.key === TOKEN_KEY) void auth.loadProfile().catch(() => {})
  if (event.key === LAST_ORDER_KEY) updateLastOrder()
}
function onExpired() {
  auth.clear()
  if (isShowcase) {
    void router.replace('/')
    return
  }
  if (route.name !== 'login') {
    ElMessage.warning('登录已失效，请重新登录。')
    void router.replace({ name: 'login', query: { redirect: route.fullPath } })
  }
}
async function logout() {
  if (loggingOut.value) return
  loggingOut.value = true
  const logoutToken = localStorage.getItem(TOKEN_KEY)
  const hasReplacementSession = () => {
    const currentToken = localStorage.getItem(TOKEN_KEY)
    return currentToken !== null && currentToken !== logoutToken
  }
  try {
    await api.logout()
    if (!hasReplacementSession()) ElMessage.success('已退出登录')
  } catch (cause) {
    if (!hasReplacementSession()) ElMessage.warning(`本地登录已退出。${errorMessage(cause)}`)
  } finally {
    loggingOut.value = false
    // A delayed logout belongs to its original session. An actual 401 may
    // already have cleared that session; neither case may clear a newer one.
    if (!hasReplacementSession()) {
      auth.clear()
      await router.push('/')
    }
  }
}
onMounted(() => {
  window.addEventListener('auth-expired', onExpired)
  window.addEventListener('order-remembered', updateLastOrder)
  if (isShowcase) window.addEventListener('storage', syncDemoTab)
  void auth.loadProfile().catch(() => {})
})
onBeforeUnmount(() => {
  window.removeEventListener('auth-expired', onExpired)
  window.removeEventListener('order-remembered', updateLastOrder)
  if (isShowcase) window.removeEventListener('storage', syncDemoTab)
})
</script>

<template>
  <div class="app-shell">
    <header class="site-header" @keydown.esc="closeMenuOnEscape">
      <div class="header-inner">
        <RouterLink class="brand" to="/"
          ><span class="brand-mark">C</span
          ><span>Full Stack<small>本地生活</small></span></RouterLink
        >
        <nav
          id="product-nav"
          class="product-nav"
          :class="{ 'is-open': menuOpen }"
          aria-label="主导航"
          @click="menuOpen = false"
        >
          <RouterLink to="/" :class="{ active: route.name === 'explore' || route.name === 'shop' }"
            >探索附近</RouterLink
          >
          <RouterLink to="/merchant" active-class="active">商家工作台</RouterLink>
          <RouterLink to="/notes/search" active-class="active">搜索笔记</RouterLink>
          <RouterLink v-if="auth.loggedIn" to="/assistant" active-class="active"
            >AI 助手</RouterLink
          >
          <RouterLink v-if="auth.loggedIn" to="/feed" active-class="active">关注动态</RouterLink>
          <RouterLink v-if="auth.loggedIn" to="/notes/new" active-class="active"
            >发布笔记</RouterLink
          >
          <RouterLink v-if="auth.loggedIn" to="/orders" active-class="active">我的订单</RouterLink>
          <RouterLink
            v-if="lastOrder && auth.loggedIn"
            :to="`/orders/${lastOrder}`"
            active-class="active"
            >最近订单</RouterLink
          >
        </nav>
        <div class="account-actions">
          <template v-if="auth.loggedIn"
            ><span class="nickname">{{ auth.user?.nickName || '已登录' }}</span
            ><el-button v-if="!isShowcase" text :loading="loggingOut" @click="logout"
              >退出</el-button
            ></template
          >
          <el-button
            v-else
            type="primary"
            @click="router.push({ name: 'login', query: { redirect: route.fullPath } })"
            >登录 / 注册</el-button
          >
          <button
            ref="menuButton"
            class="header-nav-toggle"
            type="button"
            :aria-expanded="menuOpen"
            aria-controls="product-nav"
            @click="menuOpen = !menuOpen"
          >
            导航
          </button>
        </div>
      </div>
    </header>
    <component :is="DemoToolbar" v-if="DemoToolbar" />
    <main class="main-content"><RouterView :key="isShowcase ? auth.token : undefined" /></main>
    <footer class="site-footer">
      <span>Full Stack · 本地生活服务</span><span>探索 · 到店 · 连接</span>
    </footer>
  </div>
</template>
