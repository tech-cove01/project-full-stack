// GO-074a: actual merchant Vue/Go/MySQL/Redis role matrix. No successful API
// mocking, trace or HAR. Tokens/codes/redeem credentials remain memory-only.
import assert from 'node:assert/strict'
import { randomBytes, randomInt } from 'node:crypto'
import { spawnSync } from 'node:child_process'
import { mkdir, writeFile } from 'node:fs/promises'
import { createConnection } from 'node:net'
import { dirname, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

for (const key of ['API_BASE', 'WEB_BASE', 'MYSQL_HOST', 'MYSQL_PORT', 'MYSQL_DATABASE', 'MYSQL_USER', 'MYSQL_PASSWORD', 'REDIS_HOST', 'REDIS_PORT']) {
  if (!process.env[key]) throw new Error(`Explicit isolated ${key} required`)
}
const modulePath = process.env.PLAYWRIGHT_MODULE || 'playwright'
const { chromium } = await import(/^[A-Za-z]:[\\/]/.test(modulePath) || modulePath.startsWith('.') ? pathToFileURL(resolve(modulePath)).href : modulePath)
const API = process.env.API_BASE.replace(/\/$/, ''), WEB = process.env.WEB_BASE.replace(/\/$/, '')
const output = resolve(process.env.UI_EVIDENCE || 'go-merchant-orders-ui.json')
await mkdir(dirname(output), { recursive: true })
const prefix = `M074${randomBytes(5).toString('hex')}`
const base = 9007199254740993n + BigInt(randomInt(60000000, 70000000)) * 1000n
const id = value => String(base + BigInt(value))
const roleNames = ['owner', 'manager', 'staff', 'mixed', 'otherOwner', 'buyer']
const users = Object.fromEntries(roleNames.map((name, index) => [name, id(index)]))
const merchants = [id(100), id(101)], shops = [id(200), id(201), id(202), id(203)]
const shopNames = shops.map((_, index) => `${prefix} ${['A1', 'A2', 'B1', 'B2'][index]}`)
const members = Array.from({ length: 6 }, (_, index) => id(300 + index))
const phoneSet = new Set()
while (phoneSet.size < roleNames.length) phoneSet.add(`137${randomInt(10000000, 100000000)}`)
const phones = Object.fromEntries(roleNames.map((name, index) => [name, [...phoneSet][index]]))
const sessions = new Map(), orders = new Map(), activities = new Set()
const report = { task: 'GO-074a', acceptance: ['FIN-05', 'GO-020', 'GO-032'], passed: false, checks: [], denials: [], network: [], cleanup: false, browserTimezone: process.env.UI_TIMEZONE || 'Asia/Shanghai', privacy: 'No trace/HAR; network metadata only; screenshot inputs masked' }
let browser, context, page, seeded = false
function check(value, message) { assert.ok(value, message); report.checks.push(message) }
function canonical(value) { return typeof value === 'string' && /^[1-9]\d{0,19}$/.test(value) && BigInt(value) <= 18446744073709551615n }
function sql(statement) {
  const result = spawnSync(process.env.MYSQL_BIN || 'mysql', ['--protocol=TCP', `--host=${process.env.MYSQL_HOST}`, `--port=${process.env.MYSQL_PORT}`, `--user=${process.env.MYSQL_USER}`, '--default-character-set=utf8mb4', '--batch', '--skip-column-names', process.env.MYSQL_DATABASE], { input: `SET NAMES utf8mb4; SET time_zone='+08:00'; ${statement}`, encoding: 'utf8', env: { ...process.env, MYSQL_PWD: process.env.MYSQL_PASSWORD } })
  if (result.error || result.status !== 0) throw new Error('Owned fixture SQL failed; diagnostics omitted')
  return result.stdout.trim()
}
const safePath = url => new URL(url).pathname.replace(/\/\d+(?=\/|$)/g, '/:id')
async function api(path, role, method = 'GET', body, key) {
  const response = await fetch(API + path, { method, signal: AbortSignal.timeout(20000), headers: { 'Content-Type': 'application/json', ...(sessions.has(role) ? { authorization: sessions.get(role) } : {}), ...(key ? { 'Idempotency-Key': key } : {}) }, ...(body ? { body: JSON.stringify(body) } : {}) })
  report.network.push({ step: report.step, method, path: safePath(API + path), status: response.status, boundary: 'node-real-api' })
  return { status: response.status, body: await response.json() }
}
async function eventually(read, accept, message, timeout = 60000) {
  const deadline = Date.now() + timeout
  while (Date.now() < deadline) {
    const value = await read()
    if (accept(value)) return value
    await new Promise(resolveWait => setTimeout(resolveWait, 150))
  }
  throw new Error(message)
}

// RESP2 parser including nested arrays for XRANGE, XPENDING and accepted hashes.
async function redis(...args) {
  const commands = [...(process.env.REDIS_PASSWORD ? [['AUTH', process.env.REDIS_PASSWORD]] : []), ['SELECT', process.env.REDIS_DATABASE || '0'], args]
  return new Promise((resolveReply, reject) => {
    const socket = createConnection({ host: process.env.REDIS_HOST, port: Number(process.env.REDIS_PORT) })
    let buffer = Buffer.alloc(0), replies = 0, settled = false
    const fail = () => { if (!settled) { settled = true; socket.destroy(); reject(new Error('Owned Redis operation failed; diagnostics omitted')) } }
    function parse(offset) {
      if (offset >= buffer.length) return null
      const end = buffer.indexOf('\r\n', offset)
      if (end < 0) return null
      const type = buffer[offset], line = buffer.subarray(offset + 1, end).toString()
      let next = end + 2
      if (type === 45) throw new Error('Redis error')
      if (type === 43 || type === 58) return { value: type === 43 ? line : Number(line), next }
      if (type === 36) {
        const length = Number(line)
        if (!Number.isSafeInteger(length) || length < -1) throw new Error('Invalid Redis shape')
        if (length === -1) return { value: null, next }
        return buffer.length < next + length + 2 ? null : { value: buffer.subarray(next, next + length).toString(), next: next + length + 2 }
      }
      if (type === 42) {
        const count = Number(line), value = []
        if (!Number.isSafeInteger(count) || count < -1) throw new Error('Invalid Redis shape')
        if (count === -1) return { value: null, next }
        for (let index = 0; index < count; index++) { const item = parse(next); if (!item) return null; value.push(item.value); next = item.next }
        return { value, next }
      }
      throw new Error('Invalid Redis shape')
    }
    socket.setTimeout(5000, fail).on('error', fail)
    socket.on('close', () => { if (!settled) fail() })
    socket.on('connect', () => socket.write(commands.map(command => `*${command.length}\r\n${command.map(arg => { const text = String(arg); return `$${Buffer.byteLength(text)}\r\n${text}\r\n` }).join('')}`).join('')))
    socket.on('data', chunk => {
      buffer = Buffer.concat([buffer, chunk])
      try {
        while (buffer.length) {
          const reply = parse(0); if (!reply) return
          buffer = buffer.subarray(reply.next)
          if (++replies === commands.length) { settled = true; socket.destroy(); resolveReply(reply.value); return }
        }
      } catch { fail() }
    })
  })
}
const pairs = list => Object.fromEntries(Array.from({ length: list.length / 2 }, (_, index) => [list[index * 2], list[index * 2 + 1]]))
async function ownedMessages() {
  const result = []; let start = '-'
  for (;;) {
    const batch = await redis('XRANGE', 'stream.orders', start, '+', 'COUNT', 1000)
    if (!Array.isArray(batch)) throw new Error('Owned Stream inspection failed')
    for (const [sourceID, values] of batch) {
      const fields = pairs(values)
      if (!activities.has(fields.voucherId)) continue
      if (!canonical(fields.id) || fields.userId !== users.buyer || !/^(?:0|[1-9]\d*)-(?:0|[1-9]\d*)$/.test(sourceID)) throw new Error('Owned Stream binding failed')
      result.push({ sourceID, id: fields.id, voucher: fields.voucherId })
    }
    if (batch.length < 1000) return result
    start = `(${batch.at(-1)[0]}`
  }
}
async function login(role) {
  const sent = await api(`/user/code?phone=${phones[role]}`, undefined, 'POST')
  if (!sent.body.success || sent.body.data?.mode !== 'development') throw new Error('Actual dev login unavailable')
  const reply = await api('/user/login', undefined, 'POST', { phone: phones[role], code: sent.body.data.code })
  if (!reply.body.success || !/^[a-f0-9]{32}$/.test(reply.body.data)) throw new Error('Actual dev login failed')
  sessions.set(role, reply.body.data)
}
const dateFormat = new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' })
function at(offset) { const p = Object.fromEntries(dateFormat.formatToParts(Date.now() + offset).map(part => [part.type, part.value])); return `${p.year}-${p.month}-${p.day}T${p.hour}:${p.minute}:${p.second}+08:00` }
async function paidOrder(label, shopIndex) {
  const publisher = shopIndex < 2 ? 'owner' : 'otherOwner'
  const created = await api('/merchant/activities', publisher, 'POST', { shopId: shops[shopIndex], title: `${prefix} ${label}`, subTitle: null, rules: null, payValue: '1900', actualValue: '2500', totalStock: 2, beginTime: at(-300000), endTime: at(86400000), redeemBeginTime: at(-300000), redeemEndTime: at(172800000) }, `${prefix}-${label}`)
  if (!created.body.success || !canonical(created.body.data?.id)) throw new Error('Actual activity publication failed')
  const voucher = created.body.data.id; activities.add(voucher)
  await eventually(() => api('/merchant/activities?current=1&pageSize=50', publisher), value => value.body.data?.some(row => row.id === voucher && row.publishStatus === 'ACTIVE' && row.ready), 'Actual activity gate not ready')
  const accepted = await api(`/voucher-order/seckill/${voucher}`, 'buyer', 'POST')
  if (!accepted.body.success || !canonical(accepted.body.data)) throw new Error('Actual order admission failed')
  const order = { id: accepted.body.data, voucher, shopIndex, label }
  orders.set(label, order)
  await eventually(() => api(`/voucher-order/${order.id}`, 'buyer'), value => value.body.data?.state === 'CREATED' && value.body.data.orderStatus === 1, 'Actual Stream order did not commit')
  const paid = await api(`/voucher-order/${order.id}/pay`, 'buyer', 'POST')
  if (!paid.body.success || paid.body.data?.orderStatus !== 2 || !/^[a-f0-9]{64}$/.test(paid.body.data.redeemToken)) throw new Error('Actual payment failed')
  order.redeemToken = paid.body.data.redeemToken
}
function snapshot(order) {
  return sql(`SELECT CONCAT(o.status,':',o.version,':',s.stock,':',(SELECT COUNT(*) FROM tb_audit_log WHERE resource_id='${order.id}'),':',(SELECT COUNT(*) FROM tb_outbox WHERE aggregate_type='ORDER' AND aggregate_id='${order.id}')) FROM tb_voucher_order o JOIN tb_seckill_voucher s ON s.voucher_id=o.voucher_id WHERE o.id=${order.id}`)
}
function outboxScope(activityIDs, orderIDs) {
  const list = values => values.length ? values.map(value => `'${value}'`).join(',') : "''"
  return `((aggregate_type='ACTIVITY' AND aggregate_id IN(${list(activityIDs)})) OR (aggregate_type IN('ORDER','RESERVATION') AND aggregate_id IN(${list(orderIDs)})))`
}
async function denied(label, order, perform, statuses = [403]) {
  const before = snapshot(order), response = await perform()
  check(statuses.includes(response.status) && snapshot(order) === before, label)
  report.denials.push({ label, status: response.status, unchangedOrderVersionStockAuditOutbox: true })
}
async function hiddenList(label, order, perform) {
  const before = snapshot(order), response = await perform()
  check(response.status === 200 && response.body.success === true && Array.isArray(response.body.data) && response.body.data.length === 0 && response.body.total === '0' && snapshot(order) === before, label)
  report.denials.push({ label, status: response.status, emptyAuthorizedList: true, unchangedOrderVersionStockAuditOutbox: true })
}
class MerchantOrdersPage {
  constructor(page) { this.page = page; this.orderPanel = page.locator('section').filter({ has: page.getByRole('heading', { name: '门店订单', exact: true }) }) }
  row(order) { return this.orderPanel.getByRole('row').filter({ hasText: order.id }) }
  response(path) { return this.page.waitForResponse(response => new URL(response.url()).pathname === '/api' + path) }
  async select(placeholder, name) {
    report.action = placeholder === '授权门店' ? 'select authorized redemption shop' : 'select order shop filter'
    // Non-filterable ElSelect hides its readonly combobox input. Click its
    // visible library wrapper within the semantic section. The existing filter
    // pair is shop then status, so its first scoped wrapper is deliberate.
    const control = placeholder === '授权门店'
      ? this.page.locator('section').filter({ has: this.page.getByRole('heading', { name: '到店核销', exact: true }) }).locator('.el-select__wrapper')
      : this.orderPanel.locator('.el-select__wrapper').nth(0)
    await control.click()
    report.action = placeholder === '授权门店' ? 'choose authorized redemption option' : 'choose order shop option'
    await this.page.getByRole('option', { name, exact: true }).click()
  }
  async redeem(order, { shopIndex = order.shopIndex, status = 200, forgedShop } = {}) {
    await this.select('授权门店', `${shopNames[shopIndex]}（${shops[shopIndex]}）`)
    report.action = 'enter owned redemption credential and submit'
    await this.page.getByPlaceholder('粘贴 64 位核销码', { exact: true }).fill(order.redeemToken)
    if (forgedShop) await this.page.route('**/api/merchant/orders/redeem', route => route.continue({ postData: JSON.stringify({ shopId: forgedShop, token: order.redeemToken }) }))
    const pending = this.response('/merchant/orders/redeem')
    await this.page.getByRole('button', { name: '核销', exact: true }).click()
    const response = await pending, body = await response.json()
    if (forgedShop) await this.page.unroute('**/api/merchant/orders/redeem')
    if (response.status() !== status) throw new Error('Redeem HTTP status differed')
    if (status === 200) await this.row(order).getByText('已核销', { exact: true }).waitFor()
    return { status: response.status(), body }
  }
  async refund(order) {
    report.action = 'confirm owned merchant refund'
    await this.row(order).getByRole('button', { name: '模拟退款', exact: true }).click()
    const pending = this.response(`/merchant/orders/${order.id}/refund`)
    await this.page.getByRole('dialog').getByRole('button', { name: '确认退款', exact: true }).click()
    const response = await pending
    if (response.status() !== 200 || !(await response.json()).success) throw new Error('Actual refund failed')
    await this.row(order).getByText('已退款', { exact: true }).waitFor()
  }
}
const pageErrors = []
async function as(role, path = '/merchant/orders') {
  if (context) await context.close()
  context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, timezoneId: report.browserTimezone })
  await context.addInitScript(token => localStorage.setItem('cityloop.token', token), sessions.get(role))
  page = await context.newPage()
  page.on('pageerror', () => pageErrors.push('uncaught browser error'))
  page.on('response', response => { const url = new URL(response.url()); if (url.pathname.startsWith('/api/')) report.network.push({ step: report.step, method: response.request().method(), path: safePath(response.url()), status: response.status(), boundary: 'actual-browser' }) })
  const ready = page.waitForResponse(response => new URL(response.url()).pathname === (path.startsWith('/merchant/orders') ? '/api/merchant/orders' : '/api/merchant/workspace'))
  await page.goto(WEB + path); await ready
  return new MerchantOrdersPage(page)
}
async function browserRequest(path, method = 'GET', body) {
  return page.evaluate(async args => { const response = await fetch('/api' + args.path, { method: args.method, headers: { authorization: localStorage.getItem('cityloop.token'), 'Content-Type': 'application/json' }, ...(args.body ? { body: JSON.stringify(args.body) } : {}) }); return { status: response.status, body: await response.json() } }, { path, method, body })
}
async function screenshot(suffix) { await page.screenshot({ path: output.replace(/\.json$/, '') + suffix + '.png', fullPage: true, animations: 'disabled', mask: [page.locator('input'), page.locator('[data-sensitive]')] }) }

try {
  report.step = 'isolated Go identity and explicit owned role fixture'
  const live = await api('/health/live'), proxy = await (await fetch(WEB + '/api/health/live')).json()
  check(live.body.runtime === 'go' && proxy.runtime === 'go' && live.body.version === proxy.version, 'direct and proxied API identify the same Go revision')
  report.service = proxy
  sql(`START TRANSACTION;
    INSERT INTO tb_user(id,phone,password,nick_name,icon) VALUES ${roleNames.map(role => `(${users[role]},'${phones[role]}','x','${prefix}-${role}','')`).join(',')};
    INSERT INTO tb_merchant(id,name,status) VALUES(${merchants[0]},'${prefix}-A','ACTIVE'),(${merchants[1]},'${prefix}-B','ACTIVE');
    INSERT INTO tb_shop(id,merchant_id,name,type_id,address,x,y) VALUES ${shops.map((shop, i) => `(${shop},${merchants[i < 2 ? 0 : 1]},'${shopNames[i]}',1,'合成授权地址',0,0)`).join(',')};
    INSERT INTO tb_merchant_member(id,merchant_id,user_id,role_code,status) VALUES(${members[0]},${merchants[0]},${users.owner},'OWNER','ACTIVE'),(${members[1]},${merchants[0]},${users.manager},'MANAGER','ACTIVE'),(${members[2]},${merchants[0]},${users.staff},'STAFF','ACTIVE'),(${members[3]},${merchants[0]},${users.mixed},'MANAGER','ACTIVE'),(${members[4]},${merchants[1]},${users.mixed},'STAFF','ACTIVE'),(${members[5]},${merchants[1]},${users.otherOwner},'OWNER','ACTIVE');
    INSERT INTO tb_member_shop_scope(member_id,shop_id) VALUES(${members[2]},${shops[0]}),(${members[4]},${shops[2]}); COMMIT;`)
  seeded = true
  for (const role of roleNames) await login(role)
  for (const [label, shop] of [['owner-redeem', 0], ['staff-redeem', 0], ['owner-refund', 0], ['scope-denied', 0], ['member-denied', 0], ['manager-redeem', 1], ['manager-refund', 1], ['cross-denied', 1], ['mixed-redeem', 2], ['mixed-refund-denied', 2]]) await paidOrder(label, shop)
  check(orders.size === 10 && [...orders.values()].every(order => sql(`SELECT status FROM tb_voucher_order WHERE id=${order.id}`) === '2'), 'ten orders are paid through real publication/admission/Stream/payment APIs')
  await eventually(async () => sql(`SELECT COUNT(*) FROM tb_outbox WHERE ${outboxScope([...activities], [...orders.values()].map(order => order.id))} AND delivery_status<>'DONE'`), value => value === '0', 'Initial worker events did not finish')
  browser = await chromium.launch({ ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}), headless: true })

  report.step = 'owner natural navigation and A scope'
  let ui = await as('owner', '/merchant')
  await page.getByRole('link', { name: '订单与核销', exact: true }).click(); await page.waitForURL(url => url.pathname === '/merchant/orders')
  await ui.row(orders.get('owner-refund')).waitFor()
  check(await ui.row(orders.get('mixed-refund-denied')).count() === 0 && await page.getByRole('heading', { name: '操作审计', exact: true }).count() === 1, 'OWNER sees A1/A2 and management audit, without B orders')
  await ui.redeem(orders.get('owner-redeem'))
  const repeated = await ui.redeem(orders.get('owner-redeem'))
  check(repeated.body.data.alreadyRedeemed === true && sql(`SELECT COUNT(*) FROM tb_audit_log WHERE resource_id='${orders.get('owner-redeem').id}' AND action='ORDER_REDEEMED'`) === '1', 'OWNER real redemption is idempotent with one audit')
  await ui.refund(orders.get('owner-refund')); check(true, 'OWNER can refund a paid A1 order through the real confirmation dialog')
  await denied('OWNER A cannot refund B through a forged real browser URL', orders.get('mixed-refund-denied'), () => browserRequest(`/merchant/orders/${orders.get('mixed-refund-denied').id}/refund`, 'POST', { reason: 'MERCHANT_REFUND' }))
  await screenshot('-owner-masked')
  await page.goto(WEB + '/merchant'); await page.getByRole('link', { name: '公开规则', exact: true }).click(); await page.waitForURL(url => url.pathname === '/merchant/knowledge')
  await page.getByRole('heading', { name: '门店公开规则', exact: true }).waitFor()
  check(true, 'workbench natural links open the existing order and public knowledge routes')

  report.step = 'manager A actions'
  ui = await as('manager'); await ui.row(orders.get('manager-refund')).waitFor()
  check(await ui.row(orders.get('manager-refund')).getByRole('button', { name: '模拟退款', exact: true }).count() === 1, 'MANAGER can manage paid A2 orders')
  await ui.redeem(orders.get('manager-redeem')); await ui.refund(orders.get('manager-refund'))
  check(sql(`SELECT status FROM tb_voucher_order WHERE id=${orders.get('manager-redeem').id}`) === '3' && sql(`SELECT status FROM tb_voucher_order WHERE id=${orders.get('manager-refund').id}`) === '6', 'MANAGER real A2 redemption/refund match persistent states')
  await screenshot('-manager-masked')

  report.step = 'staff A1 visibility and forged scope rejection'
  ui = await as('staff'); await ui.row(orders.get('staff-redeem')).waitFor()
  check(await ui.row(orders.get('cross-denied')).count() === 0 && await page.getByRole('button', { name: '模拟退款', exact: true }).count() === 0 && await page.getByRole('heading', { name: '操作审计', exact: true }).count() === 0, 'STAFF sees only A1 and has no refund or management audit controls')
  await hiddenList('STAFF forged A2 query returns an authorized empty list with no side effects', orders.get('cross-denied'), () => browserRequest(`/merchant/orders?current=1&pageSize=10&shopId=${shops[1]}`))
  await denied('STAFF cannot refund even its authorized A1 order', orders.get('scope-denied'), () => browserRequest(`/merchant/orders/${orders.get('scope-denied').id}/refund`, 'POST', { reason: 'MERCHANT_REFUND' }))
  await denied('STAFF forged redeem form cannot widen A1 to A2', orders.get('cross-denied'), () => ui.redeem(orders.get('cross-denied'), { shopIndex: 0, status: 403, forgedShop: shops[1] }))
  await ui.redeem(orders.get('staff-redeem')); check(true, 'STAFF can redeem its actually authorized A1 order')
  await screenshot('-staff-masked')

  report.step = 'staff scope revoked while retaining old login token'
  sql(`DELETE FROM tb_member_shop_scope WHERE member_id=${members[2]} AND shop_id=${shops[0]}`)
  await denied('old STAFF Token cannot redeem after its concrete scope is removed', orders.get('scope-denied'), () => ui.redeem(orders.get('scope-denied'), { status: 403 }))
  await hiddenList('old STAFF Token sees no removed A1 orders after scope revocation', orders.get('scope-denied'), () => browserRequest(`/merchant/orders?shopId=${shops[0]}`))
  const refreshed = ui.response('/merchant/orders'); await page.reload(); await refreshed
  await page.getByText('当前筛选下没有订单', { exact: true }).waitFor()
  check(await ui.row(orders.get('scope-denied')).count() === 0 && (await browserRequest('/user/me')).body.data.id === users.staff, 'scope refresh clears prior rows while the same identity Token remains valid')
  await screenshot('-scope-revoked-masked')

  report.step = 'mixed manager A plus staff B1'
  ui = await as('mixed'); await ui.row(orders.get('mixed-refund-denied')).waitFor()
  check(await ui.row(orders.get('member-denied')).getByRole('button', { name: '模拟退款', exact: true }).count() === 1 && await ui.row(orders.get('mixed-refund-denied')).getByRole('button', { name: '模拟退款', exact: true }).count() === 0, 'mixed roles expose A management controls without upgrading B1 staff refund permission')
  await denied('mixed A MANAGER role cannot refund its B1 STAFF order', orders.get('mixed-refund-denied'), () => browserRequest(`/merchant/orders/${orders.get('mixed-refund-denied').id}/refund`, 'POST', { reason: 'MERCHANT_REFUND' }))
  await ui.redeem(orders.get('mixed-redeem'))
  const audit = await browserRequest('/merchant/audits?current=1&pageSize=50')
  check(audit.status === 200 && audit.body.data.every(row => !row.shopId || shops.slice(0, 2).includes(row.shopId)) && !JSON.stringify(audit.body).includes(orders.get('mixed-redeem').redeemToken), 'mixed audit reads include only managed A scope and no redemption credential')
  await screenshot('-mixed-masked')

  report.step = 'mixed A membership disabled with old Token and B scope retained'
  sql(`UPDATE tb_merchant_member SET status='DISABLED' WHERE id=${members[3]}`)
  await denied('old mixed Token loses A refund authorization immediately after membership disable', orders.get('member-denied'), () => browserRequest(`/merchant/orders/${orders.get('member-denied').id}/refund`, 'POST', { reason: 'MERCHANT_REFUND' }))
  const narrowed = ui.response('/merchant/orders'); await page.reload(); await narrowed; await ui.row(orders.get('mixed-refund-denied')).waitFor()
  check(await ui.row(orders.get('member-denied')).count() === 0 && await page.getByRole('button', { name: '模拟退款', exact: true }).count() === 0 && await page.getByRole('heading', { name: '操作审计', exact: true }).count() === 0, 'mixed refresh removes A rows/refund/audits while preserving B1 staff data')
  check((await browserRequest('/user/me')).body.data.id === users.mixed, 'membership disable did not require replacing the old login Token')
  await screenshot('-member-disabled-masked')

  report.step = 'empty B2 filter and actual transport failure/retry'
  ui = await as('otherOwner'); await ui.row(orders.get('mixed-refund-denied')).waitFor()
  const empty = ui.response('/merchant/orders'); await ui.select('全部门店', shopNames[3]); await empty
  await page.getByText('当前筛选下没有订单', { exact: true }).waitFor(); check(true, 'authorized empty B2 filter shows the explicit empty state')
  await page.route('**/api/merchant/orders?**', route => route.abort('failed'))
  await ui.select('全部门店', shopNames[2]); await page.getByRole('alert').filter({ hasText: '无法连接服务' }).waitFor()
  check(true, 'actual order-list network failure displays a safe error without fake success')
  await page.unroute('**/api/merchant/orders?**')
  const recovered = ui.response('/merchant/orders'); await page.reload(); check((await recovered).status() === 200, 'normal page reload retries the actual authorized list')
  await ui.row(orders.get('mixed-refund-denied')).waitFor(); await screenshot('-other-owner-recovered-masked')

  report.step = 'persistent action audits and compensation'
  for (const [label, action, operator] of [['owner-redeem', 'ORDER_REDEEMED', 'owner'], ['staff-redeem', 'ORDER_REDEEMED', 'staff'], ['manager-redeem', 'ORDER_REDEEMED', 'manager'], ['mixed-redeem', 'ORDER_REDEEMED', 'mixed'], ['owner-refund', 'ORDER_REFUNDED', 'owner'], ['manager-refund', 'ORDER_REFUNDED', 'manager']]) {
    const order = orders.get(label)
    check(sql(`SELECT COUNT(*) FROM tb_audit_log WHERE resource_id='${order.id}' AND action='${action}' AND shop_id=${shops[order.shopIndex]} AND operator_user_id=${users[operator]}`) === '1', `${label} has one audit with the actual operator and shop`)
  }
  await eventually(async () => {
    for (const order of orders.values()) {
      const expected = order.label.endsWith('refund') ? '2' : '1'
      if (sql(`SELECT stock FROM tb_seckill_voucher WHERE voucher_id=${order.voucher}`) !== expected || await redis('GET', `seckill:stock:${order.voucher}`) !== expected) return false
    }
    return true
  }, Boolean, 'Refund inventory projections did not converge')
  check(true, 'actual compensation restores only refunded activity stock and agrees with MySQL')
  check(pageErrors.length === 0, 'no uncaught browser exceptions in the role matrix')
  report.passed = true
} catch (error) {
  report.failure = 'Merchant UI verification failed; inspect the safe step and masked screenshot.'
  report.failureCategory = ['TimeoutError', 'AssertionError', 'Error'].includes(error?.name) ? error.name : 'verification error'
  if (page && !new URL(page.url()).pathname.startsWith('/login')) await screenshot('-failure-masked').catch(() => {})
  process.exitCode = 1
} finally {
  if (browser) await browser.close().catch(() => {})
  let clean = true
  for (const role of sessions.keys()) { try { const reply = await api('/user/logout', role, 'POST'); if (![200, 401].includes(reply.status)) clean = false } catch { clean = false } }
  try {
    const loginKeys = [...[...sessions.values()].map(token => 'login:token:' + token), ...Object.values(phones).flatMap(phone => ['login:code:' + phone, 'login:code:throttle:' + phone])]
    await redis('DEL', ...loginKeys)
    if (await redis('EXISTS', ...loginKeys) !== 0) clean = false
  } catch { clean = false }
  if (seeded) {
    let stage = 'owned bindings and worker barriers'
    try {
      for (const voucher of sql(`SELECT id FROM tb_voucher WHERE shop_id IN(${shops.join(',')})`).split(/\s+/).filter(Boolean)) activities.add(voucher)
      const messages = await ownedMessages(), orderIDs = [...new Set([...orders.values()].map(order => order.id).concat(messages.map(message => message.id)))]
      if (!orderIDs.every(canonical) || ![...activities].every(canonical)) throw new Error('Unsafe owned identifiers')
      const activitySQL = [...activities].join(',') || '0', orderSQL = orderIDs.join(',') || '0'
      const orderStrings = orderIDs.length ? orderIDs.map(value => `'${value}'`).join(',') : "''"
      if (sql(`SELECT COUNT(*) FROM tb_voucher WHERE id IN(${activitySQL}) AND shop_id NOT IN(${shops.join(',')})`) !== '0' || sql(`SELECT COUNT(*) FROM tb_seckill_voucher WHERE voucher_id IN(${activitySQL}) AND merchant_id NOT IN(${merchants.join(',')})`) !== '0') throw new Error('Unsafe owned catalog binding')
      if (sql(`SELECT COUNT(*) FROM tb_voucher_order WHERE id IN(${orderSQL}) AND (user_id<>${users.buyer} OR voucher_id NOT IN(${activitySQL}))`) !== '0') throw new Error('Unsafe owned binding')
      const scope = outboxScope([...activities], orderIDs)
      await eventually(async () => {
        if (Number(sql(`SELECT COUNT(*) FROM tb_voucher_order WHERE id IN(${orderSQL}) AND user_id=${users.buyer} AND voucher_id IN(${activitySQL})`)) !== orderIDs.length) return false
        if (sql(`SELECT COUNT(*) FROM tb_outbox WHERE ${scope} AND delivery_status<>'DONE'`) !== '0' || sql(`SELECT COUNT(*) FROM tb_order_review WHERE order_id IN(${orderStrings}) AND status<>'RESOLVED'`) !== '0') return false
        for (const message of messages) if ((await redis('XPENDING', 'stream.orders', 'g1', message.sourceID, message.sourceID, 1)).length) return false
        return true
      }, Boolean, 'Owned messages/outbox did not reach durable completion')
      const eventIDs = sql(`SELECT event_id FROM tb_outbox WHERE ${scope}`).split(/\s+/).filter(Boolean)
      report.workerBarrier = { ownedMessages: messages.length, pendingBeforeCleanup: 0, unfinishedOutbox: 0, unresolvedReview: 0 }
      stage = 'precise SQL rows'
      sql(`START TRANSACTION;
        DELETE FROM tb_order_review WHERE order_id IN(${orderStrings});
        DELETE FROM tb_outbox WHERE ${scope}; DELETE FROM tb_audit_log WHERE merchant_id IN(${merchants.join(',')});
        DELETE FROM tb_voucher_order WHERE id IN(${orderSQL}) AND user_id=${users.buyer};
        DELETE FROM tb_seckill_voucher WHERE voucher_id IN(${activitySQL}); DELETE FROM tb_voucher WHERE id IN(${activitySQL});
        DELETE FROM tb_member_shop_scope WHERE member_id IN(${members.join(',')}); DELETE FROM tb_merchant_member WHERE id IN(${members.join(',')});
        DELETE FROM tb_shop WHERE id IN(${shops.join(',')}); DELETE FROM tb_merchant WHERE id IN(${merchants.join(',')}); DELETE FROM tb_user WHERE id IN(${Object.values(users).join(',')}); COMMIT;`)
      stage = 'acknowledged Stream entries and precise Redis keys'
      for (const message of messages) await redis('XDEL', 'stream.orders', message.sourceID)
      const keys = [...[...activities].flatMap(voucher => ['gate', 'stock', 'begin', 'end', 'order', 'accepted'].map(part => `seckill:${part}:${voucher}`)), ...orderIDs.map(order => `order:reservation:${order}`), ...eventIDs.map(event => `comp:done:${event}`)]
      for (let offset = 0; offset < keys.length; offset += 100) { const batch = keys.slice(offset, offset + 100); await redis('DEL', ...batch); if (await redis('EXISTS', ...batch) !== 0) clean = false }
      clean &&= (await ownedMessages()).length === 0 && sql(`SELECT (SELECT COUNT(*) FROM tb_voucher_order WHERE id IN(${orderSQL}))+(SELECT COUNT(*) FROM tb_voucher WHERE id IN(${activitySQL}))+(SELECT COUNT(*) FROM tb_user WHERE id IN(${Object.values(users).join(',')}))+(SELECT COUNT(*) FROM tb_shop WHERE id IN(${shops.join(',')}))+(SELECT COUNT(*) FROM tb_merchant_member WHERE id IN(${members.join(',')}))+(SELECT COUNT(*) FROM tb_member_shop_scope WHERE member_id IN(${members.join(',')}))+(SELECT COUNT(*) FROM tb_merchant WHERE id IN(${merchants.join(',')}))+(SELECT COUNT(*) FROM tb_outbox WHERE ${scope})`) === '0'
    } catch { clean = false; report.cleanupFailure = `Cleanup stopped at ${stage}; exact owned IDs retained in fixture manifest` }
  }
  report.fixture = { users, merchants, shops, members, activities: [...activities], orders: [...orders.values()].map(({ redeemToken, ...safe }) => safe) }
  report.cleanup = clean
  if (!clean) { report.passed = false; process.exitCode = 1 }
  report.recordedAt = new Date().toISOString()
  await writeFile(output, JSON.stringify(report, null, 2) + '\n')
  process.stdout.write(JSON.stringify({ passed: report.passed, checks: report.checks.length, cleanup: clean, output }) + '\n')
}
