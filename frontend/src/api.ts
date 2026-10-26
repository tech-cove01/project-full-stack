import type {
  Activity,
  AiCapabilities,
  AiConversation,
  AiKnowledgeDoc,
  AuditLog,
  MerchantOrder,
  MerchantWorkspace,
  OrderStatus,
  PublicKnowledge,
  Result,
  Shop,
  User,
  Voucher,
} from './types'

export const TOKEN_KEY = 'cityloop.token'
export const LAST_ORDER_KEY = 'cityloop.last-order'
const baseUrl = (import.meta.env.VITE_API_BASE_URL || '/api').replace(/\/$/, '')

export const apiBase = baseUrl

export class ApiError extends Error {
  constructor(
    message: string,
    public status?: number,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

async function request<T>(path: string, options: RequestInit = {}): Promise<Result<T>> {
  const controller = new AbortController()
  let timedOut = false
  const abort = () => controller.abort()
  if (options.signal?.aborted) controller.abort()
  options.signal?.addEventListener('abort', abort, { once: true })
  const timer = window.setTimeout(() => {
    timedOut = true
    controller.abort()
  }, 12_000)
  const headers = new Headers(options.headers)
  const token = localStorage.getItem(TOKEN_KEY)
  if (token) headers.set('authorization', token)
  if (options.body) headers.set('Content-Type', 'application/json')

  try {
    const response = await fetch(`${baseUrl}${path}`, {
      ...options,
      headers,
      signal: controller.signal,
    })
    if (response.status === 401) {
      localStorage.removeItem(TOKEN_KEY)
      window.dispatchEvent(new CustomEvent('auth-expired'))
      throw new ApiError('登录已失效，请重新登录。', 401)
    }
    if (!response.ok) {
      let message = `服务暂时无法处理请求（${response.status}），请稍后重试。`
      try {
        const body = (await response.json()) as Result<unknown>
        if (body.errorMsg) message = body.errorMsg
      } catch {
        /* ignore non-JSON error bodies */
      }
      throw new ApiError(message, response.status)
    }
    const result = (await response.json()) as Result<T>
    if (!result.success) throw new ApiError(result.errorMsg || '操作未完成，请稍后重试。')
    return result
  } catch (error) {
    if (options.signal?.aborted) throw new DOMException('请求已取消', 'AbortError')
    if (error instanceof ApiError) throw error
    if (timedOut) throw new ApiError('请求超时，请检查网络后重试。')
    throw new ApiError('无法连接服务，请确认后端已启动，或稍后重试。')
  } finally {
    clearTimeout(timer)
    options.signal?.removeEventListener('abort', abort)
  }
}

export function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : '操作未完成，请稍后重试。'
}

export const api = {
  sendCode: (phone: string) =>
    request<{ code?: string; mode?: string } | null>(
      `/user/code?phone=${encodeURIComponent(phone)}`,
      { method: 'POST' },
    ),
  login: (phone: string, code: string) =>
    request<string>('/user/login', { method: 'POST', body: JSON.stringify({ phone, code }) }),
  logout: () => request<null>('/user/logout', { method: 'POST' }),
  me: () => request<User>('/user/me'),
  shops: (name: string, current: number, signal?: AbortSignal) =>
    request<Shop[]>(`/shop/of/name?name=${encodeURIComponent(name)}&current=${current}`, {
      signal,
    }),
  shop: (id: string, signal?: AbortSignal) =>
    request<Shop>(`/shop/${encodeURIComponent(id)}`, { signal }),
  vouchers: (shopId: string, signal?: AbortSignal) =>
    request<Voucher[]>(`/voucher/list/${encodeURIComponent(shopId)}`, { signal }),
  seckill: (voucherId: string) =>
    request<string>(`/voucher-order/seckill/${encodeURIComponent(voucherId)}`, { method: 'POST' }),
  order: (id: string, signal?: AbortSignal) =>
    request<OrderStatus>(`/voucher-order/${encodeURIComponent(id)}`, { signal }),
  myOrders: (current: number, pageSize: number, signal?: AbortSignal) =>
    request<OrderStatus[]>(`/voucher-order?current=${current}&pageSize=${pageSize}`, { signal }),
  payOrder: (id: string) =>
    request<OrderStatus>(`/voucher-order/${encodeURIComponent(id)}/pay`, { method: 'POST' }),
  cancelOrder: (id: string, reason = 'USER_CANCEL') =>
    request<OrderStatus>(
      `/voucher-order/${encodeURIComponent(id)}/cancel?reason=${encodeURIComponent(reason)}`,
      { method: 'POST' },
    ),
  workspace: (signal?: AbortSignal) =>
    request<MerchantWorkspace>('/merchant/workspace', { signal }),
  activities: (current: number, pageSize: number, signal?: AbortSignal) =>
    request<Activity[]>(`/merchant/activities?current=${current}&pageSize=${pageSize}`, { signal }),
  createActivity: (body: unknown, idempotencyKey: string) =>
    request<Activity>('/merchant/activities', {
      method: 'POST',
      body: JSON.stringify(body),
      headers: { 'Idempotency-Key': idempotencyKey },
    }),
  retryActivity: (id: string) =>
    request<Activity>(`/merchant/activities/${encodeURIComponent(id)}/retry-publication`, {
      method: 'POST',
    }),
  offlineActivity: (id: string) =>
    request<Activity>(`/merchant/activities/${encodeURIComponent(id)}/offline`, {
      method: 'POST',
    }),
  merchantOrders: (
    params: { current: number; pageSize: number; shopId?: string; status?: string },
    signal?: AbortSignal,
  ) => {
    const query = new URLSearchParams({
      current: String(params.current),
      pageSize: String(params.pageSize),
    })
    if (params.shopId) query.set('shopId', params.shopId)
    if (params.status) query.set('status', params.status)
    return request<MerchantOrder[]>(`/merchant/orders?${query.toString()}`, { signal })
  },
  redeem: (shopId: string, token: string) =>
    request<{ order: MerchantOrder; alreadyRedeemed: boolean }>('/merchant/orders/redeem', {
      method: 'POST',
      body: JSON.stringify({ shopId, token }),
    }),
  refundOrder: (orderId: string, reason: string) =>
    request<MerchantOrder>(`/merchant/orders/${encodeURIComponent(orderId)}/refund`, {
      method: 'POST',
      body: JSON.stringify({ reason }),
    }),
  audits: (current: number, pageSize: number, signal?: AbortSignal) =>
    request<AuditLog[]>(`/merchant/audits?current=${current}&pageSize=${pageSize}`, { signal }),
  aiCapabilities: () => request<AiCapabilities>('/ai/capabilities'),
  createConversation: (shopId?: string) =>
    request<AiConversation>('/ai/conversations', {
      method: 'POST',
      body: JSON.stringify(shopId ? { shopId } : {}),
    }),
  conversation: (id: string, signal?: AbortSignal) =>
    request<AiConversation>(`/ai/conversations/${encodeURIComponent(id)}`, { signal }),
  deleteConversation: (id: string) =>
    request<null>(`/ai/conversations/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  publicKnowledge: (id: string, version?: number, signal?: AbortSignal) =>
    request<PublicKnowledge>(
      `/ai/knowledge/${encodeURIComponent(id)}${version ? `?version=${version}` : ''}`,
      { signal },
    ),
  merchantKnowledge: (shopId: string, signal?: AbortSignal) =>
    request<AiKnowledgeDoc[]>(`/merchant/knowledge?shopId=${encodeURIComponent(shopId)}`, {
      signal,
    }),
  createKnowledge: (shopId: string, title: string, body: string) =>
    request<AiKnowledgeDoc>('/merchant/knowledge', {
      method: 'POST',
      body: JSON.stringify({ shopId, title, body }),
    }),
  updateKnowledge: (id: string, title: string, body: string) =>
    request<AiKnowledgeDoc>(`/merchant/knowledge/${encodeURIComponent(id)}`, {
      method: 'PUT',
      body: JSON.stringify({ title, body }),
    }),
  publishKnowledge: (id: string) =>
    request<AiKnowledgeDoc>(`/merchant/knowledge/${encodeURIComponent(id)}/publish`, {
      method: 'POST',
    }),
  disableKnowledge: (id: string) =>
    request<AiKnowledgeDoc>(`/merchant/knowledge/${encodeURIComponent(id)}/disable`, {
      method: 'POST',
    }),
  rebuildKnowledge: (id: string) =>
    request<AiKnowledgeDoc>(`/merchant/knowledge/${encodeURIComponent(id)}/rebuild`, {
      method: 'POST',
    }),
}

export function money(cents: number | string | null | undefined): string {
  if (cents == null) return '0.00'

  let value: bigint
  if (typeof cents === 'number') {
    if (!Number.isSafeInteger(cents)) return '—'
    value = BigInt(cents)
  } else {
    const text = cents.trim()
    if (!text) return '0.00'
    if (!/^-?\d+$/.test(text)) return '—'
    value = BigInt(text)
  }

  const negative = value < 0n
  const absolute = negative ? -value : value
  return `${negative ? '-' : ''}${absolute / 100n}.${(absolute % 100n).toString().padStart(2, '0')}`
}

// Store returned IDs as strings so Java Long values never lose precision in the browser.
export function rememberOrder(id: string): void {
  if (typeof id !== 'string' || !/^\d+$/.test(id)) {
    throw new ApiError('订单号格式异常，请联系维护者确认服务端是否将订单号返回为字符串。')
  }
  localStorage.setItem(LAST_ORDER_KEY, id)
  window.dispatchEvent(new CustomEvent('order-remembered'))
}
