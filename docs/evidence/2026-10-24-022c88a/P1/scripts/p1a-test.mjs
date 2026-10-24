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
  const codeResponse = await api('POST', '/user/code?phone=' + phone)
  if (codeResponse.body && codeResponse.body.success === false
      && String(codeResponse.body.errorMsg).includes('频繁')) {
    await new Promise(resolve => setTimeout(resolve, 61000))
    return login(phone, cacheKey)
  }
  assert.equal(codeResponse.status, 200, JSON.stringify(codeResponse))
  assert.equal(codeResponse.body.success, true, JSON.stringify(codeResponse.body))
  const loginResponse = await api('POST', '/user/login', { body: { phone, code: codeResponse.body.data.code } })
  assert.equal(loginResponse.body.success, true, JSON.stringify(loginResponse.body))
  if (cacheKey) {
    cache[cacheKey] = loginResponse.body.data
    writeFileSync(CACHE, JSON.stringify(cache))
  }
  return loginResponse.body.data
}
const now = Date.now()
const iso = ms => new Date(ms).toISOString()
const unique = suffix => '137' + String(now + suffix).slice(-8)
const owner = await login('13800000000', 'owner')
const staff = await login('13900000000', 'staff')
const userA = await login(unique(1))
const userB = await login(unique(2))
const userC = await login(unique(3))
const key = 'p1a-' + now
const body = {
  shopId: '1',
  title: 'P1A 测试券 ' + now,
  subTitle: '自动化验收',
  rules: '仅用于本地验收',
  payValue: '1000',
  actualValue: '2000',
  totalStock: 20,
  beginTime: iso(now - 60_000),
  endTime: iso(now + 10 * 86400_000),
  redeemBeginTime: iso(now - 60_000),
  redeemEndTime: iso(now + 20 * 86400_000),
}

const created = await api('POST', '/merchant/activities', { token: owner, key, body })
assert.equal(created.status, 200, JSON.stringify(created))
assert.equal(created.body.success, true, JSON.stringify(created.body))
const activityId = created.body.data.id
assert.ok(activityId)
assert.equal(created.body.data.publishStatus, 'INITIALIZING')

const repeated = await api('POST', '/merchant/activities', { token: owner, key, body })
assert.equal(repeated.body.data.id, activityId, 'same key/body must return the same activity')
const conflict = await api('POST', '/merchant/activities', { token: owner, key, body: { ...body, title: body.title + ' changed' } })
assert.equal(conflict.status, 409, JSON.stringify(conflict))
const staffPublish = await api('POST', '/merchant/activities', { token: staff, key: key + '-staff', body })
assert.equal(staffPublish.status, 403, JSON.stringify(staffPublish))

let view
for (let i = 0; i < 30; i++) {
  const list = await api('GET', '/merchant/activities?current=1&pageSize=50', { token: owner })
  view = list.body.data.find(item => item.id === activityId)
  if (view && view.ready) break
  await new Promise(resolve => setTimeout(resolve, 500))
}
assert.ok(view, 'activity must appear in the merchant list')
assert.equal(view.ready, true, JSON.stringify(view))
assert.equal(view.publishStatus, 'ACTIVE', JSON.stringify(view))
assert.equal(view.stock, 20, JSON.stringify(view))

const first = await api('POST', '/voucher-order/seckill/' + activityId, { token: userA })
assert.equal(first.body.success, true, JSON.stringify(first.body))
const orderA = first.body.data
const retry = await api('POST', '/voucher-order/seckill/' + activityId, { token: userA })
assert.equal(retry.body.success, true, JSON.stringify(retry.body))
assert.equal(retry.body.data, orderA, 'retry must return the original accepted order ID')

let orderView
for (let i = 0; i < 30; i++) {
  const status = await api('GET', '/voucher-order/' + orderA, { token: userA })
  if (status.body.data && status.body.data.state === 'CREATED') { orderView = status.body.data; break }
  await new Promise(resolve => setTimeout(resolve, 300))
}
assert.ok(orderView, 'order must become CREATED')
assert.equal(orderView.orderStatus, 1, JSON.stringify(orderView))

const second = await api('POST', '/voucher-order/seckill/' + activityId, { token: userB })
assert.equal(second.body.success, true, JSON.stringify(second.body))
const orderB = second.body.data
assert.notEqual(orderB, orderA)
let orderBView
for (let i = 0; i < 30; i++) {
  const status = await api('GET', '/voucher-order/' + orderB, { token: userB })
  if (status.body.data && status.body.data.state === 'CREATED') { orderBView = status.body.data; break }
  await new Promise(resolve => setTimeout(resolve, 300))
}
assert.ok(orderBView, 'second order must become CREATED')

let current
for (let i = 0; i < 30; i++) {
  const afterTwo = await api('GET', '/merchant/activities?current=1&pageSize=50', { token: owner })
  current = afterTwo.body.data.find(item => item.id === activityId)
  if (current && current.stock === 18) break
  await new Promise(resolve => setTimeout(resolve, 300))
}
assert.equal(current.stock, 18, JSON.stringify(current))

const offline = await api('POST', '/merchant/activities/' + activityId + '/offline', { token: owner })
assert.equal(offline.body.success, true, JSON.stringify(offline.body))
assert.equal(offline.body.data.publishStatus, 'OFFLINE', JSON.stringify(offline.body.data))
assert.equal(offline.body.data.ready, false)

const blocked = await api('POST', '/voucher-order/seckill/' + activityId, { token: userC })
assert.equal(blocked.body.success, false, JSON.stringify(blocked.body))

const publicList = await api('GET', '/voucher/list/1')
assert.equal(publicList.body.success, true)
assert.equal(publicList.body.data.some(item => item.id === activityId), false, 'offline activity must be hidden')

console.log(JSON.stringify({
  pass: true,
  activityId,
  orderA,
  orderB,
  stockAfterTwo: current.stock,
  offlineStatus: offline.body.data.publishStatus,
  blockedMessage: blocked.body.errorMsg,
  retrySameId: retry.body.data === orderA,
}))
