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
  try {
    const { data } = await api.workspace(active.signal)
    if (!active.signal.aborted) workspace.value = data
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
    <span class="eyebrow">FULL STACK FOR BUSINESS</span>
    <h1>商家工作台</h1>
    <p>管理已授权的商家与门店，连接到店服务。</p>
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
  <el-empty
    v-else-if="!workspace?.merchants.length"
    :description="error ? '暂时无法加载工作台' : '当前账号还没有商家或门店授权'"
    ><el-button v-if="error" @click="load">重新加载</el-button
    ><RouterLink v-else to="/"><el-button>前往用户端</el-button></RouterLink></el-empty
  >
  <section
    v-for="merchant in workspace?.merchants || []"
    v-else
    :key="merchant.id"
    class="panel merchant-workspace"
  >
    <div class="section-heading">
      <div>
        <h2>{{ merchant.name }}</h2>
        <p class="muted">商家编号 {{ merchant.id }} · 可查看 {{ merchant.shops.length }} 家门店</p>
      </div>
      <el-tag>{{ roles[merchant.role] || merchant.role }}</el-tag>
    </div>
    <el-table :data="merchant.shops" empty-text="当前没有可访问的门店" style="width: 100%"
      ><el-table-column prop="name" label="门店" min-width="160" /><el-table-column
        prop="address"
        label="地址"
        min-width="200"
      /><el-table-column label="操作" width="120"
        ><template #default="{ row }"
          ><RouterLink class="text-link" :to="`/shops/${row.id}`">查看店铺 →</RouterLink></template
        ></el-table-column
      ></el-table
    >
    <p class="muted merchant-scope">
      {{
        merchant.role === 'STAFF'
          ? '当前展示分配给你的门店，更多门店请联系商家管理员授权。'
          : '当前展示你所属商家的门店。'
      }}
    </p>
  </section>
  <div class="merchant-grid">
    <article class="panel">
      <span class="step-number">01 · 后续接入</span>
      <h2>门店与员工</h2>
      <p>商家入驻、门店维护与员工授权，明确店长和核销员的工作范围。</p>
    </article>
    <article class="panel">
      <span class="step-number">02 · 后续接入</span>
      <h2>活动与库存</h2>
      <p>发布优惠活动，管理活动时间、券库存与上下架状态。</p>
    </article>
    <article class="panel">
      <span class="step-number">03 · 后续接入</span>
      <h2>核销与售后</h2>
      <p>到店核销、退款审批与操作审计，形成完整履约记录。</p>
    </article>
  </div>
</template>
