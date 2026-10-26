<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElAlert, ElButton, ElSkeleton } from 'element-plus'
import 'element-plus/es/components/alert/style/css'
import 'element-plus/es/components/skeleton/style/css'
import { useRoute } from 'vue-router'
import { api, errorMessage } from '../api'
import type { PublicKnowledge } from '../types'

const route = useRoute()
const doc = ref<PublicKnowledge | null>(null)
const loading = ref(true)
const error = ref('')

onMounted(async () => {
  try {
    const version = route.query.version ? Number(route.query.version) : undefined
    const { data } = await api.publicKnowledge(String(route.params.id), version)
    doc.value = data
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <section class="panel public-knowledge">
    <RouterLink class="text-link" to="/assistant">← 返回助手</RouterLink>
    <el-skeleton v-if="loading" :rows="5" animated />
    <el-alert v-else-if="error" :title="error" type="error" :closable="false" show-icon />
    <template v-else-if="doc">
      <span class="eyebrow">{{
        doc.scope === 'PLATFORM' ? 'PLATFORM RULE' : 'SHOP PUBLIC RULE'
      }}</span>
      <h1>{{ doc.title }}</h1>
      <p class="muted">当前发布版本 v{{ doc.version }} · {{ doc.scope }}</p>
      <p class="public-body">{{ doc.body }}</p>
      <RouterLink to="/assistant"><el-button>继续提问</el-button></RouterLink>
    </template>
  </section>
</template>
