import { handleCommunity } from './community'
import { demoActor, fail, ok, shops, shopTypes, type DemoRequest } from './support'
import { handleTrade } from './trade'

function browse(request: DemoRequest): Response | undefined {
  if (request.method !== 'GET') return
  if (request.path === '/shop-type/list') return ok(shopTypes)
  if (request.path === '/shop/of/name') {
    const name = (request.query.get('name') ?? '').trim().toLowerCase()
    const type = request.query.get('typeId')
    const pageSize = Number(request.query.get('pageSize')) || 10
    const start = (Math.max(1, Number(request.query.get('current')) || 1) - 1) * pageSize
    const results = shops.filter(
      (shop) => (!type || shop.typeId === type) && shop.name.toLowerCase().includes(name),
    )
    return ok(results.slice(start, start + pageSize), results.length)
  }
  const id = /^\/shop\/(\d+)$/.exec(request.path)?.[1]
  if (id) {
    const shop = shops.find((item) => item.id === id)
    return shop ? ok(shop) : fail('未找到店铺。', 404)
  }
}

export function handleDemoFetch(path: string, options: RequestInit): Response {
  options.signal?.throwIfAborted()
  const url = new URL(path, 'https://showcase.invalid')
  const headers = new Headers(options.headers)
  const request: DemoRequest = {
    path: url.pathname,
    method: options.method ?? 'GET',
    query: url.searchParams,
    body: typeof options.body === 'string' ? JSON.parse(options.body) : {},
    headers,
    user: demoActor(headers.get('authorization')),
  }
  if (request.path === '/user/me') {
    return request.user ? ok(request.user) : fail('请先选择演示身份。', 401)
  }
  if (request.path === '/user/logout') return ok(null)
  return (
    browse(request) ??
    handleTrade(request) ??
    handleCommunity(request) ??
    fail('这个操作未包含在公开演示中。', 404)
  )
}
