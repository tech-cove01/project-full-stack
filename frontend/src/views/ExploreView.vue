<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { ElAlert, ElButton, ElEmpty, ElImage, ElInput, ElSkeleton } from 'element-plus'
import 'element-plus/es/components/alert/style/css'
import 'element-plus/es/components/empty/style/css'
import 'element-plus/es/components/image/style/css'
import 'element-plus/es/components/input/style/css'
import 'element-plus/es/components/skeleton/style/css'
import { api, errorMessage } from '../api'
import type { Shop, ShopType } from '../types'

const pageSize = 10
const query = ref('')
const selectedTypeId = ref('')
const shopTypes = ref<ShopType[]>([])
const typesLoading = ref(false)
const typesError = ref('')
const shops = ref<Shop[]>([])
const page = ref(1)
const loading = ref(false)
const error = ref('')
const hasMore = ref(true)
const total = ref<string | null>(null)
const activeName = ref('')
const activeTypeId = ref('')
let requestController: AbortController | undefined
let typesController: AbortController | undefined

function parseTotal(value: string | number | undefined): bigint | undefined {
  if (typeof value === 'string' && /^\d+$/.test(value)) return BigInt(value)
  if (typeof value === 'number' && Number.isSafeInteger(value) && value >= 0) return BigInt(value)
  return undefined
}

async function loadTypes() {
  typesController?.abort()
  const controller = new AbortController()
  typesController = controller
  typesLoading.value = true
  typesError.value = ''
  try {
    const { data } = await api.shopTypes(controller.signal)
    if (!controller.signal.aborted) shopTypes.value = data || []
  } catch (cause) {
    if (!controller.signal.aborted) typesError.value = errorMessage(cause)
  } finally {
    if (!controller.signal.aborted) typesLoading.value = false
  }
}

function selectType(id: string) {
  if (selectedTypeId.value === id) return
  selectedTypeId.value = id
  void search()
}

async function search(reset = true) {
  if (!reset && loading.value) return
  requestController?.abort()
  const controller = new AbortController()
  requestController = controller
  if (reset) {
    page.value = 1
    activeName.value = query.value.trim()
    activeTypeId.value = selectedTypeId.value
    shops.value = []
    total.value = null
    hasMore.value = true
  }
  loading.value = true
  error.value = ''
  try {
    const { data, total: count } = await api.shops(
      activeName.value,
      page.value,
      controller.signal,
      { typeId: activeTypeId.value || undefined, pageSize },
    )
    if (controller.signal.aborted) return
    const batch = data || []
    shops.value = reset ? batch : [...shops.value, ...batch]
    const countValue = parseTotal(count)
    total.value = countValue === undefined ? null : countValue.toString()
    hasMore.value =
      batch.length > 0 &&
      (countValue === undefined
        ? batch.length === pageSize
        : BigInt(shops.value.length) < countValue)
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
  void loadTypes()
  void search()
})
onBeforeUnmount(() => {
  requestController?.abort()
  typesController?.abort()
})
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
        /><el-button type="primary" size="large" native-type="submit" :aria-busy="loading"
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
      <span class="muted"
        >已加载 {{ shops.length }} 家<span v-if="total !== null"> / 共 {{ total }} 家</span></span
      >
    </div>
    <div class="shop-categories" role="group" aria-label="店铺分类">
      <el-button
        :type="selectedTypeId === '' ? 'primary' : 'default'"
        :aria-pressed="selectedTypeId === ''"
        @click="selectType('')"
        >全部</el-button
      >
      <el-button
        v-for="type in shopTypes"
        :key="type.id"
        :type="selectedTypeId === type.id ? 'primary' : 'default'"
        :aria-pressed="selectedTypeId === type.id"
        @click="selectType(type.id)"
        >{{ type.name || '未命名分类' }}</el-button
      >
      <span v-if="typesLoading" class="muted" role="status">正在加载分类…</span>
    </div>
    <div v-if="typesError" class="category-error space-bottom" role="status">
      <span>分类暂时无法加载，可先浏览全部店铺。{{ typesError }}</span>
      <el-button :loading="typesLoading" @click="loadTypes">重试分类</el-button>
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
      ><el-button v-if="error" @click="search(false)">重新加载</el-button></el-empty
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
      <el-button v-if="hasMore || error" :loading="loading" @click="search(false)">{{
        error ? '重试加载店铺' : '加载更多店铺'
      }}</el-button
      ><span v-else class="muted">已经看完所有店铺了</span>
    </div>
  </section>
</template>

<style scoped>
.shop-categories {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  align-items: center;
  margin-bottom: 22px;
}
.shop-categories .el-button + .el-button {
  margin-left: 0;
}
.category-error {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  align-items: center;
  color: #819089;
  font-size: 13px;
}
</style>
