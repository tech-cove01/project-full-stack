<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import { ElButton, ElMessage } from 'element-plus'
import 'element-plus/es/components/button/style/css'
import 'element-plus/es/components/message/style/css'
import { api, errorMessage, LAST_ORDER_KEY } from './api'
import { useAuthStore } from './stores/auth'

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()
const lastOrder = ref(localStorage.getItem(LAST_ORDER_KEY) || '')
const loggingOut = ref(false)
function updateLastOrder() {
  lastOrder.value = localStorage.getItem(LAST_ORDER_KEY) || ''
}
function onExpired() {
  auth.clear()
  if (route.name !== 'login') {
    ElMessage.warning('登录已失效，请重新登录。')
    void router.replace({ name: 'login', query: { redirect: route.fullPath } })
  }
}
async function logout() {
  if (loggingOut.value) return
  loggingOut.value = true
  try {
    await api.logout()
    ElMessage.success('已退出登录')
  } catch (cause) {
    ElMessage.warning(`本地登录已退出。${errorMessage(cause)}`)
  } finally {
    auth.clear()
    loggingOut.value = false
    await router.push('/')
  }
}
onMounted(() => {
  window.addEventListener('auth-expired', onExpired)
  window.addEventListener('order-remembered', updateLastOrder)
  void auth.loadProfile().catch(() => {})
})
onBeforeUnmount(() => {
  window.removeEventListener('auth-expired', onExpired)
  window.removeEventListener('order-remembered', updateLastOrder)
})
</script>

<template>
  <div class="app-shell">
    <header class="site-header">
      <div class="header-inner">
        <RouterLink class="brand" to="/"
          ><span class="brand-mark">C</span
          ><span>Full Stack<small>本地生活</small></span></RouterLink
        >
        <nav aria-label="主导航">
          <RouterLink to="/" :class="{ active: route.name === 'explore' || route.name === 'shop' }"
            >探索附近</RouterLink
          >
          <RouterLink to="/merchant" active-class="active">商家工作台</RouterLink>
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
            ><el-button text :loading="loggingOut" @click="logout">退出</el-button></template
          >
          <el-button
            v-else
            type="primary"
            @click="router.push({ name: 'login', query: { redirect: route.fullPath } })"
            >登录 / 注册</el-button
          >
        </div>
      </div>
    </header>
    <main class="main-content"><RouterView /></main>
    <footer class="site-footer">
      <span>Full Stack · 本地生活服务</span><span>探索 · 到店 · 连接</span>
    </footer>
  </div>
</template>
