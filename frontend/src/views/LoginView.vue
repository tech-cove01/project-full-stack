<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElAlert, ElButton, ElForm, ElFormItem, ElInput, ElMessage } from 'element-plus'
import 'element-plus/es/components/alert/style/css'
import 'element-plus/es/components/form/style/css'
import 'element-plus/es/components/input/style/css'
import { api, errorMessage } from '../api'
import { useAuthStore } from '../stores/auth'

const phone = ref('')
const code = ref('')
const sending = ref(false)
const submitting = ref(false)
const countdown = ref(0)
const error = ref('')
const developmentCode = ref('')
const auth = useAuthStore()
const router = useRouter()
const route = useRoute()
const validPhone = computed(() => /^1[3-9]\d{9}$/.test(phone.value))
let timer: ReturnType<typeof setInterval> | undefined

async function sendCode() {
  if (!validPhone.value || sending.value || countdown.value > 0) return
  sending.value = true
  error.value = ''
  try {
    const { data } = await api.sendCode(phone.value)
    developmentCode.value = data?.mode === 'development' ? data.code || '' : ''
    if (developmentCode.value) code.value = developmentCode.value
    countdown.value = 60
    timer = setInterval(() => {
      countdown.value -= 1
      if (countdown.value <= 0) clearInterval(timer)
    }, 1000)
    ElMessage.success(
      developmentCode.value
        ? '已填入本地开发验证码。'
        : '验证码已生成，请按服务配置接收或查看后端开发日志。',
    )
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    sending.value = false
  }
}

async function login() {
  if (submitting.value) return
  if (!validPhone.value || !/^\d{6}$/.test(code.value)) {
    error.value = '请输入有效的手机号和 6 位验证码。'
    return
  }
  submitting.value = true
  error.value = ''
  try {
    await auth.login(phone.value, code.value)
    const redirect = route.query.redirect
    await router.replace(
      typeof redirect === 'string' && redirect.startsWith('/') && !redirect.startsWith('//')
        ? redirect
        : '/',
    )
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    submitting.value = false
  }
}
onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <section class="login-layout">
    <div class="login-intro">
      <span class="eyebrow">WELCOME TO FULL STACK</span>
      <h1>下一站，<br />遇见好生活。</h1>
      <p>发现附近好店，把心动的优惠留给自己。</p>
      <RouterLink class="text-link" to="/">先去逛逛 →</RouterLink>
    </div>
    <div class="panel login-panel">
      <h2>手机号登录</h2>
      <p class="muted">首次登录将自动创建账户</p>
      <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon />
      <el-form label-position="top" @submit.prevent="login">
        <el-form-item label="手机号"
          ><el-input
            v-model="phone"
            maxlength="11"
            inputmode="tel"
            autocomplete="tel"
            placeholder="请输入手机号"
            size="large"
        /></el-form-item>
        <el-form-item label="验证码"
          ><div class="code-row">
            <el-input
              v-model="code"
              maxlength="6"
              inputmode="numeric"
              autocomplete="one-time-code"
              placeholder="6 位验证码"
              size="large"
            /><el-button
              size="large"
              :loading="sending"
              :disabled="!validPhone || countdown > 0"
              @click="sendCode"
              >{{ countdown ? `${countdown} 秒后重发` : '获取验证码' }}</el-button
            >
          </div></el-form-item
        >
        <el-button
          class="full-width"
          type="primary"
          size="large"
          native-type="submit"
          :loading="submitting"
          >登录并继续</el-button
        >
      </el-form>
      <p class="dev-note">
        当前暂未接入短信服务。开发模式会返回验证码并自动填入；生产环境需要接入短信供应商。
      </p>
      <el-alert
        v-if="developmentCode"
        :title="`本地开发验证码：${developmentCode}`"
        type="info"
        :closable="false"
      />
    </div>
  </section>
</template>
