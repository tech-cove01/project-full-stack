<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { ElAlert, ElButton, ElEmpty, ElImage, ElInput, ElSkeleton } from 'element-plus'
import 'element-plus/es/components/alert/style/css'
import 'element-plus/es/components/empty/style/css'
import 'element-plus/es/components/image/style/css'
import 'element-plus/es/components/input/style/css'
import 'element-plus/es/components/skeleton/style/css'
import { api, errorMessage } from '../api'
import type { Shop } from '../types'

const query = ref('')
const shops = ref<Shop[]>([])
const page = ref(1)
const loading = ref(false)
const error = ref('')
const hasMore = ref(true)
let requestController: AbortController | undefined
let activeName = ''

async function search(reset = true) {
  if (!reset && loading.value) return
  requestController?.abort()
  const controller = new AbortController()
  requestController = controller
  if (reset) {
    page.value = 1
    activeName = query.value.trim()
    shops.value = []
  }
  loading.value = true
  error.value = ''
  try {
    const { data } = await api.shops(activeName, page.value, controller.signal)
    if (controller.signal.aborted) return
    const batch = data || []
    shops.value = reset ? batch : [...shops.value, ...batch]
    hasMore.value = batch.length > 0
    page.value += 1
  } catch (cause) {
    if (!controller.signal.aborted) error.value = errorMessage(cause)
  } finally {
    if (!controller.signal.aborted) loading.value = false
  }
}
function cover(shop: Shop) {
  return shop.images?.split(',')[0]
}
onMounted(() => {
  void search()
})
onBeforeUnmount(() => requestController?.abort())
</script>

<template>
  <section class="hero">
    <div>
      <span class="eyebrow">YOUR NEXT LOCAL FAVORITE</span>
      <h1>好店与好时光，<br />就在附近。</h1>
      <p>从一顿美食、一杯咖啡开始，发现城市里的日常惊喜。</p>
    </div>
    <form class="search-box" @submit.prevent="search()">
      <label for="shop-search">想去哪里？</label>
      <div class="search-row">
        <el-input
          id="shop-search"
          v-model="query"
          size="large"
          placeholder="搜索店铺名称"
          clearable
        /><el-button type="primary" size="large" native-type="submit" :loading="loading"
          >找好店</el-button
        >
      </div>
      <span>输入店铺名查找，或直接浏览全部店铺</span>
    </form>
  </section>
  <section class="section-block">
    <div class="section-heading">
      <div>
        <span class="eyebrow">EXPLORE THE NEIGHBORHOOD</span>
        <h2>{{ activeName ? `“${activeName}”的搜索结果` : '发现附近好店' }}</h2>
      </div>
      <span class="muted">已加载 {{ shops.length }} 家</span>
    </div>
    <el-alert
      v-if="error"
      :title="error"
      type="error"
      :closable="false"
      show-icon
      class="space-bottom"
    />
    <el-skeleton v-if="loading && !shops.length" :rows="5" animated />
    <el-empty
      v-else-if="!shops.length"
      :description="error ? '暂时无法加载店铺' : '没有找到相关店铺，换个关键词试试'"
      ><el-button v-if="error" @click="search()">重新加载</el-button></el-empty
    >
    <div v-else class="shop-grid">
      <RouterLink v-for="shop in shops" :key="shop.id" :to="`/shops/${shop.id}`" class="shop-card">
        <el-image
          v-if="cover(shop)"
          :src="cover(shop)"
          fit="cover"
          class="shop-cover"
          loading="lazy"
          ><template #error><div class="image-placeholder">Full Stack</div></template></el-image
        >
        <div v-else class="shop-cover image-placeholder">Full Stack</div>
        <div class="shop-card-body">
          <div class="shop-title-row">
            <h3>{{ shop.name }}</h3>
            <span v-if="shop.score" class="score">{{ (shop.score / 10).toFixed(1) }} 分</span>
          </div>
          <p>{{ shop.area || shop.address || '到店体验，发现更多' }}</p>
          <div class="shop-meta">
            <span>{{ shop.avgPrice != null ? `人均 ¥${shop.avgPrice}` : '查看店铺' }}</span
            ><span>{{ shop.comments ?? 0 }} 条评价 →</span>
          </div>
        </div>
      </RouterLink>
    </div>
    <div v-if="shops.length" class="load-more">
      <el-button v-if="hasMore" :loading="loading" @click="search(false)">加载更多店铺</el-button
      ><span v-else class="muted">已经看完所有店铺了</span>
    </div>
  </section>
</template>
