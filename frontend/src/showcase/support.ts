import type { Shop, ShopType, User } from '../types'

export type DemoRole = 'customer' | 'owner' | 'staff' | 'reader'
export type DemoUser = User & { role: DemoRole }

export interface DemoRequest {
  path: string
  method: string
  query: URLSearchParams
  body: Record<string, unknown>
  headers: Headers
  user: DemoUser | null
}

export const actors: Record<DemoRole, DemoUser> = {
  customer: { id: '101', nickName: '顾客', role: 'customer' },
  owner: { id: '201', nickName: '店主', role: 'owner' },
  staff: { id: '301', nickName: '店员', role: 'staff' },
  reader: { id: '102', nickName: '读者', role: 'reader' },
}

const pictures = import.meta.env.BASE_URL + 'demo/shops/'
export const shops: Shop[] = [
  {
    id: '1',
    name: '街角咖啡',
    typeId: '1',
    area: '示范街区',
    address: '演示路 1 号',
    openHours: '09:00–20:00',
    avgPrice: 30,
    score: 48,
    images: pictures + 'street-cafe.jpg',
  },
  {
    id: '2',
    name: '青禾食堂',
    typeId: '2',
    area: '示范街区',
    address: '演示路 2 号',
    openHours: '11:00–21:00',
    avgPrice: 45,
    score: 47,
    images: pictures + 'qinghe-kitchen.jpg',
  },
  {
    id: '3',
    name: '城市书房',
    typeId: '3',
    area: '书香街区',
    address: '演示路 3 号',
    openHours: '10:00–21:00',
    avgPrice: 25,
    score: 49,
    images: pictures + 'city-reading-room.jpg',
  },
]

export const shopTypes: ShopType[] = [
  { id: '1', name: '咖啡', sort: 1 },
  { id: '2', name: '餐饮', sort: 2 },
  { id: '3', name: '书店', sort: 3 },
]

const prefix = 'cityloop.showcase.data.v1.'

export function readDemo<T>(key: string, initial: () => T): T {
  const saved = localStorage.getItem(prefix + key)
  if (saved) {
    try {
      return JSON.parse(saved) as T
    } catch {
      localStorage.removeItem(prefix + key)
    }
  }
  const value = initial()
  writeDemo(key, value)
  return value
}

export function writeDemo<T>(key: string, value: T): void {
  localStorage.setItem(prefix + key, JSON.stringify(value))
}

export function clearDemoData() {
  for (const key of Object.keys(localStorage)) {
    if (key.startsWith(prefix)) localStorage.removeItem(key)
  }
}

export function demoToken(role: DemoRole): string {
  return 'showcase-' + role
}

export function demoActor(token: string | null): DemoUser | null {
  return Object.values(actors).find((actor) => demoToken(actor.role) === token) ?? null
}

export function ok(data: unknown, total?: number): Response {
  return Response.json({ success: true, data, ...(total === undefined ? {} : { total }) })
}

export function fail(errorMsg: string, status = 400): Response {
  return Response.json({ success: false, errorMsg, data: null }, { status })
}
