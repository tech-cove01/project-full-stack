<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  ElAlert,
  ElButton,
  ElEmpty,
  ElInput,
  ElMessage,
  ElOption,
  ElSelect,
  ElSkeleton,
  ElTag,
} from 'element-plus'
import 'element-plus/es/components/alert/style/css'
import 'element-plus/es/components/button/style/css'
import 'element-plus/es/components/empty/style/css'
import 'element-plus/es/components/input/style/css'
import 'element-plus/es/components/select/style/css'
import 'element-plus/es/components/skeleton/style/css'
import 'element-plus/es/components/tag/style/css'
import { useRoute, useRouter } from 'vue-router'
import { api, apiBase, errorMessage, money, TOKEN_KEY } from '../api'
import type { AiCapabilities, AiConversation, AiSource, Shop } from '../types'

interface Card {
  type: string
  items?: any[]
  [key: string]: any
}
interface ChatMessage {
  role: 'user' | 'assistant'
  text: string
  time?: string
  sources?: AiSource[]
  tool?: Card
  pending?: boolean
}

const route = useRoute()
const router = useRouter()
const capabilities = ref<AiCapabilities | null>(null)
const shops = ref<Shop[]>([])
const selectedShop = ref(String(route.query.shopId || ''))
const conversation = ref<AiConversation | null>(null)
const messages = ref<ChatMessage[]>([])
const input = ref('')
const loading = ref(false)
const stage = ref('')
const error = ref('')
const loadingPage = ref(true)
const log = ref<HTMLElement>()
let controller: AbortController | undefined

const stages: Record<string, string> = {
  retrieving: '正在检索已发布规则',
  tool: '正在查询实时业务数据',
  generating: '正在组织答复',
  insufficient_evidence: '没有找到足够依据',
  cached: '正在返回已完成的答复',
}

async function scrollToEnd() {
  await nextTick()
  if (log.value) log.value.scrollTop = log.value.scrollHeight
}

async function newConversation() {
  if (conversation.value) {
    await api.deleteConversation(conversation.value.id).catch(() => {})
  }
  const { data } = await api.createConversation(selectedShop.value || undefined)
  conversation.value = data
  messages.value = []
  await scrollToEnd()
}

async function load() {
  loadingPage.value = true
  error.value = ''
  try {
    const [capsResult, shopResult] = await Promise.all([
      api.aiCapabilities(),
      api.shops('', 1),
    ])
    capabilities.value = capsResult.data
    shops.value = shopResult.data || []
    if (capabilities.value?.ready) {
      await newConversation()
    }
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    loadingPage.value = false
  }
}

async function send() {
  const text = input.value.trim()
  if (!text || loading.value || !conversation.value) return
  if (!capabilities.value?.ready) {
    ElMessage.warning('AI 助手尚未启用。')
    return
  }
  const clientMessageId =
    typeof crypto !== 'undefined' && 'randomUUID' in crypto
      ? crypto.randomUUID()
      : 'msg-' + Date.now() + '-' + Math.random().toString(16).slice(2)
  input.value = ''
  error.value = ''
  loading.value = true
  stage.value = 'retrieving'
  messages.value.push({ role: 'user', text, time: new Date().toLocaleString() })
  const assistant: ChatMessage = { role: 'assistant', text: '', pending: true }
  messages.value.push(assistant)
  await scrollToEnd()

  controller = new AbortController()
  try {
    const token = localStorage.getItem(TOKEN_KEY)
    const response = await fetch(
      apiBase + '/ai/conversations/' + encodeURIComponent(conversation.value.id) + '/messages',
      {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          ...(token ? { authorization: token } : {}),
        },
        body: JSON.stringify({
          clientMessageId,
          message: text,
          shopId: selectedShop.value || undefined,
        }),
        signal: controller.signal,
      },
    )
    if (!response.ok) {
      let message = 'AI 请求未完成（' + response.status + '）'
      try {
        const body = await response.json()
        if (body.errorMsg) message = body.errorMsg
      } catch {
        /* ignore */
      }
      assistant.text = message
      assistant.pending = false
      error.value = message
      return
    }
    if (!response.body) throw new Error('浏览器不支持流式响应。')
    const reader = response.body.getReader()
    const decoder = new TextDecoder()
    let buffer = ''
    let finished = false
    while (!finished) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      let index
      while ((index = buffer.indexOf('\n\n')) >= 0) {
        const block = buffer.slice(0, index)
        buffer = buffer.slice(index + 2)
        let event = 'message'
        let data: any = null
        for (const line of block.split('\n')) {
          if (line.startsWith('event:')) event = line.slice(6).trim()
          if (line.startsWith('data:')) {
            try {
              data = JSON.parse(line.slice(5).trim())
            } catch {
              data = line.slice(5).trim()
            }
          }
        }
        if (event === 'status' && data?.stage) {
          stage.value = stages[data.stage] || data.stage
        } else if (event === 'sources') {
          assistant.sources = data as AiSource[]
        } else if (event === 'tool_result') {
          assistant.tool = data as Card
        } else if (event === 'answer') {
          assistant.text = data?.text || ''
          assistant.pending = false
        } else if (event === 'error') {
          assistant.text = data?.message || '请求未完成'
          assistant.pending = false
          error.value = assistant.text
          finished = true
        } else if (event === 'done') {
          stage.value = ''
          finished = true
        }
        await scrollToEnd()
      }
    }
    if (assistant.pending) {
      assistant.pending = false
      if (!assistant.text) assistant.text = '连接已中断，结果未确认。'
    }
  } catch (cause) {
    if ((cause as Error).name === 'AbortError') {
      assistant.pending = false
      assistant.text = assistant.text || '已停止本次查询。'
      stage.value = ''
    } else {
      assistant.pending = false
      assistant.text = errorMessage(cause)
      error.value = assistant.text
    }
  } finally {
    loading.value = false
    stage.value = ''
    controller = undefined
    await scrollToEnd()
  }
}

function stop() {
  controller?.abort()
}

async function switchShop(value: string) {
  selectedShop.value = value
  await router.replace({ query: value ? { shopId: value } : {} })
  if (capabilities.value?.ready) await newConversation()
}

function sourceLink(source: AiSource) {
  return source.href || '/assistant'
}

onMounted(() => {
  void load()
})
onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <section class="assistant-page">
    <div class="assistant-heading">
      <div>
        <span class="eyebrow">BUSINESS ASSISTANT</span>
        <h1>店铺助手</h1>
        <p>只依据已发布规则、公开活动信息和本人订单回答；不会替你下单或退款。</p>
      </div>
      <div class="assistant-controls">
        <el-select v-model="selectedShop" placeholder="通用规则" clearable style="width: 190px" @change="switchShop">
          <el-option label="通用规则（不选门店）" value="" />
          <el-option v-for="shop in shops" :key="shop.id" :label="shop.name" :value="shop.id" />
        </el-select>
        <el-button :disabled="loading" @click="newConversation">新会话</el-button>
      </div>
    </div>

    <el-alert
      v-if="capabilities && !capabilities.ready"
      :title="capabilities.reason || 'AI 尚未启用'"
      type="warning"
      :closable="false"
      show-icon
      class="space-bottom"
    />
    <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon class="space-bottom" />
    <el-skeleton v-if="loadingPage" :rows="6" animated />

    <section v-else-if="capabilities?.ready" class="assistant-shell">
      <div ref="log" class="assistant-log">
        <el-empty
          v-if="!messages.length"
          description="问门店规则、查本人订单，或了解当前活动"
        />
        <article
          v-for="(message, index) in messages"
          :key="index"
          class="chat-message"
          :class="message.role"
        >
          <div class="chat-role">{{ message.role === 'user' ? '你' : '助手' }}</div>
          <div class="chat-bubble">
            <p v-if="message.text" class="chat-text">{{ message.text }}</p>
            <span v-if="message.pending" class="muted">正在处理…</span>
            <div v-if="message.sources?.length" class="source-list">
              <span class="eyebrow">SOURCES</span>
              <RouterLink
                v-for="source in message.sources"
                :key="source.chunkId"
                :to="sourceLink(source)"
                class="source-card"
              >
                <strong>{{ source.title }}</strong>
                <span>v{{ source.version }} · {{ source.scope === 'PLATFORM' ? '平台规则' : '门店公开规则' }}</span>
                <small>{{ source.excerpt }}</small>
              </RouterLink>
            </div>
            <div v-if="message.tool" class="tool-card">
              <span class="eyebrow">LIVE DATA</span>
              <template v-if="message.tool.type === 'orders'">
                <RouterLink
                  v-for="item in message.tool.items || []"
                  :key="item.id"
                  :to="item.href || '/orders'"
                  class="tool-row"
                >
                  <span>订单 {{ item.id }}</span>
                  <el-tag size="small">{{ item.statusText }}</el-tag>
                  <span>¥{{ money(item.payAmount) }}</span>
                </RouterLink>
              </template>
              <template v-else-if="message.tool.type === 'order'">
                <RouterLink :to="message.tool.href || '/orders'" class="tool-row">
                  <span>订单 {{ message.tool.id }}</span>
                  <el-tag size="small">{{ message.tool.statusText }}</el-tag>
                  <span>¥{{ money(message.tool.payAmount) }}</span>
                </RouterLink>
              </template>
              <template v-else-if="message.tool.type === 'activities'">
                <RouterLink
                  v-for="item in message.tool.items || []"
                  :key="item.id"
                  :to="item.href || '/'"
                  class="tool-row"
                >
                  <span>{{ item.title }}</span>
                  <span>库存 {{ item.stock }}</span>
                  <span>¥{{ money(item.payValue) }}</span>
                </RouterLink>
              </template>
              <template v-else-if="message.tool.type === 'shops'">
                <RouterLink
                  v-for="item in message.tool.items || []"
                  :key="item.id"
                  :to="item.href || '/'"
                  class="tool-row"
                >
                  <span>{{ item.name }}</span>
                  <span>{{ item.area }}</span>
                  <span>{{ item.score }} 分</span>
                </RouterLink>
              </template>
            </div>
          </div>
        </article>
      </div>

      <div v-if="stage" class="assistant-stage">{{ stage }}<span class="stage-dot" /></div>
      <div class="assistant-input">
        <el-input
          v-model="input"
          type="textarea"
          :rows="2"
          maxlength="2000"
          show-word-limit
          placeholder="例如：未支付取消后还能再买吗？街角咖啡停车怎么收费？"
          @keydown.enter.exact.prevent="send"
        />
        <div class="assistant-actions">
          <span class="muted">不要发送手机号、验证码、Token 或完整核销码。</span>
          <div>
            <el-button v-if="loading" @click="stop">停止</el-button>
            <el-button type="primary" :loading="loading" @click="send">发送</el-button>
          </div>
        </div>
      </div>
    </section>
    <el-empty v-else description="AI 助手未启用；交易、支付和核销功能不受影响">
      <RouterLink to="/"><el-button>返回首页</el-button></RouterLink>
    </el-empty>
  </section>
</template>
