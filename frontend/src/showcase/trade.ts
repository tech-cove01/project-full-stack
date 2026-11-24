import { parseBusinessTime } from '../api'
import type {
  Activity,
  AuditLog,
  MerchantOrder,
  MerchantWorkspace,
  OrderStatus,
  OrderStatusValue,
  Voucher,
} from '../types'
import { fail, ok, readDemo, shops, writeDemo, type DemoRequest, type DemoUser } from './support'

interface DemoActivity extends Activity {
  rules?: string
  readyAt: number
  publishKey?: string
}

interface DemoOrder {
  id: string
  userId: string
  voucherId: string
  status: OrderStatusValue
  createdAt: number
  payDeadline: number
  token?: string
  payTime?: string
  useTime?: string
  refundTime?: string
  cancelTime?: string
  cancelReason?: string
}

interface Trade {
  activities: DemoActivity[]
  orders: DemoOrder[]
  audits: AuditLog[]
}

const storageKey = 'trade'
const day = 24 * 60 * 60 * 1000
const acceptanceDelay = 1350
const publicationDelay = 750

function businessTime(instant: number) {
  const shanghai = new Date(instant + 8 * 60 * 60 * 1000)
  return shanghai.toISOString().slice(0, 19).replace('T', ' ')
}

function initial(): Trade {
  const now = Date.now()
  return {
    activities: [
      {
        id: '1001',
        shopId: '1',
        shopName: shops.find((shop) => shop.id === '1')!.name,
        title: '30 元到店代金券',
        subTitle: '到街角咖啡坐一会儿，每人限领一张。',
        rules: '在核销有效期内到门店出示订单凭证。',
        payValue: '1900',
        actualValue: '3000',
        totalStock: 5,
        stock: 5,
        publishStatus: 'ACTIVE',
        publishVersion: 1,
        ready: true,
        readyAt: now,
        beginTime: businessTime(now - day),
        endTime: businessTime(now + 7 * day),
        redeemBeginTime: businessTime(now - day),
        redeemEndTime: businessTime(now + 14 * day),
      },
    ],
    orders: [],
    audits: [],
  }
}

function save(state: Trade) {
  writeDemo(storageKey, state)
}

function load(now: number) {
  const state = readDemo(storageKey, initial)
  const preparing = state.activities.filter(
    (activity) => activity.publishStatus === 'INITIALIZING' && activity.readyAt <= now,
  )
  for (const activity of preparing) {
    activity.ready = true
    activity.publishStatus = 'ACTIVE'
  }
  const expired = state.orders.filter((order) => order.status === 1 && order.payDeadline <= now)
  for (const order of expired) cancel(state, order, 'PAY_TIMEOUT', now)
  if (preparing.length || expired.length) save(state)
  return state
}

function activityFor(state: Trade, order: DemoOrder) {
  return state.activities.find((activity) => activity.id === order.voucherId)!
}

function cancel(state: Trade, order: DemoOrder, reason: string, now: number) {
  order.status = 4
  order.cancelReason = reason
  order.cancelTime = businessTime(now)
  activityFor(state, order).stock += 1
}

function isCreated(order: DemoOrder, now: number) {
  return order.createdAt + acceptanceDelay <= now
}

function customerOrder(
  state: Trade,
  order: DemoOrder,
  now: number,
  includeToken = false,
): OrderStatus {
  if (!isCreated(order, now)) {
    return { id: order.id, state: 'PROCESSING', message: '已受理，正在确认订单。' }
  }
  const activity = activityFor(state, order)
  return {
    id: order.id,
    state: 'CREATED',
    orderStatus: order.status,
    voucherId: activity.id,
    shopId: activity.shopId,
    payAmount: activity.payValue,
    createTime: businessTime(order.createdAt),
    payDeadlineAt: businessTime(order.payDeadline),
    redeemBeginTime: activity.redeemBeginTime,
    redeemEndTime: activity.redeemEndTime,
    redeemToken: includeToken && order.status === 2 ? order.token : undefined,
    payTime: order.payTime,
    useTime: order.useTime,
    refundTime: order.refundTime,
    cancelTime: order.cancelTime,
    cancelReason: order.cancelReason,
  }
}

function merchantOrder(state: Trade, order: DemoOrder): MerchantOrder {
  const activity = activityFor(state, order)
  return {
    id: order.id,
    shopId: activity.shopId,
    shopName: activity.shopName,
    userId: order.userId,
    phone: '138****0000',
    voucherId: activity.id,
    voucherTitle: activity.title,
    status: order.status,
    payAmount: activity.payValue,
    createTime: businessTime(order.createdAt),
    payTime: order.payTime,
    useTime: order.useTime,
    refundTime: order.refundTime,
    redeemEndTime: activity.redeemEndTime,
  }
}

function scope(user: DemoUser) {
  if (user.role === 'owner') return ['1', '2']
  if (user.role === 'staff') return ['1']
  return []
}

function workspace(user: DemoUser): MerchantWorkspace {
  const allowed = scope(user)
  if (!allowed.length) return { merchants: [] }
  return {
    merchants: [
      {
        id: '1',
        name: '社区生活商家',
        role: user.role === 'owner' ? 'OWNER' : 'STAFF',
        shops: shops
          .filter((shop) => allowed.includes(shop.id))
          .map(({ id, name, address }) => ({ id, name, address: address || '' })),
      },
    ],
  }
}

function page<T>(rows: T[], query: URLSearchParams) {
  const current = Math.max(1, Number.parseInt(query.get('current') || '1', 10) || 1)
  const size = Math.min(100, Math.max(1, Number.parseInt(query.get('pageSize') || '10', 10) || 10))
  const start = (current - 1) * size
  return ok(rows.slice(start, start + size), rows.length)
}

function publicVouchers(state: Trade, shopId: string) {
  return state.activities
    .filter((activity) => activity.shopId === shopId && activity.ready)
    .map<Voucher>((activity) => ({
      id: activity.id,
      shopId: activity.shopId,
      title: activity.title,
      subTitle: activity.subTitle,
      rules: activity.rules,
      payValue: activity.payValue,
      actualValue: activity.actualValue,
      type: 1,
      stock: activity.stock,
      beginTime: activity.beginTime,
      endTime: activity.endTime,
    }))
}

function publicActivity({
  rules: _rules,
  readyAt: _at,
  publishKey: _key,
  ...activity
}: DemoActivity): Activity {
  return activity
}

function createActivity(state: Trade, request: DemoRequest, user: DemoUser, now: number) {
  const { body, headers } = request
  const shopId = String(body.shopId || '')
  if (user.role !== 'owner' || !scope(user).includes(shopId))
    return fail('无此门店的活动管理权限。', 403)
  const key = headers.get('Idempotency-Key')
  const publishKey = key ? `${user.id}:${key}` : undefined
  const existing = key && state.activities.find((activity) => activity.publishKey === publishKey)
  if (existing) return ok(publicActivity(existing))

  const title = typeof body.title === 'string' ? body.title.trim() : ''
  const totalStock = Number(body.totalStock)
  const payValue = String(body.payValue || '')
  const actualValue = String(body.actualValue || '')
  const beginTime = parseBusinessTime(String(body.beginTime || ''))
  const endTime = parseBusinessTime(String(body.endTime || ''))
  const redeemBeginTime = parseBusinessTime(String(body.redeemBeginTime || ''))
  const redeemEndTime = parseBusinessTime(String(body.redeemEndTime || ''))
  if (
    !title ||
    !Number.isInteger(totalStock) ||
    totalStock < 1 ||
    totalStock > 1_000_000 ||
    !/^[1-9]\d{0,18}$/.test(payValue) ||
    !/^[1-9]\d{0,18}$/.test(actualValue) ||
    BigInt(payValue) > BigInt(actualValue) ||
    BigInt(actualValue) > 9223372036854775807n ||
    !(beginTime < endTime && endTime < redeemEndTime && redeemBeginTime < redeemEndTime)
  )
    return fail('请核对活动标题、金额、库存与销售和核销时间。', 400)

  const activity: DemoActivity = {
    id: String(1001 + state.activities.length),
    shopId,
    shopName: shops.find((shop) => shop.id === shopId)!.name,
    title,
    subTitle: typeof body.subTitle === 'string' ? body.subTitle : undefined,
    rules: typeof body.rules === 'string' ? body.rules : undefined,
    payValue,
    actualValue,
    totalStock,
    stock: totalStock,
    publishStatus: 'INITIALIZING',
    publishVersion: 1,
    ready: false,
    readyAt: now + publicationDelay,
    publishKey,
    beginTime: businessTime(beginTime),
    endTime: businessTime(endTime),
    redeemBeginTime: businessTime(redeemBeginTime),
    redeemEndTime: businessTime(redeemEndTime),
  }
  state.activities.unshift(activity)
  save(state)
  return ok(publicActivity(activity))
}

function buy(state: Trade, user: DemoUser, voucherId: string, now: number) {
  const existing = state.orders.find(
    (order) => order.userId === user.id && order.voucherId === voucherId && order.status !== 4,
  )
  if (existing) return ok(existing.id)
  const activity = state.activities.find((item) => item.id === voucherId)
  if (!activity?.ready) return fail('活动暂不可购买。', 409)
  if (activity.stock <= 0) return fail('本场优惠已抢完。', 409)
  if (!(
    parseBusinessTime(activity.beginTime) <= now && now < parseBusinessTime(activity.endTime)
  )) {
    return fail('当前不在活动销售时段。', 409)
  }
  const id = String(70001 + state.orders.length)
  state.orders.unshift({
    id,
    userId: user.id,
    voucherId,
    status: 1,
    createdAt: now,
    payDeadline: now + 15 * 60 * 1000,
  })
  activity.stock -= 1
  save(state)
  return ok(id)
}

function redemptionToken() {
  return Array.from(crypto.getRandomValues(new Uint8Array(32)), (byte) =>
    byte.toString(16).padStart(2, '0'),
  ).join('')
}

function actOnCustomerOrder(state: Trade, order: DemoOrder, action: string, now: number) {
  if (!isCreated(order, now)) return ok(customerOrder(state, order, now))
  if (order.status === 1) {
    if (action === 'pay') {
      const token = redemptionToken()
      order.status = 2
      order.token = token
      order.payTime = businessTime(now)
    } else {
      cancel(state, order, 'USER_CANCEL', now)
    }
    save(state)
  }
  return ok(customerOrder(state, order, now, true))
}

function audit(
  state: Trade,
  order: DemoOrder,
  user: DemoUser,
  action: 'ORDER_REDEEMED' | 'ORDER_REFUNDED',
  detail: string,
  now: number,
) {
  state.audits.unshift({
    id: String(state.audits.length + 1),
    shopId: activityFor(state, order).shopId,
    operatorUserId: user.id,
    operatorType: 'MERCHANT',
    action,
    resourceId: order.id,
    detail,
    createTime: businessTime(now),
  })
}

function redeem(state: Trade, request: DemoRequest, user: DemoUser, now: number) {
  const shopId = String(request.body.shopId || '')
  if (!scope(user).includes(shopId)) return fail('无此门店的核销权限。', 403)
  const token = String(request.body.token || '').trim()
  if (!/^[a-fA-F0-9]{64}$/.test(token)) return fail('核销凭证无效。', 400)
  const order = state.orders.find(
    (item) => item.token === token && activityFor(state, item).shopId === shopId,
  )
  if (!order) return fail('核销凭证无效。', 400)
  if (order.status === 3) return ok({ order: merchantOrder(state, order), alreadyRedeemed: true })
  const activity = activityFor(state, order)
  if (
    order.status !== 2 ||
    !(
      parseBusinessTime(activity.redeemBeginTime) <= now &&
      now < parseBusinessTime(activity.redeemEndTime)
    )
  )
    return fail('当前订单不可核销。', 409)
  order.status = 3
  order.useTime = businessTime(now)
  audit(state, order, user, 'ORDER_REDEEMED', '顾客出示凭证，门店确认核销。', now)
  save(state)
  return ok({ order: merchantOrder(state, order), alreadyRedeemed: false })
}

function refund(state: Trade, orderId: string, user: DemoUser, now: number) {
  const order = state.orders.find((item) => item.id === orderId)
  if (!order || !isCreated(order, now)) return fail('订单不存在。', 404)
  const activity = activityFor(state, order)
  if (user.role !== 'owner' || !scope(user).includes(activity.shopId))
    return fail('无此订单的退款权限。', 403)
  if (order.status === 6) return ok(merchantOrder(state, order))
  if (order.status !== 2 || now >= parseBusinessTime(activity.redeemEndTime)) {
    return fail('当前订单不可退款。', 409)
  }
  order.status = 6
  order.refundTime = businessTime(now)
  activity.stock += 1
  audit(state, order, user, 'ORDER_REFUNDED', '店主执行模拟退款，库存已恢复。', now)
  save(state)
  return ok(merchantOrder(state, order))
}

export function findDemoUserOrder(userId: string, orderId?: string): OrderStatus | undefined {
  const now = Date.now()
  const state = load(now)
  const order = state.orders.find(
    (item) => item.userId === userId && isCreated(item, now) && (!orderId || item.id === orderId),
  )
  return order && customerOrder(state, order, now)
}

export function handleTrade(request: DemoRequest): Response | undefined {
  const { method, path, query, user } = request
  const voucherRoute = /^\/voucher\/list\/(\d+)$/.exec(path)
  const seckillRoute = /^\/voucher-order\/seckill\/(\d+)$/.exec(path)
  const orderRoute = /^\/voucher-order\/(\d+)(?:\/(pay|cancel))?$/.exec(path)
  const activityRoute = /^\/merchant\/activities\/(\d+)\/(retry-publication|offline)$/.exec(path)
  const refundRoute = /^\/merchant\/orders\/(\d+)\/refund$/.exec(path)
  if (
    !voucherRoute &&
    !seckillRoute &&
    !orderRoute &&
    !activityRoute &&
    !refundRoute &&
    ![
      '/voucher-order',
      '/merchant/workspace',
      '/merchant/activities',
      '/merchant/orders',
      '/merchant/orders/redeem',
      '/merchant/audits',
    ].includes(path)
  ) {
    return
  }

  const now = Date.now()
  const state = load(now)
  if (voucherRoute && method === 'GET') return ok(publicVouchers(state, voucherRoute[1]!))
  if (!user) return fail('请先登录。', 401)
  if (seckillRoute && method === 'POST') return buy(state, user, seckillRoute[1]!, now)
  if (orderRoute) {
    const order = state.orders.find((item) => item.id === orderRoute[1] && item.userId === user.id)
    if (!order) return fail('订单不存在或无法查看。', 404)
    const action = orderRoute[2]
    if (!action && method === 'GET') return ok(customerOrder(state, order, now, true))
    if (action && method === 'POST') return actOnCustomerOrder(state, order, action, now)
  }
  if (path === '/voucher-order' && method === 'GET') {
    const orders = state.orders.filter((order) => order.userId === user.id && isCreated(order, now))
    return page(
      orders.map((order) => customerOrder(state, order, now)),
      query,
    )
  }
  if (path === '/merchant/workspace' && method === 'GET') return ok(workspace(user))

  const allowed = scope(user)
  const shopId = query.get('shopId')
  if (shopId && !allowed.includes(shopId)) return fail('无此门店的访问权限。', 403)
  if (path === '/merchant/activities' && method === 'GET') {
    const status = query.get('status')
    const rows = state.activities.filter(
      (activity) =>
        allowed.includes(activity.shopId) &&
        (!shopId || activity.shopId === shopId) &&
        (!status || activity.publishStatus === status),
    )
    return page(rows.map(publicActivity), query)
  }
  if (path === '/merchant/activities' && method === 'POST')
    return createActivity(state, request, user, now)
  if (activityRoute && method === 'POST') {
    const activity = state.activities.find((item) => item.id === activityRoute[1])
    if (!activity) return fail('未找到活动。', 404)
    if (user.role !== 'owner' || !allowed.includes(activity.shopId))
      return fail('无此门店的活动管理权限。', 403)
    if (activityRoute[2] === 'offline') {
      activity.publishStatus = 'OFFLINE'
      activity.ready = false
    } else if (activity.publishStatus === 'OFFLINE') {
      return fail('已下架的活动不能重试发布。', 409)
    }
    save(state)
    return ok(publicActivity(activity))
  }
  if (path === '/merchant/orders' && method === 'GET') {
    const status = query.get('status')
    const orders = state.orders
      .filter((order) => isCreated(order, now))
      .map((order) => merchantOrder(state, order))
      .filter(
        (order) =>
          allowed.includes(order.shopId) &&
          (!shopId || order.shopId === shopId) &&
          (!status || String(order.status) === status),
      )
    return page(orders, query)
  }
  if (path === '/merchant/orders/redeem' && method === 'POST')
    return redeem(state, request, user, now)
  if (refundRoute && method === 'POST') return refund(state, refundRoute[1]!, user, now)
  if (path === '/merchant/audits' && method === 'GET') {
    if (user.role !== 'owner') return fail('无操作审计权限。', 403)
    return page(state.audits, query)
  }
  return fail('示例中未提供此操作。', 405)
}
