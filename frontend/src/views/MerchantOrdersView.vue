<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  ElAlert,
  ElButton,
  ElEmpty,
  ElInput,
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
import 'element-plus/es/components/empty/style/css'
import 'element-plus/es/components/input/style/css'
import 'element-plus/es/components/select/style/css'
import 'element-plus/es/components/skeleton/style/css'
import 'element-plus/es/components/table/style/css'
import 'element-plus/es/components/table-column/style/css'
import 'element-plus/es/components/tag/style/css'
import { api, errorMessage, money } from '../api'
import type { AuditLog, MerchantOrder, MerchantWorkspace } from '../types'

const workspace = ref<MerchantWorkspace | null>(null)
const orders = ref<MerchantOrder[]>([])
const audits = ref<AuditLog[]>([])
const loading = ref(false)
const error = ref('')
const refundingId = ref('')
const redeeming = ref(false)
const filterShop = ref('')
const filterStatus = ref('')
const redeemShop = ref('')
const tokenInput = ref('')
// Server-side pagination: the list never silently shows only the first page.
const orderPage = ref(1)
const orderPageSize = 10
const orderTotal = ref(0)
const auditPage = ref(1)
const auditPageSize = 10
const auditTotal = ref(0)
let controller: AbortController | undefined

const shops = computed(() =>
  (workspace.value?.merchants || []).flatMap((merchant) =>
    merchant.shops.map((shop) => ({ ...shop, role: merchant.role })),
  ),
)
const canManage = computed(() =>
  (workspace.value?.merchants || []).some(
    (merchant) => merchant.role === 'OWNER' || merchant.role === 'MANAGER',
  ),
)
function canManageShop(shopId: string) {
  return shops.value.some(
    (shop) => shop.id === shopId && (shop.role === 'OWNER' || shop.role === 'MANAGER'),
  )
}
const labels: Record<number, string> = {
  1: '待支付',
  2: '已支付',
  3: '已核销',
  4: '已取消',
  6: '已退款',
}
const tagTypes: Record<number, 'warning' | 'success' | 'info' | 'danger'> = {
  1: 'warning',
  2: 'success',
  3: 'info',
  4: 'info',
  6: 'danger',
}

async function load() {
  controller?.abort()
  const active = new AbortController()
  controller = active
  loading.value = true
  error.value = ''
  try {
    const workspaceResult = await api.workspace(active.signal)
    if (active.signal.aborted) return
    workspace.value = workspaceResult.data
    if (!redeemShop.value && shops.value.length) redeemShop.value = shops.value[0].id
    const orderResult = await api.merchantOrders(
      {
        current: orderPage.value,
        pageSize: orderPageSize,
        shopId: filterShop.value || undefined,
        status: filterStatus.value || undefined,
      },
      active.signal,
    )
    if (active.signal.aborted) return
    orders.value = orderResult.data || []
    orderTotal.value = Number(orderResult.total || 0)
    if (canManage.value) {
      const auditResult = await api.audits(auditPage.value, auditPageSize, active.signal)
      if (active.signal.aborted) return
      audits.value = auditResult.data || []
      auditTotal.value = Number(auditResult.total || 0)
    }
  } catch (cause) {
    if (!active.signal.aborted) error.value = errorMessage(cause)
  } finally {
    if (!active.signal.aborted) loading.value = false
  }
}

// A new filter always restarts at page 1 so the user never sees an empty page 3.
function applyFilters() {
  orderPage.value = 1
  void load()
}

async function redeem() {
  if (redeeming.value) return
  if (!redeemShop.value || !tokenInput.value.trim()) {
    ElMessage.warning('请选择门店并粘贴核销码。')
    return
  }
  redeeming.value = true
  try {
    const { data } = await api.redeem(redeemShop.value, tokenInput.value.trim())
    ElMessage.success(
      data.alreadyRedeemed
        ? `订单 ${data.order.id} 之前已核销，无需重复操作。`
        : `核销成功：订单 ${data.order.id}，状态已更新。`,
    )
    tokenInput.value = ''
    await load()
  } catch (cause) {
    ElMessage.error(errorMessage(cause))
  } finally {
    redeeming.value = false
  }
}

async function refund(order: any) {
  try {
    await ElMessageBox.confirm(
      `确认对订单 ${order.id} 执行模拟退款？退款后不可再次核销，用户不能重复购买。`,
      '模拟退款',
      { confirmButtonText: '确认退款', cancelButtonText: '取消', type: 'warning' },
    )
  } catch {
    return
  }
  refundingId.value = order.id
  try {
    await api.refundOrder(order.id, 'MERCHANT_REFUND')
    ElMessage.success('模拟退款完成，库存已回补。')
    await load()
  } catch (cause) {
    ElMessage.error(errorMessage(cause))
  } finally {
    refundingId.value = ''
  }
}

onMounted(() => {
  void load()
})
onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <section class="merchant-header">
    <span class="eyebrow">ORDERS & REDEMPTION</span>
    <h1>订单与核销</h1>
    <p>查看授权门店订单，使用用户出示的核销码完成到店核销；店主可执行模拟退款并查看审计。</p>
  </section>
  <div class="page-top merchant-actions">
    <RouterLink class="text-link" to="/merchant">← 返回工作台</RouterLink>
    <RouterLink class="text-link" to="/merchant/activities">活动管理 →</RouterLink>
  </div>

  <el-alert
    v-if="error"
    :title="error"
    type="error"
    :closable="false"
    show-icon
    class="space-bottom"
  />

  <section class="panel space-bottom">
    <div class="section-heading">
      <div>
        <span class="eyebrow">REDEEM</span>
        <h2>到店核销</h2>
      </div>
    </div>
    <div class="redeem-form">
      <el-select v-model="redeemShop" placeholder="授权门店" style="width: 220px">
        <el-option
          v-for="shop in shops"
          :key="shop.id"
          :label="`${shop.name}（${shop.id}）`"
          :value="shop.id"
        />
      </el-select>
      <el-input
        v-model="tokenInput"
        maxlength="64"
        placeholder="粘贴 64 位核销码"
        style="flex: 1; min-width: 240px"
      />
      <el-button type="primary" :loading="redeeming" @click="redeem">核销</el-button>
    </div>
    <p class="muted">核销码仅由用户订单详情页展示；店员只能核销授权门店的订单。</p>
  </section>

  <section class="panel space-bottom">
    <div class="section-heading">
      <div>
        <span class="eyebrow">ORDERS</span>
        <h2>门店订单</h2>
      </div>
      <div class="filter-row">
        <el-select
          v-model="filterShop"
          clearable
          placeholder="全部门店"
          style="width: 180px"
          @change="applyFilters"
        >
          <el-option v-for="shop in shops" :key="shop.id" :label="shop.name" :value="shop.id" />
        </el-select>
        <el-select
          v-model="filterStatus"
          clearable
          placeholder="全部状态"
          style="width: 150px"
          @change="applyFilters"
        >
          <el-option label="待支付" value="1" />
          <el-option label="已支付" value="2" />
          <el-option label="已核销" value="3" />
          <el-option label="已取消" value="4" />
          <el-option label="已退款" value="6" />
        </el-select>
      </div>
    </div>
    <el-skeleton v-if="loading" :rows="5" animated />
    <el-empty v-else-if="!orders.length" description="当前筛选下没有订单" />
    <el-table v-else :data="orders" style="width: 100%">
      <el-table-column label="订单号" min-width="180">
        <template #default="{ row }"
          ><span class="mono">{{ row.id }}</span></template
        >
      </el-table-column>
      <el-table-column prop="shopName" label="门店" width="130" />
      <el-table-column label="用户" width="150">
        <template #default="{ row }">{{ row.phone }}</template>
      </el-table-column>
      <el-table-column prop="voucherTitle" label="活动" min-width="150" />
      <el-table-column label="状态" width="110">
        <template #default="{ row }">
          <el-tag :type="tagTypes[row.status] || 'info'">{{
            labels[row.status] || row.status
          }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="金额" width="100">
        <template #default="{ row }">¥{{ money(row.payAmount) }}</template>
      </el-table-column>
      <el-table-column prop="createTime" label="创建时间" min-width="170" />
      <el-table-column label="操作" width="140">
        <template #default="{ row }">
          <el-button
            v-if="canManageShop(row.shopId) && row.status === 2"
            link
            type="danger"
            :loading="refundingId === row.id"
            @click="refund(row)"
            >模拟退款</el-button
          >
          <span v-else class="muted">—</span>
        </template>
      </el-table-column>
    </el-table>
    <el-pagination
      v-if="orderTotal > orderPageSize"
      v-model:current-page="orderPage"
      :page-size="orderPageSize"
      :total="orderTotal"
      layout="prev, pager, next, total"
      style="margin-top: 12px; justify-content: flex-end"
      @current-change="load"
    />
  </section>

  <section v-if="canManage" class="panel">
    <div class="section-heading">
      <div>
        <span class="eyebrow">AUDIT</span>
        <h2>操作审计</h2>
      </div>
      <span class="muted">共 {{ auditTotal }} 条</span>
    </div>
    <el-empty v-if="!audits.length" description="还没有审计记录" />
    <el-table v-else :data="audits" style="width: 100%">
      <el-table-column prop="action" label="动作" width="170" />
      <el-table-column prop="operatorType" label="操作方" width="110" />
      <el-table-column prop="resourceId" label="资源" width="180" />
      <el-table-column prop="detail" label="详情" min-width="240" show-overflow-tooltip />
      <el-table-column prop="createTime" label="时间" width="170" />
    </el-table>
    <el-pagination
      v-if="auditTotal > auditPageSize"
      v-model:current-page="auditPage"
      :page-size="auditPageSize"
      :total="auditTotal"
      layout="prev, pager, next, total"
      style="margin-top: 12px; justify-content: flex-end"
      @current-change="load"
    />
  </section>
</template>
