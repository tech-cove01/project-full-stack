<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { ElAlert, ElButton, ElEmpty, ElImage, ElSkeleton, ElTag } from 'element-plus'
import 'element-plus/es/components/alert/style/css'
import 'element-plus/es/components/empty/style/css'
import 'element-plus/es/components/image/style/css'
import 'element-plus/es/components/skeleton/style/css'
import 'element-plus/es/components/tag/style/css'
import { useRoute, useRouter } from 'vue-router'
import { api, errorMessage, money, parseBusinessTime, rememberOrder } from '../api'
import { useAuthStore } from '../stores/auth'
import type { Shop, Voucher } from '../types'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const shop = ref<Shop | null>(null)
const vouchers = ref<Voucher[]>([])
const loading = ref(true)
const vouchersLoading = ref(false)
const error = ref('')
const voucherError = ref('')
const purchasing = ref<string | null>(null)
const purchaseError = ref('')
const now = ref(Date.now())
const clock = setInterval(() => {
  now.value = Date.now()
}, 1000)
const images = computed(() => shop.value?.images?.split(',').filter(Boolean) || [])
let shopController: AbortController | undefined
let voucherController: AbortController | undefined
let purchaseController: AbortController | undefined

function load(id: string) {
  shopController?.abort()
  voucherController?.abort()
  purchaseController?.abort()
  purchasing.value = null
  shop.value = null
  vouchers.value = []
  purchaseError.value = ''
  void loadShop(id)
  void loadVouchers(id)
}

async function loadShop(id: string) {
  shopController?.abort()
  const active = new AbortController()
  shopController = active
  loading.value = true
  error.value = ''
  try {
    const { data } = await api.shop(id, active.signal)
    if (!active.signal.aborted) shop.value = data
  } catch (cause) {
    if (!active.signal.aborted) error.value = errorMessage(cause)
  } finally {
    if (!active.signal.aborted) loading.value = false
  }
}

async function loadVouchers(id: string) {
  voucherController?.abort()
  const active = new AbortController()
  voucherController = active
  vouchersLoading.value = true
  voucherError.value = ''
  try {
    const { data } = await api.vouchers(id, active.signal)
    if (!active.signal.aborted) vouchers.value = data || []
  } catch (cause) {
    if (!active.signal.aborted) voucherError.value = errorMessage(cause)
  } finally {
    if (!active.signal.aborted) vouchersLoading.value = false
  }
}

function disabledReason(voucher: Voucher): string {
  if (voucher.type !== 1) return '普通券购买待接入'
  if (voucher.stock != null && voucher.stock <= 0) return '已抢完'
  const start = parseBusinessTime(voucher.beginTime)
  const end = parseBusinessTime(voucher.endTime)
  if (!Number.isNaN(start) && now.value < start) return '尚未开始'
  if (!Number.isNaN(end) && now.value >= end) return '活动已结束'
  return ''
}

async function buy(voucher: Voucher) {
  if (purchasing.value || disabledReason(voucher)) return
  if (!auth.loggedIn) {
    await router.push({ name: 'login', query: { redirect: route.fullPath } })
    return
  }
  purchasing.value = voucher.id
  purchaseError.value = ''
  const active = new AbortController()
  purchaseController = active
  try {
    const { data: id } = await api.seckill(voucher.id, active.signal)
    if (active.signal.aborted) return
    rememberOrder(id)
    await router.push(`/orders/${id}`)
  } catch (cause) {
    if (active.signal.aborted) return
    purchaseError.value = errorMessage(cause)
    if (purchaseError.value.includes('超时') || purchaseError.value.includes('无法连接')) {
      purchaseError.value += ' 提交结果暂不确定；若已保存订单号，请先查看最近订单。'
    }
  } finally {
    if (purchaseController === active) purchasing.value = null
  }
}
watch(
  () => String(route.params.id),
  (id) => {
    void load(id)
  },
  { immediate: true },
)
onBeforeUnmount(() => {
  shopController?.abort()
  voucherController?.abort()
  purchaseController?.abort()
  clearInterval(clock)
})
</script>

<template>
  <div class="page-top"><RouterLink class="text-link" to="/">← 返回探索</RouterLink></div>
  <el-skeleton v-if="loading" :rows="8" animated />
  <div v-else-if="error || !shop" class="panel">
    <el-empty :description="error || '店铺不存在'"
      ><el-button @click="load(String(route.params.id))">重新加载</el-button></el-empty
    >
  </div>
  <template v-else>
    <section class="shop-detail-hero panel">
      <div class="shop-info">
        <span class="eyebrow">LOCAL FAVORITE</span>
        <h1>{{ shop.name }}</h1>
        <p v-if="shop.score" class="score">
          {{ (shop.score / 10).toFixed(1) }} 分 · {{ shop.comments ?? 0 }} 条评价
        </p>
        <p>{{ shop.address || '商家暂未填写地址' }}</p>
        <p class="muted">
          营业时间：{{ shop.openHours || '请联系商家确认'
          }}<span v-if="shop.avgPrice != null"> · 人均 ¥{{ shop.avgPrice }}</span>
        </p>
      </div>
      <el-image v-if="images[0]" :src="images[0]" fit="cover" class="detail-cover"
        ><template #error><div class="image-placeholder">Full Stack</div></template></el-image
      >
    </section>
    <section class="section-block">
      <div class="section-heading">
        <div>
          <span class="eyebrow">A LITTLE TREAT FOR YOU</span>
          <h2>到店优惠</h2>
        </div>
        <div class="voucher-actions">
          <span class="muted">活动库存与资格以提交结果为准</span>
          <el-button :loading="vouchersLoading" @click="loadVouchers(String(route.params.id))">{{
            voucherError ? '重试优惠' : '刷新优惠'
          }}</el-button>
        </div>
      </div>
      <el-alert
        v-if="voucherError"
        :title="voucherError"
        type="error"
        :closable="false"
        show-icon
      />
      <el-alert
        v-if="purchaseError"
        :title="purchaseError"
        type="error"
        :closable="false"
        show-icon
        class="space-bottom"
      />
      <el-skeleton v-if="vouchersLoading && !vouchers.length" :rows="3" animated />
      <el-empty
        v-else-if="!vouchers.length && !voucherError"
        description="这家店暂时没有上架优惠券"
      />
      <div v-else class="voucher-list">
        <article v-for="voucher in vouchers" :key="voucher.id" class="voucher-card">
          <div class="voucher-value">
            <span
              >¥<strong>{{ money(voucher.payValue) }}</strong></span
            ><small>可抵 ¥{{ money(voucher.actualValue) }}</small>
          </div>
          <div class="voucher-copy">
            <el-tag v-if="voucher.type === 1" type="warning" size="small">限时抢购</el-tag>
            <h3>{{ voucher.title }}</h3>
            <p>{{ voucher.subTitle }}</p>
            <p v-if="voucher.rules" class="muted rules">{{ voucher.rules }}</p>
            <p v-if="voucher.beginTime || voucher.endTime" class="muted">
              {{ voucher.beginTime }} — {{ voucher.endTime }}
            </p>
          </div>
          <el-button
            type="primary"
            size="large"
            :loading="purchasing === voucher.id"
            :disabled="
              Boolean(disabledReason(voucher)) || (purchasing !== null && purchasing !== voucher.id)
            "
            @click="buy(voucher)"
            >{{ disabledReason(voucher) || (auth.loggedIn ? '立即抢购' : '登录后抢购') }}</el-button
          >
        </article>
      </div>
    </section>
  </template>
</template>

<style scoped>
.voucher-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  align-items: center;
  justify-content: flex-end;
}
</style>
