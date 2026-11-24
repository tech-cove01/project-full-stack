import type {
  AiConversation,
  AiKnowledgeDoc,
  AiMessageView,
  AiSource,
  PublicKnowledge,
} from '../types'
import { fail, ok, readDemo, shops, writeDemo, type DemoRequest } from './support'
import { findDemoUserOrder } from './trade'

interface Knowledge extends AiKnowledgeDoc {
  published?: PublicKnowledge
}

type Conversation = AiConversation & {
  lastReply?: { id: string; events: ChatEvent[] }
}

type ChatEvent = [name: string, data: unknown]

const scope = 'SHOP_PUBLIC'
const knowledgeKey = 'knowledge'
const activeConversations = new Map<string, Conversation>()
const statusText: Record<number, string> = {
  1: '待支付',
  2: '已支付',
  3: '已核销',
  4: '已取消',
  6: '已退款',
}

function initialKnowledge(): Knowledge[] {
  const published: PublicKnowledge = {
    documentId: '1',
    version: 1,
    scope,
    shopId: '1',
    title: '街角咖啡停车规则',
    body: '到店顾客可在梧桐路公共停车场停车。前 30 分钟免费，超出后按每小时 5 元收费。离店前请按停车场公示结算。',
    epoch: 1,
  }
  return [
    {
      id: published.documentId,
      title: published.title,
      body: published.body,
      shopId: published.shopId,
      scope,
      status: 'PUBLISHED',
      publishedVersion: 1,
      latestVersion: 1,
      latestStatus: 'READY',
      epoch: 1,
      disabled: false,
      published,
    },
  ]
}

function formatDoc(doc: Knowledge, publicOnly = false): AiKnowledgeDoc {
  const { published, ...draft } = doc
  if (!publicOnly || !published) return draft
  return { ...draft, title: published.title, body: published.body }
}

function merchantCanView(role: string, shopId?: string): boolean {
  return (
    (shopId === '1' && (role === 'owner' || role === 'staff')) ||
    (shopId === '2' && role === 'owner')
  )
}

function editKnowledge(
  { method, body, user, query }: DemoRequest,
  id?: string,
  action?: string,
): Response {
  if (!user) return fail('请先选择演示身份。', 401)
  const docs = readDemo(knowledgeKey, initialKnowledge)

  if (!id && method === 'GET') {
    const shopId = query.get('shopId') || undefined
    if (!merchantCanView(user.role, shopId)) return fail('没有该门店的规则查看权限。', 403)
    const staff = user.role === 'staff'
    return ok(
      docs
        .filter((doc) => doc.shopId === shopId && (!staff || (doc.published && !doc.disabled)))
        .map((doc) => formatDoc(doc, staff)),
    )
  }

  if (user.role !== 'owner') return fail('只有店主可以管理门店规则。', 403)
  const create = !id && method === 'POST'
  const update = !!id && !action && method === 'PUT'
  const existing = id ? docs.find((doc) => doc.id === id) : undefined
  if (id && !existing) return fail('找不到这条规则。', 404)
  const shopId = existing?.shopId || (typeof body.shopId === 'string' ? body.shopId : undefined)
  if (!merchantCanView(user.role, shopId)) return fail('没有该门店的规则管理权限。', 403)
  if (existing?.disabled) return fail('这条规则已经停用。', 409)

  let doc = existing
  if (create || update) {
    const title = typeof body.title === 'string' ? body.title.trim() : ''
    const text = typeof body.body === 'string' ? body.body.trim() : ''
    if (!title || title.length > 255 || !text || text.length > 8000) {
      return fail('请填写不超过 255 字的标题和不超过 8000 字的正文。', 400)
    }
    if (create) {
      doc = {
        id: String(Number(docs[0]?.id ?? 0) + 1),
        shopId,
        scope,
        title,
        body: text,
        status: 'DRAFT',
        epoch: 0,
        disabled: false,
      }
      docs.unshift(doc)
    } else if (doc) {
      doc.title = title
      doc.body = text
    }
  } else if (doc && method === 'POST' && action === 'publish') {
    const version = (doc.publishedVersion ?? 0) + 1
    doc.epoch++
    doc.status = 'PUBLISHED'
    doc.publishedVersion = version
    doc.latestVersion = version
    doc.latestStatus = 'READY'
    doc.published = {
      documentId: doc.id,
      version,
      scope,
      shopId: doc.shopId,
      title: doc.title,
      body: doc.body,
      epoch: doc.epoch,
    }
  } else if (doc && method === 'POST' && action === 'disable') {
    doc.disabled = true
    doc.status = 'DISABLED'
    doc.epoch++
  } else if (doc && method === 'POST' && action === 'rebuild') {
    if (!doc.published) return fail('请先发布规则。', 409)
    doc.latestStatus = 'READY'
  } else {
    return fail('此演示操作不受支持。', 405)
  }

  if (!doc) return fail('找不到这条规则。', 404)
  doc.updateTime = new Date().toISOString()
  writeDemo(knowledgeKey, docs)
  return ok(formatDoc(doc))
}

function publicKnowledge(id: string, version: string | null): Response {
  const found = readDemo(knowledgeKey, initialKnowledge).find((item) => item.id === id)
  const published = found?.published
  if (
    !found ||
    found.disabled ||
    !published ||
    (version && version !== String(published.version))
  ) {
    return fail('这条公开规则已失效或不存在。', 404)
  }
  return ok(published)
}

function orderReply(userId: string, message: string): ChatEvent[] {
  const orderId =
    /订单(?:号)?\s*[:：#]?\s*(\d+)/.exec(message)?.[1] || /\b\d{3,}\b/.exec(message)?.[0]
  const order = findDemoUserOrder(userId, orderId)
  if (!order) {
    return [['answer', { text: '当前演示身份下没有查到这笔订单。你可以先去门店领取一张券。' }]]
  }

  const status = statusText[order.orderStatus ?? 0] || '处理中'
  return [
    [
      'tool_result',
      {
        type: 'order',
        id: order.id,
        href: '/orders/' + order.id,
        statusText: status,
        payAmount: order.payAmount,
      },
    ],
    ['answer', { text: '当前演示订单状态为「' + status + '」。打开下方订单可查看详情。' }],
  ]
}

function ruleReply(message: string, selectedShop?: string): ChatEvent[] {
  const mentionedShop = shops.find((shop) => message.includes(shop.name))?.id
  const shopId = selectedShop || mentionedShop || '1'
  const parkingQuestion = /停车|车库|车位/.test(message)
  const rule = readDemo(knowledgeKey, initialKnowledge)
    .flatMap((doc) => (!doc.disabled && doc.published?.shopId === shopId ? [doc.published] : []))
    .find(
      (doc) => message.includes(doc.title) || (parkingQuestion && /停车|车库|车位/.test(doc.title)),
    )
  if (!rule) {
    return [
      [
        'answer',
        {
          text: '本演示暂未找到对应的已发布规则。可以问「街角咖啡停车怎么收费？」或「我的订单」。',
        },
      ],
    ]
  }
  const source: AiSource = {
    chunkId: rule.documentId + ':' + rule.version,
    documentId: rule.documentId,
    version: rule.version,
    title: rule.title,
    scope: rule.scope,
    shopId: rule.shopId,
    excerpt: rule.body.slice(0, 120),
    href: '/ai/knowledge/' + rule.documentId + '?version=' + rule.version,
  }
  return [
    ['sources', [source]],
    ['answer', { text: '按门店已发布规则，' + rule.body.slice(0, 400) }],
  ]
}

function eventResponse(events: ChatEvent[]): Response {
  const frames = [...events, ['done', {}] as ChatEvent]
    .map(([event, data]) => 'event: ' + event + '\ndata: ' + JSON.stringify(data) + '\n\n')
    .join('')
  return new Response(frames, { headers: { 'Content-Type': 'text/event-stream; charset=utf-8' } })
}

function createConversation(userId: string, body: DemoRequest['body']): Response {
  const shopId = typeof body.shopId === 'string' ? body.shopId : undefined
  if (shopId && !shops.some((shop) => shop.id === shopId)) return fail('没有找到这家门店。', 400)
  const conversation: Conversation = {
    id: crypto.randomUUID(),
    shopId,
    messages: [],
  }
  activeConversations.set(userId, conversation)
  return ok(conversation)
}

function addMessage(
  conversation: Conversation,
  userId: string,
  body: DemoRequest['body'],
): Response {
  const text = typeof body.message === 'string' ? body.message.trim() : ''
  const id = typeof body.clientMessageId === 'string' ? body.clientMessageId : ''
  if (!id || !text || text.length > 2000) return fail('请输入 1 至 2000 字的演示问题。', 400)
  if (conversation.lastReply?.id === id) return eventResponse(conversation.lastReply.events)

  const events = /订单/.test(text) ? orderReply(userId, text) : ruleReply(text, conversation.shopId)
  const reply = events.at(-1)?.[1] as { text: string }
  const time = new Date().toISOString()
  const sources = events.find(([name]) => name === 'sources')?.[1] as AiSource[] | undefined
  const messages: AiMessageView[] = [
    { role: 'user', text, time, citationIds: [] },
    {
      role: 'assistant',
      text: reply.text,
      time,
      citationIds: sources?.map((source) => source.chunkId) || [],
    },
  ]
  conversation.messages = conversation.messages.concat(messages).slice(-20)
  conversation.lastReply = { id, events }
  return eventResponse(events)
}

export function handleAssistant(request: DemoRequest): Response | undefined {
  const { user, path, method, body, query } = request
  const merchant = /^\/merchant\/knowledge(?:\/(\d+)(?:\/(publish|disable|rebuild))?)?$/.exec(path)
  if (merchant) return editKnowledge(request, merchant[1], merchant[2])
  if (!path.startsWith('/ai/')) return undefined
  if (!user) return fail('请先选择演示身份。', 401)

  if (path === '/ai/capabilities' && method === 'GET') {
    return ok({
      enabled: true,
      ready: true,
      mode: 'stub',
      reason: '店铺助手使用预设回答、当前示例规则和浏览器中的订单。',
      chatModel: '',
      embeddingModel: '',
      embeddingDimensions: 0,
    })
  }
  if (path === '/ai/conversations' && method === 'POST') return createConversation(user.id, body)
  const knowledge = /^\/ai\/knowledge\/(\d+)$/.exec(path)
  if (knowledge && method === 'GET') return publicKnowledge(knowledge[1], query.get('version'))
  const route = /^\/ai\/conversations\/([a-f0-9-]+)(\/messages)?$/.exec(path)
  if (!route) return undefined
  const conversation = activeConversations.get(user.id)
  if (!conversation || conversation.id !== route[1]) return fail('没有找到这段对话。', 404)
  if (route[2] && method === 'POST') return addMessage(conversation, user.id, body)
  if (!route[2] && method === 'GET') {
    const { id, shopId, messages } = conversation
    return ok({ id, shopId, messages })
  }
  if (!route[2] && method === 'DELETE') {
    activeConversations.delete(user.id)
    return ok(null)
  }
  return fail('此演示操作不受支持。', 405)
}
