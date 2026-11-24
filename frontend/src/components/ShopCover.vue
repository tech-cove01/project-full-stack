<script setup lang="ts">
import { computed } from 'vue'
import { ElImage } from 'element-plus'
import 'element-plus/es/components/image/style/css'

const props = withDefaults(
  defineProps<{
    images?: string
    alt?: string
    loading?: 'eager' | 'lazy'
  }>(),
  { alt: '门店照片', loading: 'eager' },
)

const source = computed(() =>
  props.images
    ?.split(',')
    .map((image) => image.trim())
    .find(Boolean),
)
</script>

<template>
  <div class="shop-cover-media">
    <el-image
      v-if="source"
      :src="source"
      :alt="alt"
      :loading="loading"
      fit="cover"
      class="shop-cover-image"
    >
      <template #error>
        <div class="shop-cover-fallback" role="img" :aria-label="`${alt}：暂无门店图片`">
          暂无门店图片
        </div>
      </template>
    </el-image>
    <div v-else class="shop-cover-fallback" role="img" :aria-label="`${alt}：暂无门店图片`">
      暂无门店图片
    </div>
  </div>
</template>

<style scoped>
.shop-cover-media {
  overflow: hidden;
}

.shop-cover-image,
.shop-cover-fallback {
  display: block;
  width: 100%;
  height: 100%;
}

.shop-cover-fallback {
  display: grid;
  place-items: center;
  background: #edf0ea;
  color: #52634f;
  font-size: 16px;
}
</style>
