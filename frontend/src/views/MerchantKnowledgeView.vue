<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import {
  ElAlert,
  ElButton,
  ElDialog,
  ElEmpty,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessage,
  ElMessageBox,
  ElOption,
  ElSelect,
  ElSkeleton,
  ElTable,
  ElTableColumn,
  ElTag,
} from 'element-plus'
import 'element-plus/es/components/alert/style/css'
import 'element-plus/es/components/dialog/style/css'
import 'element-plus/es/components/empty/style/css'
import 'element-plus/es/components/form/style/css'
import 'element-plus/es/components/input/style/css'
import 'element-plus/es/components/select/style/css'
import 'element-plus/es/components/skeleton/style/css'
import 'element-plus/es/components/table/style/css'
import 'element-plus/es/components/table-column/style/css'
import 'element-plus/es/components/tag/style/css'
import { api, errorMessage } from '../api'
import type { AiKnowledgeDoc, MerchantWorkspace } from '../types'

const workspace = ref<MerchantWorkspace | null>(null)
const docs = ref<AiKnowledgeDoc[]>([])
const selectedShop = ref('')
const loading = ref(false)
const error = ref('')
const dialogOpen = ref(false)
const submitting = ref(false)
const editingId = ref('')
const form = reactive({ title: '', body: '' })
let controller: AbortController | undefined

const shops = computed(() =>
  (workspace.value?.merchants || []).flatMap((merchant) =>
    merchant.shops.map((shop) => ({ ...shop, role: merchant.role })),
  ),
)
const selectedRole = computed(
  () => shops.value.find((shop) => shop.id === selectedShop.value)?.role,
)
const canManage = computed(() => selectedRole.value === 'OWNER' || selectedRole.value === 'MANAGER')

async function loadWorkspace() {
  const { data } = await api.workspace()
  workspace.value = data
  if (!selectedShop.value && shops.value.length) selectedShop.value = shops.value[0].id
}

async function loadDocs() {
  if (!selectedShop.value) return
  controller?.abort()
  const active = new AbortController()
  controller = active
  loading.value = true
  error.value = ''
  try {
    const { data } = await api.merchantKnowledge(selectedShop.value, active.signal)
    if (!active.signal.aborted) docs.value = data || []
  } catch (cause) {
    if (!active.signal.aborted) error.value = errorMessage(cause)
  } finally {
    if (!active.signal.aborted) loading.value = false
  }
}

async function load() {
  try {
    await loadWorkspace()
    await loadDocs()
  } catch (cause) {
    error.value = errorMessage(cause)
  }
}

function openCreate() {
  editingId.value = ''
  form.title = ''
  form.body = ''
  dialogOpen.value = true
}

function openEdit(doc: any) {
  editingId.value = doc.id
  form.title = doc.title
  form.body = doc.body
  dialogOpen.value = true
}

async function submit() {
  if (!form.title.trim() || !form.body.trim()) {
    ElMessage.warning('请填写标题和公开规则正文。')
    return
  }
  submitting.value = true
  try {
    if (editingId.value) {
      await api.updateKnowledge(editingId.value, form.title.trim(), form.body.trim())
      ElMessage.success('草稿已更新。')
    } else {
      await api.createKnowledge(selectedShop.value, form.title.trim(), form.body.trim())
      ElMessage.success('草稿已创建，发布后才会进入助手检索。')
    }
    dialogOpen.value = false
    await loadDocs()
  } catch (cause) {
    ElMessage.error(errorMessage(cause))
  } finally {
    submitting.value = false
  }
}

async function publish(doc: any) {
  submitting.value = true
  try {
    await api.publishKnowledge(doc.id)
    ElMessage.success('已发布新版本并重建检索索引。')
    await loadDocs()
  } catch (cause) {
    ElMessage.error(errorMessage(cause))
  } finally {
    submitting.value = false
  }
}

async function rebuild(doc: any) {
  submitting.value = true
  try {
    await api.rebuildKnowledge(doc.id)
    ElMessage.success('已重建当前公开内容。')
    await loadDocs()
  } catch (cause) {
    ElMessage.error(errorMessage(cause))
  } finally {
    submitting.value = false
  }
}

async function disable(doc: any) {
  try {
    await ElMessageBox.confirm(
      '停用后该规则将立即退出助手检索和来源引用，确定继续吗？',
      '停用知识',
      {
        confirmButtonText: '停用',
        cancelButtonText: '取消',
        type: 'warning',
      },
    )
  } catch {
    return
  }
  submitting.value = true
  try {
    await api.disableKnowledge(doc.id)
    ElMessage.success('知识已停用。')
    await loadDocs()
  } catch (cause) {
    ElMessage.error(errorMessage(cause))
  } finally {
    submitting.value = false
  }
}

function statusLabel(doc: any) {
  if (doc.disabled) return '已停用'
  if (doc.publishedVersion) return '已发布 v' + doc.publishedVersion
  if (doc.latestStatus === 'INDEXING') return '索引中'
  if (doc.latestStatus === 'FAILED') return '发布失败'
  return '草稿'
}
function statusType(doc: any): 'success' | 'warning' | 'danger' | 'info' {
  if (doc.disabled) return 'info'
  if (doc.publishedVersion) return 'success'
  if (doc.latestStatus === 'FAILED') return 'danger'
  return 'warning'
}

function switchShop() {
  void loadDocs()
}

onMounted(() => {
  void load()
})
onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <section class="merchant-header">
    <span class="eyebrow">AI KNOWLEDGE</span>
    <h1>门店公开规则</h1>
    <p>只有在这里发布的门店公开规则才会进入 AI 检索；草稿、停用内容和私密资料不会发送给模型。</p>
  </section>
  <div class="page-top merchant-actions">
    <RouterLink class="text-link" to="/merchant">← 返回工作台</RouterLink>
    <RouterLink class="text-link" to="/assistant">打开助手 →</RouterLink>
    <el-button v-if="canManage" type="primary" @click="openCreate">新建公开规则</el-button>
  </div>

  <el-alert
    v-if="error"
    :title="error"
    type="error"
    :closable="false"
    show-icon
    class="space-bottom"
  />
  <div class="filter-row space-bottom">
    <el-select
      v-model="selectedShop"
      placeholder="选择门店"
      style="width: 240px"
      @change="switchShop"
    >
      <el-option
        v-for="shop in shops"
        :key="shop.id"
        :label="shop.name + '（' + shop.id + '）'"
        :value="shop.id"
      />
    </el-select>
    <span class="muted">
      {{
        selectedRole === 'STAFF'
          ? '店员只读：仅显示已发布公开规则'
          : '店主/管理者可编辑、发布、停用和重建'
      }}
    </span>
  </div>

  <el-skeleton v-if="loading" :rows="5" animated />
  <el-empty v-else-if="!docs.length" description="这家门店还没有公开规则">
    <el-button v-if="canManage" type="primary" @click="openCreate">新建公开规则</el-button>
  </el-empty>
  <section v-else class="panel">
    <el-table :data="docs" style="width: 100%">
      <el-table-column prop="id" label="ID" width="80" />
      <el-table-column label="标题" min-width="220">
        <template #default="{ row }">
          <strong>{{ row.title }}</strong>
          <div class="muted">{{ row.seedKey || '商家维护' }}</div>
        </template>
      </el-table-column>
      <el-table-column label="状态" width="140">
        <template #default="{ row }">
          <el-tag :type="statusType(row)">{{ statusLabel(row) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="版本 / 索引" width="150">
        <template #default="{ row }">
          v{{ row.latestVersion || row.publishedVersion || '-' }} ·
          {{ row.latestStatus || (row.publishedVersion ? 'READY' : '未发布') }}
          <div v-if="row.latestError" class="muted">{{ row.latestError }}</div>
        </template>
      </el-table-column>
      <el-table-column prop="updateTime" label="更新时间" width="170" />
      <el-table-column label="操作" width="300">
        <template #default="{ row }">
          <el-button v-if="canManage && !row.disabled" link type="primary" @click="openEdit(row)"
            >编辑</el-button
          >
          <el-button
            v-if="canManage && !row.disabled"
            link
            type="primary"
            :loading="submitting"
            @click="publish(row)"
            >发布</el-button
          >
          <el-button
            v-if="canManage && row.publishedVersion && !row.disabled"
            link
            :loading="submitting"
            @click="rebuild(row)"
            >重建</el-button
          >
          <el-button
            v-if="canManage && !row.disabled"
            link
            type="danger"
            :loading="submitting"
            @click="disable(row)"
            >停用</el-button
          >
          <span v-if="!canManage" class="muted">只读</span>
        </template>
      </el-table-column>
    </el-table>
  </section>

  <el-dialog
    v-model="dialogOpen"
    :title="editingId ? '编辑公开规则' : '新建公开规则'"
    width="680px"
  >
    <el-form label-position="top">
      <el-form-item label="标题">
        <el-input v-model="form.title" maxlength="255" placeholder="例如：街角咖啡停车规则" />
      </el-form-item>
      <el-form-item label="公开正文（发布前不会进入检索）">
        <el-input
          v-model="form.body"
          type="textarea"
          :rows="8"
          maxlength="20000"
          show-word-limit
          placeholder="只写可以被顾客公开看到的规则；不要填写私密资料。"
        />
      </el-form-item>
      <el-alert
        title="发布后会生成不可变版本并生成向量；旧版本继续可用，失败时不会替换旧发布指针。"
        type="info"
        :closable="false"
        show-icon
      />
    </el-form>
    <template #footer>
      <el-button @click="dialogOpen = false">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="submit">保存草稿</el-button>
    </template>
  </el-dialog>
</template>
