<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { ElAlert, ElButton, ElMessage, ElMessageBox } from 'element-plus'
import 'element-plus/es/components/alert/style/css'
import 'element-plus/es/components/message-box/style/css'
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
const action = ref<'pay' | 'cancel' | ''>('')
const confirming = ref(false)
const actionError = ref('')
const notice = ref('')
const needsManualCheck = ref(false)
const pendingAction = ref<'pay' | 'cancel' | ''>('')
const orderId = computed(() => String(route.params.id || ''))
const delays = [1000, 1000, 1500, 2000, 3000, 5000, 5000, 8000, 10000, 10000]
let timer: ReturnType<typeof setTimeout> | undefined
let controller: AbortController | undefined
let actionController: AbortController | undefined
let generation = 0

const statusLabels: Record<number, string> = {
  1: '待支付',
  2: '已支付',
  3: '已核销',
  4: '已取消',
  6: '已退款',
}
const unpaid = computed(() => status.value?.state === 'CREATED' && status.value.orderStatus === 1)
const credential = computed(() =>
  status.value?.state === 'CREATED' &&
  status.value.orderStatus === 2 &&
  !error.value &&
  !actionError.value &&
  !action.value
    ? status.value.redeemToken || ''
    : '',
)
const busy = computed(() => !!action.value || confirming.value)
const shouldPoll = computed(() => status.value?.state === 'PROCESSING' || unpaid.value)
const cancelReason = computed(() => {
  const reason = status.value?.cancelReason
  if (!reason) return ''
  return (
    (
      {
        USER_CANCEL: '用户主动取消',
        PAY_TIMEOUT: '支付超时关闭',
        TIMEOUT: '支付超时关闭',
      } as Record<string, string>
    )[reason] || reason
  )
})

function stop() {
  generation += 1
  clearTimeout(timer)
  timer = undefined
  controller?.abort()
  controller = undefined
  actionController?.abort()
  actionController = undefined
  querying.value = false
  polling.value = false
}

function acceptView(data: OrderStatus, id: string) {
  if (!data || data.id !== id || !['PROCESSING', 'CREATED', 'FAILED'].includes(data.state)) {
    throw new ApiError('订单查询结果异常，请稍后重新查询。')
  }
  const next = { ...data }
  if (next.state !== 'CREATED' || next.orderStatus !== 2) delete next.redeemToken
  status.value = next
  rememberOrder(next.id)
}

function reconcileAction(data: OrderStatus) {
  const kind = pendingAction.value
  if (!kind) return
  if (data.state === 'CREATED' && data.orderStatus === (kind === 'pay' ? 2 : 4)) {
    actionError.value = ''
    needsManualCheck.value = false
    pendingAction.value = ''
    notice.value =
      kind === 'pay' ? '模拟支付已确认成功；本次演示不涉及真实资金。' : '订单已取消，稍后查看活动。'
    return
  }
  if (
    data.state === 'FAILED' ||
    (data.state === 'CREATED' && data.orderStatus != null && data.orderStatus !== 1)
  ) {
    needsManualCheck.value = false
    pendingAction.value = ''
    notice.value =
      data.state === 'FAILED'
        ? '当前请求未生成订单，请以页面结果为准。'
        : `当前订单${statusLabels[data.orderStatus!] || '状态已更新'}，本次${kind === 'pay' ? '支付' : '取消'}未确认完成，请以当前状态为准。`
    return
  }
  if (needsManualCheck.value) {
    notice.value = '尚未确认上次操作结果，请先点击“重新查询”，再决定是否继续操作。'
  } else if (actionError.value) {
    pendingAction.value = ''
    notice.value = '当前订单仍待支付，本次操作未完成。'
  }
}

function schedule(run: number, id: string, attempt: number) {
  if (run !== generation || busy.value || !shouldPoll.value || error.value) return
  if (attempt >= delays.length) {
    polling.value = false
    paused.value = true
    return
  }
  polling.value = true
  clearTimeout(timer)
  timer = setTimeout(() => {
    timer = undefined
    void check(run, id, attempt + 1)
  }, delays[attempt])
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
    acceptView(data, id)
    reconcileAction(data)
    if (!shouldPoll.value) {
      polling.value = false
      needsManualCheck.value = false
      return
    }
    schedule(run, id, attempt)
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
  if (confirming.value) ElMessageBox.close()
  stop()
  status.value = null
  error.value = ''
  unavailable.value = false
  paused.value = false
  action.value = ''
  confirming.value = false
  actionError.value = ''
  notice.value = ''
  needsManualCheck.value = false
  pendingAction.value = ''
  const id = orderId.value
  if (!/^[1-9]\d*$/.test(id)) {
    error.value = '订单号格式不正确。'
    return
  }
  polling.value = true
  void check(generation, id)
}

async function perform(kind: 'pay' | 'cancel') {
  if (!unpaid.value || busy.value || needsManualCheck.value) return
  stop()
  const run = generation
  const id = orderId.value
  confirming.value = true
  paused.value = false
  actionError.value = ''
  notice.value = ''
  try {
    await ElMessageBox.confirm(
      kind === 'pay'
        ? '这是本地演示支付，不会扣除真实资金。确认模拟支付此订单吗？'
        : '仅未支付订单可以取消。确定取消此订单吗？',
      kind === 'pay' ? '模拟支付' : '取消订单',
      {
        confirmButtonText: kind === 'pay' ? '确认模拟支付' : '确定取消',
        cancelButtonText: '再想想',
        type: 'warning',
      },
    )
  } catch {
    if (run === generation) {
      confirming.value = false
      schedule(run, id, 0)
    }
    return
  }
  if (run !== generation) return
  confirming.value = false
  action.value = kind
  pendingAction.value = kind
  const active = new AbortController()
  actionController = active
  status.value = null
  error.value = ''
  try {
    const result =
      kind === 'pay'
        ? await api.payOrder(id, active.signal)
        : await api.cancelOrder(id, 'USER_CANCEL', active.signal)
    if (run !== generation) return
    if (!result.data || result.data.id !== id)
      throw new ApiError('操作响应异常，请重新查询订单。', undefined, true)
  } catch (cause) {
    if (run !== generation) return
    actionError.value = errorMessage(cause)
    needsManualCheck.value = cause instanceof ApiError && cause.outcomeUncertain
    notice.value = needsManualCheck.value
      ? '本次操作结果尚未确认，正在查询当前订单。请勿重复提交或切换其他操作。'
      : '操作未完成，正在刷新当前订单状态。'
  }
  if (run !== generation) return
  // A write response never substitutes for a fresh owned query. The same
  // generation fences both requests so a late poll cannot restore stale data.
  await check(run, id)
  if (run !== generation) return
  action.value = ''
  actionController = undefined
  if (needsManualCheck.value && error.value) {
    notice.value = '尚未确认上次操作结果，请先点击“重新查询”，再决定是否继续操作。'
  } else if (error.value) {
    notice.value = '暂时无法确认当前订单，请点击“重新查询”。'
  }
  schedule(run, id, 0)
}

async function copyCredential() {
  const value = credential.value
  if (!value) return
  const run = generation
  try {
    await navigator.clipboard.writeText(value)
    if (run === generation && credential.value === value)
      ElMessage.success('核销凭证已复制，请妥善保管。')
  } catch {
    if (run === generation && credential.value === value)
      ElMessage.warning('复制失败，可手动选中凭证复制。')
  }
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
onBeforeUnmount(() => {
  if (confirming.value) ElMessageBox.close()
  status.value = null
  pendingAction.value = ''
  stop()
})
</script>

<template>
  <section
    class="order-layout panel"
    :data-order-id="orderId"
    :data-order-state="status?.state"
    :data-order-status="status?.orderStatus"
  >
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
      <span v-if="status.payTime">支付时间：{{ status.payTime }}（北京时间）</span>
      <span v-if="status.cancelTime">取消时间：{{ status.cancelTime }}（北京时间）</span>
      <span v-if="cancelReason">取消原因：{{ cancelReason }}</span>
    </div>
    <div v-if="credential" class="redeem-token" data-sensitive="redeem-token">
      <span class="eyebrow">到店核销凭证</span>
      <code class="redeem-token-value">{{ credential }}</code>
      <el-button @click="copyCredential">复制核销凭证</el-button>
      <p class="muted">凭证仅供本人查看，请妥善保管并在到店时按商家要求出示。</p>
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
      title="模拟支付不连接真实资金通道。支付期限由服务端校验，页面时间仅作提示；到期后订单将自动关闭。"
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
    <el-alert
      v-if="actionError"
      :title="actionError"
      type="error"
      :closable="false"
      show-icon
      class="space-bottom"
    />
    <p v-if="notice" class="muted" role="status">{{ notice }}</p>
    <p v-if="paused" class="muted" role="status">
      自动查询已暂停，可点击“重新查询”继续确认当前状态；等待或网络错误不代表操作失败。
    </p>
    <div class="order-actions">
      <el-button
        v-if="unpaid || action === 'pay'"
        type="primary"
        :loading="action === 'pay'"
        :disabled="busy || needsManualCheck"
        @click="perform('pay')"
        >模拟支付</el-button
      >
      <el-button
        v-if="unpaid || action === 'cancel'"
        :loading="action === 'cancel'"
        :disabled="busy || needsManualCheck"
        @click="perform('cancel')"
        >取消订单</el-button
      >
      <el-button :loading="querying" :disabled="busy" @click="restart">重新查询</el-button>
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
.redeem-token code {
  display: block;
  overflow-wrap: anywhere;
}
</style>
