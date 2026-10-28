<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { ElAlert, ElButton, ElEmpty, ElSkeleton, ElTable, ElTableColumn, ElTag } from 'element-plus'
import 'element-plus/es/components/alert/style/css'
import 'element-plus/es/components/empty/style/css'
import 'element-plus/es/components/skeleton/style/css'
import 'element-plus/es/components/table/style/css'
import 'element-plus/es/components/table-column/style/css'
import 'element-plus/es/components/tag/style/css'
import { api, errorMessage } from '../api'
import type { MerchantWorkspace } from '../types'

const workspace = ref<MerchantWorkspace | null>(null)
const loading = ref(false)
const error = ref('')
const roles = { OWNER: '商家所有者', MANAGER: '门店管理者', STAFF: '门店员工' }
let controller: AbortController | undefined

async function load() {
  controller?.abort()
  const active = new AbortController()
  controller = active
  loading.value = true
  error.value = ''
  workspace.value = null
  try {
    const { data } = await api.workspace(active.signal)
    if (!active.signal.aborted) workspace.value = data
  } catch (cause) {
    if (!active.signal.aborted) {
      workspace.value = null
      error.value = errorMessage(cause)
    }
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
    <span class="eyebrow">FULL STACK FOR BUSINESS</span>
    <h1>商家工作台</h1>
    <p>查看当前角色和已授权门店，了解可访问的范围。</p>
  </section>
  <div class="page-top merchant-actions">
    <el-button type="primary" :loading="loading" @click="load">刷新授权</el-button>
    <RouterLink to="/merchant/activities"><el-button>活动管理</el-button></RouterLink>
    <RouterLink to="/merchant/orders"><el-button>订单与核销</el-button></RouterLink>
    <RouterLink to="/merchant/knowledge"><el-button>公开规则</el-button></RouterLink>
  </div>
  <el-alert
    v-if="error"
    :title="error"
    type="error"
    :closable="false"
    show-icon
    class="space-bottom"
  />
  <el-skeleton v-if="loading" :rows="5" animated aria-label="正在加载门店授权" />
  <el-empty v-else-if="error" description="暂时无法加载工作台">
    <el-button @click="load">重新加载</el-button>
  </el-empty>
  <el-empty v-else-if="!workspace?.merchants.length" description="当前账号还没有商家或门店授权">
    <RouterLink to="/"><el-button>前往用户端</el-button></RouterLink>
  </el-empty>
  <template v-else>
    <section
      v-for="merchant in workspace.merchants"
      :key="merchant.id"
      class="panel merchant-workspace"
      :data-merchant-id="merchant.id"
      :data-role="merchant.role"
    >
      <div class="section-heading">
        <div>
          <h2>{{ merchant.name }}</h2>
          <p class="muted">
            商家编号 {{ merchant.id }} · 可查看 {{ merchant.shops.length }} 家门店
          </p>
        </div>
        <el-tag class="merchant-role">{{ roles[merchant.role] || merchant.role }}</el-tag>
      </div>
      <el-empty
        v-if="!merchant.shops.length"
        class="merchant-empty-shops"
        description="当前商家暂无可访问的门店"
      />
      <el-table v-else :data="merchant.shops" row-key="id" style="width: 100%">
        <el-table-column label="门店" min-width="160">
          <template #default="{ row }">
            <span class="merchant-shop" :data-shop-id="row.id">{{ row.name }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="address" label="地址" min-width="200" />
        <el-table-column label="授权范围" min-width="120">
          <template #default>
            <span class="merchant-shop-scope">
              {{ merchant.role === 'STAFF' ? '授权门店' : '管理门店' }}
            </span>
          </template>
        </el-table-column>
        <el-table-column label="店铺信息" width="120">
          <template #default="{ row }">
            <RouterLink class="text-link" :to="`/shops/${row.id}`">查看店铺 →</RouterLink>
          </template>
        </el-table-column>
      </el-table>
      <p class="muted merchant-scope">
        {{
          merchant.role === 'STAFF'
            ? '当前仅展示分配给你的门店，更多门店请联系商家管理员授权。'
            : '当前展示你所属商家的全部可访问门店。'
        }}
      </p>
    </section>
  </template>
  <aside class="panel merchant-scope-help" aria-label="角色与门店授权说明">
    <h2>你的授权范围</h2>
    <p>商家所有者与门店管理者可查看所属商家的门店，门店员工仅可查看已分配的门店。</p>
    <p>同一账号在不同商家的角色分别生效。授权调整后，点击“刷新授权”查看当前范围。</p>
  </aside>
</template>
