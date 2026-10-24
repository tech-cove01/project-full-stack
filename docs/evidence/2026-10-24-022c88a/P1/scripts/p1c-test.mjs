import assert from 'node:assert/strict'
import { existsSync, readFileSync, writeFileSync } from 'node:fs'

const BASE = process.env.API_BASE || 'http://127.0.0.1:8081'
const CACHE = '.qa/tokens.json'
const cache = existsSync(CACHE) ? JSON.parse(readFileSync(CACHE, 'utf8')) : {}
async function api(method, path, { token, body, key } = {}) {
  const headers = { 'Content-Type': 'application/json' }
  if (token) headers.authorization = token
  if (key) headers['Idempotency-Key'] = key
  const response = await fetch(BASE + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) })
  const text = await response.text()
  let json
  try { json = text ? JSON.parse(text) : null } catch { json = text }
  return { status: response.status, body: json }
}
async function login(phone, cacheKey) {
  if (cacheKey && cache[cacheKey]) {
    const profile = await api('GET', '/user/me', { token: cache[cacheKey] })
    if (profile.body.success) return cache[cacheKey]
  }
  const code = await api('POST', '/user/code?phone=' + phone)
  if (code.body && code.body.success === false && String(code.body.errorMsg).includes('频繁')) {
    await new Promise(resolve => setTimeout(resolve, 61000))
    return login(phone, cacheKey)
  }
  assert.equal(code.body.success, true, JSON.stringify(code.body))
  const session = await api('POST', '/user/login', { body: { phone, code: code.body.data.code } })
  assert.equal(session.body.success, true, JSON.stringify(session.body))
  if (cacheKey) { cache[cacheKey] = session.body.data; writeFileSync(CACHE, JSON.stringify(cache)) }
  return session.body.data
}
const now = Date.now()
const iso = ms => new Date(ms).toISOString()
const uniquePhone = suffix => '135' + String(now + suffix).slice(-8)
const owner = await login('13800000000', 'owner')
const staff = await login('13900000000', 'staff')
const userA = await login(uniquePhone(1))
const userB = await login(uniquePhone(2))
const outsider = await login(uniquePhone(3))

const key = 'p1c-' + now
const body = {
  shopId: '1', title: 'P1C 测试券 ' + now, subTitle: '核销退款验收', rules: '本地验收',
  payValue: '1000', actualValue: '2000', totalStock: 5,
  beginTime: iso(now - 60_000), endTime: iso(now + 10 * 86400_000),
  redeemBeginTime: iso(now - 60_000), redeemEndTime: iso(now + 20 * 86400_000),
}
const created = await api('POST', '/merchant/activities', { token: owner, key, body })
assert.equal(created.body.success, true, JSON.stringify(created))
const activityId = created.body.data.id
for (let i = 0; i < 30; i++) {
  const list = await api('GET', '/merchant/activities?current=1&pageSize=50', { token: owner })
  const view = list.body.data.find(item => item.id === activityId)
  if (view && view.ready) break
  await new Promise(resolve => setTimeout(resolve, 500))
}
async function waitCreated(orderId, token) {
  for (let i = 0; i < 40; i++) {
    const response = await api('GET', '/voucher-order/' + orderId, { token })
    if (response.body.success && response.body.data.state === 'CREATED') return response.body.data
    await new Promise(resolve => setTimeout(resolve, 250))
  }
  throw new Error('order not created ' + orderId)
}
async function orderOf(userToken, orderId) {
  const response = await api('GET', '/voucher-order/' + orderId, { token: userToken })
  return response.body.data
}

const seckillA = await api('POST', '/voucher-order/seckill/' + activityId, { token: userA })
assert.equal(seckillA.body.success, true, JSON.stringify(seckillA.body))
const orderA = seckillA.body.data
await waitCreated(orderA, userA)
const paidA = await api('POST', '/voucher-order/' + orderA + '/pay', { token: userA })
assert.equal(paidA.body.data.orderStatus, 2, JSON.stringify(paidA.body))
const tokenA = paidA.body.data.redeemToken
assert.equal(tokenA.length, 64)

const ownerOrders = await api('GET', '/merchant/orders?current=1&pageSize=50', { token: owner })
assert.equal(ownerOrders.body.success, true, JSON.stringify(ownerOrders.body))
const merchantOrderA = ownerOrders.body.data.find(item => item.id === orderA)
assert.ok(merchantOrderA, 'owner must see the order')
assert.equal(merchantOrderA.orderStatus, 2, JSON.stringify(merchantOrderA))
assert.match(merchantOrderA.userPhone, /^\d{3}\*{4}\d{4}$/)
assert.equal(merchantOrderA.redeemToken, undefined, 'merchant list must not expose tokens')

const staffOrders = await api('GET', '/merchant/orders?current=1&pageSize=50', { token: staff })
assert.equal(staffOrders.body.success, true, JSON.stringify(staffOrders.body))
assert.ok(staffOrders.body.data.some(item => item.id === orderA))

const staffRefund = await api('POST', '/merchant/orders/' + orderA + '/refund', { token: staff, body: { reason: 'staff should not' } })
assert.equal(staffRefund.status, 403, JSON.stringify(staffRefund))
const outsiderRedeem = await api('POST', '/merchant/orders/redeem', { token: outsider, body: { shopId: '1', token: tokenA } })
assert.equal(outsiderRedeem.status, 403, JSON.stringify(outsiderRedeem))
const invalidToken = await api('POST', '/merchant/orders/redeem', { token: staff, body: { shopId: '1', token: '0'.repeat(64) } })
assert.equal(invalidToken.status, 404, JSON.stringify(invalidToken))
const wrongShop = await api('POST', '/merchant/orders/redeem', { token: staff, body: { shopId: '2', token: tokenA } })
assert.equal(wrongShop.status, 404, JSON.stringify(wrongShop))

const redeem = await api('POST', '/merchant/orders/redeem', { token: staff, body: { shopId: '1', token: tokenA } })
assert.equal(redeem.body.success, true, JSON.stringify(redeem.body))
assert.equal(redeem.body.data.order.orderStatus, 3, JSON.stringify(redeem.body.data))
assert.equal(redeem.body.data.alreadyRedeemed, false)
const redeemAgain = await api('POST', '/merchant/orders/redeem', { token: staff, body: { shopId: '1', token: tokenA } })
assert.equal(redeemAgain.body.success, true, JSON.stringify(redeemAgain.body))
assert.equal(redeemAgain.body.data.alreadyRedeemed, true)
assert.equal(redeemAgain.body.data.order.orderStatus, 3)

const refundRedeemed = await api('POST', '/merchant/orders/' + orderA + '/refund', { token: owner, body: { reason: 'after redeem' } })
assert.equal(refundRedeemed.status, 409, JSON.stringify(refundRedeemed))
const userAOrder = await orderOf(userA, orderA)
assert.equal(userAOrder.orderStatus, 3)
assert.equal(userAOrder.redeemToken, undefined)

const seckillB = await api('POST', '/voucher-order/seckill/' + activityId, { token: userB })
assert.equal(seckillB.body.success, true, JSON.stringify(seckillB.body))
const orderB = seckillB.body.data
await waitCreated(orderB, userB)
const paidB = await api('POST', '/voucher-order/' + orderB + '/pay', { token: userB })
assert.equal(paidB.body.data.orderStatus, 2)
const refundB = await api('POST', '/merchant/orders/' + orderB + '/refund', { token: owner, body: { reason: '验收退款' } })
assert.equal(refundB.body.success, true, JSON.stringify(refundB.body))
assert.equal(refundB.body.data.orderStatus, 6, JSON.stringify(refundB.body.data))
const refundAgain = await api('POST', '/merchant/orders/' + orderB + '/refund', { token: owner, body: { reason: '验收退款' } })
assert.equal(refundAgain.body.data.orderStatus, 6, 'refund retry must be idempotent')
const orderBView = await orderOf(userB, orderB)
assert.equal(orderBView.orderStatus, 6)
assert.equal(orderBView.redeemToken, undefined)

const reBuyAfterRefund = await api('POST', '/voucher-order/seckill/' + activityId, { token: userB })
if (reBuyAfterRefund.body.success) {
  assert.equal(reBuyAfterRefund.body.data, orderB, 'refunded user must not receive a new order')
} else {
  assert.ok(true)
}
const orderBAfter = await orderOf(userB, orderB)
assert.equal(orderBAfter.orderStatus, 6)

let activityView
for (let i = 0; i < 20; i++) {
  const list = await api('GET', '/merchant/activities?current=1&pageSize=50', { token: owner })
  activityView = list.body.data.find(item => item.id === activityId)
  if (activityView && activityView.stock === 4) break
  await new Promise(resolve => setTimeout(resolve, 300))
}
assert.equal(activityView.stock, 4, JSON.stringify(activityView))

const audits = await api('GET', '/merchant/audits?current=1&pageSize=50', { token: owner })
assert.equal(audits.body.success, true, JSON.stringify(audits.body))
const actions = audits.body.data.map(item => item.action)
assert.ok(actions.includes('ORDER_REDEEMED'), actions.join(','))
assert.ok(actions.includes('ORDER_REFUNDED'), actions.join(','))
const staffAudits = await api('GET', '/merchant/audits', { token: staff })
assert.equal(staffAudits.status, 403, JSON.stringify(staffAudits))
const redeemedOnly = await api('GET', '/merchant/orders?current=1&pageSize=50&status=3', { token: owner })
assert.ok(redeemedOnly.body.data.some(item => item.id === orderA))

console.log(JSON.stringify({
  pass: true, activityId, orderA, orderB, finalStock: activityView.stock,
  redeemIdempotent: redeemAgain.body.data.alreadyRedeemed,
  staffRefundStatus: staffRefund.status, outsiderRedeemStatus: outsiderRedeem.status,
  tokenLength: tokenA.length, auditCount: audits.body.total,
}))
