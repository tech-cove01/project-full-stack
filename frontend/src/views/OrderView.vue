<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { ElAlert, ElButton, ElMessage, ElMessageBox } from 'element-plus'
import 'element-plus/es/components/alert/style/css'
import { useRoute } from 'vue-router'
import { api, errorMessage, money, rememberOrder } from '../api'
import type { OrderStatus } from '../types'

const route = useRoute()
const status = ref<OrderStatus | null>(null)
const error = ref('')
const querying = ref(false)
const polling = ref(false)
const paused = ref(false)
const paying = ref(false)
const cancelling = ref(false)
const orderId = computed(() => String(route.params.id))
const delays = [1000, 1000, 1500, 2000, 3000, 5000, 5000, 8000, 10000, 10000]
let timer: ReturnType<typeof setTimeout> | undefined
let controller: AbortController | undefined
let generation = 0

const statusLabels: Record<number, string> = {
  1: '待支付',
  2: '已支付，待到店核销',
  3: '已核销',
  4: '已取消',
  6: '已退款',
}

const canPay = computed(() => status.value?.state === 'CREATED' && status.value.orderStatus === 1)
const canCancel = computed(() => canPay.value)
const token = computed(() => status.value?.redeemToken || '')

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

async function pay() {
  if (paying.value) return
  paying.value = true
  try {
    const { data } = await api.payOrder(orderId.value)
    status.value = data
    ElMessage.success('模拟支付成功，请到店核销。')
  } catch (cause) {
    ElMessage.error(errorMessage(cause))
  } finally {
    paying.value = false
  }
}

async function cancel() {
  if (cancelling.value) return
  try {
    await ElMessageBox.confirm('取消后库存会释放，但已支付订单不能取消。确定取消吗？', '取消订单', {
      confirmButtonText: '确定取消',
      cancelButtonText: '再想想',
      type: 'warning',
    })
  } catch {
    return
  }
  cancelling.value = true
  try {
    const { data } = await api.cancelOrder(orderId.value)
    status.value = data
    ElMessage.success('订单已取消。')
  } catch (cause) {
    ElMessage.error(errorMessage(cause))
  } finally {
    cancelling.value = false
  }
}

async function copyToken() {
  if (!token.value) return
  try {
    await navigator.clipboard.writeText(token.value)
    ElMessage.success('核销码已复制，可交给店员。')
  } catch {
    ElMessage.warning('复制失败，请手动选中核销码。')
  }
}

const title = computed(() => {
  if (status.value?.state === 'FAILED') return '本次抢购未成功'
  if (status.value?.state === 'PROCESSING') return '已受理，正在确认订单'
  if (status.value?.orderStatus) return statusLabels[status.value.orderStatus] || '订单处理中'
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
    <div
      class="status-symbol"
      :class="
        status?.state === 'FAILED' ? 'failed' : status?.orderStatus === 2 ? 'paid' : 'processing'
      "
    >
      {{
        status?.orderStatus === 3
          ? '✓'
          : status?.orderStatus === 4
            ? '—'
            : status?.orderStatus === 6
              ? '↩'
              : status?.orderStatus === 2
                ? '✓'
                : status?.state === 'FAILED'
                  ? '×'
                  : '…'
      }}
    </div>
    <h1>{{ title }}</h1>
    <p class="order-id">订单号：{{ orderId }}</p>
    <p v-if="status?.orderStatus === 1" class="muted">
      请在 {{ status.payDeadlineAt }} 前完成支付，超时订单会自动关闭。
    </p>
    <p v-else-if="status?.state === 'PROCESSING'" class="muted">
      请求已受理，正在异步生成订单。刷新页面可以继续查询。
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
      <span>门店编号 {{ status.shopId }}</span>
      <span>金额 ¥{{ money(status.payAmount) }}</span>
      <span v-if="status.orderStatus">状态：{{ statusLabels[status.orderStatus] }}</span>
    </div>

    <div v-if="token" class="redeem-token">
      <span class="eyebrow">REDEEM TOKEN</span>
      <code>{{ token }}</code>
      <el-button size="small" @click="copyToken">复制核销码</el-button>
      <p class="muted">到店后把核销码交给店员，由店员在商家工作台完成核销。</p>
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
      v-if="status?.orderStatus === 1"
      title="当前为本地演示支付，不连接真实资金通道。"
      type="warning"
      :closable="false"
      show-icon
      class="space-bottom"
    />

    <div class="order-actions">
      <el-button v-if="canPay" type="primary" :loading="paying" @click="pay">模拟支付</el-button>
      <el-button v-if="canCancel" :loading="cancelling" @click="cancel">取消订单</el-button>
      <el-button
        v-if="!polling && !canPay && status?.state !== 'CREATED'"
        type="primary"
        :loading="querying"
        @click="restart"
        >重新查询</el-button
      >
      <RouterLink to="/orders"><el-button>我的订单</el-button></RouterLink>
      <RouterLink to="/"><el-button>继续探索店铺</el-button></RouterLink>
    </div>
  </section>
</template>
