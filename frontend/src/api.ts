import type {
  Activity,
  AiCapabilities,
  AiConversation,
  AiKnowledgeDoc,
  AuditLog,
  CreateActivityRequest,
  FeedPage,
  LikeState,
  MerchantOrder,
  MerchantWorkspace,
  Note,
  NoteSearchPage,
  NoteSearchSort,
  OrderStatus,
  Relationship,
  PublicKnowledge,
  Result,
  Shop,
  ShopType,
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
    public outcomeUncertain = false,
    public identityChanged = false,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

async function request<T>(
  path: string,
  options: RequestInit = {},
  sessionToken: string | null = localStorage.getItem(TOKEN_KEY),
): Promise<Result<T>> {
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
  if (sessionToken) headers.set('authorization', sessionToken)
  if (options.body) headers.set('Content-Type', 'application/json')
  const identityChanged = () =>
    sessionToken !== null && localStorage.getItem(TOKEN_KEY) !== sessionToken
  const changedIdentityError = () =>
    new ApiError(
      '登录状态已变化，请重新确认当前账号与本次操作结果。',
      undefined,
      !['GET', 'HEAD'].includes(options.method || 'GET'),
      true,
    )

  try {
    const response = await fetch(`${baseUrl}${path}`, {
      ...options,
      headers,
      signal: controller.signal,
    })
    if (response.status === 401) {
      // An older tab/request must not revoke the session that replaced it.
      const expiresCurrentSession = localStorage.getItem(TOKEN_KEY) === sessionToken
      if (expiresCurrentSession) {
        localStorage.removeItem(TOKEN_KEY)
        window.dispatchEvent(new CustomEvent('auth-expired'))
      }
      throw new ApiError('登录已失效，请重新登录。', 401, false, !expiresCurrentSession)
    }
    if (identityChanged()) throw changedIdentityError()
    if (!response.ok) {
      let message = `服务暂时无法处理请求（${response.status}），请稍后重试。`
      try {
        const body = (await response.json()) as Result<unknown>
        if (body.errorMsg) message = body.errorMsg
      } catch {
        /* ignore non-JSON error bodies */
      }
      throw new ApiError(message, response.status, response.status >= 500)
    }
    const result = (await response.json()) as Result<T>
    if (identityChanged()) throw changedIdentityError()
    if (!result.success) throw new ApiError(result.errorMsg || '操作未完成，请稍后重试。')
    return result
  } catch (error) {
    // The 401 handler intentionally removed its own session above. That is a
    // definitive rejection, not an uncertain mutation or a later account swap.
    if (error instanceof ApiError && error.status === 401) throw error
    if (options.signal?.aborted) throw new DOMException('请求已取消', 'AbortError')
    if (identityChanged()) throw changedIdentityError()
    if (error instanceof ApiError) throw error
    if (timedOut) throw new ApiError('请求超时，请检查网络后重试。', undefined, true)
    throw new ApiError('无法连接服务，请确认后端已启动，或稍后重试。', undefined, true)
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
  me: (signal?: AbortSignal, sessionToken?: string) =>
    request<User>('/user/me', { signal }, sessionToken),
  shopTypes: (signal?: AbortSignal) => request<ShopType[]>('/shop-type/list', { signal }),
  shops: (
    name: string,
    current: number,
    signal?: AbortSignal,
    filters: { typeId?: string; pageSize?: number } = {},
  ) => {
    const query = new URLSearchParams({
      name,
      current: String(current),
      pageSize: String(filters.pageSize ?? 10),
    })
    if (filters.typeId) query.set('typeId', filters.typeId)
    return request<Shop[]>(`/shop/of/name?${query.toString()}`, { signal })
  },
  shop: (id: string, signal?: AbortSignal) =>
    request<Shop>(`/shop/${encodeURIComponent(id)}`, { signal }),
  vouchers: (shopId: string, signal?: AbortSignal) =>
    request<Voucher[]>(`/voucher/list/${encodeURIComponent(shopId)}`, { signal }),
  seckill: (voucherId: string, signal?: AbortSignal, sessionToken?: string) =>
    request<string>(
      `/voucher-order/seckill/${encodeURIComponent(voucherId)}`,
      {
        method: 'POST',
        signal,
      },
      sessionToken,
    ),
  order: (id: string, signal?: AbortSignal) =>
    request<OrderStatus>(`/voucher-order/${encodeURIComponent(id)}`, { signal }),
  myOrders: (current: number, pageSize: number, signal?: AbortSignal) =>
    request<OrderStatus[]>(`/voucher-order?current=${current}&pageSize=${pageSize}`, { signal }),
  payOrder: (id: string, signal?: AbortSignal) =>
    request<OrderStatus>(`/voucher-order/${encodeURIComponent(id)}/pay`, {
      method: 'POST',
      signal,
    }),
  cancelOrder: (id: string, reason = 'USER_CANCEL', signal?: AbortSignal) =>
    request<OrderStatus>(
      `/voucher-order/${encodeURIComponent(id)}/cancel?reason=${encodeURIComponent(reason)}`,
      { method: 'POST', signal },
    ),
  workspace: (signal?: AbortSignal) =>
    request<MerchantWorkspace>('/merchant/workspace', { signal }),
  activities: (
    current: number,
    pageSize: number,
    signal?: AbortSignal,
    filters: { shopId?: string; status?: Activity['publishStatus'] } = {},
  ) => {
    const query = new URLSearchParams({ current: String(current), pageSize: String(pageSize) })
    if (filters.shopId) query.set('shopId', filters.shopId)
    if (filters.status) query.set('status', filters.status)
    return request<Activity[]>(`/merchant/activities?${query.toString()}`, { signal })
  },
  createActivity: (body: CreateActivityRequest, idempotencyKey: string, signal?: AbortSignal) =>
    request<Activity>('/merchant/activities', {
      method: 'POST',
      body: JSON.stringify(body),
      headers: { 'Idempotency-Key': idempotencyKey },
      signal,
    }),
  retryActivity: (id: string, signal?: AbortSignal) =>
    request<Activity>(`/merchant/activities/${encodeURIComponent(id)}/retry-publication`, {
      method: 'POST',
      signal,
    }),
  offlineActivity: (id: string, signal?: AbortSignal) =>
    request<Activity>(`/merchant/activities/${encodeURIComponent(id)}/offline`, {
      method: 'POST',
      signal,
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
  createNote: (
    body: { shopId?: string; title: string; content: string; images: string[] },
    signal?: AbortSignal,
  ) =>
    request<Note>('/notes', {
      method: 'POST',
      body: JSON.stringify(body),
      signal,
    }),
  note: (id: string, signal?: AbortSignal) =>
    request<Note>(`/notes/${encodeURIComponent(id)}`, { signal }),
  searchNotes: (
    params: { q: string; shopId?: string; sort?: NoteSearchSort; cursor?: string; limit?: number },
    signal?: AbortSignal,
  ) => {
    const query = new URLSearchParams({ q: params.q, limit: String(params.limit ?? 10) })
    if (params.shopId) query.set('shopId', params.shopId)
    if (params.sort) query.set('sort', params.sort)
    if (params.cursor) query.set('cursor', params.cursor)
    return request<NoteSearchPage>(`/notes/search?${query.toString()}`, { signal }, null)
  },
  setLike: (noteId: string, liked: boolean) =>
    request<LikeState>(`/notes/${encodeURIComponent(noteId)}/like`, {
      method: 'PUT',
      body: JSON.stringify({ liked }),
    }),
  noteLike: (noteId: string, signal?: AbortSignal) =>
    request<LikeState>(`/notes/${encodeURIComponent(noteId)}/like`, { signal }),
  setFollow: (authorId: string, follow: boolean) =>
    request<Relationship>(`/authors/${encodeURIComponent(authorId)}/follow`, {
      method: 'PUT',
      body: JSON.stringify({ follow }),
    }),
  relationship: (authorId: string, signal?: AbortSignal) =>
    request<Relationship>(`/authors/${encodeURIComponent(authorId)}/relationship`, { signal }),
  feed: (
    params: { cursorTime?: string; cursorId?: string; limit?: number },
    signal?: AbortSignal,
  ) => {
    const query = new URLSearchParams()
    if (params.cursorTime) query.set('cursorTime', params.cursorTime)
    if (params.cursorId) query.set('cursorId', params.cursorId)
    query.set('limit', String(params.limit ?? 10))
    return request<FeedPage>(`/feed/notes?${query.toString()}`, { signal })
  },
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

// Offset-free API datetimes are Shanghai local business times. Preserve an
// explicit Z/offset, and never inherit the browser's local time zone.
export function parseBusinessTime(value: string | null | undefined): number {
  if (!value) return NaN
  const text = value.trim().replace(' ', 'T')
  const localDateTime = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?$/.test(text)
  return Date.parse(localDateTime ? `${text}+08:00` : text)
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
