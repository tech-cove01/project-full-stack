import assert from 'node:assert/strict'
import { existsSync, readFileSync, writeFileSync } from 'node:fs'
import { spawnSync } from 'node:child_process'

const BASE = process.env.API_BASE || 'http://127.0.0.1:8081'
const CACHE = '.qa/tokens.json'
const cache = existsSync(CACHE) ? JSON.parse(readFileSync(CACHE, 'utf8')) : {}
const mysql = process.env.MYSQL_BIN || 'D:/MySQL/Install/bin/mysql.exe'
function sql(query) {
  const result = spawnSync(mysql, ['--protocol=TCP', '--host=127.0.0.1', '--port=3307',
    '--user=fullstack', '--database=project_full_stack', '--default-character-set=utf8mb4', '-e', query],
    { env: { ...process.env, MYSQL_PWD: 'fullstack_dev_only' }, encoding: 'utf8' })
  if (result.status !== 0) throw new Error(result.stderr || 'mysql failed')
  return result.stdout
}
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
  const login = await api('POST', '/user/login', { body: { phone, code: code.body.data.code } })
  assert.equal(login.body.success, true, JSON.stringify(login.body))
  if (cacheKey) { cache[cacheKey] = login.body.data; writeFileSync(CACHE, JSON.stringify(cache)) }
  return login.body.data
}
const now = Date.now()
const iso = ms => new Date(ms).toISOString()
const uniquePhone = suffix => '136' + String(now + suffix).slice(-8)
const owner = await login('13800000000', 'owner')
const userA = await login(uniquePhone(1))
const userB = await login(uniquePhone(2))
const userC = await login(uniquePhone(3))

async function createActivity(stock, suffix) {
  const key = 'p1b-' + now + '-' + suffix
  const body = {
    shopId: '1', title: 'P1B 测试券 ' + now + '-' + suffix, subTitle: '支付取消验收',
    rules: '本地验收', payValue: '1000', actualValue: '2000', totalStock: stock,
    beginTime: iso(now - 60_000), endTime: iso(now + 10 * 86400_000),
    redeemBeginTime: iso(now - 60_000), redeemEndTime: iso(now + 20 * 86400_000),
  }
  const created = await api('POST', '/merchant/activities', { token: owner, key, body })
  assert.equal(created.body.success, true, JSON.stringify(created))
  const id = created.body.data.id
  for (let i = 0; i < 30; i++) {
    const list = await api('GET', '/merchant/activities?current=1&pageSize=50', { token: owner })
    const view = list.body.data.find(item => item.id === id)
    if (view && view.ready) return id
    await new Promise(resolve => setTimeout(resolve, 500))
  }
  throw new Error('activity not ready: ' + id)
}
async function waitCreated(orderId, token) {
  for (let i = 0; i < 40; i++) {
    const response = await api('GET', '/voucher-order/' + orderId, { token })
    if (response.body.success && response.body.data.state === 'CREATED') return response.body.data
    await new Promise(resolve => setTimeout(resolve, 250))
  }
  throw new Error('order not created: ' + orderId)
}
async function waitStatus(orderId, token, status) {
  for (let i = 0; i < 40; i++) {
    const response = await api('GET', '/voucher-order/' + orderId, { token })
    if (response.body.success && response.body.data.orderStatus === status) return response.body.data
    await new Promise(resolve => setTimeout(resolve, 250))
  }
  throw new Error('order did not reach status ' + status + ': ' + orderId)
}

const activityId = await createActivity(5, 'main')
const seckillA = await api('POST', '/voucher-order/seckill/' + activityId, { token: userA })
assert.equal(seckillA.body.success, true, JSON.stringify(seckillA.body))
const orderA = seckillA.body.data
const unpaid = await waitCreated(orderA, userA)
assert.equal(unpaid.orderStatus, 1, JSON.stringify(unpaid))
assert.equal(unpaid.payAmount, '1000', JSON.stringify(unpaid))
assert.equal(unpaid.shopId, '1', JSON.stringify(unpaid))
assert.ok(unpaid.payDeadlineAt)
assert.equal(unpaid.redeemToken, undefined)

const cross = await api('GET', '/voucher-order/' + orderA, { token: userB })
assert.equal(cross.body.success, false, JSON.stringify(cross))
const anonymousPay = await api('POST', '/voucher-order/' + orderA + '/pay')
assert.equal(anonymousPay.status, 401, JSON.stringify(anonymousPay))

const paid = await api('POST', '/voucher-order/' + orderA + '/pay', { token: userA })
assert.equal(paid.body.success, true, JSON.stringify(paid.body))
assert.equal(paid.body.data.orderStatus, 2, JSON.stringify(paid.body.data))
assert.equal(paid.body.data.redeemToken.length, 64, JSON.stringify(paid.body.data))
assert.ok(paid.body.data.payTime)
const payAgain = await api('POST', '/voucher-order/' + orderA + '/pay', { token: userA })
assert.equal(payAgain.body.data.orderStatus, 2)
assert.equal(payAgain.body.data.redeemToken, paid.body.data.redeemToken, 'pay retry must be idempotent')
const cancelPaid = await api('POST', '/voucher-order/' + orderA + '/cancel', { token: userA })
assert.equal(cancelPaid.status, 409, JSON.stringify(cancelPaid))

const seckillB = await api('POST', '/voucher-order/seckill/' + activityId, { token: userB })
assert.equal(seckillB.body.success, true, JSON.stringify(seckillB.body))
const orderB = seckillB.body.data
await waitCreated(orderB, userB)
const cancelled = await api('POST', '/voucher-order/' + orderB + '/cancel', { token: userB })
assert.equal(cancelled.body.success, true, JSON.stringify(cancelled.body))
assert.equal(cancelled.body.data.orderStatus, 4, JSON.stringify(cancelled.body.data))
assert.equal(cancelled.body.data.redeemToken, undefined)
const cancelAgain = await api('POST', '/voucher-order/' + orderB + '/cancel', { token: userB })
assert.equal(cancelAgain.body.data.orderStatus, 4, 'cancel retry must be idempotent')

const reBuy = await api('POST', '/voucher-order/seckill/' + activityId, { token: userB })
assert.equal(reBuy.body.success, true, JSON.stringify(reBuy.body))
const orderB2 = reBuy.body.data
assert.notEqual(orderB2, orderB, 'cancel must release eligibility and allow a new order')
await waitCreated(orderB2, userB)

const timeoutActivity = await createActivity(2, 'timeout')
const seckillC = await api('POST', '/voucher-order/seckill/' + timeoutActivity, { token: userC })
assert.equal(seckillC.body.success, true, JSON.stringify(seckillC.body))
const orderC = seckillC.body.data
await waitCreated(orderC, userC)
sql("UPDATE tb_voucher_order SET pay_deadline_at = DATE_SUB(NOW(3), INTERVAL 5 SECOND) WHERE id = " + orderC)
const timedOut = await waitStatus(orderC, userC, 4)
assert.equal(timedOut.cancelTime !== undefined || timedOut.orderStatus === 4, true)
const payExpired = await api('POST', '/voucher-order/' + orderC + '/pay', { token: userC })
assert.equal(payExpired.status, 409, JSON.stringify(payExpired))

const myOrders = await api('GET', '/voucher-order?current=1&pageSize=50', { token: userA })
assert.equal(myOrders.body.success, true)
assert.equal(myOrders.body.data.some(item => item.id === orderA && item.orderStatus === 2), true)
assert.ok(Number(myOrders.body.total) >= 1)

console.log(JSON.stringify({
  pass: true, activityId, orderA, paidTokenLength: paid.body.data.redeemToken.length,
  cancelledOrder: orderB, reBuyOrder: orderB2, timeoutActivity, timeoutOrder: orderC,
  timeoutStatus: timedOut.orderStatus, payExpiredStatus: payExpired.status,
  myOrdersTotal: myOrders.body.total,
}))
