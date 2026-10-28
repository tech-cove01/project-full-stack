<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElButton, ElEmpty, ElImage, ElInput, ElMessage, ElSkeleton } from 'element-plus'
import 'element-plus/es/components/button/style/css'
import 'element-plus/es/components/input/style/css'
import 'element-plus/es/components/image/style/css'
import 'element-plus/es/components/empty/style/css'
import 'element-plus/es/components/skeleton/style/css'
import { api, errorMessage } from '../api'
import { useAuthStore } from '../stores/auth'
import type { LikeState, Note } from '../types'

const auth = useAuthStore()

const route = useRoute()
const router = useRouter()
const isCompose = computed(() => route.path === '/notes/new')
const note = ref<Note | null>(null)
const loading = ref(false)
const error = ref('')
const submitting = ref(false)
const form = ref({ shopId: '', title: '', content: '', images: '' })
const following = ref(false)
const followerCount = ref(0)
const followLoading = ref(false)
const likeState = ref<LikeState | null>(null)
const likeLoading = ref(false)
const canFollow = computed(
  () => auth.loggedIn && !!note.value && auth.user?.id !== note.value.userId,
)
let controller: AbortController | undefined

async function loadRelationship(target: Note, active: AbortController) {
  if (!auth.loggedIn || auth.user?.id === target.userId) return
  try {
    const { data } = await api.relationship(target.userId, active.signal)
    if (active.signal.aborted || controller !== active || note.value?.id !== target.id) return
    following.value = data.following
    followerCount.value = data.followerCount
  } catch {
    // A relationship read failure must not hide the note itself.
  }
}

async function loadLike(target: Note, active: AbortController) {
  if (!auth.loggedIn) return
  try {
    const { data } = await api.noteLike(target.id, active.signal)
    if (active.signal.aborted || controller !== active || note.value?.id !== target.id) return
    likeState.value = data
  } catch {
    // A like-state read failure must not hide the note itself.
  }
}

async function toggleLike() {
  if (!note.value || likeLoading.value) return
  const target = note.value.id
  const active = controller
  likeLoading.value = true
  try {
    const { data } = await api.setLike(target, !(likeState.value?.liked ?? false))
    if (active?.signal.aborted || controller !== active || note.value?.id !== target) return
    likeState.value = data
  } catch (cause) {
    if (!active?.signal.aborted && controller === active) ElMessage.error(errorMessage(cause))
  } finally {
    if (controller === active) likeLoading.value = false
  }
}

async function toggleFollow() {
  if (!note.value || followLoading.value) return
  const target = note.value
  const active = controller
  followLoading.value = true
  try {
    const { data } = await api.setFollow(target.userId, !following.value)
    if (active?.signal.aborted || controller !== active || note.value?.id !== target.id) return
    following.value = data.following
    followerCount.value = data.followerCount
    ElMessage.success(data.following ? '已关注该作者' : '已取消关注')
  } catch (cause) {
    if (!active?.signal.aborted && controller === active) ElMessage.error(errorMessage(cause))
  } finally {
    if (controller === active) followLoading.value = false
  }
}

const imageList = computed(() =>
  (note.value?.images || []).filter((image) => /^https?:\/\//.test(image)),
)

async function loadNote() {
  controller?.abort()
  const active = new AbortController()
  controller = active
  loading.value = true
  error.value = ''
  note.value = null
  likeState.value = null
  following.value = false
  followerCount.value = 0
  likeLoading.value = false
  followLoading.value = false
  try {
    const { data } = await api.note(String(route.params.id), active.signal)
    if (active.signal.aborted) return
    note.value = data
    await Promise.all([loadRelationship(data, active), loadLike(data, active)])
  } catch (cause) {
    if (!active.signal.aborted) {
      error.value = errorMessage(cause)
      note.value = null
    }
  } finally {
    if (!active.signal.aborted) loading.value = false
  }
}

async function submit() {
  if (submitting.value) return
  const title = form.value.title.trim()
  const content = form.value.content.trim()
  if (!title || !content) {
    ElMessage.warning('请填写标题和正文。')
    return
  }
  const images = form.value.images
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
  if (images.some((image) => !/^https?:\/\//.test(image))) {
    ElMessage.warning('图片地址必须以 http:// 或 https:// 开头。')
    return
  }
  submitting.value = true
  try {
    const { data } = await api.createNote({
      shopId: form.value.shopId.trim() || undefined,
      title,
      content,
      images,
    })
    ElMessage.success('笔记已发布。')
    await router.push({ name: 'note', params: { id: data.id } })
  } catch (cause) {
    ElMessage.error(errorMessage(cause))
  } finally {
    submitting.value = false
  }
}

watch(
  () => route.fullPath,
  () => {
    controller?.abort()
    error.value = ''
    if (isCompose.value) {
      form.value.shopId = String(route.query.shopId || '')
      note.value = null
    } else {
      void loadNote()
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <section v-if="isCompose" class="panel">
    <div class="section-heading">
      <div>
        <span class="eyebrow">NOTE</span>
        <h2>发布探店笔记</h2>
      </div>
      <span class="muted">正文最多 2000 字，图片仅接受 http(s) 链接</span>
    </div>
    <el-skeleton v-if="loading" :rows="4" animated />
    <div v-else class="note-form">
      <label class="field">
        <span>关联门店（可选）</span>
        <el-input v-model="form.shopId" placeholder="门店 ID，例如 1" clearable />
      </label>
      <label class="field">
        <span>标题</span>
        <el-input
          v-model="form.title"
          maxlength="100"
          show-word-limit
          placeholder="例如：这家咖啡店的核销很顺畅"
        />
      </label>
      <label class="field">
        <span>正文</span>
        <el-input
          v-model="form.content"
          type="textarea"
          :rows="6"
          maxlength="2000"
          show-word-limit
          placeholder="写下你的到店体验"
        />
      </label>
      <label class="field">
        <span>图片地址（每行一个）</span>
        <el-input
          v-model="form.images"
          type="textarea"
          :rows="3"
          placeholder="https://cdn.example.com/a.jpg"
        />
      </label>
      <div class="note-actions">
        <el-button type="primary" :loading="submitting" @click="submit">发布</el-button>
        <el-button @click="router.back()">返回</el-button>
      </div>
    </div>
  </section>

  <section v-else class="panel">
    <el-skeleton v-if="loading" :rows="6" animated />
    <el-empty v-else-if="!note" :description="error || '笔记不存在'" />
    <article v-else class="note-detail">
      <div class="section-heading">
        <div>
          <span class="eyebrow">NOTE</span>
          <h2>{{ note.title }}</h2>
        </div>
        <div class="note-head-actions">
          <el-button
            v-if="auth.loggedIn"
            :type="likeState?.liked ? 'success' : 'default'"
            size="small"
            :loading="likeLoading"
            @click="toggleLike"
            >{{ likeState?.liked ? '已点赞' : '点赞' }}</el-button
          >
          <el-button
            v-if="canFollow"
            :type="following ? 'default' : 'primary'"
            size="small"
            :loading="followLoading"
            @click="toggleFollow"
            >{{ following ? '已关注' : '关注作者' }}</el-button
          >
          <span class="muted">{{ note.createTime }}</span>
        </div>
      </div>
      <p v-if="note.shopId" class="muted">关联门店 #{{ note.shopId }}</p>
      <div v-if="imageList.length" class="note-images">
        <el-image
          v-for="image in imageList"
          :key="image"
          :src="image"
          :preview-src-list="imageList"
          fit="cover"
          class="note-image"
        />
      </div>
      <p class="note-content">{{ note.content }}</p>
      <div class="note-meta">
        <span>作者 #{{ note.userId }}</span>
        <span>{{ likeState?.likes ?? note.liked }} 人觉得有用</span>
        <span>{{ note.comments }} 条评论</span>
        <span>{{ followerCount }} 位关注者</span>
      </div>
    </article>
  </section>
</template>

<style scoped>
.note-form {
  display: flex;
  flex-direction: column;
  gap: 16px;
  max-width: 720px;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 14px;
  color: var(--muted-text, #606266);
}

.note-actions {
  display: flex;
  gap: 12px;
}

.note-head-actions {
  display: flex;
  align-items: center;
  gap: 12px;
}

.note-detail {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.note-images {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
}

.note-image {
  width: 180px;
  height: 180px;
  border-radius: 12px;
  overflow: hidden;
}

.note-content {
  white-space: pre-wrap;
  line-height: 1.8;
  font-size: 15px;
}

.note-meta {
  display: flex;
  gap: 24px;
  color: var(--muted-text, #909399);
  font-size: 13px;
}
</style>
