<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import {
  ElAlert,
  ElButton,
  ElDatePicker,
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
import 'element-plus/es/components/date-picker/style/css'
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
import { api, errorMessage, money } from '../api'
import type { Activity, MerchantWorkspace } from '../types'

const workspace = ref<MerchantWorkspace | null>(null)
const activities = ref<Activity[]>([])
const loading = ref(false)
const error = ref('')
const dialogOpen = ref(false)
const submitting = ref(false)
const busyId = ref('')
const pendingKey = ref('')
let controller: AbortController | undefined

const shops = computed(() =>
  (workspace.value?.merchants || []).flatMap((merchant) =>
    merchant.shops.map((shop) => ({ ...shop, role: merchant.role })),
  ),
)
const canManage = computed(() =>
  (workspace.value?.merchants || []).some((merchant) => merchant.role === 'OWNER' || merchant.role === 'MANAGER'),
)

function pad(value: number) {
  return String(value).padStart(2, '0')
}
function localTime(offsetMs: number) {
  const date = new Date(Date.now() + offsetMs)
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

const form = reactive({
  shopId: '',
  title: '',
  subTitle: '',
  rules: '',
  payYuan: 9.9,
  actualYuan: 20,
  totalStock: 100,
  beginTime: localTime(-60_000),
  endTime: localTime(10 * 86400_000),
  redeemBeginTime: localTime(-60_000),
  redeemEndTime: localTime(20 * 86400_000),
})

async function load() {
  controller?.abort()
  const active = new AbortController()
  controller = active
  loading.value = true
  error.value = ''
  try {
    const [workspaceResult, activityResult] = await Promise.all([
      api.workspace(active.signal),
      api.activities(1, 50, active.signal),
    ])
    if (active.signal.aborted) return
    workspace.value = workspaceResult.data
    activities.value = activityResult.data || []
    if (!form.shopId && shops.value.length) form.shopId = shops.value[0].id
  } catch (cause) {
    if (!active.signal.aborted) error.value = errorMessage(cause)
  } finally {
    if (!active.signal.aborted) loading.value = false
  }
}

function openDialog() {
  pendingKey.value = ''
  form.title = ''
  form.subTitle = ''
  form.rules = ''
  form.payYuan = 9.9
  form.actualYuan = 20
  form.totalStock = 100
  form.beginTime = localTime(-60_000)
  form.endTime = localTime(10 * 86400_000)
  form.redeemBeginTime = localTime(-60_000)
  form.redeemEndTime = localTime(20 * 86400_000)
  if (shops.value.length) form.shopId = shops.value[0].id
  dialogOpen.value = true
}

function newKey() {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) return crypto.randomUUID()
  return `activity-${Date.now()}-${Math.random().toString(16).slice(2)}`
}

async function submit() {
  if (submitting.value) return
  const payValue = Math.round(Number(form.payYuan) * 100)
  const actualValue = Math.round(Number(form.actualYuan) * 100)
  if (!form.shopId || !form.title.trim()) {
    ElMessage.warning('请选择门店并填写活动标题。')
    return
  }
  if (payValue <= 0 || actualValue < payValue) {
    ElMessage.warning('购买价格必须为正，且不能高于抵扣面值。')
    return
  }
  if (!form.totalStock || form.totalStock <= 0) {
    ElMessage.warning('库存必须为正整数。')
    return
  }
  if (!pendingKey.value) pendingKey.value = newKey()
  submitting.value = true
  try {
    const { data } = await api.createActivity(
      {
        shopId: form.shopId,
        title: form.title.trim(),
        subTitle: form.subTitle.trim() || null,
        rules: form.rules.trim() || null,
        payValue: String(payValue),
        actualValue: String(actualValue),
        totalStock: form.totalStock,
        beginTime: form.beginTime,
        endTime: form.endTime,
        redeemBeginTime: form.redeemBeginTime,
        redeemEndTime: form.redeemEndTime,
      },
      pendingKey.value,
    )
    ElMessage.success(`活动 ${data.id} 已创建，正在准备 Redis 库存。`)
    dialogOpen.value = false
    pendingKey.value = ''
    await load()
    pollUntilReady(data.id)
  } catch (cause) {
    ElMessage.error(errorMessage(cause))
  } finally {
    submitting.value = false
  }
}

function pollUntilReady(id: string, attempt = 0) {
  window.setTimeout(async () => {
    await load()
    const activity = activities.value.find((item) => item.id === id)
    if (activity && !activity.ready && activity.publishStatus !== 'OFFLINE' && attempt < 12) {
      pollUntilReady(id, attempt + 1)
    }
  }, 1000)
}

async function retry(id: string) {
  busyId.value = id
  try {
    await api.retryActivity(id)
    ElMessage.success('已触发发布重试。')
    await load()
  } catch (cause) {
    ElMessage.error(errorMessage(cause))
  } finally {
    busyId.value = ''
  }
}

async function offline(activity: any) {
  try {
    await ElMessageBox.confirm(
      `下架后新用户不能再抢购，已受理和已创建的订单仍按原规则履约。确定下架“${activity.title}”吗？`,
      '下架活动',
      { confirmButtonText: '确定下架', cancelButtonText: '取消', type: 'warning' },
    )
  } catch {
    return
  }
  busyId.value = activity.id
  try {
    await api.offlineActivity(activity.id)
    ElMessage.success('活动已下架。')
    await load()
  } catch (cause) {
    ElMessage.error(errorMessage(cause))
  } finally {
    busyId.value = ''
  }
}

function statusLabel(activity: any) {
  if (activity.publishStatus === 'OFFLINE') return '已下架'
  if (activity.publishStatus === 'INITIALIZING') return '准备中'
  return activity.ready ? '已发布' : '待开闸'
}
function statusType(activity: any): 'success' | 'warning' | 'info' {
  if (activity.publishStatus === 'OFFLINE') return 'info'
  if (activity.publishStatus === 'INITIALIZING') return 'warning'
  return activity.ready ? 'success' : 'warning'
}

onMounted(() => {
  void load()
})
onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <section class="merchant-header">
    <span class="eyebrow">ACTIVITIES</span>
    <h1>活动管理</h1>
    <p>发布门店优惠活动，准备 Redis 库存与准入开关；发布后不可修改价格、库存和时间。</p>
  </section>
  <div class="page-top merchant-actions">
    <RouterLink class="text-link" to="/merchant">← 返回工作台</RouterLink>
    <RouterLink class="text-link" to="/merchant/orders">订单与核销 →</RouterLink>
    <el-button v-if="canManage" type="primary" @click="openDialog">发布活动</el-button>
  </div>

  <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon class="space-bottom" />
  <el-skeleton v-if="loading" :rows="6" animated />
  <el-empty v-else-if="!activities.length" description="还没有活动，先发布一个门店优惠券吧">
    <el-button v-if="canManage" type="primary" @click="openDialog">发布活动</el-button>
  </el-empty>
  <section v-else class="panel">
    <el-table :data="activities" style="width: 100%">
      <el-table-column prop="id" label="活动" width="90" />
      <el-table-column label="标题" min-width="180">
        <template #default="{ row }">
          <strong>{{ row.title }}</strong>
          <div class="muted">{{ row.shopName }} · {{ row.subTitle || '门店活动' }}</div>
        </template>
      </el-table-column>
      <el-table-column label="价格 / 面值" width="150">
        <template #default="{ row }">¥{{ money(row.payValue) }} / ¥{{ money(row.actualValue) }}</template>
      </el-table-column>
      <el-table-column label="库存" width="110">
        <template #default="{ row }">{{ row.stock }} / {{ row.totalStock }}</template>
      </el-table-column>
      <el-table-column label="状态" width="110">
        <template #default="{ row }">
          <el-tag :type="statusType(row)">{{ statusLabel(row) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="销售时间" min-width="180">
        <template #default="{ row }">{{ row.beginTime }}<br /><span class="muted">至 {{ row.endTime }}</span></template>
      </el-table-column>
      <el-table-column label="操作" width="180">
        <template #default="{ row }">
          <el-button
            v-if="row.publishStatus !== 'OFFLINE' && !row.ready"
            link
            type="primary"
            :loading="busyId === row.id"
            @click="retry(row.id)"
            >重试发布</el-button
          >
          <el-button
            v-if="row.publishStatus !== 'OFFLINE' && canManage"
            link
            type="danger"
            :loading="busyId === row.id"
            @click="offline(row)"
            >下架</el-button
          >
          <RouterLink class="text-link" :to="`/shops/${row.shopId}`">看店铺 →</RouterLink>
        </template>
      </el-table-column>
    </el-table>
  </section>

  <el-dialog v-model="dialogOpen" title="发布门店活动" width="720px" :close-on-click-modal="false">
    <el-form label-position="top">
      <el-form-item label="门店">
        <el-select v-model="form.shopId" placeholder="选择门店" style="width: 100%">
          <el-option
            v-for="shop in shops.filter((item) => item.role === 'OWNER' || item.role === 'MANAGER')"
            :key="shop.id"
            :label="`${shop.name}（${shop.id}）`"
            :value="shop.id"
          />
        </el-select>
      </el-form-item>
      <el-form-item label="活动标题">
        <el-input v-model="form.title" maxlength="255" placeholder="例如：周末咖啡体验券" />
      </el-form-item>
      <el-form-item label="副标题 / 使用规则">
        <el-input v-model="form.subTitle" maxlength="255" placeholder="限量体验，每人限购一张" />
        <el-input
          v-model="form.rules"
          type="textarea"
          :rows="2"
          maxlength="1024"
          placeholder="使用时段、适用门店等说明"
          style="margin-top: 8px"
        />
      </el-form-item>
      <div class="form-grid">
        <el-form-item label="购买价（元）">
          <el-input-number v-model="form.payYuan" :min="0.01" :precision="2" :step="1" style="width: 100%" />
        </el-form-item>
        <el-form-item label="抵扣面值（元）">
          <el-input-number v-model="form.actualYuan" :min="0.01" :precision="2" :step="1" style="width: 100%" />
        </el-form-item>
        <el-form-item label="总库存">
          <el-input-number v-model="form.totalStock" :min="1" :max="1000000" :step="10" style="width: 100%" />
        </el-form-item>
      </div>
      <div class="form-grid">
        <el-form-item label="销售开始">
          <el-date-picker
            v-model="form.beginTime"
            type="datetime"
            value-format="YYYY-MM-DDTHH:mm:ss"
            style="width: 100%"
          />
        </el-form-item>
        <el-form-item label="销售结束">
          <el-date-picker
            v-model="form.endTime"
            type="datetime"
            value-format="YYYY-MM-DDTHH:mm:ss"
            style="width: 100%"
          />
        </el-form-item>
      </div>
      <div class="form-grid">
        <el-form-item label="核销开始">
          <el-date-picker
            v-model="form.redeemBeginTime"
            type="datetime"
            value-format="YYYY-MM-DDTHH:mm:ss"
            style="width: 100%"
          />
        </el-form-item>
        <el-form-item label="核销结束（需晚于销售结束）">
          <el-date-picker
            v-model="form.redeemEndTime"
            type="datetime"
            value-format="YYYY-MM-DDTHH:mm:ss"
            style="width: 100%"
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
      <el-button @click="dialogOpen = false">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="submit">发布</el-button>
    </template>
  </el-dialog>
</template>
