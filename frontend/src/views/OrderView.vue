<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { ElAlert, ElButton } from 'element-plus'
import 'element-plus/es/components/alert/style/css'
import { useRoute } from 'vue-router'
import { api, errorMessage, rememberOrder } from '../api'
import type { OrderStatus } from '../types'

const route = useRoute()
const status = ref<OrderStatus | null>(null)
const error = ref('')
const querying = ref(false)
const polling = ref(false)
const paused = ref(false)
const orderId = computed(() => String(route.params.id))
const delays = [1000, 1000, 1500, 2000, 3000, 5000, 5000, 8000, 10000, 10000]
let timer: ReturnType<typeof setTimeout> | undefined
let controller: AbortController | undefined
let generation = 0

function stop() {
  generation += 1
  clearTimeout(timer)
  controller?.abort()
  querying.value = false
  polling.value = false
}

async function check(run: number, attempt = 0) {
  if (run !== generation) return
  const active = new AbortController()
  controller = active
  querying.value = true
  error.value = ''
  try {
    const { data } = await api.order(orderId.value, active.signal)
    if (run !== generation) return
    status.value = data
    if (data.state !== 'PROCESSING') {
      polling.value = false
      return
    }
    if (attempt >= delays.length) {
      polling.value = false
      paused.value = true
      return
    }
    timer = setTimeout(() => {
      void check(run, attempt + 1)
    }, delays[attempt])
  } catch (cause) {
    if (run === generation && !active.signal.aborted) {
      error.value = errorMessage(cause)
      polling.value = false
      paused.value = true
    }
  } finally {
    if (run === generation) querying.value = false
  }
}

function restart() {
  stop()
  paused.value = false
  if (!/^\d+$/.test(orderId.value)) {
    error.value = '订单号格式不正确。'
    return
  }
  rememberOrder(orderId.value)
  polling.value = true
  void check(generation)
}

const title = computed(() => {
  if (status.value?.state === 'CREATED') return '抢购成功，订单已创建'
  if (status.value?.state === 'FAILED') return '本次抢购未成功'
  if (error.value) return '暂时无法确认订单结果'
  if (paused.value) return '订单仍在处理中'
  return '已受理，正在确认订单'
})
watch(
  orderId,
  () => {
    status.value = null
    restart()
  },
  { immediate: true },
)
onBeforeUnmount(stop)
</script>

<template>
  <section class="order-layout panel">
    <span class="eyebrow">YOUR ORDER</span>
    <div class="status-symbol" :class="status?.state?.toLowerCase()">
      {{ status?.state === 'CREATED' ? '✓' : status?.state === 'FAILED' ? '×' : '…' }}
    </div>
    <h1>{{ title }}</h1>
    <p class="order-id">订单号：{{ orderId }}</p>
    <p v-if="status?.state === 'CREATED'" class="muted">
      订单已保存。支付与到店核销功能将在后续版本开放。
    </p>
    <p v-else-if="status?.state === 'FAILED'" class="muted">
      {{
        status.failureReason ||
        status.errorMsg ||
        status.message ||
        '未能创建订单，请返回店铺查看活动情况。'
      }}
    </p>
    <p v-else-if="polling" class="muted">
      正在自动更新结果，请稍候。刷新页面后可以继续查询这笔订单。
    </p>
    <p v-else class="muted">
      {{ error || '自动查询已暂停，处理结果尚未确认。可以稍后手动刷新，请勿重复抢购。' }}
    </p>
    <el-alert
      v-if="status?.state === 'PROCESSING'"
      title="请求已受理不代表下单成功，请以最终订单状态为准。"
      type="info"
      :closable="false"
      show-icon
      class="space-bottom"
    />
    <div class="order-actions">
      <el-button
        v-if="!polling && status?.state !== 'CREATED' && status?.state !== 'FAILED'"
        type="primary"
        :loading="querying"
        @click="restart"
        >重新查询</el-button
      ><RouterLink to="/"><el-button>继续探索店铺</el-button></RouterLink>
    </div>
  </section>
</template>
