<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { ElButton, ElEmpty, ElInput, ElSkeleton } from 'element-plus'
import 'element-plus/es/components/button/style/css'
import 'element-plus/es/components/empty/style/css'
import 'element-plus/es/components/input/style/css'
import 'element-plus/es/components/skeleton/style/css'
import { api, ApiError } from '../api'
import type { NoteSearchItem, NoteSearchSort } from '../types'

type Filters = { q: string; shopId?: string; sort: NoteSearchSort }
const route = useRoute()
const router = useRouter()
const query = ref('')
const shopId = ref('')
const sort = ref<NoteSearchSort>('relevance')
const items = ref<NoteSearchItem[]>([])
const cursor = ref<string | null>(null)
const loading = ref(false)
const loadingMore = ref(false)
const searched = ref(false)
const error = ref('')
const validation = ref('')
const failedMore = ref(false)
const paginationExpired = ref(false)
const queryLength = computed(() => Array.from(query.value.trim()).length)
let controller: AbortController | undefined
let generation = 0
let submitted: Filters | null = null

function clearResults() {
  controller?.abort()
  generation++
  items.value = []
  cursor.value = null
  loading.value = false
  loadingMore.value = false
  searched.value = false
  failedMore.value = false
  paginationExpired.value = false
  submitted = null
  error.value = ''
  validation.value = ''
}

function validShop(value: string) {
  return /^[1-9]\d{0,19}$/.test(value) && (value.length < 20 || value <= '18446744073709551615')
}

function validate(filters: Filters) {
  if (!filters.q) return '请输入搜索关键词。'
  if (Array.from(filters.q).length > 100) return '搜索关键词最多 100 字。'
  if (filters.q.includes('\0')) return '搜索关键词包含无效字符。'
  if (filters.shopId && !validShop(filters.shopId)) return '请输入有效的门店 ID。'
  return ''
}

function draftChanged() {
  clearResults()
  if (queryLength.value > 100) validation.value = '搜索关键词最多 100 字。'
}

async function load(more: boolean) {
  if (!submitted || (more && (!cursor.value || loadingMore.value))) return
  const filters = submitted
  const next = more ? cursor.value : null
  controller?.abort()
  const active = new AbortController()
  controller = active
  const version = ++generation
  error.value = ''
  failedMore.value = false
  paginationExpired.value = false
  loading.value = !more
  loadingMore.value = more
  try {
    const { data } = await api.searchNotes(
      { ...filters, limit: 10, cursor: next ?? undefined },
      active.signal,
    )
    if (active.signal.aborted || version !== generation) return
    if (more) {
      const seen = new Set(items.value.map((item) => item.id))
      items.value = [...items.value, ...data.items.filter((item) => !seen.has(item.id))]
    } else {
      items.value = data.items
    }
    cursor.value = data.nextCursor || null
  } catch (cause) {
    if (active.signal.aborted || version !== generation) return
    if (more && cause instanceof ApiError && cause.status === 400) {
      items.value = []
      cursor.value = null
      error.value = '分页已失效，请重新搜索'
      paginationExpired.value = true
      return
    }
    error.value = '笔记搜索暂时不可用，请稍后重试'
    failedMore.value = more
  } finally {
    if (version === generation && !active.signal.aborted) {
      loading.value = false
      loadingMore.value = false
    }
  }
}

function start(filters: Filters) {
  clearResults()
  validation.value = validate(filters)
  if (validation.value) return
  submitted = filters
  searched.value = true
  void load(false)
}

async function submit() {
  const filters: Filters = {
    q: query.value.trim(),
    shopId: shopId.value.trim() || undefined,
    sort: sort.value,
  }
  const problem = validate(filters)
  if (problem) {
    clearResults()
    validation.value = problem
    return
  }
  const destination = {
    name: 'note-search',
    query: {
      q: filters.q,
      ...(filters.shopId ? { shopId: filters.shopId } : {}),
      sort: filters.sort,
    },
  }
  if (router.resolve(destination).fullPath === route.fullPath) {
    start(filters)
  } else {
    await router.replace(destination)
  }
}

function excerpt(text: string) {
  const runes = Array.from(text.trim())
  return runes.length > 140 ? `${runes.slice(0, 140).join('')}…` : runes.join('')
}

function thumbnail(images: string[]) {
  return images.find((image) => /^https?:\/\//.test(image))
}

watch(
  () => route.query,
  (params) => {
    clearResults()
    query.value = typeof params.q === 'string' ? params.q : ''
    shopId.value = typeof params.shopId === 'string' ? params.shopId : ''
    sort.value = params.sort === 'recent' ? 'recent' : 'relevance'
    if (Array.isArray(params.q) || Array.isArray(params.shopId) || Array.isArray(params.sort)) {
      validation.value = '搜索条件格式不正确，请重新输入。'
      return
    }
    if (params.sort && params.sort !== 'recent' && params.sort !== 'relevance') {
      validation.value = '请选择有效的排序方式。'
      return
    }
    if (query.value || params.q !== undefined) {
      start({ q: query.value.trim(), shopId: shopId.value.trim() || undefined, sort: sort.value })
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <section class="panel note-search">
    <div class="section-heading">
      <div>
        <span class="eyebrow">EXPLORE NOTES</span>
        <h2>搜索笔记</h2>
      </div>
      <span class="muted">从大家的探店分享中寻找灵感</span>
    </div>
    <form class="search-form" aria-label="笔记搜索条件" @submit.prevent="submit">
      <div class="field keyword-field">
        <label for="note-search-keyword">关键词</label>
        <el-input
          id="note-search-keyword"
          v-model="query"
          placeholder="搜索笔记标题或正文"
          clearable
          aria-describedby="note-search-length"
          @input="draftChanged"
        />
        <span id="note-search-length" class="input-hint">{{ queryLength }} / 100 字</span>
      </div>
      <div class="field">
        <label for="note-search-shop">门店 ID（可选）</label>
        <el-input
          id="note-search-shop"
          v-model="shopId"
          placeholder="仅看这家门店的笔记"
          inputmode="numeric"
          clearable
          @input="draftChanged"
        />
      </div>
      <div class="field sort-field">
        <label for="note-search-sort">排序</label>
        <select id="note-search-sort" v-model="sort" @change="draftChanged">
          <option value="relevance">相关度</option>
          <option value="recent">最新发布</option>
        </select>
      </div>
      <el-button type="primary" native-type="submit" :loading="loading">搜索</el-button>
    </form>
    <p v-if="validation" role="alert" class="validation">{{ validation }}</p>
    <el-skeleton v-if="loading" :rows="6" animated aria-label="正在搜索笔记" />
    <template v-else>
      <div v-if="error" role="alert" class="search-error">
        <span>{{ error }}</span>
        <el-button :loading="loadingMore" @click="load(failedMore)">
          {{ paginationExpired ? '重新搜索' : failedMore ? '重试加载' : '重试' }}
        </el-button>
      </div>
      <el-empty
        v-if="!searched && !validation"
        description="输入关键词，查找大家分享的探店笔记。"
      />
      <el-empty
        v-else-if="searched && !items.length && !error"
        :description="
          cursor
            ? '暂时没有可展示的笔记，可以继续加载更多。'
            : '没有找到相关笔记，换个关键词或门店试试。'
        "
      />
      <ul v-if="items.length" class="search-list" aria-label="笔记搜索结果">
        <li v-for="item in items" :key="item.id">
          <article class="search-card">
            <div class="search-body">
              <h3>
                <RouterLink :to="{ name: 'note', params: { id: item.id } }">{{
                  item.title
                }}</RouterLink>
              </h3>
              <p class="search-excerpt">{{ excerpt(item.content) }}</p>
              <div class="search-meta">
                <span>作者 #{{ item.userId }}</span>
                <time>{{ item.createTime }}</time>
                <RouterLink v-if="item.shopId" :to="`/shops/${item.shopId}`"
                  >查看门店 #{{ item.shopId }}</RouterLink
                >
              </div>
            </div>
            <img
              v-if="thumbnail(item.images)"
              :src="thumbnail(item.images)"
              alt=""
              loading="lazy"
              class="search-thumbnail"
            />
          </article>
        </li>
      </ul>
      <div v-if="searched && (cursor || items.length)" class="search-more">
        <el-button v-if="cursor && !failedMore" :loading="loadingMore" @click="load(true)"
          >加载更多</el-button
        >
        <span v-else-if="!cursor && !error" class="muted">已经看完本次搜索结果了</span>
      </div>
    </template>
  </section>
</template>

<style scoped>
.search-form {
  display: flex;
  align-items: flex-start;
  flex-wrap: wrap;
  gap: 16px;
  margin-bottom: 20px;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 180px;
  font-size: 14px;
}

.keyword-field {
  flex: 1;
  min-width: 240px;
}

.sort-field {
  min-width: 120px;
}

.sort-field select {
  height: 32px;
  padding: 0 10px;
  border: 1px solid var(--border-color, #dcdfe6);
  border-radius: 4px;
  color: inherit;
  background: var(--card-bg, #fff);
}

.search-form > .el-button {
  margin-top: 23px;
}

.input-hint,
.search-meta {
  color: var(--muted-text, #606266);
  font-size: 12px;
}

.validation,
.search-error {
  color: #b42318;
}

.search-error {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
  margin-bottom: 16px;
}

.search-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 0;
  margin: 0;
  list-style: none;
}

.search-card {
  display: flex;
  gap: 16px;
  border: 1px solid var(--border-color, #ebeef5);
  border-radius: 12px;
  padding: 16px;
}

.search-body {
  flex: 1;
  min-width: 0;
}

.search-body h3 {
  margin: 0 0 8px;
  font-size: 17px;
}

.search-body a {
  color: inherit;
  text-decoration: none;
}

.search-body a:hover {
  text-decoration: underline;
}

.search-excerpt {
  margin: 0 0 10px;
  color: var(--muted-text, #606266);
  line-height: 1.7;
  overflow-wrap: anywhere;
}

.search-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 20px;
  overflow-wrap: anywhere;
}

.search-thumbnail {
  width: 88px;
  height: 88px;
  object-fit: cover;
  border-radius: 8px;
}

.search-more {
  display: flex;
  justify-content: center;
  margin-top: 20px;
}

@media (max-width: 600px) {
  .field,
  .keyword-field {
    flex: 1 0 100%;
    min-width: 0;
  }

  .search-form > .el-button {
    margin-top: 0;
  }

  .search-thumbnail {
    width: 64px;
    height: 64px;
  }
}
</style>
