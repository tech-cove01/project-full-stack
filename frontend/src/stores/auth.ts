import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api, TOKEN_KEY, LAST_ORDER_KEY } from '../api'
import type { User } from '../types'

export const useAuthStore = defineStore('auth', () => {
  const token = ref(localStorage.getItem(TOKEN_KEY) || '')
  const user = ref<User | null>(null)
  const loggedIn = computed(() => Boolean(token.value))

  async function login(phone: string, code: string) {
    const result = await api.login(phone, code)
    if (typeof result.data !== 'string' || !result.data)
      throw new Error('服务未返回有效的登录凭证。')
    token.value = result.data
    localStorage.setItem(TOKEN_KEY, result.data)
    // Profile loading is separate from authentication; a transient profile error must not undo login.
    void loadProfile().catch(() => {})
  }

  async function loadProfile() {
    if (!token.value) return
    user.value = (await api.me()).data
  }

  function clear() {
    token.value = ''
    user.value = null
    localStorage.removeItem(TOKEN_KEY)
    localStorage.removeItem(LAST_ORDER_KEY)
    window.dispatchEvent(new CustomEvent('order-remembered'))
  }

  return { token, user, loggedIn, login, loadProfile, clear }
})
