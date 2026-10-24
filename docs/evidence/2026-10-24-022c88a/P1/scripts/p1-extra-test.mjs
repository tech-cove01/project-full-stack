import assert from 'node:assert/strict'
import { existsSync, readFileSync, writeFileSync } from 'node:fs'

const BASE = 'http://127.0.0.1:8081'
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
    const me = await api('GET', '/user/me', { token: cache[cacheKey] })
    if (me.body.success) return cache[cacheKey]
  }
  const code = await api('POST', '/user/code?phone=' + phone)
  if (code.body.success === false && String(code.body.errorMsg).includes('频繁')) {
    await new Promise(resolve => setTimeout(resolve, 61000))
    return login(phone, cacheKey)
  }
  const session = await api('POST', '/user/login', { body: { phone, code: code.body.data.code } })
  if (cacheKey) { cache[cacheKey] = session.body.data; writeFileSync(CACHE, JSON.stringify(cache)) }
  return session.body.data
}
const now = Date.now()
const iso = ms => new Date(ms).toISOString()
const owner = await login('13800000000', 'owner')
const staff = await login('13900000000', 'staff')
const user = await login('133' + String(now).slice(-8))
const body = {
  shopId: '1', title: 'P1 并发核销 ' + now, subTitle: '20 并发核销', rules: '验收',
  payValue: '100', actualValue: '200', totalStock: 3,
  beginTime: iso(now - 60_000), endTime: iso(now + 10 * 86400_000),
  redeemBeginTime: iso(now - 60_000), redeemEndTime: iso(now + 20 * 86400_000),
}
const created = await api('POST', '/merchant/activities', { token: owner, key: 'p1-extra-' + now, body })
const activityId = created.body.data.id
for (let i = 0; i < 30; i++) {
  const list = await api('GET', '/merchant/activities?current=1&pageSize=50', { token: owner })
  const view = list.body.data.find(item => item.id === activityId)
  if (view && view.ready) break
  await new Promise(resolve => setTimeout(resolve, 400))
}
const seckill = await api('POST', '/voucher-order/seckill/' + activityId, { token: user })
assert.equal(seckill.body.success, true, JSON.stringify(seckill.body))
const orderId = seckill.body.data
let order
for (let i = 0; i < 40; i++) {
  const detail = await api('GET', '/voucher-order/' + orderId, { token: user })
  if (detail.body.data && detail.body.data.state === 'CREATED') { order = detail.body.data; break }
  await new Promise(resolve => setTimeout(resolve, 200))
}
assert.ok(order)
const paid = await api('POST', '/voucher-order/' + orderId + '/pay', { token: user })
assert.equal(paid.body.data.orderStatus, 2, JSON.stringify(paid.body))
const token = paid.body.data.redeemToken

const attempts = await Promise.all(Array.from({ length: 20 }, () =>
  api('POST', '/merchant/orders/redeem', { token: staff, body: { shopId: '1', token } })
))
const ok = attempts.filter(item => item.status === 200 && item.body.success).length
const already = attempts.filter(item => item.body.success && item.body.data.alreadyRedeemed).length
const conflicts = attempts.filter(item => item.status === 409).length
const errors = attempts.filter(item => item.status >= 500).length
const first = attempts.find(item => item.body.success && !item.body.data.alreadyRedeemed)
assert.ok(first, 'one request must perform the redemption')
assert.equal(errors, 0)
assert.equal(ok + conflicts, 20)

const detail = await api('GET', '/voucher-order/' + orderId, { token: user })
assert.equal(detail.body.data.orderStatus, 3)
console.log(JSON.stringify({ pass: true, activityId, orderId, tokenLength: token.length, ok, already, conflicts, errors, finalStatus: detail.body.data.orderStatus }))
