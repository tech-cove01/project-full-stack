<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { ElAlert, ElButton, ElEmpty, ElSkeleton, ElTable, ElTableColumn, ElTag } from 'element-plus'
import 'element-plus/es/components/alert/style/css'
import 'element-plus/es/components/empty/style/css'
import 'element-plus/es/components/skeleton/style/css'
import 'element-plus/es/components/table/style/css'
import 'element-plus/es/components/table-column/style/css'
import 'element-plus/es/components/tag/style/css'
import { api, errorMessage, money } from '../api'
import type { OrderStatus } from '../types'

const orders = ref<OrderStatus[]>([])
const loading = ref(false)
const error = ref('')
let controller: AbortController | undefined

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
    const { data } = await api.myOrders(1, 50, active.signal)
    if (!active.signal.aborted) orders.value = data || []
  } catch (cause) {
    if (!active.signal.aborted) error.value = errorMessage(cause)
  } finally {
    if (!active.signal.aborted) loading.value = false
  }
}
onMounted(() => {
  void load()
})
onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <section class="merchant-header">
    <span class="eyebrow">YOUR ORDERS</span>
    <h1>我的订单</h1>
    <p>查看抢购、支付和核销状态；取消或退款后的订单仍可在这里找回。</p>
  </section>
  <el-alert
    v-if="error"
    :title="error"
    type="error"
    :closable="false"
    show-icon
    class="space-bottom"
  />
  <el-skeleton v-if="loading" :rows="5" animated />
  <el-empty v-else-if="!orders.length" description="还没有订单">
    <RouterLink to="/"><el-button type="primary">去逛逛</el-button></RouterLink>
  </el-empty>
  <section v-else class="panel">
    <el-table :data="orders" style="width: 100%">
      <el-table-column label="订单号" min-width="180">
        <template #default="{ row }">
          <RouterLink class="text-link" :to="`/orders/${row.id}`">{{ row.id }}</RouterLink>
        </template>
      </el-table-column>
      <el-table-column label="状态" width="120">
        <template #default="{ row }">
          <el-tag :type="tagTypes[row.orderStatus] || 'info'">
            {{ labels[row.orderStatus] || row.message || row.state }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="金额" width="120">
        <template #default="{ row }">¥{{ money(row.payAmount) }}</template>
      </el-table-column>
      <el-table-column prop="createTime" label="创建时间" min-width="170" />
      <el-table-column label="操作" width="120">
        <template #default="{ row }">
          <RouterLink class="text-link" :to="`/orders/${row.id}`">查看 →</RouterLink>
        </template>
      </el-table-column>
    </el-table>
  </section>
</template>
