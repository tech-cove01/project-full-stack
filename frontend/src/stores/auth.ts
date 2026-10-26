import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api, TOKEN_KEY, LAST_ORDER_KEY } from '../api'
import type { User } from '../types'

export const useAuthStore = defineStore('auth', () => {
  const token = ref(localStorage.getItem(TOKEN_KEY) || '')
  const user = ref<User | null>(null)
  const loggedIn = computed(() => Boolean(token.value))
  let profileGeneration = 0

  async function login(phone: string, code: string) {
    const result = await api.login(phone, code)
    if (typeof result.data !== 'string' || !result.data)
      throw new Error('服务未返回有效的登录凭证。')
    token.value = result.data
    profileGeneration += 1
    user.value = null
    localStorage.setItem(TOKEN_KEY, result.data)
    // Profile loading is separate from authentication; a transient profile error must not undo login.
    void loadProfile().catch(() => {})
  }

  async function verifySession(signal?: AbortSignal) {
    const currentToken = localStorage.getItem(TOKEN_KEY)
    const run = ++profileGeneration
    if (token.value !== (currentToken || '')) user.value = null
    token.value = currentToken || ''
    if (!currentToken) {
      user.value = null
      return null
    }
    const profile = (await api.me(signal, currentToken)).data
    if (localStorage.getItem(TOKEN_KEY) !== currentToken) return null
    if (!profile || !/^[1-9]\d*$/.test(profile.id)) {
      throw new Error('服务未返回有效的当前账号，请重新登录。')
    }
    // A profile requested before logout/login cannot overwrite the new user.
    if (run === profileGeneration) user.value = profile
    return { token: currentToken, user: profile }
  }

  async function loadProfile() {
    await verifySession()
  }

  function clear() {
    profileGeneration += 1
    token.value = ''
    user.value = null
    localStorage.removeItem(TOKEN_KEY)
    localStorage.removeItem(LAST_ORDER_KEY)
    window.dispatchEvent(new CustomEvent('order-remembered'))
  }

  return { token, user, loggedIn, login, loadProfile, verifySession, clear }
})
