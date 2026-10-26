<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  ElAlert,
  ElButton,
  ElEmpty,
  ElOption,
  ElSelect,
  ElSkeleton,
  ElTable,
  ElTableColumn,
  ElTag,
} from 'element-plus'
import 'element-plus/es/components/alert/style/css'
import 'element-plus/es/components/empty/style/css'
import 'element-plus/es/components/select/style/css'
import 'element-plus/es/components/skeleton/style/css'
import 'element-plus/es/components/table/style/css'
import 'element-plus/es/components/table-column/style/css'
import 'element-plus/es/components/tag/style/css'
import { api, errorMessage, money } from '../api'
import type { OrderStatus } from '../types'

const orders = ref<OrderStatus[]>([])
const loading = ref(false)
const error = ref('')
const current = ref(1)
const pageSize = ref(10)
const total = ref<string | null>(null)
let controller: AbortController | undefined
let disposed = false

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
const hasNext = computed(() => {
  if (!Number.isSafeInteger(current.value + 1)) return false
  return total.value === null
    ? orders.value.length === pageSize.value
    : BigInt(current.value) * BigInt(pageSize.value) < BigInt(total.value)
})

function normalizeTotal(value: string | number | undefined): string | null {
  if (typeof value === 'string' && /^\d+$/.test(value)) return BigInt(value).toString()
  if (typeof value === 'number' && Number.isSafeInteger(value) && value >= 0) return String(value)
  return null
}

async function load() {
  if (disposed) return
  controller?.abort()
  const active = new AbortController()
  controller = active
  loading.value = true
  error.value = ''
  orders.value = []
  total.value = null
  try {
    const result = await api.myOrders(current.value, pageSize.value, active.signal)
    if (active.signal.aborted) return
    orders.value = result.data || []
    total.value = normalizeTotal(result.total)
    if (!orders.value.length && current.value > 1 && total.value !== null) {
      const count = BigInt(total.value)
      if (BigInt(current.value - 1) * BigInt(pageSize.value) >= count) {
        // This branch only reduces the current safe integer page.
        current.value = count === 0n ? 1 : Number((count - 1n) / BigInt(pageSize.value) + 1n)
        void load()
      }
    }
  } catch (cause) {
    if (!active.signal.aborted) {
      orders.value = []
      total.value = null
      error.value = errorMessage(cause)
    }
  } finally {
    if (!active.signal.aborted) loading.value = false
  }
}

function changePage(direction: -1 | 1) {
  if (loading.value || (direction === -1 ? current.value <= 1 : !hasNext.value)) return
  current.value += direction
  void load()
}

function changePageSize() {
  current.value = 1
  void load()
}

onMounted(() => void load())
onBeforeUnmount(() => {
  disposed = true
  controller?.abort()
})
</script>

<template>
  <section class="merchant-header">
    <span class="eyebrow">YOUR ORDERS</span>
    <h1>我的订单</h1>
    <p>查看你已创建的订单；抢券结果仍在确认时，可在订单详情继续查询。</p>
  </section>
  <div class="page-top my-orders-toolbar">
    <el-button :loading="loading" @click="load">刷新订单</el-button>
    <el-select
      v-model="pageSize"
      aria-label="每页订单数"
      :disabled="loading"
      @change="changePageSize"
    >
      <el-option
        v-for="size in [10, 20, 50]"
        :key="size"
        :label="`每页 ${size} 条`"
        :value="size"
      />
    </el-select>
  </div>
  <el-alert
    v-if="error"
    :title="error"
    type="error"
    :closable="false"
    show-icon
    class="space-bottom"
  />
  <el-skeleton v-if="loading" :rows="5" animated aria-label="正在加载订单" />
  <el-empty v-else-if="error" description="暂时无法加载订单">
    <el-button @click="load">重新加载</el-button>
  </el-empty>
  <el-empty v-else-if="!orders.length" description="还没有已创建的订单">
    <RouterLink to="/"><el-button type="primary">去逛逛</el-button></RouterLink>
  </el-empty>
  <section v-else class="panel">
    <el-table :data="orders" row-key="id" row-class-name="my-order-row" style="width: 100%">
      <el-table-column label="订单号" min-width="180">
        <template #default="{ row }">
          <RouterLink
            class="text-link my-order-card order-number"
            :data-order-id="row.id"
            :to="`/orders/${row.id}`"
            >{{ row.id }}</RouterLink
          >
        </template>
      </el-table-column>
      <el-table-column label="状态" width="120">
        <template #default="{ row }">
          <el-tag :type="tagTypes[row.orderStatus] || 'info'">{{
            labels[row.orderStatus] || row.message || row.state
          }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="金额" min-width="190">
        <template #default="{ row }"
          ><span class="order-number">{{
            row.payAmount == null ? '未记录' : `¥${money(row.payAmount)}`
          }}</span></template
        >
      </el-table-column>
      <el-table-column prop="createTime" label="创建时间（北京时间）" min-width="190" />
      <el-table-column label="操作" width="100">
        <template #default="{ row }"
          ><RouterLink class="text-link" :to="`/orders/${row.id}`">查看 →</RouterLink></template
        >
      </el-table-column>
    </el-table>
  </section>
  <div
    v-if="!loading && !error && (orders.length || current > 1)"
    class="my-orders-pagination"
    aria-label="订单分页"
  >
    <span
      >第 {{ current }} 页<span v-if="total !== null"> · 共 {{ total }} 条</span></span
    >
    <el-button :disabled="current <= 1" @click="changePage(-1)">上一页</el-button>
    <el-button :disabled="!hasNext" @click="changePage(1)">下一页</el-button>
  </div>
</template>

<style scoped>
.my-orders-toolbar,
.my-orders-pagination {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  margin-bottom: 20px;
}
.my-orders-toolbar .el-select {
  width: 140px;
}
.my-orders-pagination {
  margin-top: 20px;
}
.order-number {
  display: inline-block;
  white-space: nowrap;
  word-break: normal;
  overflow-wrap: normal;
}
</style>
