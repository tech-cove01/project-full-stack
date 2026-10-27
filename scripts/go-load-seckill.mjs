// Concurrent seckill load: proves the inventory invariant instead of throughput.
// One activity with a known stock is offered to many users at once; the script
// checks that the accepted count equals the stock, that the durable orders match
// it, and that Redis agrees with MySQL.
//
// Required: API_BASE, MYSQL_HOST, MYSQL_PORT, MYSQL_DATABASE, MYSQL_USER,
// MYSQL_PASSWORD, REDIS_HOST, REDIS_PORT.
// Usage: node scripts/go-load-seckill.mjs [stock] [users]
import { randomBytes, randomInt } from 'node:crypto'
import { spawnSync } from 'node:child_process'
import { mkdir, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'

for (const key of ['MYSQL_HOST', 'MYSQL_PORT', 'MYSQL_DATABASE', 'MYSQL_USER', 'MYSQL_PASSWORD', 'REDIS_HOST', 'REDIS_PORT']) {
  if (!process.env[key]) throw new Error(`Explicit isolated ${key} is required`)
}
const API = process.env.API_BASE || 'http://127.0.0.1:8082'
const output = resolve(process.env.LOAD_EVIDENCE || 'docs/evidence/go-load-seckill-result.json')
await mkdir(dirname(output), { recursive: true })
const stock = Number(process.argv[2] || 5)
const users = Number(process.argv[3] || 30)
const suffix = randomBytes(4).toString('hex')
const report = { task: 'GO-070', kind: 'seckill-load', passed: false, stock, users, checks: [] }

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

function redis(...args) {
  // redis-cli usually lives inside the container, not on the host.
  const container = process.env.REDIS_CONTAINER
  const command = container
    ? ['docker', 'exec', container, 'redis-cli', ...args]
    : [process.env.REDIS_BIN || 'redis-cli', '-h', process.env.REDIS_HOST, '-p', process.env.REDIS_PORT, ...args]
  const result = spawnSync(command[0], command.slice(1), { encoding: 'utf8' })
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

const phones = []
while (phones.length < users) {
  const phone = `139${randomInt(10000000, 100000000)}`
  if (!phones.includes(phone)) phones.push(phone)
}
const merchantPhone = `138${randomInt(10000000, 100000000)}`
const userIDs = []
const ids = {}
try {
  ids.merchantUser = Number(sql(`INSERT INTO tb_user (phone,password,nick_name,icon) VALUES ('${merchantPhone}','x','m',''); SELECT LAST_INSERT_ID();`))
  const merchant = Number(sql(`INSERT INTO tb_merchant (name,status) VALUES ('load-${suffix}','ACTIVE'); SELECT LAST_INSERT_ID();`))
  const shop = Number(sql(`INSERT INTO tb_shop (merchant_id,name,type_id,images,address,x,y,open_hours) VALUES (${merchant},'load-${suffix}',1,'','addr',0,0,'09:00-21:00'); SELECT LAST_INSERT_ID();`))
  sql(`INSERT INTO tb_merchant_member (merchant_id,user_id,role_code,status) VALUES (${merchant},${ids.merchantUser},'OWNER','ACTIVE')`)
  for (const phone of phones) {
    userIDs.push(Number(sql(`INSERT INTO tb_user (phone,password,nick_name,icon) VALUES ('${phone}','x','u',''); SELECT LAST_INSERT_ID();`)))
  }
  const owner = await login(merchantPhone)
  const begin = new Date(Date.now() - 3600_000).toISOString().replace(/\.\d{3}Z$/, '.000Z')
  const end = new Date(Date.now() + 3600_000).toISOString().replace(/\.\d{3}Z$/, '.000Z')
  const redeemEnd = new Date(Date.now() + 7200_000).toISOString().replace(/\.\d{3}Z$/, '.000Z')
  const activity = await api('POST', '/merchant/activities', {
    shopId: String(shop), title: `load-${suffix}`, payValue: '1900', actualValue: '2500',
    totalStock: stock, beginTime: begin, endTime: end, redeemBeginTime: begin, redeemEndTime: redeemEnd,
  }, owner, { 'Idempotency-Key': `load-${suffix}` })
  check(activity.status === 200 && activity.body?.success, 'the merchant can publish the load activity')
  const voucherId = activity.body.data.id
  let published = null
  for (let attempt = 0; attempt < 40; attempt++) {
    const reply = await api('GET', '/merchant/activities?current=1&pageSize=50', undefined, owner)
    published = (reply.body?.data || []).find(item => item.id === voucherId)
    if (published?.publishStatus === 'ACTIVE' && published.ready) break
    await new Promise(wait => setTimeout(wait, 250))
  }
  report.published = published
  check(published?.publishStatus === 'ACTIVE' && published?.ready === true, 'the activity is ACTIVE with a ready gate')
  const sessions = []
  for (const phone of phones) sessions.push(await login(phone))
  const results = await Promise.all(sessions.map(session =>
    api('POST', `/voucher-order/seckill/${voucherId}`, undefined, session)))
  // The API reports a business rejection as HTTP 200 with success=false and an
  // error message, which is what the web client checks.
  const accepted = results.filter(item => item.status === 200 && item.body?.success === true &&
    typeof item.body?.data === 'string')
  const rejected = results.filter(item => !(item.status === 200 && item.body?.success === true))
  report.accepted = accepted.length
  report.rejected = rejected.length
  report.samples = rejected.slice(0, 3)
    .map(item => ({ status: item.status, errorMsg: item.body?.errorMsg }))
  check(accepted.length === stock, `exactly ${stock} of ${users} requests may be accepted (got ${accepted.length})`)
  check(rejected.length === users - stock, 'every other request is rejected instead of queued')
  let orders = 0
  for (let attempt = 0; attempt < 60; attempt++) {
    orders = Number(sql(`SELECT COUNT(*) FROM tb_voucher_order WHERE voucher_id=${voucherId}`) || 0)
    if (orders === stock) break
    await new Promise(wait => setTimeout(wait, 250))
  }
  check(orders === stock, `the durable orders must match the accepted count (${orders})`)
  const remaining = Number(sql(`SELECT stock FROM tb_seckill_voucher WHERE voucher_id=${voucherId}`) || 0)
  check(remaining === 0, `the database stock must be zero (${remaining})`)
  const redisStock = Number(redis('GET', `seckill:stock:${voucherId}`) || 0)
  check(redisStock === 0, `the Redis stock must be zero (${redisStock})`)
  const acceptedRows = Number(redis('HLEN', `seckill:accepted:${voucherId}`) || 0)
  check(acceptedRows === stock, `the Redis acceptance map must hold ${stock} rows (${acceptedRows})`)
  check(new Set(accepted.map(item => item.body.data)).size === stock, 'every accepted request owns a different order id')
  report.voucherId = voucherId
  report.orders = orders
  report.redisStock = redisStock
  report.passed = true
} catch (error) {
  report.error = String(error.message)
} finally {
  const list = userIDs.length ? userIDs.join(',') : '0'
  for (const statement of [
    `DELETE FROM tb_outbox WHERE aggregate_type='ORDER' AND aggregate_id IN (SELECT CAST(id AS CHAR) FROM tb_voucher_order WHERE user_id IN (${list}))`,
    `DELETE FROM tb_voucher_order WHERE user_id IN (${list})`,
    `DELETE FROM tb_audit_log WHERE operator_user_id IN (${list})`,
    `DELETE FROM tb_seckill_voucher WHERE voucher_id IN (SELECT id FROM tb_voucher WHERE shop_id IN (SELECT id FROM tb_shop WHERE merchant_id IN (SELECT id FROM tb_merchant WHERE name LIKE 'load-${suffix}%')))`,
    `DELETE FROM tb_voucher WHERE shop_id IN (SELECT id FROM tb_shop WHERE merchant_id IN (SELECT id FROM tb_merchant WHERE name LIKE 'load-${suffix}%'))`,
    `DELETE FROM tb_merchant_member WHERE user_id IN (${list},${ids.merchantUser || 0})`,
    `DELETE FROM tb_shop WHERE merchant_id IN (SELECT id FROM tb_merchant WHERE name LIKE 'load-${suffix}%')`,
    `DELETE FROM tb_merchant WHERE name LIKE 'load-${suffix}%'`,
    `DELETE FROM tb_user WHERE id IN (${list}) OR phone='${merchantPhone}'`,
  ]) {
    try { sql(statement) } catch { /* cleanup stays best effort */ }
  }
}
await writeFile(output, JSON.stringify(report, null, 2) + '\n', 'utf8')
console.log(JSON.stringify({ passed: report.passed, accepted: report.accepted, rejected: report.rejected, checks: report.checks.length, error: report.error || null }))
process.exit(report.passed ? 0 : 1)
