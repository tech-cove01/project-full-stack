// Real Vue -> Go lifecycle over orders committed by the real Stream worker.
// Sensitive credential values are never logged and every screenshot is masked.
// SQL only moves owned deadlines as explicitly reported clock fixtures.
import assert from 'node:assert/strict'
import { randomBytes, randomInt } from 'node:crypto'
import { spawn, spawnSync } from 'node:child_process'
import { mkdir, writeFile } from 'node:fs/promises'
import { createConnection } from 'node:net'
import { dirname, resolve } from 'node:path'

for (const key of ['MYSQL_HOST', 'MYSQL_PORT', 'MYSQL_DATABASE', 'MYSQL_USER', 'MYSQL_PASSWORD', 'REDIS_HOST', 'REDIS_PORT']) {
  if (!process.env[key]) throw new Error(`Explicit isolated ${key} is required`)
}
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const WEB = process.env.WEB_BASE || 'http://127.0.0.1:5174'
const API = process.env.API_BASE || 'http://127.0.0.1:8082'
const output = resolve(process.env.UI_EVIDENCE || 'go-order-lifecycle-ui-result.json')
await mkdir(dirname(output), { recursive: true })
const prefix = `GOL${randomBytes(4).toString('hex')}`
const base = 9007199254740993n + BigInt(randomInt(40000000, 50000000)) * 1000n
const id = n => String(base + BigInt(n))
const merchant = id(0), shop = id(0), member = id(0)
const users = Array.from({ length: 3 }, (_, index) => id(index))
const phones = new Set()
while (phones.size < users.length) phones.add(`139${randomInt(10000000, 100000000)}`)
const phoneList = [...phones]
const sessions = new Set(), activityIDs = new Set(), orderIDs = new Set()
const mysqlLocks = new Set(), pendingPayments = new Set()
const uint64Max = 18446744073709551615n, orderMax = 9223372036854775807n
function canonicalID(value, maximum = uint64Max) {
  return typeof value === 'string' && /^[1-9]\d*$/.test(value) && BigInt(value) <= maximum
}
function canonicalSource(value) {
  return typeof value === 'string' && /^(?:0|[1-9]\d*)-(?:0|[1-9]\d*)$/.test(value)
    && value.split('-').every(part => BigInt(part) <= uint64Max)
}
const report = {
  task: 'GO-031d', passed: false, checks: [], cleanup: false,
  browserTimeZone: process.env.BROWSER_TIMEZONE || 'UTC',
  fixtureBoundary: {
    realWorker: 'All orders use real API admission and the real Stream worker.',
    deadlines: 'Only owned committed orders have SQL deadlines moved as explicit expiry clock fixtures.',
    credentials: 'Credential values, codes, phones and headers are never logged; screenshots mask sensitive elements.',
    restart: 'Process restart scanning is verified separately by root, not this browser script.',
  },
}
function check(value, description) {
  assert.ok(value, description)
  report.checks.push(description)
}
function sql(statement) {
  const result = spawnSync(process.env.MYSQL_BIN || 'mysql', [
    '--protocol=TCP', `--host=${process.env.MYSQL_HOST}`, `--port=${process.env.MYSQL_PORT}`,
    `--user=${process.env.MYSQL_USER}`, '--default-character-set=utf8mb4', '--batch', '--skip-column-names', process.env.MYSQL_DATABASE,
  ], {
    input: `SET NAMES utf8mb4; SET time_zone='+08:00'; ${statement}`,
    encoding: 'utf8', env: { ...process.env, MYSQL_PWD: process.env.MYSQL_PASSWORD },
  })
  if (result.error || result.status !== 0) throw new Error('Isolated fixture SQL failed; SQL and credential diagnostics omitted')
  return result.stdout.trim()
}
// Keep the row lock in a separate real mysql session. Output is consumed only
// for a fixed readiness marker; SQL, stderr and credentials never escape.
async function inventoryLock(voucher) {
  if (!canonicalID(voucher) || !activityIDs.has(voucher)) throw new Error('Owned lock binding is invalid')
  const child = spawn(process.env.MYSQL_BIN || 'mysql', [
    '--protocol=TCP', `--host=${process.env.MYSQL_HOST}`, `--port=${process.env.MYSQL_PORT}`,
    `--user=${process.env.MYSQL_USER}`, '--default-character-set=utf8mb4', '--batch', '--skip-column-names', '--unbuffered', process.env.MYSQL_DATABASE,
  ], { env: { ...process.env, MYSQL_PWD: process.env.MYSQL_PASSWORD }, stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true })
  let closed = false, released = false, readyBuffer = ''
  const completion = new Promise(resolveExit => {
    child.once('error', () => { closed = true; resolveExit(false) })
    child.once('close', code => { closed = true; resolveExit(code === 0) })
  })
  child.stderr.on('data', () => {})
  child.stdin.on('error', () => {})
  const handle = {
    async release() {
      if (!released) {
        released = true
        if (!closed) child.stdin.end('COMMIT;\n')
      }
      let timer
      const exited = await Promise.race([completion, new Promise(resolveWait => { timer = setTimeout(() => resolveWait(null), 4000) })])
      clearTimeout(timer)
      if (exited === null) {
        child.kill()
        await completion
      }
      mysqlLocks.delete(handle)
      if (exited !== true) throw new Error('Owned lock helper did not exit cleanly')
    },
  }
  mysqlLocks.add(handle)
  try {
    await new Promise((resolveReady, rejectReady) => {
      const timer = setTimeout(() => rejectReady(new Error('Owned lock readiness timed out')), 4000)
      completion.then(() => { clearTimeout(timer); rejectReady(new Error('Owned lock exited before readiness')) })
      child.stdout.on('data', chunk => {
        readyBuffer = (readyBuffer + chunk.toString()).slice(-512)
        if (readyBuffer.includes('GO031_OWNED_LOCK_READY')) { clearTimeout(timer); resolveReady() }
      })
      child.stdin.write(`SET NAMES utf8mb4; SET time_zone='+08:00'; START TRANSACTION; SELECT voucher_id FROM tb_seckill_voucher WHERE voucher_id=${voucher} FOR UPDATE; SELECT 'GO031_OWNED_LOCK_READY';\n`)
    })
    return handle
  } catch {
    await handle.release().catch(() => {})
    throw new Error('Owned lock fixture could not be prepared')
  }
}
// Incremental RESP2 parser including nested arrays for XRANGE and XPENDING.
// Neither Redis errors nor command arguments are included in diagnostics.
async function redis(...args) {
  const commands = []
  if (process.env.REDIS_PASSWORD) commands.push(['AUTH', process.env.REDIS_PASSWORD])
  if (process.env.REDIS_DATABASE) commands.push(['SELECT', process.env.REDIS_DATABASE])
  commands.push(args)
  return await new Promise((resolveReply, reject) => {
    const socket = createConnection({ host: process.env.REDIS_HOST, port: Number(process.env.REDIS_PORT) })
    let buffer = Buffer.alloc(0), replies = 0, settled = false
    const fail = () => {
      if (settled) return
      settled = true; socket.destroy(); reject(new Error('Isolated fixture Redis command failed'))
    }
    function parse(offset) {
      if (offset >= buffer.length) return null
      const end = buffer.indexOf('\r\n', offset)
      if (end < 0) return null
      const type = buffer[offset], line = buffer.subarray(offset + 1, end).toString()
      let next = end + 2
      if (type === 45) throw new Error('Redis reply error')
      if (type === 43) return { value: line, next }
      if (type === 58) return { value: Number(line), next }
      if (type === 36) {
        const length = Number(line)
        if (!Number.isSafeInteger(length) || length < -1) throw new Error('Redis reply shape')
        if (length === -1) return { value: null, next }
        if (buffer.length < next + length + 2) return null
        return { value: buffer.subarray(next, next + length).toString(), next: next + length + 2 }
      }
      if (type === 42) {
        const count = Number(line)
        if (!Number.isSafeInteger(count) || count < -1) throw new Error('Redis reply shape')
        if (count === -1) return { value: null, next }
        const value = []
        for (let index = 0; index < count; index++) {
          const item = parse(next)
          if (!item) return null
          value.push(item.value); next = item.next
        }
        return { value, next }
      }
      throw new Error('Redis reply shape')
    }
    socket.setTimeout(5000, fail).on('error', fail)
    socket.on('close', () => { if (!settled) fail() })
    socket.on('connect', () => socket.write(commands.map(command => `*${command.length}\r\n${command.map(arg => {
      const text = String(arg); return `$${Buffer.byteLength(text)}\r\n${text}\r\n`
    }).join('')}`).join('')))
    socket.on('data', chunk => {
      buffer = Buffer.concat([buffer, chunk])
      try {
        while (buffer.length) {
          const reply = parse(0)
          if (!reply) return
          buffer = buffer.subarray(reply.next)
          if (++replies === commands.length) {
            settled = true; socket.destroy(); resolveReply(reply.value); return
          }
        }
      } catch { fail() }
    })
  })
}
const pairs = values => Object.fromEntries(Array.from({ length: values.length / 2 }, (_, index) => [values[index * 2], values[index * 2 + 1]]))
async function api(path, token, method = 'GET', body, key) {
  const response = await fetch(`${API}${path}`, {
    method, signal: AbortSignal.timeout(20000), headers: {
      ...(token ? { authorization: token } : {}), ...(body ? { 'Content-Type': 'application/json' } : {}),
      ...(key ? { 'Idempotency-Key': key } : {}),
    }, ...(body ? { body: JSON.stringify(body) } : {}),
  })
  let result
  try { result = await response.json() } catch { throw new Error('Go API returned an unexpected response shape') }
  return { status: response.status, body: result }
}
async function eventually(read, accept, description, timeout = 30000) {
  const deadline = Date.now() + timeout
  while (Date.now() < deadline) {
    const result = await read()
    if (accept(result)) return result
    await new Promise(resolveWait => setTimeout(resolveWait, 150))
  }
  throw new Error(description)
}
async function mapLimit(items, limit, perform) {
  let next = 0, failed = false
  const results = new Array(items.length)
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, async () => {
    while (next < items.length) {
      const index = next++
      try { results[index] = await perform(items[index], index) } catch { failed = true }
    }
  }))
  if (failed) throw new Error('Parallel API setup or verification failed; credential diagnostics omitted')
  return results
}
async function loginAPI(index) {
  const sent = await api(`/user/code?phone=${encodeURIComponent(phoneList[index])}`, undefined, 'POST')
  if (!sent.body.success || !/^\d{6}$/.test(sent.body.data?.code || '')) throw new Error('Isolated development SMS login is unavailable')
  const result = await api('/user/login', undefined, 'POST', { phone: phoneList[index], code: sent.body.data.code })
  const token = result.body.data
  if (!result.body.success || typeof token !== 'string' || !/^[a-f0-9]{32}$/.test(token)) throw new Error('Real API fixture login failed')
  sessions.add(token)
  return token
}
const formatter = new Intl.DateTimeFormat('en-CA', {
  timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23',
})
function businessTime(offset) {
  const p = Object.fromEntries(formatter.formatToParts(Date.now() + offset).map(part => [part.type, part.value]))
  return `${p.year}-${p.month}-${p.day}T${p.hour}:${p.minute}:${p.second}+08:00`
}
async function publish(owner, name, stock, amount = '990', actual = '2000') {
  const result = await api('/merchant/activities', owner, 'POST', {
    shopId: shop, title: `${prefix} ${name}`, subTitle: null, rules: 'Go真实受理与成单验证',
    payValue: amount, actualValue: actual, totalStock: stock,
    beginTime: businessTime(-60000), endTime: businessTime(86400000),
    redeemBeginTime: businessTime(-60000), redeemEndTime: businessTime(172800000),
  }, `${prefix}-${name}`)
  if (result.status !== 200 || !result.body.success || typeof result.body.data?.id !== 'string') throw new Error('Real owner publication failed')
  const voucher = result.body.data.id
  activityIDs.add(voucher)
  await eventually(() => api(`/merchant/activities?shopId=${shop}&pageSize=50`, owner), value => value.body.data?.some(item => item.id === voucher && item.ready), 'Activity worker did not open the owned activity')
  return voucher
}
async function ownedMessages() {
  const rows = []
  let start = '-'
  while (true) {
    const batch = await redis('XRANGE', 'stream.orders', start, '+', 'COUNT', 1000)
    if (!Array.isArray(batch)) throw new Error('Owned Stream inspection failed')
    for (const [sourceID, values] of batch) {
      const fields = pairs(values)
      if (activityIDs.has(fields.voucherId)) {
        if (!canonicalSource(sourceID) || !canonicalID(fields.id, orderMax) || !users.includes(fields.userId) || !canonicalID(fields.voucherId)) {
          throw new Error('Owned Stream binding requires manual inspection')
        }
        rows.push({ id: fields.id, userId: fields.userId, voucherId: fields.voucherId, sourceID })
      }
    }
    if (batch.length < 1000) return rows
    start = `(${batch.at(-1)[0]}`
  }
}
async function waitCreated(token, orderID) {
  return await eventually(() => api(`/voucher-order/${orderID}`, token), result => result.status === 200 && result.body.data?.state === 'CREATED' && result.body.data.orderStatus === 1, 'Accepted order did not reach committed CREATED/status 1', 60000)
}
let browser, mainPage, seeded = false, releaseStale
const browserErrors = []
async function screenshot(page, suffix) {
  await page.screenshot({
    path: output.replace(/\.json$/, '') + suffix + '.png', fullPage: true, animations: 'disabled',
    mask: [page.locator('[data-sensitive]'), page.locator('.redeem-token'), page.locator('.redeem-token-value')],
  })
}
try {
  report.step = 'Go identity and owned fixtures'
  const health = await api('/actuator/health')
  const proxy = await (await fetch(`${WEB}/api/actuator/health`)).json()
  check(health.status === 200 && health.body.runtime === 'go' && proxy.runtime === 'go' && proxy.version === health.body.version, 'direct and proxied requests identify the same Go runtime')
  report.service = proxy
  sql(`START TRANSACTION;
    INSERT INTO tb_user(id,phone,nick_name) VALUES ${users.map((user, index) => `(${user},'${phoneList[index]}','${prefix} 用户${index}')`).join(',')};
    INSERT INTO tb_merchant(id,name) VALUES(${merchant},'${prefix} 商家');
    INSERT INTO tb_shop(id,merchant_id,name,type_id,address,x,y) VALUES(${shop},${merchant},'${prefix} 门店',1,'生命周期隔离地址',120,30);
    INSERT INTO tb_merchant_member(id,merchant_id,user_id,role_code) VALUES(${member},${merchant},${users[0]},'OWNER'); COMMIT;`)
  seeded = true
  const owner = await loginAPI(0)
  browser = await chromium.launch({ ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}), headless: true })
  async function loginUI(index, destination) {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1080 }, timezoneId: report.browserTimeZone, permissions: ['clipboard-read', 'clipboard-write'] })
    const page = await context.newPage()
    page.on('pageerror', () => browserErrors.push('uncaught browser error'))
    await page.goto(`${WEB}/login?redirect=${encodeURIComponent(destination)}`)
    await page.getByPlaceholder('请输入手机号').fill(phoneList[index])
    await page.getByRole('button', { name: '获取验证码', exact: true }).click()
    await page.waitForFunction(() => /^\d{6}$/.test(document.querySelector('input[placeholder="6 位验证码"]')?.value || ''))
    await page.getByRole('button', { name: '登录并继续', exact: true }).click()
    await page.waitForURL(url => url.pathname === destination)
    const token = await page.evaluate(() => localStorage.getItem('cityloop.token'))
    if (typeof token !== 'string' || !/^[a-f0-9]{32}$/.test(token)) throw new Error('Real Vue login failed')
    sessions.add(token)
    return { page, token }
  }
  const buyer = await loginUI(1, '/orders')
  const { page, token } = buyer
  mainPage = page
  check(true, 'buyer uses a real Vue login and server session')
  const fixtures = {}
  for (const name of ['paid', 'pay-race', 'cancelled', 'expired', 'lost-payment', 'late-query', 'delayed-payment']) {
    const voucher = await publish(owner, name, 10)
    const admitted = await api(`/voucher-order/seckill/${voucher}`, token, 'POST')
    if (!admitted.body.success || !canonicalID(admitted.body.data, orderMax)) throw new Error('Owned lifecycle admission failed')
    orderIDs.add(admitted.body.data)
    await waitCreated(token, admitted.body.data)
    fixtures[name] = { voucher, order: admitted.body.data }
  }
  check(sql(`SELECT COUNT(*) FROM tb_voucher_order WHERE user_id=${users[1]}`) === '7', 'all seven lifecycle orders were committed by real admission Stream and DB creation')
  const state = status => page.locator(`.order-layout[data-order-status="${status}"]`)
  async function open(order, status = 1) {
    await page.goto(`${WEB}/orders/${order}`)
    await state(status).waitFor()
  }
  async function confirm(kind) {
    await page.getByRole('button', { name: kind === 'pay' ? '模拟支付' : '取消订单', exact: true }).click()
    const box = page.locator('.el-message-box')
    await box.waitFor()
    await box.getByRole('button', { name: kind === 'pay' ? '确认模拟支付' : '确定取消', exact: true }).click()
  }

  report.step = 'real Vue simulated payment and credential privacy'
  const paid = fixtures.paid
  await open(paid.order)
  check(await page.locator('.redeem-token').count() === 0 && await page.getByRole('button', { name: '模拟支付', exact: true }).isEnabled(), 'unpaid detail exposes simulation controls and no redemption credential')
  await confirm('pay')
  await state(2).waitFor()
  await page.locator('.redeem-token-value').waitFor()
  check(await page.getByText('模拟支付已确认成功；本次演示不涉及真实资金。', { exact: true }).count() === 1
    && await page.locator('.redeem-token-value').evaluate(node => /^[a-f0-9]{64}$/.test(node.textContent || '')), 'Vue confirms simulated payment with a 256-bit credential only on the paid owned detail')
  await page.getByRole('button', { name: '复制核销凭证', exact: true }).click()
  check(await page.evaluate(async () => (await navigator.clipboard.readText()) === document.querySelector('.redeem-token-value')?.textContent), 'credential copy succeeds without logging or reporting its value')
  await page.reload()
  await state(2).waitFor()
  check(await page.locator('.redeem-token-value').count() === 1 && await page.getByRole('button', { name: '取消订单', exact: true }).count() === 0, 'refresh preserves paid state and owner-only credential while removing unpaid actions')
  await screenshot(page, '-paid-masked')
  await page.route(`**/api/voucher-order/${paid.order}`, async route => { await route.abort('failed').catch(() => {}) })
  await page.getByRole('button', { name: '重新查询', exact: true }).click()
  await page.getByRole('heading', { name: '暂时无法确认订单结果', exact: true }).waitFor()
  check(await page.locator('.redeem-token').count() === 0 && await page.locator('.order-meta').count() === 0, 'a query error immediately removes the paid credential and stale order snapshot')
  await page.unroute(`**/api/voucher-order/${paid.order}`)
  await page.getByRole('button', { name: '重新查询', exact: true }).click()
  await state(2).waitFor()
  await page.locator('.order-actions').getByRole('link', { name: '我的订单', exact: true }).click()
  await page.getByRole('heading', { name: '我的订单', exact: true }).waitFor()
  check(await page.locator('.redeem-token').count() === 0
    && (await api('/voucher-order?pageSize=50', token)).body.data.every(order => !('redeemToken' in order)), 'leaving detail clears the credential and the real owner list never includes it')
  const other = await loginUI(2, `/orders/${paid.order}`)
  await other.page.getByRole('heading', { name: '无法查看此订单', exact: true }).waitFor()
  const forbidden = await api(`/voucher-order/${paid.order}`, other.token)
  check(forbidden.status === 404 && !forbidden.body.data && await other.page.locator('.redeem-token').count() === 0, 'another real account receives 404 and no paid credential')
  check((await api(`/voucher-order/${paid.order}/pay`, other.token, 'POST')).status === 404
    && (await api(`/voucher-order/${paid.order}/cancel`, other.token, 'POST')).status === 404, 'other accounts cannot pay or cancel this owned order')

  report.step = 'twenty concurrent payment transitions'
  const raced = fixtures['pay-race']
  const payments = await Promise.allSettled(Array.from({ length: 20 }, () => api(`/voucher-order/${raced.order}/pay`, token, 'POST')))
  check(payments.every(value => value.status === 'fulfilled' && value.value.status === 200 && value.value.body.data?.orderStatus === 2), 'twenty concurrent real payments return the same paid business state')
  check(sql(`SELECT COUNT(DISTINCT redeem_token) FROM tb_voucher_order WHERE id=${raced.order} AND status=2 AND version=1 AND LENGTH(redeem_token)=64`) === '1'
    && sql(`SELECT COUNT(*) FROM tb_audit_log WHERE resource_id='${raced.order}' AND action='ORDER_PAID'`) === '1'
    && sql(`SELECT COUNT(*) FROM tb_outbox WHERE aggregate_type='ORDER' AND aggregate_id='${raced.order}' AND aggregate_version=1 AND event_type='ORDER_CHANGED'`) === '1'
    && sql(`SELECT stock FROM tb_seckill_voucher WHERE voucher_id=${raced.voucher}`) === '9', 'concurrent payment commits one version audit and outbox transition and never changes inventory')
  check((await api(`/voucher-order/${paid.order}/cancel`, token, 'POST')).status === 409, 'cancel on a paid order is a conflict with no reverse transition')

  report.step = 'real Vue cancel and safe repeated operations'
  const cancelled = fixtures.cancelled
  await open(cancelled.order)
  await confirm('cancel')
  await state(4).waitFor()
  await page.getByText('订单已取消，稍后查看活动。', { exact: true }).waitFor()
  check(await page.getByText('订单已取消，稍后查看活动。', { exact: true }).count() === 1
    && await page.getByText('取消原因：用户主动取消', { exact: true }).count() === 1
    && await page.locator('.order-meta').getByText('取消时间：', { exact: false }).count() === 1
    && await page.locator('.redeem-token').count() === 0, 'Vue cancellation displays current cancellation time reason and no redemption credential')
  const cancellations = await Promise.allSettled(Array.from({ length: 20 }, () => api(`/voucher-order/${cancelled.order}/cancel?reason=USER_CANCEL`, token, 'POST')))
  check(cancellations.every(value => value.status === 'fulfilled' && value.value.status === 200 && value.value.body.data?.orderStatus === 4)
    && sql(`SELECT version FROM tb_voucher_order WHERE id=${cancelled.order}`) === '1'
    && sql(`SELECT stock FROM tb_seckill_voucher WHERE voucher_id=${cancelled.voucher}`) === '10', 'repeated cancellation stays at one version and restores DB inventory only once')
  check((await api(`/voucher-order/${cancelled.order}/pay`, token, 'POST')).status === 409, 'payment on the old cancelled order remains a conflict')
  await eventually(async () => await redis('GET', `seckill:stock:${cancelled.voucher}`) === '10'
    && await redis('SISMEMBER', `seckill:order:${cancelled.voucher}`, users[1]) === 0
    && await redis('HGET', `seckill:accepted:${cancelled.voucher}`, users[1]) === null,
  Boolean, 'durable cancellation compensation did not restore stock and release the old reservation')
  const repurchased = await api(`/voucher-order/seckill/${cancelled.voucher}`, token, 'POST')
  check(repurchased.body.success && canonicalID(repurchased.body.data, orderMax)
    && repurchased.body.data !== cancelled.order, 'after compensation the same buyer can receive a new order ID')
  orderIDs.add(repurchased.body.data)
  await waitCreated(token, repurchased.body.data)
  await screenshot(page, '-cancelled')

  report.step = 'deadline clock fixture and DB scanner without Redis reminders'
  const expired = fixtures.expired
  await open(expired.order)
  sql(`UPDATE tb_voucher_order SET pay_deadline_at=DATE_SUB(NOW(),INTERVAL 1 SECOND) WHERE id=${expired.order} AND user_id=${users[1]};`)
  const latePay = await api(`/voucher-order/${expired.order}/pay`, token, 'POST')
  check(latePay.status === 409 && sql(`SELECT COUNT(*) FROM tb_audit_log WHERE resource_id='${expired.order}' AND action='ORDER_PAID'`) === '0', 'a real payment after the controlled DB deadline is rejected regardless of browser time')
  await eventually(() => sql(`SELECT status FROM tb_voucher_order WHERE id=${expired.order}`), value => value === '4', 'DB scanner did not close the expired owned order', 30000)
  await state(4).waitFor({ timeout: 30000 })
  check(sql(`SELECT stock FROM tb_seckill_voucher WHERE voucher_id=${expired.voucher}`) === '10'
    && await page.locator('.redeem-token').count() === 0, 'bounded unpaid-page polling observes automatic DB closure without a Redis ZSet reminder')
  report.timeoutReminder = 'No ZSet reminder is read or written by this implementation; no guessed key was removed.'
  await screenshot(page, '-expired')

  report.step = 'lost payment response is reconciled by an owned read'
  const lost = fixtures['lost-payment']
  await open(lost.order)
  let writes = 0, dropped = false, interceptFailed = false
  await page.route(`**/api/voucher-order/${lost.order}/pay`, async route => {
    try {
      writes += 1
      const result = await route.fetch({ timeout: 15000 })
      const body = await result.json()
      if (result.status() !== 200 || body.data?.orderStatus !== 2) throw new Error('Payment interception failed')
      dropped = true
      await route.abort('failed')
    } catch {
      interceptFailed = true
      await route.abort('failed').catch(() => {})
    }
  })
  await confirm('pay')
  await eventually(async () => dropped || interceptFailed, Boolean, 'Payment response-loss route did not complete')
  if (interceptFailed) throw new Error('Real payment interception failed; request diagnostics omitted')
  await state(2).waitFor()
  await page.locator('.redeem-token-value').waitFor()
  check(writes === 1 && sql(`SELECT COUNT(*) FROM tb_audit_log WHERE resource_id='${lost.order}' AND action='ORDER_PAID'`) === '1', 'a lost committed payment response is confirmed by GET without automatically repeating a write')
  await page.unroute(`**/api/voucher-order/${lost.order}/pay`)
  await screenshot(page, '-lost-payment-masked')

  report.step = 'late unpaid response cannot overwrite a completed payment'
  const late = fixtures['late-query']
  await open(late.order)
  let captured = false, staleFailed = false
  const staleGate = new Promise(resolveGate => { releaseStale = resolveGate })
  await page.route(`**/api/voucher-order/${late.order}`, async route => {
    try {
      if (captured) return await route.continue()
      const result = await route.fetch({ timeout: 15000 })
      const body = await result.json()
      if (body.data?.orderStatus !== 1) throw new Error('Unpaid query fixture failed')
      captured = true
      await staleGate
      await route.fulfill({ response: result }).catch(() => {})
    } catch {
      staleFailed = true
      await route.abort('failed').catch(() => {})
    }
  })
  await eventually(async () => captured || staleFailed, Boolean, 'Unpaid poll was not captured')
  if (staleFailed) throw new Error('Stale query fixture failed; request diagnostics omitted')
  await confirm('pay')
  await state(2).waitFor()
  releaseStale()
  await new Promise(resolveWait => setTimeout(resolveWait, 250))
  check(await state(2).count() === 1 && await page.locator('.redeem-token-value').count() === 1, 'aborting and fencing an old unpaid poll prevents it from replacing a newer paid view')
  await page.unroute(`**/api/voucher-order/${late.order}`)
  await screenshot(page, '-late-query-masked')

  report.step = 'delayed real payment confirmation after proxy loss and an initial unpaid read'
  const delayed = fixtures['delayed-payment']
  await open(delayed.order)
  const lock = await inventoryLock(delayed.voucher)
  let proxyWrites = 0, forwarded
  await page.route(`**/api/voucher-order/${delayed.order}/pay`, async route => {
    try {
      proxyWrites += 1
      if (!forwarded) {
        // This independent server request survives aborting the browser side.
        // Convert both outcomes to values so finally can always settle it.
        forwarded = api(`/voucher-order/${delayed.order}/pay`, token, 'POST')
          .then(result => ({ ok: true, result }), () => ({ ok: false }))
        pendingPayments.add(forwarded)
      }
      await route.abort('failed')
    } catch { await route.abort('failed').catch(() => {}) }
  })
  await confirm('pay')
  await page.getByText('尚未确认上次操作结果，请先点击“重新查询”，再决定是否继续操作。', { exact: true }).waitFor({ timeout: 3000 })
  check(sql(`SELECT status FROM tb_voucher_order WHERE id=${delayed.order} AND user_id=${users[1]}`) === '1'
    && await state(1).count() === 1 && await page.locator('.redeem-token').count() === 0,
    'a real lock-held payment loses its proxy response and the first owned GET still reads genuine unpaid state')
  await lock.release()
  const committed = await forwarded
  pendingPayments.delete(forwarded)
  check(committed?.ok && committed.result.status === 200 && committed.result.body.data?.orderStatus === 2,
    'releasing the real inventory lock allows the independently forwarded payment to commit')
  await state(2).waitFor({ timeout: 10000 })
  check(proxyWrites === 1 && await page.locator('.redeem-token-value').count() === 1
    && await page.getByText('模拟支付已确认成功；本次演示不涉及真实资金。', { exact: true }).count() === 1
    && await page.getByText('尚未确认上次操作结果，请先点击“重新查询”，再决定是否继续操作。', { exact: true }).count() === 0,
    'a later real owned GET clears the prior payment error and uncertainty notice and reveals the credential without another POST')
  await page.unroute(`**/api/voucher-order/${delayed.order}/pay`)
  await screenshot(page, '-delayed-payment-masked')
  check(browserErrors.length === 0, 'lifecycle browser scenarios finish without uncaught errors')
  report.passed = true
} catch (error) {
  // Discard arbitrary driver/browser error text. Credential-bearing responses
  // and request diagnostics must never enter a report or terminal output.
  report.failure = 'Lifecycle UI verification failed; inspect the safe step and masked screenshot.'
  if (mainPage && !new URL(mainPage.url()).pathname.startsWith('/login')) await screenshot(mainPage, '-failure-masked').catch(() => {})
  process.exitCode = 1
} finally {
  releaseStale?.()
  if (browser) await browser.close().catch(() => {})
  for (const lock of [...mysqlLocks]) await lock.release().catch(() => {})
  await Promise.all([...pendingPayments])
  pendingPayments.clear()
  report.forcedCleanup = !report.passed
  let clean = true
  for (const token of sessions) {
    try { const result = await api('/user/logout', token, 'POST'); if (result.status !== 200 && result.status !== 401) clean = false } catch { clean = false }
  }
  if (seeded) {
    try {
      for (const voucher of sql(`SELECT id FROM tb_voucher WHERE shop_id=${shop}`).split(/\s+/).filter(Boolean)) activityIDs.add(voucher)
      const messages = await ownedMessages()
      for (const message of messages) orderIDs.add(message.id)
      for (const voucher of activityIDs) {
        const values = await redis('HGETALL', `seckill:accepted:${voucher}`)
        for (let index = 0; index < values.length; index += 2) {
          if (!users.includes(values[index]) || !canonicalID(values[index + 1], orderMax)) throw new Error('Accepted binding requires manual cleanup')
          orderIDs.add(values[index + 1])
        }
      }
      const voucherList = [...activityIDs]
      if (!voucherList.every(value => canonicalID(value))) throw new Error('Catalog identifiers require manual cleanup')
      for (const orderID of sql(`SELECT id FROM tb_voucher_order WHERE user_id IN (${users.join(',')}) AND voucher_id IN (${voucherList.length ? voucherList.join(',') : '0'})`).split(/\s+/).filter(Boolean)) orderIDs.add(orderID)
      if (report.passed) {
        await eventually(async () => await mapLimit(messages, 10, message => redis('XPENDING', 'stream.orders', 'g1', message.sourceID, message.sourceID, 1)), rows => rows.every(row => row.length === 0), 'Owned worker messages did not drain')
        report.workerACK = { ownedMessages: messages.length, pendingBeforeCleanup: 0 }
      }
      const vouchers = [...activityIDs]
      const orders = [...orderIDs]
      if (!orders.every(value => canonicalID(value, orderMax))) throw new Error('Order identifiers require manual cleanup')
      const voucherSQL = vouchers.length ? vouchers.join(',') : '0'
      const orderSQL = orders.length ? orders.join(',') : '0'
      const orderStrings = orders.length ? orders.map(value => `'${value}'`).join(',') : "''"
      // Never let a corrupted cache/message authorize deleting an unrelated
      // order or outbox record even if its ID was returned by an owned index.
      if (sql(`SELECT (SELECT COUNT(*) FROM tb_voucher WHERE id IN (${voucherSQL}) AND shop_id<>${shop})
        +(SELECT COUNT(*) FROM tb_seckill_voucher WHERE voucher_id IN (${voucherSQL}) AND merchant_id<>${merchant})
        +(SELECT COUNT(*) FROM tb_voucher_order WHERE user_id IN (${users.join(',')}) AND voucher_id NOT IN (${voucherSQL}))
        +(SELECT COUNT(*) FROM tb_voucher_order WHERE voucher_id IN (${voucherSQL}) AND user_id NOT IN (${users.join(',')}))
        +(SELECT COUNT(*) FROM tb_voucher_order WHERE id IN (${orderSQL}) AND (user_id NOT IN (${users.join(',')}) OR voucher_id NOT IN (${voucherSQL})))
        +(SELECT COUNT(*) FROM tb_outbox WHERE aggregate_type IN ('ORDER','RESERVATION') AND aggregate_id IN (${orderStrings})
          AND (COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload,'$.userId')),'') NOT IN (${users.map(value => `'${value}'`).join(',')})
            OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload,'$.voucherId')),'') NOT IN (${vouchers.length ? vouchers.map(value => `'${value}'`).join(',') : "''"})))`) !== '0') {
        report.cleanupUnsafeBinding = true
        throw new Error('Owned cleanup binding requires manual inspection')
      }
      sql(`START TRANSACTION;
        SELECT id FROM tb_voucher WHERE shop_id=${shop} ORDER BY id FOR UPDATE;
        SELECT voucher_id FROM tb_seckill_voucher WHERE voucher_id IN (${vouchers.length ? vouchers.join(',') : '0'}) ORDER BY voucher_id FOR UPDATE;
        DELETE FROM tb_outbox WHERE (aggregate_type='ACTIVITY' AND aggregate_id IN (${vouchers.length ? vouchers.map(value => `'${value}'`).join(',') : "''"})) OR (aggregate_type IN ('ORDER','RESERVATION') AND aggregate_id IN (${orders.length ? orders.map(value => `'${value}'`).join(',') : "''"}));
        DELETE FROM tb_audit_log WHERE merchant_id=${merchant};
        DELETE FROM tb_voucher_order WHERE id IN (${orderSQL}) AND user_id IN (${users.join(',')}) AND voucher_id IN (${voucherSQL});
        DELETE FROM tb_seckill_voucher WHERE voucher_id IN (${vouchers.length ? vouchers.join(',') : '0'});
        DELETE FROM tb_voucher WHERE shop_id=${shop}; COMMIT;`)
      for (const message of messages) { await redis('XACK', 'stream.orders', 'g1', message.sourceID); await redis('XDEL', 'stream.orders', message.sourceID) }
      const keys = [...vouchers.flatMap(voucher => ['gate', 'stock', 'begin', 'end', 'order', 'accepted'].map(part => `seckill:${part}:${voucher}`)), ...orders.map(orderID => `order:reservation:${orderID}`)]
      for (let offset = 0; offset < keys.length; offset += 100) { const batch = keys.slice(offset, offset + 100); await redis('DEL', ...batch); if (await redis('EXISTS', ...batch) !== 0) clean = false }
      sql(`START TRANSACTION; DELETE FROM tb_member_shop_scope WHERE member_id=${member}; DELETE FROM tb_merchant_member WHERE id=${member}; DELETE FROM tb_shop WHERE id=${shop}; DELETE FROM tb_merchant WHERE id=${merchant}; DELETE FROM tb_user WHERE id IN (${users.join(',')}); COMMIT;`)
      clean &&= sql(`SELECT (SELECT COUNT(*) FROM tb_voucher_order WHERE user_id IN (${users.join(',')}))+(SELECT COUNT(*) FROM tb_voucher WHERE shop_id=${shop})+(SELECT COUNT(*) FROM tb_audit_log WHERE merchant_id=${merchant})+(SELECT COUNT(*) FROM tb_outbox WHERE aggregate_id IN (${[...orders, ...vouchers].map(value => `'${value}'`).join(',') || "''"}))`) === '0'
      clean &&= (await ownedMessages()).length === 0
      report.cleanupBoundary = { ownedRowsKeysAndMessages: clean ? 0 : 'verification failed', sharedStreamGroupAndDailyCounter: 'preserved' }
    } catch { clean = false }
  }
  report.cleanup = clean
  if (!clean) { report.passed = false; process.exitCode = 1 }
  report.recordedAt = new Date().toISOString()
  await writeFile(output, JSON.stringify(report, null, 2) + '\n')
  process.stdout.write(JSON.stringify({ passed: report.passed, checks: report.checks.length, cleanup: clean, output }) + '\n')
}
