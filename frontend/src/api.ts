import type { MerchantWorkspace, OrderStatus, Result, Shop, User, Voucher } from './types'

export const TOKEN_KEY = 'cityloop.token'
export const LAST_ORDER_KEY = 'cityloop.last-order'
const baseUrl = (import.meta.env.VITE_API_BASE_URL || '/api').replace(/\/$/, '')

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
      throw new ApiError(
        `服务暂时无法处理请求（${response.status}），请稍后重试。`,
        response.status,
      )
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
  workspace: (signal?: AbortSignal) =>
    request<MerchantWorkspace>('/merchant/workspace', { signal }),
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
