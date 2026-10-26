<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import {
  ElAlert,
  ElButton,
  ElDialog,
  ElEmpty,
  ElForm,
  ElFormItem,
  ElInput,
  ElInputNumber,
  ElMessage,
  ElMessageBox,
  ElOption,
  ElSelect,
  ElSkeleton,
  ElTable,
  ElTableColumn,
  ElTag,
} from 'element-plus'
import 'element-plus/es/components/alert/style/css'
import 'element-plus/es/components/dialog/style/css'
import 'element-plus/es/components/empty/style/css'
import 'element-plus/es/components/form/style/css'
import 'element-plus/es/components/input-number/style/css'
import 'element-plus/es/components/input/style/css'
import 'element-plus/es/components/select/style/css'
import 'element-plus/es/components/skeleton/style/css'
import 'element-plus/es/components/table/style/css'
import 'element-plus/es/components/table-column/style/css'
import 'element-plus/es/components/tag/style/css'
import { api, ApiError, errorMessage, money, parseBusinessTime } from '../api'
import type { Activity, CreateActivityRequest, MerchantWorkspace } from '../types'

const workspace = ref<MerchantWorkspace | null>(null)
const activities = ref<Activity[]>([])
const loading = ref(false)
const error = ref('')
const current = ref(1)
const pageSize = ref(10)
const total = ref<string | null>(null)
const filterShop = ref('')
const filterStatus = ref<Activity['publishStatus'] | ''>('')
const dialogOpen = ref(false)
const submitting = ref(false)
const busyId = ref('')
const publishError = ref('')
const pending = ref<{ key: string; body: Readonly<CreateActivityRequest> } | null>(null)
let controller: AbortController | undefined
let mutationController: AbortController | undefined
let pollTimer: number | undefined
let pollAttempts = 0
let disposed = false

const shops = computed(() =>
  (workspace.value?.merchants || []).flatMap((merchant) =>
    merchant.shops.map((shop) => ({ ...shop, role: merchant.role })),
  ),
)
const managedShops = computed(() =>
  shops.value.filter((shop) => shop.role === 'OWNER' || shop.role === 'MANAGER'),
)
const managedShopIds = computed(() => new Set(managedShops.value.map((shop) => shop.id)))
const canPublish = computed(() => managedShops.value.length > 0)
const canSubmit = computed(() =>
  managedShopIds.value.has(pending.value?.body.shopId || form.shopId),
)
const hasNext = computed(() => {
  if (!Number.isSafeInteger(current.value + 1)) return false
  return total.value === null
    ? activities.value.length === pageSize.value
    : BigInt(current.value) * BigInt(pageSize.value) < BigInt(total.value)
})
const hasPreparing = computed(() =>
  activities.value.some((activity) => activity.publishStatus !== 'OFFLINE' && !activity.ready),
)
const fieldsDisabled = computed(() => submitting.value || !!pending.value)

const businessFormatter = new Intl.DateTimeFormat('en-CA', {
  timeZone: 'Asia/Shanghai',
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hourCycle: 'h23',
})

function businessTime(offsetMs: number) {
  const parts = Object.fromEntries(
    businessFormatter.formatToParts(Date.now() + offsetMs).map((part) => [part.type, part.value]),
  )
  return `${parts.year}-${parts.month}-${parts.day}T${parts.hour}:${parts.minute}:${parts.second}`
}

const form = reactive({
  shopId: '',
  title: '',
  subTitle: '',
  rules: '',
  payYuan: '9.90',
  actualYuan: '20.00',
  totalStock: 100,
  beginTime: businessTime(-60_000),
  endTime: businessTime(10 * 86400_000),
  redeemBeginTime: businessTime(-60_000),
  redeemEndTime: businessTime(20 * 86400_000),
})

function stopPolling() {
  if (pollTimer !== undefined) window.clearTimeout(pollTimer)
  pollTimer = undefined
}

function schedulePoll() {
  if (
    disposed ||
    loading.value ||
    submitting.value ||
    busyId.value ||
    error.value ||
    pollTimer !== undefined ||
    pollAttempts >= 12 ||
    !hasPreparing.value
  )
    return
  pollTimer = window.setTimeout(() => {
    pollTimer = undefined
    pollAttempts += 1
    void load(true)
  }, 1500)
}

function normalizeTotal(value: string | number | undefined): string | null {
  if (typeof value === 'string' && /^\d+$/.test(value)) return BigInt(value).toString()
  if (typeof value === 'number' && Number.isSafeInteger(value) && value >= 0) return String(value)
  return null
}

async function load(poll = false) {
  if (disposed) return
  stopPolling()
  if (!poll) pollAttempts = 0
  controller?.abort()
  const active = new AbortController()
  controller = active
  loading.value = true
  error.value = ''
  try {
    const workspaceResult = await api.workspace(active.signal)
    if (active.signal.aborted) return
    workspace.value = workspaceResult.data
    if (filterShop.value && !shops.value.some((shop) => shop.id === filterShop.value)) {
      filterShop.value = ''
      current.value = 1
    }
    if (!pending.value && !managedShopIds.value.has(form.shopId)) {
      form.shopId = managedShops.value[0]?.id || ''
    }
    const activityResult = await api.activities(current.value, pageSize.value, active.signal, {
      shopId: filterShop.value || undefined,
      status: filterStatus.value || undefined,
    })
    if (active.signal.aborted) return
    activities.value = activityResult.data || []
    total.value = normalizeTotal(activityResult.total)
    if (!activities.value.length && current.value > 1 && total.value !== null) {
      const count = BigInt(total.value)
      if (BigInt(current.value - 1) * BigInt(pageSize.value) >= count) {
        current.value = count === 0n ? 1 : Number((count - 1n) / BigInt(pageSize.value) + 1n)
        void load(poll)
      }
    }
  } catch (cause) {
    if (!active.signal.aborted) {
      workspace.value = null
      activities.value = []
      total.value = null
      error.value = errorMessage(cause)
    }
  } finally {
    if (!active.signal.aborted) {
      loading.value = false
      schedulePoll()
    }
  }
}

function applyFilters() {
  current.value = 1
  void load()
}

function changePage(direction: -1 | 1) {
  if (loading.value || (direction === -1 ? current.value <= 1 : !hasNext.value)) return
  current.value += direction
  void load()
}

function resetForm() {
  pending.value = null
  publishError.value = ''
  Object.assign(form, {
    shopId: managedShops.value[0]?.id || '',
    title: '',
    subTitle: '',
    rules: '',
    payYuan: '9.90',
    actualYuan: '20.00',
    totalStock: 100,
    beginTime: businessTime(-60_000),
    endTime: businessTime(10 * 86400_000),
    redeemBeginTime: businessTime(-60_000),
    redeemEndTime: businessTime(20 * 86400_000),
  })
}

function openDialog() {
  if (!pending.value) resetForm()
  dialogOpen.value = true
}

async function abandonPublish() {
  if (submitting.value) return
  try {
    await ElMessageBox.confirm(
      '上次发布可能已经成功。请先刷新活动列表核对；另起一次发布会创建新的活动。确定放弃本次重试并新建吗？',
      '新建另一活动',
      { confirmButtonText: '放弃本次并新建', cancelButtonText: '保留并重试', type: 'warning' },
    )
  } catch {
    return
  }
  if (!disposed && !submitting.value) resetForm()
}

function cents(value: string, name: string): string {
  const text = value.trim()
  if (!/^\d+(?:\.\d{1,2})?$/.test(text)) {
    throw new Error(`${name}请填写最多两位小数的正数。`)
  }
  const [whole = '0', fraction = ''] = text.split('.')
  const amount = BigInt(whole) * 100n + BigInt(fraction.padEnd(2, '0'))
  if (amount <= 0n || amount > 9223372036854775807n) {
    throw new Error(`${name}必须大于零且不超过 92233720368547758.07 元。`)
  }
  return amount.toString()
}

function offsetTime(value: string, name: string): string {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(?::\d{2})?$/.test(value)) {
    throw new Error(`请填写${name}，全部时间均为北京时间。`)
  }
  const result = `${value.length === 16 ? `${value}:00` : value}+08:00`
  if (!Number.isFinite(Date.parse(result))) throw new Error(`${name}格式不正确。`)
  return result
}

function publishBody(): Readonly<CreateActivityRequest> {
  if (!form.shopId || !managedShopIds.value.has(form.shopId) || !form.title.trim()) {
    throw new Error('请选择可管理的门店并填写活动标题。')
  }
  const payValue = cents(form.payYuan, '购买价格')
  const actualValue = cents(form.actualYuan, '抵扣面值')
  if (BigInt(payValue) > BigInt(actualValue)) throw new Error('购买价格不能高于抵扣面值。')
  if (
    !Number.isSafeInteger(form.totalStock) ||
    form.totalStock < 1 ||
    form.totalStock > 1_000_000
  ) {
    throw new Error('库存必须为 1 到 1000000 的整数。')
  }
  const beginTime = offsetTime(form.beginTime, '销售开始时间')
  const endTime = offsetTime(form.endTime, '销售结束时间')
  const redeemBeginTime = offsetTime(form.redeemBeginTime, '核销开始时间')
  const redeemEndTime = offsetTime(form.redeemEndTime, '核销结束时间')
  if (parseBusinessTime(beginTime) >= parseBusinessTime(endTime)) {
    throw new Error('销售开始时间必须早于结束时间。')
  }
  if (parseBusinessTime(redeemBeginTime) >= parseBusinessTime(redeemEndTime)) {
    throw new Error('核销开始时间必须早于结束时间。')
  }
  if (parseBusinessTime(redeemEndTime) <= parseBusinessTime(endTime)) {
    throw new Error('核销结束时间必须晚于销售结束时间。')
  }
  return Object.freeze({
    shopId: form.shopId,
    title: form.title.trim(),
    subTitle: form.subTitle.trim() || null,
    rules: form.rules.trim() || null,
    payValue,
    actualValue,
    totalStock: form.totalStock,
    beginTime,
    endTime,
    redeemBeginTime,
    redeemEndTime,
  })
}

async function submit() {
  if (submitting.value || busyId.value || loading.value || disposed) return
  publishError.value = ''
  if (!canSubmit.value) {
    publishError.value = '当前门店已无管理权限，请刷新授权或选择可管理的门店。'
    return
  }
  try {
    if (!pending.value) pending.value = { key: crypto.randomUUID(), body: publishBody() }
  } catch (cause) {
    publishError.value = errorMessage(cause)
    return
  }
  const attempt = pending.value
  if (!attempt) return
  stopPolling()
  submitting.value = true
  const active = new AbortController()
  mutationController = active
  try {
    const { data } = await api.createActivity(attempt.body, attempt.key, active.signal)
    if (active.signal.aborted) return
    ElMessage.success(`活动 ${data.id} 已创建，可在列表查看发布状态。`)
    dialogOpen.value = false
    pending.value = null
    filterShop.value = data.shopId
    filterStatus.value = ''
    current.value = 1
    await load()
  } catch (cause) {
    if (!active.signal.aborted) {
      publishError.value = errorMessage(cause)
      if (cause instanceof ApiError && (cause.status === 401 || cause.status === 403)) await load()
    }
  } finally {
    if (!active.signal.aborted) {
      submitting.value = false
      mutationController = undefined
      schedulePoll()
    }
  }
}

async function mutate(activity: Activity, action: 'retry' | 'offline') {
  if (
    disposed ||
    loading.value ||
    submitting.value ||
    busyId.value ||
    !managedShopIds.value.has(activity.shopId) ||
    activity.publishStatus === 'OFFLINE'
  )
    return
  if (action === 'offline') {
    try {
      await ElMessageBox.confirm(
        `下架后停止接受新购买，已受理和已创建的订单仍按原规则履约。确定下架“${activity.title}”吗？`,
        '下架活动',
        { confirmButtonText: '确定下架', cancelButtonText: '取消', type: 'warning' },
      )
    } catch {
      return
    }
    if (disposed || loading.value || submitting.value || busyId.value) return
  }
  stopPolling()
  busyId.value = activity.id
  const active = new AbortController()
  mutationController = active
  try {
    if (action === 'retry') await api.retryActivity(activity.id, active.signal)
    else await api.offlineActivity(activity.id, active.signal)
    if (active.signal.aborted) return
    ElMessage.success(action === 'retry' ? '已请求重新发布，请查看活动状态。' : '活动已下架。')
    await load()
  } catch (cause) {
    if (!active.signal.aborted) {
      ElMessage.error(errorMessage(cause))
      if (cause instanceof ApiError && (cause.status === 401 || cause.status === 403)) await load()
    }
  } finally {
    if (!active.signal.aborted) {
      busyId.value = ''
      mutationController = undefined
      schedulePoll()
    }
  }
}

function statusLabel(activity: Activity) {
  if (activity.publishStatus === 'OFFLINE') return '已下架'
  if (activity.publishStatus === 'INITIALIZING') return '准备中'
  return activity.ready ? '已发布' : '待开放'
}

function statusType(activity: Activity): 'success' | 'warning' | 'info' {
  if (activity.publishStatus === 'OFFLINE') return 'info'
  return activity.ready ? 'success' : 'warning'
}

onMounted(() => void load())
onBeforeUnmount(() => {
  disposed = true
  stopPolling()
  controller?.abort()
  mutationController?.abort()
})
</script>

<template>
  <section class="merchant-header">
    <span class="eyebrow">ACTIVITIES</span>
    <h1>活动管理</h1>
    <p>发布与下架门店优惠活动；发布后价格、库存和时间不可修改。</p>
  </section>
  <div class="page-top merchant-actions">
    <RouterLink class="text-link" to="/merchant">← 返回工作台</RouterLink>
    <el-button :loading="loading" :disabled="submitting || !!busyId" @click="load()"
      >刷新授权与活动</el-button
    >
    <el-button
      v-if="canPublish || pending"
      type="primary"
      :disabled="loading || !!busyId"
      @click="openDialog"
    >
      {{ pending ? '继续上次发布' : '发布活动' }}
    </el-button>
  </div>
  <section class="panel activity-filters" aria-label="活动筛选">
    <div class="filter-row">
      <label for="activity-shop">门店</label>
      <el-select
        id="activity-shop"
        v-model="filterShop"
        aria-label="筛选门店"
        placeholder="全部授权门店"
        :disabled="loading || submitting || !!busyId"
        @change="applyFilters"
      >
        <el-option label="全部授权门店" value="" />
        <el-option
          v-for="shop in shops"
          :key="shop.id"
          :label="`${shop.name}（${shop.id}）`"
          :value="shop.id"
        />
      </el-select>
      <label for="activity-status">状态</label>
      <el-select
        id="activity-status"
        v-model="filterStatus"
        aria-label="筛选状态"
        placeholder="全部状态"
        :disabled="loading || submitting || !!busyId"
        @change="applyFilters"
      >
        <el-option label="全部状态" value="" />
        <el-option label="准备中" value="INITIALIZING" />
        <el-option label="已发布 / 待开放" value="ACTIVE" />
        <el-option label="已下架" value="OFFLINE" />
      </el-select>
    </div>
    <p class="muted">
      列表包含你当前可查看的门店；发布、重试和下架仅对可管理的门店开放。全部时间均为北京时间。
    </p>
  </section>
  <el-alert
    v-if="error"
    :title="error"
    type="error"
    :closable="false"
    show-icon
    class="space-bottom"
  />
  <el-skeleton v-if="loading" :rows="6" animated aria-label="正在加载活动" />
  <el-empty v-else-if="error" description="暂时无法加载活动">
    <el-button @click="load()">重新加载</el-button>
  </el-empty>
  <el-empty
    v-else-if="!activities.length"
    :description="shops.length ? '当前筛选下没有活动' : '当前账号没有可查看的门店活动'"
  >
    <el-button v-if="canPublish" type="primary" @click="openDialog">发布活动</el-button>
  </el-empty>
  <section v-else class="panel">
    <el-table :data="activities" row-key="id" row-class-name="activity-row" style="width: 100%">
      <el-table-column label="活动编号" min-width="170">
        <template #default="{ row }"
          ><span class="activity-number" :data-activity-id="row.id" :data-shop-id="row.shopId">{{
            row.id
          }}</span></template
        >
      </el-table-column>
      <el-table-column label="标题" min-width="180">
        <template #default="{ row }">
          <strong>{{ row.title }}</strong>
          <div class="muted">{{ row.shopName }} · {{ row.subTitle || '门店活动' }}</div>
        </template>
      </el-table-column>
      <el-table-column label="价格 / 面值" min-width="190">
        <template #default="{ row }">
          <div class="activity-prices">
            <span class="activity-number">¥{{ money(row.payValue) }}</span>
            <span class="activity-number">/ ¥{{ money(row.actualValue) }}</span>
          </div>
        </template>
      </el-table-column>
      <el-table-column label="库存" width="110">
        <template #default="{ row }">{{ row.stock }} / {{ row.totalStock }}</template>
      </el-table-column>
      <el-table-column label="状态" width="110">
        <template #default="{ row }"
          ><el-tag :type="statusType(row as Activity)">{{
            statusLabel(row as Activity)
          }}</el-tag></template
        >
      </el-table-column>
      <el-table-column label="销售时间（北京时间）" min-width="190">
        <template #default="{ row }"
          >{{ row.beginTime }}<br /><span class="muted">至 {{ row.endTime }}</span></template
        >
      </el-table-column>
      <el-table-column label="操作" width="180">
        <template #default="{ row }">
          <el-button
            v-if="managedShopIds.has(row.shopId) && row.publishStatus !== 'OFFLINE' && !row.ready"
            link
            type="primary"
            :loading="busyId === row.id"
            :disabled="loading || submitting || !!busyId"
            @click="mutate(row as Activity, 'retry')"
            >重试发布</el-button
          >
          <el-button
            v-if="managedShopIds.has(row.shopId) && row.publishStatus !== 'OFFLINE'"
            link
            type="danger"
            :loading="busyId === row.id"
            :disabled="loading || submitting || !!busyId"
            @click="mutate(row as Activity, 'offline')"
            >下架</el-button
          >
          <RouterLink class="text-link" :to="`/shops/${row.shopId}`">看店铺 →</RouterLink>
        </template>
      </el-table-column>
    </el-table>
    <p v-if="hasPreparing" class="muted" role="status">
      部分活动正在准备。页面会短暂自动刷新，也可手动刷新或由管理者重试发布。
    </p>
  </section>
  <div
    v-if="!loading && !error && (activities.length || current > 1)"
    class="activity-pagination"
    aria-label="活动分页"
  >
    <span
      >第 {{ current }} 页<span v-if="total !== null"> · 共 {{ total }} 条</span></span
    >
    <el-select
      v-model="pageSize"
      aria-label="每页活动数"
      :disabled="submitting || !!busyId"
      @change="applyFilters"
    >
      <el-option
        v-for="size in [10, 20, 50]"
        :key="size"
        :label="`每页 ${size} 条`"
        :value="size"
      />
    </el-select>
    <el-button :disabled="current <= 1 || submitting || !!busyId" @click="changePage(-1)"
      >上一页</el-button
    >
    <el-button :disabled="!hasNext || submitting || !!busyId" @click="changePage(1)"
      >下一页</el-button
    >
  </div>

  <el-dialog
    v-model="dialogOpen"
    title="发布门店活动"
    width="720px"
    :close-on-click-modal="false"
    :close-on-press-escape="!submitting"
    :show-close="!submitting"
  >
    <el-alert
      v-if="publishError"
      :title="publishError"
      type="error"
      :closable="false"
      show-icon
      class="space-bottom"
    />
    <el-alert
      v-if="pending"
      title="本次内容已锁定。重试会沿用原内容与发布请求；请先核对活动列表，避免另起发布造成重复活动。"
      type="warning"
      :closable="false"
      show-icon
      class="space-bottom"
    />
    <el-form label-position="top" :disabled="fieldsDisabled">
      <el-form-item label="门店" for="publish-shop">
        <el-select
          id="publish-shop"
          v-model="form.shopId"
          aria-label="发布门店"
          placeholder="选择可管理的门店"
          style="width: 100%"
        >
          <el-option
            v-for="shop in managedShops"
            :key="shop.id"
            :label="`${shop.name}（${shop.id}）`"
            :value="shop.id"
          />
        </el-select>
      </el-form-item>
      <el-form-item label="活动标题" for="publish-title">
        <el-input
          id="publish-title"
          v-model="form.title"
          maxlength="255"
          placeholder="例如：周末咖啡体验券"
        />
      </el-form-item>
      <el-form-item label="副标题 / 使用规则">
        <el-input
          v-model="form.subTitle"
          aria-label="副标题"
          maxlength="255"
          placeholder="限量体验，每人限购一张"
        />
        <el-input
          v-model="form.rules"
          aria-label="使用规则"
          type="textarea"
          :rows="2"
          maxlength="1024"
          placeholder="使用时段、适用门店等说明"
          style="margin-top: 8px"
        />
      </el-form-item>
      <div class="form-grid">
        <el-form-item label="购买价（元）" for="publish-price">
          <el-input
            id="publish-price"
            v-model="form.payYuan"
            inputmode="decimal"
            maxlength="21"
            placeholder="9.90"
          />
        </el-form-item>
        <el-form-item label="抵扣面值（元）" for="publish-value">
          <el-input
            id="publish-value"
            v-model="form.actualYuan"
            inputmode="decimal"
            maxlength="21"
            placeholder="20.00"
          />
        </el-form-item>
        <el-form-item label="总库存" for="publish-stock">
          <el-input-number
            id="publish-stock"
            v-model="form.totalStock"
            :min="1"
            :max="1000000"
            :precision="0"
            :step="1"
            style="width: 100%"
          />
        </el-form-item>
      </div>
      <p class="muted">以下输入均为北京时间（UTC+08:00），与你设备所在时区无关。</p>
      <div class="form-grid">
        <el-form-item label="销售开始（北京时间）" for="publish-begin">
          <input
            id="publish-begin"
            v-model="form.beginTime"
            class="activity-datetime"
            type="datetime-local"
            step="1"
            :disabled="fieldsDisabled"
          />
        </el-form-item>
        <el-form-item label="销售结束（北京时间）" for="publish-end">
          <input
            id="publish-end"
            v-model="form.endTime"
            class="activity-datetime"
            type="datetime-local"
            step="1"
            :disabled="fieldsDisabled"
          />
        </el-form-item>
      </div>
      <div class="form-grid">
        <el-form-item label="核销开始（北京时间）" for="publish-redeem-begin">
          <input
            id="publish-redeem-begin"
            v-model="form.redeemBeginTime"
            class="activity-datetime"
            type="datetime-local"
            step="1"
            :disabled="fieldsDisabled"
          />
        </el-form-item>
        <el-form-item label="核销结束（晚于销售结束）" for="publish-redeem-end">
          <input
            id="publish-redeem-end"
            v-model="form.redeemEndTime"
            class="activity-datetime"
            type="datetime-local"
            step="1"
            :disabled="fieldsDisabled"
          />
        </el-form-item>
      </div>
      <el-alert
        title="发布后价格、库存和起止时间不可修改；需要调整时请下架后重新发布。"
        type="info"
        :closable="false"
        show-icon
      />
    </el-form>
    <template #footer>
      <el-button :disabled="submitting" @click="dialogOpen = false">{{
        pending ? '暂时关闭' : '取消'
      }}</el-button>
      <el-button v-if="pending" :disabled="submitting" @click="abandonPublish"
        >放弃本次并新建</el-button
      >
      <el-button
        type="primary"
        :loading="submitting"
        :disabled="loading || !!busyId || !canSubmit"
        @click="submit"
        >{{ pending ? '重试本次发布' : '发布' }}</el-button
      >
    </template>
  </el-dialog>
</template>

<style scoped>
.activity-filters {
  margin-bottom: 20px;
}
.activity-filters .el-select {
  width: min(260px, 100%);
}
.activity-pagination {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  margin: 20px 0;
}
.activity-pagination .el-select {
  width: 140px;
}
.activity-number {
  display: inline-block;
  white-space: nowrap;
  word-break: normal;
  overflow-wrap: normal;
}
.activity-prices {
  display: flex;
  flex-wrap: wrap;
  column-gap: 4px;
}
.activity-datetime {
  width: 100%;
  min-width: 0;
  box-sizing: border-box;
  padding: 9px 10px;
  border: 1px solid #dcdfe6;
  border-radius: 4px;
  font: inherit;
  color: #303133;
  background: #fff;
}
.activity-datetime:disabled {
  background: #f5f7fa;
  color: #a8abb2;
  cursor: not-allowed;
}
</style>
