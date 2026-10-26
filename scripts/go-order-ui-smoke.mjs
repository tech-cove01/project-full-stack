// Real Vue -> Go -> Redis Stream -> isolated MySQL. Short bursts prove
// correctness, not sustained throughput. Read-only SQL/query fixtures are
// explicitly separate from orders produced by the real worker.
import assert from 'node:assert/strict'
import { randomBytes, randomInt } from 'node:crypto'
import { spawnSync } from 'node:child_process'
import { mkdir, writeFile } from 'node:fs/promises'
import { createConnection } from 'node:net'
import { dirname, resolve } from 'node:path'

for (const key of ['MYSQL_HOST', 'MYSQL_PORT', 'MYSQL_DATABASE', 'MYSQL_USER', 'MYSQL_PASSWORD', 'REDIS_HOST', 'REDIS_PORT']) {
  if (!process.env[key]) throw new Error(`Explicit isolated ${key} is required`)
}
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const WEB = process.env.WEB_BASE || 'http://127.0.0.1:5174'
const API = process.env.API_BASE || 'http://127.0.0.1:8082'
const output = resolve(process.env.UI_EVIDENCE || 'go-order-ui-result.json')
await mkdir(dirname(output), { recursive: true })
const prefix = `GOO${randomBytes(4).toString('hex')}`
const base = 9007199254740993n + BigInt(randomInt(20000000, 30000000)) * 1000n
const id = n => String(base + BigInt(n))
const merchant = id(0), shop = id(0), member = id(0)
const users = Array.from({ length: 204 }, (_, index) => id(index))
const paginationVouchers = Array.from({ length: 12 }, (_, index) => id(1000 + index))
const paginationOrders = Array.from({ length: 12 }, (_, index) => id(1100 + index))
const pendingID = id(1200)
const phones = new Set()
while (phones.size < users.length) phones.add(`139${randomInt(10000000, 100000000)}`)
const phoneList = [...phones]
const sessions = new Set(), activityIDs = new Set(), orderIDs = new Set([pendingID, ...paginationOrders])
const report = {
  task: 'GO-030', passed: false, checks: [], cleanup: false,
  browserTimeZone: process.env.BROWSER_TIMEZONE || 'UTC',
  fixtureBoundary: {
    realWorker: 'All admitted orders in the single-user, 200-user and lost-response cases use the real Stream worker.',
    pagination: 'Twelve SQL orders with separate disabled vouchers and identical create_time are read-only pagination fixtures.',
    pending: 'One owned Redis reservation without a Stream message or DB order is a query-only PROCESSING fixture.',
    performance: 'Short-burst correctness only; no sustained throughput or capacity claim.',
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
      if (activityIDs.has(fields.voucherId)) rows.push({ sourceID, ...fields })
    }
    if (batch.length < 1000) return rows
    start = `(${batch.at(-1)[0]}`
  }
}
async function waitCreated(token, orderID) {
  return await eventually(() => api(`/voucher-order/${orderID}`, token), result => result.status === 200 && result.body.data?.state === 'CREATED' && result.body.data.orderStatus === 1, 'Accepted order did not reach committed CREATED/status 1', 60000)
}
let browser, mainPage, seeded = false
const pageErrors = []
try {
  report.step = 'service identity and isolated fixtures'
  const health = await api('/actuator/health')
  const proxy = await (await fetch(`${WEB}/api/actuator/health`)).json()
  check(health.status === 200 && health.body.runtime === 'go' && proxy.runtime === 'go' && proxy.version === health.body.version, 'direct and proxied requests identify the same Go service')
  report.service = proxy
  check((await api('/voucher-order')).status === 401, 'anonymous order list is 401')
  sql(`START TRANSACTION;
    INSERT INTO tb_user(id,phone,nick_name) VALUES ${users.map((user, index) => `(${user},'${phoneList[index]}','${prefix} 用户${index}')`).join(',')};
    INSERT INTO tb_merchant(id,name) VALUES(${merchant},'${prefix} 商家');
    INSERT INTO tb_shop(id,merchant_id,name,type_id,address,x,y) VALUES(${shop},${merchant},'${prefix} 门店',1,'GO-030隔离地址',120,30);
    INSERT INTO tb_merchant_member(id,merchant_id,user_id,role_code) VALUES(${member},${merchant},${users[0]},'OWNER'); COMMIT;`)
  seeded = true
  const owner = await loginAPI(0)
  const single = await publish(owner, 'single', 100)
  const burst = await publish(owner, 'burst', 100)
  const uiVoucher = await publish(owner, 'response-loss', 1, '9007199254740993', '9007199254740994')
  browser = await chromium.launch({ ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}), headless: true })
  async function loginUI(index, destination) {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1080 }, timezoneId: report.browserTimeZone })
    const page = await context.newPage()
    page.on('pageerror', () => pageErrors.push('uncaught browser error'))
    await page.goto(`${WEB}/login?redirect=${encodeURIComponent(destination)}`)
    await page.getByPlaceholder('请输入手机号').fill(phoneList[index])
    await page.getByRole('button', { name: '获取验证码', exact: true }).click()
    await page.waitForFunction(() => /^\d{6}$/.test(document.querySelector('input[placeholder="6 位验证码"]')?.value || ''))
    await page.getByRole('button', { name: '登录并继续', exact: true }).click()
    await page.waitForURL(url => url.pathname === destination)
    const token = await page.evaluate(() => localStorage.getItem('cityloop.token'))
    if (typeof token !== 'string' || !/^[a-f0-9]{32}$/.test(token)) throw new Error('Real Vue fixture login failed')
    sessions.add(token)
    return { page, token }
  }
  const buyer = await loginUI(1, `/shops/${shop}`)
  const { page, token } = buyer
  mainPage = page
  check(true, 'buyer logs in through real Vue with a server-created session')

  report.step = 'same buyer twenty concurrent requests'
  const duplicates = await Promise.allSettled(Array.from({ length: 20 }, () => api(`/voucher-order/seckill/${single}`, token, 'POST')))
  check(duplicates.every(result => result.status === 'fulfilled' && result.value.status === 200 && result.value.body.success && typeof result.value.body.data === 'string'), 'twenty concurrent requests from one real session all return accepted string IDs')
  const singleIDs = new Set(duplicates.map(result => result.value.body.data))
  check(singleIDs.size === 1, 'twenty retries return exactly one accepted order ID')
  const singleID = [...singleIDs][0]
  orderIDs.add(singleID)
  await waitCreated(token, singleID)
  check(sql(`SELECT COUNT(*) FROM tb_voucher_order WHERE voucher_id=${single}`) === '1'
    && sql(`SELECT stock FROM tb_seckill_voucher WHERE voucher_id=${single}`) === '99'
    && await redis('GET', `seckill:stock:${single}`) === '99', 'same-user retries create one real DB order and consume DB and Redis inventory exactly once')
  check(sql(`SELECT COUNT(*) FROM tb_audit_log WHERE action='ORDER_CREATE' AND resource_id='${singleID}'`) === '1'
    && sql(`SELECT COUNT(*) FROM tb_outbox WHERE aggregate_type='ORDER' AND aggregate_id='${singleID}' AND event_type='ORDER_CHANGED'`) === '1'
    && (await ownedMessages()).filter(item => item.voucherId === single).length === 1, 'same-user retries produce one creation audit one order outbox event and one Stream entry')

  report.step = '200 authenticated buyers compete for 100 inventory'
  const pool = await mapLimit(Array.from({ length: 200 }, (_, index) => index + 4), 20, async index => ({ token: await loginAPI(index), user: users[index] }))
  const started = Date.now()
  const attempts = await Promise.allSettled(pool.map(async buyer => ({ ...buyer, result: await api(`/voucher-order/seckill/${burst}`, buyer.token, 'POST') })))
  check(attempts.every(value => value.status === 'fulfilled'), 'all 200 independently authenticated short-burst requests receive a response')
  const accepted = attempts.map(value => value.value).filter(value => value.result.status === 200 && value.result.body.success)
  const soldOut = attempts.map(value => value.value).filter(value => value.result.status === 200 && value.result.body.success === false && value.result.body.errorMsg === '库存不足')
  const unique = new Set(accepted.map(value => value.result.body.data))
  check(accepted.length === 100 && soldOut.length === 100 && unique.size === 100 && [...unique].every(value => typeof value === 'string' && /^[1-9]\d*$/.test(value)), '200 users obtain exactly 100 unique accepted IDs and 100 stock business rejections')
  for (const orderID of unique) orderIDs.add(orderID)
  await eventually(() => sql(`SELECT COUNT(*) FROM tb_voucher_order WHERE voucher_id=${burst}`), value => value === '100', 'Real worker did not persist all 100 admitted orders', 60000)
  await mapLimit(accepted, 20, async value => {
    const queried = await waitCreated(value.token, value.result.body.data)
    if (queried.body.data.id !== value.result.body.data) throw new Error('Owned order query identity mismatch')
  })
  check(sql(`SELECT stock FROM tb_seckill_voucher WHERE voucher_id=${burst}`) === '0' && await redis('GET', `seckill:stock:${burst}`) === '0'
    && sql(`SELECT COUNT(*) FROM (SELECT user_id FROM tb_voucher_order WHERE voucher_id=${burst} AND active_flag=1 GROUP BY user_id HAVING COUNT(*)>1) d`) === '0', 'real worker commits 100 orders with zero DB and Redis inventory and no duplicate active user order')
  check((await ownedMessages()).filter(item => item.voucherId === burst).length === 100
    && sql(`SELECT COUNT(*) FROM tb_audit_log WHERE action='ORDER_CREATE' AND resource_id IN (SELECT CAST(id AS CHAR) FROM tb_voucher_order WHERE voucher_id=${burst})`) === '100'
    && sql(`SELECT COUNT(*) FROM tb_outbox WHERE aggregate_type='ORDER' AND aggregate_id IN (SELECT CAST(id AS CHAR) FROM tb_voucher_order WHERE voucher_id=${burst}) AND event_type='ORDER_CHANGED'`) === '100', 'every burst order is owner-queryable CREATED/status 1 with exactly one Stream audit and outbox effect')
  report.shortBurst = { users: 200, initialInventory: 100, acceptedUnique: 100, stockRejected: 100, remainingInventory: 0, observedElapsedMs: Date.now() - started, interpretation: 'short-burst correctness, not throughput or sustained capacity' }

  report.step = 'real Vue lost response and recovery after offline'
  await page.locator(`[data-voucher-id="${uiVoucher}"]`).waitFor()
  let lostID, dropped = false, routeFailure = false
  await page.route(`**/api/voucher-order/seckill/${uiVoucher}`, async route => {
    try {
      if (dropped) return await route.continue()
      const response = await route.fetch({ timeout: 15000 })
      const result = await response.json()
      if (response.status() !== 200 || !result.success || typeof result.data !== 'string') throw new Error('Response-loss fixture failed')
      lostID = result.data
      orderIDs.add(lostID)
      dropped = true
      await route.abort('failed')
    } catch {
      routeFailure = true
      await route.abort('failed').catch(() => {})
    }
  })
  await page.locator(`[data-voucher-id="${uiVoucher}"]`).getByRole('button', { name: '立即抢购', exact: true }).click()
  await eventually(async () => dropped || routeFailure, Boolean, 'Lost-response route did not complete')
  if (routeFailure) throw new Error('Real response-loss interception failed; request diagnostics omitted')
  await page.getByRole('button', { name: '重试本次抢券', exact: true }).waitFor()
  check(typeof lostID === 'string' && BigInt(lostID) > 9007199254740992n, 'real Vue purchase can lose a committed acceptance response and expose an independent recovery action')
  const offline = await api(`/merchant/activities/${uiVoucher}/offline`, owner, 'POST')
  check(offline.status === 200 && offline.body.data?.publishStatus === 'OFFLINE', 'real owner closes the accepted activity before the buyer recovers its result')
  await page.getByRole('button', { name: '刷新优惠', exact: true }).click()
  await eventually(() => page.locator(`[data-voucher-id="${uiVoucher}"]`).count(), count => count === 0, 'Offlined activity remained publicly visible')
  check(await page.getByRole('button', { name: '重试本次抢券', exact: true }).isEnabled(), 'recovery remains available after the activity disappears from the public shop list')
  const recovered = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === `/api/voucher-order/seckill/${uiVoucher}`)
  await page.getByRole('button', { name: '重试本次抢券', exact: true }).click()
  const replay = await (await recovered).json()
  await page.waitForURL(url => url.pathname === `/orders/${lostID}`)
  await page.locator('.order-layout[data-order-state="CREATED"]').waitFor()
  await page.unroute(`**/api/voucher-order/seckill/${uiVoucher}`)
  check(replay.data === lostID && sql(`SELECT COUNT(*) FROM tb_voucher_order WHERE voucher_id=${uiVoucher}`) === '1'
    && sql(`SELECT stock FROM tb_seckill_voucher WHERE voucher_id=${uiVoucher}`) === '0' && await redis('GET', `seckill:stock:${uiVoucher}`) === '0'
    && (await ownedMessages()).filter(item => item.voucherId === uiVoucher).length === 1, 'offline recovery returns the same order ID without a second DB order inventory deduction or Stream entry')
  check(await page.getByText('金额 ¥90071992547409.93', { exact: true }).count() === 1
    && await page.getByRole('button', { name: '模拟支付 · 稍后开放', exact: true }).isDisabled()
    && await page.getByRole('button', { name: '取消订单 · 稍后开放', exact: true }).isDisabled()
    && await page.locator('.redeem-token').count() === 0, 'CREATED page displays exact money above 2^53 and leaves payment cancellation and token actions unavailable')
  await page.reload()
  await page.locator('.order-layout[data-order-state="CREATED"]').waitFor()
  const createdAt = sql(`SELECT DATE_FORMAT(create_time,'%Y-%m-%d %H:%i:%s') FROM tb_voucher_order WHERE id=${lostID}`)
  check(await page.getByText(`创建时间：${createdAt}（北京时间）`, { exact: true }).count() === 1
    && await page.getByText('金额 ¥90071992547409.93', { exact: true }).count() === 1
    && await page.evaluate(() => localStorage.getItem('cityloop.last-order')) === lostID, 'refresh restores the real owned order exact money Shanghai creation time and recent order')
  await page.screenshot({ path: output.replace(/\.json$/, '') + '-created.png', fullPage: true, animations: 'disabled' })

  report.step = 'explicit read-only pagination fixtures and real owner list'
  sql(`START TRANSACTION;
    INSERT INTO tb_voucher(id,shop_id,title,pay_value,actual_value,type,status) VALUES ${paginationVouchers.map((voucher, index) => `(${voucher},${shop},'${prefix} 只读分页夹具${index}',990,2000,0,0)`).join(',')};
    INSERT INTO tb_voucher_order(id,user_id,voucher_id,shop_id,pay_type,pay_amount,status,version,create_time,pay_deadline_at,redeem_begin_time,redeem_end_time) VALUES
      ${paginationOrders.map((orderID, index) => `(${orderID},${users[1]},${paginationVouchers[index]},${shop},1,990,1,0,DATE_ADD(NOW(),INTERVAL 1 SECOND),DATE_ADD(NOW(),INTERVAL 15 MINUTE),NOW(),DATE_ADD(NOW(),INTERVAL 2 DAY))`).join(',')}; COMMIT;`)
  const expected = sql(`SELECT id FROM tb_voucher_order WHERE user_id=${users[1]} ORDER BY create_time DESC,id DESC`).split(/\s+/)
  const listed = await api('/voucher-order?current=1&pageSize=50', token)
  check(listed.body.total === '14' && listed.body.data.every(item => typeof item.id === 'string' && !('redeemToken' in item))
    && sql(`SELECT COUNT(DISTINCT create_time) FROM tb_voucher_order WHERE id IN (${paginationOrders.join(',')})`) === '1', 'owner list has fourteen exact-string orders including twelve explicitly labeled same-time SQL pagination fixtures and omits tokens')
  await page.locator('nav').getByRole('link', { name: '我的订单', exact: true }).click()
  const cards = () => page.locator('.my-order-card[data-order-id]')
  await eventually(() => cards().count(), count => count === 10, 'First owned orders page did not load')
  const first = await cards().evaluateAll(nodes => nodes.map(node => node.dataset.orderId))
  await page.getByRole('button', { name: '下一页', exact: true }).click()
  await eventually(() => cards().count(), count => count === 4, 'Second owned orders page did not load')
  const second = await cards().evaluateAll(nodes => nodes.map(node => node.dataset.orderId))
  check(JSON.stringify([...first, ...second]) === JSON.stringify(expected) && new Set([...first, ...second]).size === 14
    && await page.getByRole('button', { name: '下一页', exact: true }).isDisabled(), 'two real pages preserve create_time and ID descending order with no duplicates or omissions')
  async function pageSize(size) {
    await page.locator('.el-select').filter({ has: page.getByRole('combobox', { name: '每页订单数', exact: true }) }).click()
    await page.getByRole('option', { name: `每页 ${size} 条`, exact: true }).click()
    await eventually(() => cards().count(), count => count === Math.min(size, 14), 'Page-size change did not complete')
  }
  await pageSize(20)
  check(await page.getByText('第 1 页', { exact: false }).count() > 0 && await page.getByRole('button', { name: '下一页', exact: true }).isDisabled(), 'changing the real page size resets to page one and respects total')
  await pageSize(50)
  check(await cards().count() === 14, 'fifty-item page size is supported without fabricating extra rows')
  await page.route('**/api/voucher-order?*', route => route.abort('failed').catch(() => {}))
  await page.getByRole('button', { name: '刷新订单', exact: true }).click()
  await page.getByText('暂时无法加载订单', { exact: true }).waitFor()
  check(await cards().count() === 0, 'a failed real list refresh clears previously displayed owned rows')
  await page.unroute('**/api/voucher-order?*')
  await page.getByRole('button', { name: '重新加载', exact: true }).click()
  await eventually(() => cards().count(), count => count === 14, 'Owned list retry did not recover')
  check(true, 'list retry restores owned rows from the real API')
  await page.screenshot({ path: output.replace(/\.json$/, '') + '-list.png', fullPage: true, animations: 'disabled' })

  report.step = 'other user 404 and empty list'
  const foreignID = accepted[0].result.body.data
  const previousRecent = await page.evaluate(() => localStorage.getItem('cityloop.last-order'))
  await page.goto(`${WEB}/orders/${foreignID}`)
  await page.getByRole('heading', { name: '无法查看此订单', exact: true }).waitFor()
  check((await api(`/voucher-order/${foreignID}`, token)).status === 404 && await page.locator('.order-meta').count() === 0
    && await page.evaluate(() => localStorage.getItem('cityloop.last-order')) === previousRecent, 'visiting another buyer order returns 404 clears details and does not replace the recent owned order')
  const other = await loginUI(2, `/orders/${lostID}`)
  await other.page.getByRole('heading', { name: '无法查看此订单', exact: true }).waitFor()
  check((await api(`/voucher-order/${lostID}`, other.token)).status === 404 && await other.page.locator('.order-meta').count() === 0
    && await other.page.evaluate(() => localStorage.getItem('cityloop.last-order')) === null, 'another real Vue login cannot read the original order or learn its money and does not remember a forbidden ID')
  const empty = await loginUI(3, '/orders')
  await empty.page.getByText('还没有已创建的订单', { exact: true }).waitFor()
  check((await api('/voucher-order', empty.token)).body.total === '0' && await empty.page.locator('.my-order-card').count() === 0, 'an authenticated buyer without orders receives a real empty list and zero string total')

  report.step = 'explicit PROCESSING query fixture and poll cleanup'
  await redis('HSET', `order:reservation:${pendingID}`, 'userId', users[1], 'voucherId', single, 'state', 'PROCESSING', 'createdAt', Date.now(), 'retries', 0)
  await redis('EXPIRE', `order:reservation:${pendingID}`, 300)
  const pending = await api(`/voucher-order/${pendingID}`, token)
  check(pending.body.data?.state === 'PROCESSING' && pending.body.data.orderStatus === null
    && ['payAmount', 'shopId', 'createTime', 'payDeadlineAt', 'redeemToken'].every(field => !(field in pending.body.data))
    && sql(`SELECT COUNT(*) FROM tb_voucher_order WHERE id=${pendingID}`) === '0'
    && !(await ownedMessages()).some(item => item.id === pendingID), 'query-only reservation fixture remains PROCESSING with null business status and no fabricated DB snapshot or Stream message')
  await page.goto(`${WEB}/orders/${pendingID}`)
  await page.locator('.order-layout[data-order-state="PROCESSING"]').waitFor()
  check(await page.locator('.order-meta').count() === 0 && await page.getByText('请求已受理不代表下单成功，请以最终订单状态为准。', { exact: true }).count() === 1, 'real PROCESSING page distinguishes acceptance from creation and displays no amount or deadline')
  let pendingRequests = 0
  page.on('request', request => { if (new URL(request.url()).pathname === `/api/voucher-order/${pendingID}`) pendingRequests++ })
  await page.locator('.order-actions').getByRole('link', { name: '我的订单', exact: true }).click()
  await page.getByRole('heading', { name: '我的订单', exact: true }).waitFor()
  const atUnmount = pendingRequests
  await new Promise(resolveWait => setTimeout(resolveWait, 1500))
  check(pendingRequests === atUnmount, 'leaving the PROCESSING page cancels its scheduled order polls')
  check(pageErrors.length === 0, 'all real browser pages complete without uncaught page errors')
  report.passed = true
} catch (error) {
  report.failure = error instanceof Error ? error.message.split('\n')[0].slice(0, 180) : 'Order UI verification failed'
  if (mainPage && !new URL(mainPage.url()).pathname.startsWith('/login')) {
    await mainPage.screenshot({ path: output.replace(/\.json$/, '') + '-failure.png', fullPage: true, animations: 'disabled' }).catch(() => {})
  }
  process.exitCode = 1
} finally {
  // Cleanup ACKs in a failed run only remove owned test fixtures. They are
  // separate from the successful run's pre-cleanup PEL-drain verification.
  report.forcedCleanup = !report.passed
  if (browser) await browser.close().catch(() => {})
  let clean = true
  await mapLimit([...sessions], 20, async token => {
    try { const result = await api('/user/logout', token, 'POST'); if (result.status !== 200 && result.status !== 401) clean = false } catch { clean = false }
  }).catch(() => { clean = false })
  if (seeded) {
    try {
      const catalog = sql(`SELECT id FROM tb_voucher WHERE shop_id=${shop}`).split(/\s+/).filter(Boolean)
      for (const voucher of catalog) activityIDs.add(voucher)
      const messages = await ownedMessages()
      for (const message of messages) orderIDs.add(message.id)
      for (const voucher of activityIDs) {
        const accepted = await redis('HGETALL', `seckill:accepted:${voucher}`)
        for (let index = 1; index < accepted.length; index += 2) orderIDs.add(accepted[index])
      }
      for (const orderID of sql(`SELECT id FROM tb_voucher_order WHERE user_id IN (${users.join(',')})`).split(/\s+/).filter(Boolean)) orderIDs.add(orderID)
      // A settled successful run must let the real worker ACK its own durable
      // work before any source entries or database facts are removed.
      if (report.passed) {
        await eventually(async () => await mapLimit(messages, 20, message => redis('XPENDING', 'stream.orders', 'g1', message.sourceID, message.sourceID, 1)), values => values.every(value => value.length === 0), 'Owned worker deliveries did not drain before cleanup')
        report.workerACK = { ownedMessages: messages.length, pendingBeforeCleanup: 0 }
      }
      const allOrders = [...orderIDs].filter(value => /^[1-9]\d*$/.test(value))
      const allVouchers = [...activityIDs].filter(value => /^[1-9]\d*$/.test(value))
      // Deleting the durable producers first waits for worker-held InnoDB row
      // locks. No shared Stream/group or real daily ID sequence is removed.
      sql(`START TRANSACTION;
        SELECT id FROM tb_voucher WHERE shop_id=${shop} ORDER BY id FOR UPDATE;
        ${allVouchers.length ? `SELECT voucher_id FROM tb_seckill_voucher WHERE voucher_id IN (${allVouchers.join(',')}) ORDER BY voucher_id FOR UPDATE;` : ''}
        DELETE FROM tb_outbox WHERE (aggregate_type='ACTIVITY' AND aggregate_id IN (${allVouchers.length ? allVouchers.map(value => `'${value}'`).join(',') : "''"}))
          OR (aggregate_type IN ('ORDER','RESERVATION') AND aggregate_id IN (${allOrders.map(value => `'${value}'`).join(',')}));
        DELETE FROM tb_audit_log WHERE merchant_id=${merchant};
        DELETE FROM tb_voucher_order WHERE user_id IN (${users.join(',')});
        DELETE FROM tb_seckill_voucher WHERE voucher_id IN (${allVouchers.length ? allVouchers.join(',') : '0'});
        DELETE FROM tb_voucher WHERE shop_id=${shop}; COMMIT;`)
      for (const message of messages) {
        await redis('XACK', 'stream.orders', 'g1', message.sourceID)
        await redis('XDEL', 'stream.orders', message.sourceID)
      }
      const keys = [...allVouchers.flatMap(voucher => ['gate', 'stock', 'begin', 'end', 'order', 'accepted'].map(part => `seckill:${part}:${voucher}`)), ...allOrders.map(orderID => `order:reservation:${orderID}`)]
      for (let offset = 0; offset < keys.length; offset += 100) {
        const batch = keys.slice(offset, offset + 100)
        await redis('DEL', ...batch)
        if (await redis('EXISTS', ...batch) !== 0) clean = false
      }
      sql(`START TRANSACTION; DELETE FROM tb_member_shop_scope WHERE member_id=${member}; DELETE FROM tb_merchant_member WHERE id=${member};
        DELETE FROM tb_shop WHERE id=${shop}; DELETE FROM tb_merchant WHERE id=${merchant}; DELETE FROM tb_user WHERE id IN (${users.join(',')}); COMMIT;`)
      clean &&= sql(`SELECT (SELECT COUNT(*) FROM tb_voucher_order WHERE user_id IN (${users.join(',')}))+(SELECT COUNT(*) FROM tb_voucher WHERE shop_id=${shop})+(SELECT COUNT(*) FROM tb_shop WHERE id=${shop})+(SELECT COUNT(*) FROM tb_merchant WHERE id=${merchant})+(SELECT COUNT(*) FROM tb_audit_log WHERE merchant_id=${merchant})+(SELECT COUNT(*) FROM tb_outbox WHERE aggregate_id IN (${[...allOrders, ...allVouchers].map(value => `'${value}'`).join(',')}))`) === '0'
      clean &&= (await ownedMessages()).length === 0
      report.cleanupBoundary = { ownedDatabaseRowsRemaining: clean ? 0 : 'verification failed', ownedRedisKeysRemaining: clean ? 0 : 'verification failed', ownedStreamEntriesRemaining: clean ? 0 : 'verification failed', sharedStreamAndGroup: 'preserved', realDailyOrderCounter: 'preserved' }
    } catch { clean = false }
  }
  report.cleanup = clean
  if (!clean) { report.passed = false; process.exitCode = 1 }
  report.recordedAt = new Date().toISOString()
  await writeFile(output, JSON.stringify(report, null, 2) + '\n')
  process.stdout.write(JSON.stringify({ passed: report.passed, checks: report.checks.length, cleanup: report.cleanup, output }) + '\n')
}
