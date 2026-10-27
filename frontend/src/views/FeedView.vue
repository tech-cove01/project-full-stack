<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import 'element-plus/es/components/button/style/css'
import 'element-plus/es/components/empty/style/css'
import 'element-plus/es/components/skeleton/style/css'
import { api, errorMessage } from '../api'
import type { FeedItem } from '../types'

const items = ref<FeedItem[]>([])
const cursor = ref<{ publishedAt: string; id: string } | null>(null)
const loading = ref(false)
const loadingMore = ref(false)
const error = ref('')
let controller: AbortController | undefined

function excerpt(text: string): string {
  const runes = Array.from(text.trim())
  return runes.length > 120 ? `${runes.slice(0, 120).join('')}…` : runes.join('')
}

async function load(reset: boolean) {
  if (reset) {
    controller?.abort()
    controller = new AbortController()
    loading.value = true
    error.value = ''
  } else {
    if (!cursor.value || loadingMore.value) return
    loadingMore.value = true
  }
  const signal = controller?.signal
  try {
    const params = reset
      ? { limit: 10 }
      : { limit: 10, cursorTime: cursor.value!.publishedAt, cursorId: cursor.value!.id }
    const { data } = await api.feed(params, signal)
    if (signal?.aborted) return
    items.value = reset ? data.items : [...items.value, ...data.items]
    cursor.value = data.nextCursor ?? null
  } catch (cause) {
    if (!signal?.aborted) error.value = errorMessage(cause)
  } finally {
    if (!signal?.aborted) {
      loading.value = false
      loadingMore.value = false
    }
  }
}

onMounted(() => void load(true))
</script>

<template>
  <section class="panel">
    <div class="section-heading">
      <div>
        <span class="eyebrow">FEED</span>
        <h2>关注动态</h2>
      </div>
      <span class="muted">按发布时间倒序，复合游标翻页</span>
    </div>
    <el-skeleton v-if="loading" :rows="6" animated />
    <template v-else>
      <el-empty v-if="error" :description="error">
        <el-button @click="load(true)">重试</el-button>
      </el-empty>
      <el-empty
        v-else-if="!items.length"
        description="还没有关注动态：先到笔记详情页关注作者，或发布你的第一篇笔记。"
      />
      <div v-else class="feed-list">
        <article v-for="item in items" :key="item.id" class="feed-card">
          <div class="feed-body">
            <RouterLink :to="`/notes/${item.id}`" class="feed-title">{{ item.title }}</RouterLink>
            <p class="feed-excerpt">{{ excerpt(item.content) }}</p>
            <div class="feed-meta">
              <span>作者 #{{ item.userId }}</span>
              <span>{{ item.createTime }}</span>
              <span>{{ item.liked }} 人觉得有用</span>
            </div>
          </div>
          <img v-if="item.images.length" :src="item.images[0]" alt="" class="feed-thumb" />
        </article>
      </div>
      <div v-if="items.length" class="feed-more">
        <el-button v-if="cursor" :loading="loadingMore" @click="load(false)">加载更多</el-button>
        <span v-else class="muted">已经到底了</span>
      </div>
    </template>
  </section>
</template>

<style scoped>
.feed-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.feed-card {
  display: flex;
  gap: 16px;
  padding: 16px;
  border: 1px solid var(--border-color, #ebeef5);
  border-radius: 12px;
  background: var(--card-bg, #fff);
}

.feed-body {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.feed-title {
  font-size: 16px;
  font-weight: 600;
  color: inherit;
  text-decoration: none;
}

.feed-title:hover {
  text-decoration: underline;
}

.feed-excerpt {
  margin: 0;
  color: var(--muted-text, #606266);
  line-height: 1.6;
}

.feed-meta {
  display: flex;
  gap: 16px;
  font-size: 12px;
  color: var(--muted-text, #909399);
}

.feed-thumb {
  width: 96px;
  height: 96px;
  object-fit: cover;
  border-radius: 8px;
}

.feed-more {
  display: flex;
  justify-content: center;
  margin-top: 16px;
}
</style>
