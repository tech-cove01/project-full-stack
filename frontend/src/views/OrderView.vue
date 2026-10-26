<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { ElAlert, ElButton } from 'element-plus'
import 'element-plus/es/components/alert/style/css'
import { useRoute } from 'vue-router'
import { api, ApiError, errorMessage, money, rememberOrder } from '../api'
import type { OrderStatus } from '../types'

const route = useRoute()
const status = ref<OrderStatus | null>(null)
const error = ref('')
const unavailable = ref(false)
const querying = ref(false)
const polling = ref(false)
const paused = ref(false)
const orderId = computed(() => String(route.params.id || ''))
const delays = [1000, 1000, 1500, 2000, 3000, 5000, 5000, 8000, 10000, 10000]
let timer: ReturnType<typeof setTimeout> | undefined
let controller: AbortController | undefined
let generation = 0

const statusLabels: Record<number, string> = {
  1: '待支付',
  2: '已支付',
  3: '已核销',
  4: '已取消',
  6: '已退款',
}
const unpaid = computed(() => status.value?.state === 'CREATED' && status.value.orderStatus === 1)

function stop() {
  generation += 1
  clearTimeout(timer)
  timer = undefined
  controller?.abort()
  controller = undefined
  querying.value = false
  polling.value = false
}

async function check(run: number, id: string, attempt = 0) {
  if (run !== generation) return
  const active = new AbortController()
  controller = active
  querying.value = true
  error.value = ''
  try {
    const { data } = await api.order(id, active.signal)
    if (run !== generation || active.signal.aborted) return
    if (!data || data.id !== id || !['PROCESSING', 'CREATED', 'FAILED'].includes(data.state)) {
      throw new ApiError('订单查询结果异常，请稍后重新查询。')
    }
    status.value = data
    rememberOrder(data.id)
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
      timer = undefined
      void check(run, id, attempt + 1)
    }, delays[attempt])
  } catch (cause) {
    if (run === generation && !active.signal.aborted) {
      status.value = null
      error.value = errorMessage(cause)
      unavailable.value =
        cause instanceof ApiError && (cause.status === 403 || cause.status === 404)
      polling.value = false
      paused.value = !unavailable.value
    }
  } finally {
    if (run === generation) {
      querying.value = false
      if (controller === active) controller = undefined
    }
  }
}

function restart() {
  stop()
  status.value = null
  error.value = ''
  unavailable.value = false
  paused.value = false
  const id = orderId.value
  if (!/^[1-9]\d*$/.test(id)) {
    error.value = '订单号格式不正确。'
    return
  }
  polling.value = true
  void check(generation, id)
}

const title = computed(() => {
  if (unavailable.value) return '无法查看此订单'
  if (error.value) return '暂时无法确认订单结果'
  if (status.value?.state === 'FAILED') return '本次抢购未成功'
  if (status.value?.state === 'PROCESSING') return '已受理，正在确认订单'
  if (status.value?.state === 'CREATED') {
    return status.value.orderStatus
      ? statusLabels[status.value.orderStatus] || '订单已创建'
      : '订单已创建'
  }
  return '正在查询订单'
})

watch(orderId, restart, { immediate: true })
onBeforeUnmount(stop)
</script>

<template>
  <section class="order-layout panel" :data-order-id="orderId" :data-order-state="status?.state">
    <span class="eyebrow">YOUR ORDER</span>
    <div class="status-symbol" :class="status?.state === 'FAILED' ? 'failed' : 'processing'">
      {{ status?.state === 'FAILED' ? '×' : status?.state === 'CREATED' ? '✓' : '…' }}
    </div>
    <h1>{{ title }}</h1>
    <p class="order-id">订单号：{{ orderId }}</p>
    <p v-if="status?.state === 'PROCESSING'" class="muted">
      {{ status.message || '本次请求已受理，订单结果仍在确认中。' }} 刷新页面可以继续查询。
    </p>
    <p v-else-if="status?.state === 'FAILED'" class="muted">
      {{
        status.failureReason ||
        status.errorMsg ||
        status.message ||
        '未能创建订单，请返回店铺查看活动情况。'
      }}
    </p>
    <div v-if="status?.state === 'CREATED'" class="order-meta">
      <span v-if="status.shopId">门店编号 {{ status.shopId }}</span>
      <span v-if="status.payAmount != null">金额 ¥{{ money(status.payAmount) }}</span>
      <span v-if="status.orderStatus"
        >状态：{{ statusLabels[status.orderStatus] || '未知状态' }}</span
      >
      <span v-if="status.createTime">创建时间：{{ status.createTime }}（北京时间）</span>
      <span v-if="status.payDeadlineAt">支付截止：{{ status.payDeadlineAt }}（北京时间）</span>
    </div>
    <el-alert
      v-if="status?.state === 'PROCESSING'"
      title="请求已受理不代表下单成功，请以最终订单状态为准。"
      type="info"
      :closable="false"
      show-icon
      class="space-bottom"
    />
    <el-alert
      v-if="unpaid"
      title="当前仅支持订单查询；模拟支付、取消和超时关闭暂未开放。"
      type="info"
      :closable="false"
      show-icon
      class="space-bottom"
    />
    <el-alert
      v-if="error"
      :title="error"
      type="error"
      :closable="false"
      show-icon
      class="space-bottom"
    />
    <p v-if="paused" class="muted" role="status">
      自动查询已暂停，可点击“重新查询”继续确认；等待或网络错误不代表抢券失败。
    </p>
    <div class="order-actions">
      <el-button v-if="unpaid" type="primary" disabled>模拟支付 · 稍后开放</el-button>
      <el-button v-if="unpaid" disabled>取消订单 · 稍后开放</el-button>
      <el-button v-if="!polling" :loading="querying" @click="restart">重新查询</el-button>
      <RouterLink to="/orders"><el-button>我的订单</el-button></RouterLink>
      <RouterLink to="/"><el-button>继续探索店铺</el-button></RouterLink>
    </div>
  </section>
</template>

<style scoped>
.order-actions {
  flex-wrap: wrap;
  max-width: 100%;
}
.order-actions > a {
  display: inline-flex;
  max-width: 100%;
}
.order-actions :deep(.el-button) {
  max-width: 100%;
  min-height: 32px;
  height: auto;
  margin-left: 0;
  white-space: normal;
  line-height: 1.4;
}
</style>
