// Database outage drill: MySQL is stopped while a shopper buys. The drill proves
// that an accepted request is never lost: while the database is down the Stream
// entry stays pending and no order is written, and after the database is back the
// entry is either consumed into a durable order or parked in tb_order_review for
// an operator. An accepted request must never end with neither outcome.
//
// Required: API_BASE, MYSQL_HOST, MYSQL_PORT, MYSQL_DATABASE, MYSQL_USER,
// MYSQL_PASSWORD, MYSQL_CONTAINER, REDIS_CONTAINER.
// Usage: node scripts/go-fault-mysql.mjs
import { randomBytes, randomInt } from 'node:crypto'
import { spawnSync } from 'node:child_process'
import { mkdir, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'

for (const key of ['MYSQL_HOST', 'MYSQL_PORT', 'MYSQL_DATABASE', 'MYSQL_USER', 'MYSQL_PASSWORD', 'MYSQL_CONTAINER', 'REDIS_CONTAINER']) {
  if (!process.env[key]) throw new Error(`Explicit isolated ${key} is required`)
}
const API = process.env.API_BASE || 'http://127.0.0.1:8082'
const output = resolve(process.env.FAULT_EVIDENCE || 'docs/evidence/go-fault-mysql-result.json')
await mkdir(dirname(output), { recursive: true })
const suffix = randomBytes(4).toString('hex')
const report = { task: 'GO-070', kind: 'mysql-fault', passed: false, checks: [] }

function sql(statement) {
  const result = spawnSync(process.env.MYSQL_BIN || 'mysql', [
    '--protocol=TCP', `--host=${process.env.MYSQL_HOST}`, `--port=${process.env.MYSQL_PORT}`,
    `--user=${process.env.MYSQL_USER}`, '--default-character-set=utf8mb4', '--batch', '--skip-column-names',
    process.env.MYSQL_DATABASE,
  ], {
    input: `SET NAMES utf8mb4; SET time_zone='+08:00'; ${statement}`,
    encoding: 'utf8', env: { ...process.env, MYSQL_PWD: process.env.MYSQL_PASSWORD },
  })
  if (result.error || result.status !== 0) throw new Error('Isolated fixture SQL failed')
  return result.stdout.trim()
}

function docker(...args) {
  const result = spawnSync('docker', args, { encoding: 'utf8' })
  if (result.error || result.status !== 0) throw new Error('docker command failed')
  return result.stdout.trim()
}

function redis(...args) {
  const result = spawnSync('docker', ['exec', process.env.REDIS_CONTAINER, 'redis-cli', ...args], { encoding: 'utf8' })
  if (result.error || result.status !== 0) throw new Error('Isolated fixture Redis command failed')
  return result.stdout.trim()
}

async function api(method, path, body, token, headers) {
  const requestHeaders = { 'Content-Type': 'application/json', ...(headers || {}) }
  if (token) requestHeaders.authorization = token
  const response = await fetch(API + path, {
    method, headers: requestHeaders, body: body === undefined ? undefined : JSON.stringify(body),
  })
  const text = await response.text()
  let payload = {}
  try { payload = JSON.parse(text) } catch { payload = { errorMsg: text } }
  return { status: response.status, body: payload }
}

function check(value, description) {
  if (!value) throw new Error(`Check failed: ${description}`)
  report.checks.push(description)
}

async function login(phone) {
  const code = await api('POST', `/user/code?phone=${phone}`)
  if (code.body?.data?.mode !== 'development') throw new Error('Development profile required')
  const session = await api('POST', '/user/login', { phone, code: code.body.data.code })
  if (!session.body?.data) throw new Error('Login failed')
  return session.body.data
}

const merchantPhone = `138${randomInt(10000000, 100000000)}`
const shopperPhone = `139${randomInt(10000000, 100000000)}`
const ids = {}
try {
  ids.merchantUser = sql(`INSERT INTO tb_user (phone,password,nick_name,icon) VALUES ('${merchantPhone}','x','m',''); SELECT LAST_INSERT_ID();`)
  ids.merchant = sql(`INSERT INTO tb_merchant (name,status) VALUES ('fault-${suffix}','ACTIVE'); SELECT LAST_INSERT_ID();`)
  ids.shop = sql(`INSERT INTO tb_shop (merchant_id,name,type_id,images,address,x,y,open_hours) VALUES (${ids.merchant},'fault-${suffix}',1,'','addr',0,0,'09:00-21:00'); SELECT LAST_INSERT_ID();`)
  sql(`INSERT INTO tb_merchant_member (merchant_id,user_id,role_code,status) VALUES (${ids.merchant},${ids.merchantUser},'OWNER','ACTIVE')`)
  ids.shopper = sql(`INSERT INTO tb_user (phone,password,nick_name,icon) VALUES ('${shopperPhone}','x','u',''); SELECT LAST_INSERT_ID();`)
  const owner = await login(merchantPhone)
  const shopper = await login(shopperPhone)
  const begin = new Date(Date.now() - 3600_000).toISOString().replace(/\.\d{3}Z$/, '.000Z')
  const end = new Date(Date.now() + 3600_000).toISOString().replace(/\.\d{3}Z$/, '.000Z')
  const redeemEnd = new Date(Date.now() + 7200_000).toISOString().replace(/\.\d{3}Z$/, '.000Z')
  const activity = await api('POST', '/merchant/activities', {
    shopId: String(ids.shop), title: `fault-${suffix}`, payValue: '1900', actualValue: '2500',
    totalStock: 2, beginTime: begin, endTime: end, redeemBeginTime: begin, redeemEndTime: redeemEnd,
  }, owner, { 'Idempotency-Key': `fault-${suffix}` })
  check(activity.status === 200 && activity.body?.success, 'the merchant can publish the activity')
  const voucherId = activity.body.data.id
  let published = null
  for (let attempt = 0; attempt < 120; attempt++) {
    const reply = await api('GET', '/merchant/activities?current=1&pageSize=50', undefined, owner)
    published = (reply.body?.data || []).find(item => item.id === voucherId)
    if (published?.publishStatus === 'ACTIVE' && published.ready) break
    await new Promise(wait => setTimeout(wait, 250))
  }
  check(published?.publishStatus === 'ACTIVE' && published?.ready === true, 'the activity is ACTIVE with a ready gate')

  // The message of an already accepted request is written straight into the
  // Stream, so the outage lands in the middle of the asynchronous order
  // creation instead of racing the consumer.
  const orderId = `${BigInt(Date.now()) * 1000n + BigInt(randomInt(100, 999))}`
  docker('stop', process.env.MYSQL_CONTAINER)
  await new Promise(wait => setTimeout(wait, 1000))
  const added = redis('XADD', 'stream.orders', '*', 'id', String(orderId),
    'userId', String(ids.shopper), 'voucherId', String(voucherId))
  report.orderId = String(orderId)
  report.fixtureBoundary = 'Direct Stream fixture for consumer outage recovery; this drill does not execute Lua admission or reserve Redis stock.'
  report.streamEntry = added
  check(added.length > 0, 'the explicit consumer test message enters the isolated Stream')
  await new Promise(wait => setTimeout(wait, 5000))
  const pendingDuringOutage = Number(redis('XPENDING', 'stream.orders', 'g1').split('\n')[0] || 0)
  report.pendingDuringOutage = pendingDuringOutage
  check(pendingDuringOutage >= 1, 'the message stays pending while the database is down')  // The consumer keeps retrying; without a durable outcome it must not
  // acknowledge the entry.
  await new Promise(wait => setTimeout(wait, 35000))
  const pendingAfterRetries = Number(redis('XPENDING', 'stream.orders', 'g1').split('\n')[0] || 0)
  report.pendingAfterRetries = pendingAfterRetries
  check(pendingAfterRetries >= 1, 'the entry is still pending after several retries')

  // The database comes back.
  docker('start', process.env.MYSQL_CONTAINER)
  for (let attempt = 0; attempt < 60; attempt++) {
    const ping = spawnSync('docker', ['exec', process.env.MYSQL_CONTAINER, 'mysqladmin', 'ping', '-h', 'localhost', '-uroot', '-proot_go_dev_only', '--silent'], { encoding: 'utf8' })
    if (ping.status === 0) break
    await new Promise(wait => setTimeout(wait, 500))
  }
  let orders = 0
  let reviews = 0
  for (let attempt = 0; attempt < 120; attempt++) {
    orders = Number(sql(`SELECT COUNT(*) FROM tb_voucher_order WHERE voucher_id=${voucherId}`) || 0)
    reviews = Number(sql(`SELECT COUNT(*) FROM tb_order_review WHERE order_id='${orderId}'`) || 0)
    if (orders >= 1) break
    await new Promise(wait => setTimeout(wait, 500))
  }
  report.orders = orders
  report.reviews = reviews
  check(orders === 1 || reviews >= 1,
    `the accepted request must end in a durable outcome (orders=${orders}, reviews=${reviews})`)
  let pendingFinal = Number(redis('XPENDING', 'stream.orders', 'g1').split('\n')[0] || 0)
  for (let attempt = 0; attempt < 40 && pendingFinal >= 1; attempt++) {
    await new Promise(wait => setTimeout(wait, 500))
    pendingFinal = Number(redis('XPENDING', 'stream.orders', 'g1').split('\n')[0] || 0)
  }
  report.pendingFinal = pendingFinal
  check(pendingFinal === 0, `the entry is acknowledged once a durable outcome exists (pending ${pendingFinal})`)
  report.voucherId = voucherId
  report.passed = true
} catch (error) {
  report.error = String(error.message)
} finally {
  spawnSync('docker', ['start', process.env.MYSQL_CONTAINER], { encoding: 'utf8' })
  const list = ids.shopper || 0
  for (const statement of [
    `DELETE FROM tb_order_review WHERE order_id IN (SELECT id FROM tb_voucher_order WHERE user_id IN (${list}))`,
    `DELETE FROM tb_outbox WHERE aggregate_type='ORDER' AND aggregate_id IN (SELECT CAST(id AS CHAR) FROM tb_voucher_order WHERE user_id IN (${list}))`,
    `DELETE FROM tb_voucher_order WHERE user_id IN (${list})`,
    `DELETE FROM tb_audit_log WHERE operator_user_id IN (${list})`,
    `DELETE FROM tb_seckill_voucher WHERE voucher_id IN (SELECT id FROM tb_voucher WHERE shop_id IN (SELECT id FROM tb_shop WHERE merchant_id IN (SELECT id FROM tb_merchant WHERE name LIKE 'fault-${suffix}%')))`,
    `DELETE FROM tb_voucher WHERE shop_id IN (SELECT id FROM tb_shop WHERE merchant_id IN (SELECT id FROM tb_merchant WHERE name LIKE 'fault-${suffix}%'))`,
    `DELETE FROM tb_merchant_member WHERE user_id IN (${list},${ids.merchantUser || 0})`,
    `DELETE FROM tb_shop WHERE merchant_id IN (SELECT id FROM tb_merchant WHERE name LIKE 'fault-${suffix}%')`,
    `DELETE FROM tb_merchant WHERE name LIKE 'fault-${suffix}%'`,
    `DELETE FROM tb_user WHERE id IN (${list}) OR phone IN ('${merchantPhone}','${shopperPhone}')`,
  ]) {
    try { sql(statement) } catch { /* cleanup stays best effort */ }
  }
}
await writeFile(output, JSON.stringify(report, null, 2) + '\n', 'utf8')
console.log(JSON.stringify({ passed: report.passed, acceptedStatus: report.acceptedStatus, pendingDuringOutage: report.pendingDuringOutage, orders: report.orders, reviews: report.reviews, pendingFinal: report.pendingFinal, checks: report.checks.length, error: report.error || null }))
process.exit(report.passed ? 0 : 1)
