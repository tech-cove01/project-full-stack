// Real Vue -> Go -> isolated MySQL/Redis. Only explicitly owned fixture IDs/keys.
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
const output = resolve(process.env.UI_EVIDENCE || 'go-activity-ui-result.json')
await mkdir(dirname(output), { recursive: true })
const prefix = `GOA${randomBytes(4).toString('hex')}`
const base = 9007199254740993n + BigInt(randomInt(10000000, 20000000)) * 100n
const id = n => String(base + BigInt(n))
// STAFF merchant sorts before OWNER merchant: the default publish shop must
// come from management scope rather than the first shop in workspace.
const merchants = [id(0), id(1)]
const shops = [id(0), id(1), id(2), id(3)]
const users = [id(0), id(1)]
const members = [id(0), id(1), id(2)]
const phones = new Set()
while (phones.size < users.length) phones.add(`138${randomInt(10000000, 100000000)}`)
const phoneList = [...phones]
const seededActivities = Array.from({ length: 15 }, (_, i) => id(20 + i))
const sessions = []
const report = { task: 'GO-021c', passed: false, checks: [], browserTimeZone: process.env.BROWSER_TIMEZONE || 'UTC', cleanup: false }
function check(value, description) {
  assert.ok(value, description)
  report.checks.push(description)
}
function sql(statement) {
  const result = spawnSync(process.env.MYSQL_BIN || 'mysql', [
    '--protocol=TCP', `--host=${process.env.MYSQL_HOST}`, `--port=${process.env.MYSQL_PORT}`,
    `--user=${process.env.MYSQL_USER}`, '--default-character-set=utf8mb4',
    '--batch', '--skip-column-names', process.env.MYSQL_DATABASE,
  ], { input: statement, encoding: 'utf8', env: { ...process.env, MYSQL_PWD: process.env.MYSQL_PASSWORD } })
  if (result.error || result.status !== 0) throw new Error('Isolated fixture SQL failed; SQL and credential diagnostics omitted')
  return result.stdout.trim()
}
// Only simple RESP replies are needed for exact-key GET/SET/DEL/DECR commands.
async function redis(...args) {
  const commands = []
  if (process.env.REDIS_PASSWORD) commands.push(['AUTH', process.env.REDIS_PASSWORD])
  if (process.env.REDIS_DATABASE) commands.push(['SELECT', process.env.REDIS_DATABASE])
  commands.push(args)
  return await new Promise((resolve, reject) => {
    const socket = createConnection({ host: process.env.REDIS_HOST, port: Number(process.env.REDIS_PORT) })
    let buffer = Buffer.alloc(0), replies = 0, last
    const fail = () => { socket.destroy(); reject(new Error('Isolated fixture Redis command failed')) }
    socket.setTimeout(3000, fail).on('error', fail)
    socket.on('connect', () => socket.write(commands.map(command => `*${command.length}\r\n${command.map(arg => {
      const text = String(arg); return `$${Buffer.byteLength(text)}\r\n${text}\r\n`
    }).join('')}`).join('')))
    socket.on('data', chunk => {
      buffer = Buffer.concat([buffer, chunk])
      while (buffer.length) {
        const end = buffer.indexOf('\r\n')
        if (end < 0) return
        const line = buffer.subarray(1, end).toString()
        let consumed = end + 2
        if (buffer[0] === 36) {
          const length = Number(line)
          if (length >= 0 && buffer.length < consumed + length + 2) return
          last = length === -1 ? null : buffer.subarray(consumed, consumed + length).toString()
          if (length >= 0) consumed += length + 2
        } else if (buffer[0] === 58) last = Number(line)
        else if (buffer[0] === 43) last = line
        else return fail()
        buffer = buffer.subarray(consumed)
        if (++replies === commands.length) { socket.destroy(); resolve(last); return }
      }
    })
  })
}
async function api(path, token, method = 'GET', body, key) {
  const response = await fetch(`${API}${path}`, { method, headers: {
    ...(token ? { authorization: token } : {}),
    ...(body ? { 'Content-Type': 'application/json' } : {}),
    ...(key ? { 'Idempotency-Key': key } : {}),
  }, ...(body ? { body: JSON.stringify(body) } : {}) })
  return { status: response.status, body: await response.json() }
}
async function eventually(read, accept, description) {
  const deadline = Date.now() + 20000
  while (Date.now() < deadline) {
    const value = await read()
    if (accept(value)) return value
    await new Promise(resolve => setTimeout(resolve, 150))
  }
  throw new Error(description)
}
let browser, mainPage, seeded = false
try {
  const health = await api('/actuator/health')
  const proxy = await (await fetch(`${WEB}/api/actuator/health`)).json()
  check(health.status === 200 && proxy.runtime === 'go' && proxy.version === health.body.version,
    'direct and proxied requests identify the same Go service')
  report.service = proxy
  sql(`SET NAMES utf8mb4; SET time_zone='+08:00'; START TRANSACTION;
    INSERT INTO tb_user(id,phone,nick_name) VALUES ${users.map((user, i) => `(${user},'${phoneList[i]}','${prefix} 用户${i}')`).join(',')};
    INSERT INTO tb_merchant(id,name) VALUES(${merchants[0]},'${prefix} B'),(${merchants[1]},'${prefix} A');
    INSERT INTO tb_shop(id,merchant_id,name,type_id,address,x,y) VALUES
      (${shops[0]},${merchants[0]},'${prefix} B1',1,'B1',120,30),
      (${shops[1]},${merchants[0]},'${prefix} B2',1,'B2',120,30),
      (${shops[2]},${merchants[1]},'${prefix} A1',1,'A1',120,30),
      (${shops[3]},${merchants[1]},'${prefix} A2',1,'A2',120,30);
    INSERT INTO tb_merchant_member(id,merchant_id,user_id,role_code) VALUES
      (${members[0]},${merchants[0]},${users[0]},'STAFF'),
      (${members[1]},${merchants[1]},${users[0]},'OWNER'),
      (${members[2]},${merchants[1]},${users[1]},'STAFF');
    INSERT INTO tb_member_shop_scope(member_id,shop_id) VALUES
      (${members[0]},${shops[0]}),(${members[2]},${shops[2]}),(${members[2]},${shops[1]});
    INSERT INTO tb_voucher(id,shop_id,title,pay_value,actual_value,type,status) VALUES
      ${seededActivities.map((activity, i) => `(${activity},${i < 12 ? shops[2] : i === 12 ? shops[3] : i === 13 ? shops[0] : shops[1]},'${prefix} 活动${i}',990,2000,1,1)`).join(',')};
    INSERT INTO tb_seckill_voucher(voucher_id,merchant_id,stock,total_stock,publish_status,publish_version,begin_time,end_time,redeem_begin_time,redeem_end_time) VALUES
      ${seededActivities.map((activity, i) => `(${activity},${i < 13 ? merchants[1] : merchants[0]},100,100,'ACTIVE',1,NOW(),DATE_ADD(NOW(),INTERVAL 1 DAY),NOW(),DATE_ADD(NOW(),INTERVAL 2 DAY))`).join(',')};
    COMMIT;`)
  seeded = true
  for (const activity of seededActivities) {
    await redis('SET', `seckill:stock:${activity}`, 100)
    await redis('SET', `seckill:begin:${activity}`, Date.now() - 60000)
    await redis('SET', `seckill:end:${activity}`, Date.now() + 86400000)
    await redis('HSET', `seckill:gate:${activity}`, 'state', 'OPEN', 'version', 1, 'phase', 'OPEN')
  }
  browser = await chromium.launch({ ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}), headless: true })
  const errors = []
  async function login(index) {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1080 }, timezoneId: report.browserTimeZone })
    const page = await context.newPage()
    page.on('pageerror', () => errors.push('uncaught page error'))
    await page.goto(`${WEB}/merchant`)
    await page.getByPlaceholder('请输入手机号').fill(phoneList[index])
    await page.getByRole('button', { name: '获取验证码', exact: true }).click()
    await page.waitForFunction(() => /^\d{6}$/.test(document.querySelector('input[placeholder="6 位验证码"]')?.value || ''))
    const loaded = page.waitForResponse(r => new URL(r.url()).pathname === '/api/merchant/workspace')
    await page.getByRole('button', { name: '登录并继续', exact: true }).click()
    await loaded
    const token = await page.evaluate(() => localStorage.getItem('cityloop.token'))
    if (typeof token === 'string') sessions.push(token)
    check(typeof token === 'string', `fixture user ${index} logs in through real Vue`)
    return { page, token }
  }
  const mixed = await login(0)
  const { page, token } = mixed
  mainPage = page
  await page.getByRole('link', { name: '活动管理', exact: true }).click()
  await page.waitForURL(/\/merchant\/activities/)
  await page.getByRole('button', { name: '发布活动', exact: true }).waitFor()
  const rows = () => page.locator('tr.activity-row')
  const row = activity => rows().filter({ has: page.locator(`[data-activity-id="${activity}"]`) })
  await eventually(() => rows().count(), count => count === 10, 'first page did not load')
  const firstPage = await page.locator('[data-activity-id]').evaluateAll(nodes => nodes.map(node => node.dataset.activityId))
  check(firstPage.length === 10 && firstPage.every(value => typeof value === 'string' && BigInt(value) > 9007199254740992n),
    'first page has ten activities and preserves large string IDs')
  check(await row(seededActivities[13]).getByRole('button').count() === 0,
    'OWNER-A plus STAFF-B has no management buttons on the B1 row')
  check(await row(seededActivities[12]).getByRole('button', { name: '下架', exact: true }).count() === 1,
    'the same mixed account can manage an A2 row')
  check((await api('/merchant/activities?current=1&pageSize=50', token)).body.total === '14',
    'mixed roles list only A1 A2 B1; unscoped B2 is excluded and total is a string')
  await page.getByRole('button', { name: '下一页', exact: true }).click()
  await eventually(() => rows().count(), count => count === 4, 'second page did not load')
  const secondPage = await page.locator('[data-activity-id]').evaluateAll(nodes => nodes.map(node => node.dataset.activityId))
  check(new Set([...firstPage, ...secondPage]).size === 14 && await page.getByRole('button', { name: '下一页', exact: true }).isDisabled(),
    'two real pages cover all fourteen authorized activities without overlap')
  async function select(name, option) {
    report.step = `select ${name}`
    await page.locator('.el-select').filter({ has: page.getByRole('combobox', { name, exact: true }) }).click()
    report.step = `option ${name}`
    await page.getByRole('option').filter({ hasText: option }).click()
  }
  await select('筛选门店', `${prefix} B1（${shops[0]}）`)
  await eventually(() => rows().count(), count => count === 1, 'B1 filter did not load')
  check(await row(seededActivities[13]).count() === 1 && await page.getByText('第 1 页', { exact: false }).count() > 0,
    'shop filter reaches the backend and resets pagination to page one')
  await select('筛选状态', '已下架')
  await page.getByText('当前筛选下没有活动', { exact: true }).waitFor()
  check(true, 'status filter shows the real empty state')
  await select('筛选状态', '全部状态')
  await eventually(() => rows().count(), count => count === 1, 'B1 status reset did not load')
  await page.getByRole('button', { name: '发布活动', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '发布门店活动', exact: true })
  await dialog.waitFor()
  check((await dialog.locator('.el-select__selected-item').allTextContents()).join(' ').includes(`${prefix} A1`),
    'publish defaults to the first manageable shop even when STAFF merchant sorts first')
  const defaultBegin = await page.locator('#publish-begin').inputValue()
  check(Math.abs(Date.parse(`${defaultBegin}+08:00`) - (Date.now() - 60000)) < 15000,
    'default datetime is Shanghai business time in this browser time zone')
  await page.locator('#publish-title').fill(`${prefix} 新发布`)
  await page.locator('#publish-price').fill('90071992547409.93')
  await page.locator('#publish-value').fill('90071992547409.94')
  let firstAttempt, secondAttempt, created, routeFailure, lostResponse = false
  const capture = request => ({ key: request.headers()['idempotency-key'], body: request.postDataJSON() })
  await page.route('**/api/merchant/activities', async route => {
    try {
      if (route.request().method() !== 'POST') return await route.continue()
      if (!lostResponse) {
        firstAttempt = capture(route.request())
        const response = await route.fetch({ timeout: 15000 })
        const result = await response.json()
        assert.equal(response.status(), 200, 'first publish must reach the real Go API')
        created = result.data
        lostResponse = true
        return await route.abort('failed') // Lose only the response after the real commit.
      }
      secondAttempt = capture(route.request())
      return await route.continue()
    } catch {
      // Playwright exceptions can contain request headers. Never print them.
      routeFailure = true
      await route.abort('failed').catch(() => {})
    }
  })
  await dialog.getByRole('button', { name: '发布', exact: true }).click()
  await dialog.getByRole('button', { name: '重试本次发布', exact: true }).waitFor()
  await eventually(async () => { if (routeFailure) throw new Error('Real publish proxy request failed'); return lostResponse }, value => value, 'real publish response was not intercepted')
  await eventually(() => dialog.getByRole('button', { name: '重试本次发布', exact: true }).isEnabled(), value => value, 'lost response did not finish the attempt')
  await eventually(() => page.locator('#publish-title').isDisabled(), value => value, 'failed attempt was not locked')
  check(firstAttempt?.body.payValue === '9007199254740993' && firstAttempt.body.actualValue === '9007199254740994'
    && ['beginTime', 'endTime', 'redeemBeginTime', 'redeemEndTime'].every(name => firstAttempt.body[name].endsWith('+08:00')),
    'real publish sends exact cents above 2^53 and four explicit Shanghai offsets')
  check(await page.locator('#publish-title').isDisabled() && created?.shopId === shops[2],
    'lost committed response keeps immutable content and targets the manageable shop')
  await dialog.getByRole('button', { name: '暂时关闭', exact: true }).click()
  await page.getByRole('button', { name: '继续上次发布', exact: true }).click()
  const replay = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/merchant/activities')
  await dialog.getByRole('button', { name: '重试本次发布', exact: true }).click()
  const replayResult = await (await replay).json()
  await dialog.waitFor({ state: 'hidden' })
  check(secondAttempt?.key === firstAttempt.key && JSON.stringify(secondAttempt.body) === JSON.stringify(firstAttempt.body)
    && replayResult.data.id === created.id,
    'closing and reopening retries the original request key and content and returns the same activity')
  check(sql(`SELECT COUNT(*) FROM tb_voucher WHERE shop_id=${shops[2]} AND title='${prefix} 新发布'`) === '1'
    && sql(`SELECT COUNT(*) FROM tb_audit_log WHERE resource_id='${created.id}' AND action='ACTIVITY_CREATE' AND merchant_id=${merchants[1]}`) === '1',
    'lost-response replay creates exactly one activity and one creation audit')
  await page.unroute('**/api/merchant/activities')
  await eventually(() => api(`/merchant/activities?shopId=${shops[2]}&pageSize=50`, token), r => r.body.data?.find(item => item.id === created.id)?.ready,
    'real worker did not publish the committed activity')
  async function refresh() {
    await page.getByRole('button', { name: '刷新授权与活动', exact: true }).click()
    await eventually(() => rows().count(), count => count > 0, 'activity refresh did not complete')
  }
  await refresh()
  await row(created.id).getByText('已发布', { exact: true }).waitFor()
  check(sql(`SELECT COUNT(*) FROM tb_outbox WHERE aggregate_type='ACTIVITY' AND aggregate_id='${created.id}' AND delivery_status='DONE' AND event_type IN ('ACTIVITY_PREPARE','ACTIVITY_OPEN')`) === '2'
    && sql(`SELECT COUNT(*) FROM tb_audit_log WHERE resource_id='${created.id}' AND action='ACTIVITY_ACTIVE' AND merchant_id=${merchants[1]}`) === '1',
    'real worker commits ACTIVE with one activation audit and delivers PREPARE and OPEN')
  await redis('SET', `seckill:stock:${created.id}`, 97)
  const retry = await api(`/merchant/activities/${created.id}/retry-publication`, token, 'POST')
  const replayCreate = await api('/merchant/activities', token, 'POST', firstAttempt.body, firstAttempt.key)
  check(retry.status === 200 && replayCreate.body.data.id === created.id && await redis('GET', `seckill:stock:${created.id}`) === '97',
    'retry and original create replay preserve already consumed Redis inventory at 97')
  const forbidden = await api('/merchant/activities', token, 'POST', { ...firstAttempt.body, shopId: shops[0] }, `${prefix}-forbidden`)
  check(forbidden.status === 403
    && (await api(`/merchant/activities/${seededActivities[13]}/retry-publication`, token, 'POST')).status === 403
    && (await api(`/merchant/activities/${seededActivities[13]}/offline`, token, 'POST')).status === 403,
    'server rejects create retry and offline on this account\'s STAFF shop')
  await redis('DEL', `seckill:stock:${created.id}`)
  await refresh()
  await row(created.id).getByRole('button', { name: '重试发布', exact: true }).waitFor()
  const retryUI = page.waitForResponse(r => new URL(r.url()).pathname.endsWith(`/${created.id}/retry-publication`))
  await row(created.id).getByRole('button', { name: '重试发布', exact: true }).click()
  await retryUI
  await eventually(() => row(created.id).getByRole('button', { name: '下架', exact: true }).isEnabled(), value => value, 'retry did not complete')
  check(await redis('GET', `seckill:stock:${created.id}`) === null,
    'retrying an ACTIVE activity with missing inventory never reconstructs stock')
  await row(created.id).getByRole('button', { name: '下架', exact: true }).click()
  const offlined = page.waitForResponse(r => new URL(r.url()).pathname.endsWith(`/${created.id}/offline`))
  await page.getByRole('button', { name: '确定下架', exact: true }).click()
  check((await (await offlined).json()).data.publishStatus === 'OFFLINE', 'real UI offline returns only after synchronous close and DB commit')
  await row(created.id).getByText('已下架', { exact: true }).waitFor()
  sql(`UPDATE tb_outbox SET delivery_status='PENDING',next_attempt_at=NOW(3),lease_until=NULL WHERE aggregate_type='ACTIVITY' AND aggregate_id='${created.id}' AND event_type='ACTIVITY_OPEN' AND aggregate_version=1;`)
  await eventually(() => sql(`SELECT delivery_status FROM tb_outbox WHERE aggregate_type='ACTIVITY' AND aggregate_id='${created.id}' AND event_type='ACTIVITY_OPEN' AND aggregate_version=1`), value => value === 'DONE', 'old OPEN replay did not complete')
  check(await redis('HGET', `seckill:gate:${created.id}`, 'state') === 'CLOSED'
    && await redis('HGET', `seckill:gate:${created.id}`, 'version') === '2'
    && await redis('HGET', `seckill:gate:${created.id}`, 'phase') === 'OFFLINE',
    'a late real OPEN event cannot reopen the newer offline gate')
  await page.screenshot({ path: output.replace(/\.json$/, '') + '-offline.png', fullPage: true, animations: 'disabled' })
  sql(`UPDATE tb_merchant_member SET status='DISABLED' WHERE id=${members[1]}`)
  await refresh()
  await eventually(() => rows().count(), count => count === 1, 'revoked owner scope remained visible')
  check(await page.getByRole('button', { name: '发布活动', exact: true }).count() === 0
    && await page.locator(`[data-shop-id="${shops[2]}"]`).count() === 0
    && (await api('/merchant/activities', token, 'POST', firstAttempt.body, `${prefix}-disabled`)).status === 403
    && (await api('/user/me', token)).status === 200,
    'disabling a membership removes old-token activity scope and management without invalidating login')
  sql(`UPDATE tb_merchant_member SET status='ACTIVE' WHERE id=${members[1]}`)
  await refresh()
  await page.route('**/api/merchant/activities?*', route => route.abort('failed'))
  await page.getByRole('button', { name: '刷新授权与活动', exact: true }).click()
  await page.getByText('暂时无法加载活动', { exact: true }).waitFor()
  check(await rows().count() === 0 && await page.getByRole('button', { name: '发布活动', exact: true }).count() === 0,
    'failed activity refresh clears old rows and management controls')
  await page.unroute('**/api/merchant/activities?*')
  await page.getByRole('button', { name: '重新加载', exact: true }).click()
  await eventually(() => rows().count(), count => count > 0, 'error retry did not recover')
  check(true, 'error retry restores rows from the real API')
  await page.screenshot({ path: output.replace(/\.json$/, '') + '-mixed.png', fullPage: true, animations: 'disabled' })
  const staff = await login(1)
  await staff.page.getByRole('link', { name: '活动管理', exact: true }).click()
  await eventually(() => staff.page.locator('tr.activity-row').count(), count => count > 0, 'STAFF activity page did not load')
  check(await staff.page.getByRole('button', { name: '发布活动', exact: true }).count() === 0
    && await staff.page.locator('tr.activity-row').getByRole('button').count() === 0
    && (await api('/merchant/activities?pageSize=50', staff.token)).body.data.every(item => item.shopId === shops[2]),
    'STAFF page is read-only and dirty cross-merchant scope does not appear')
  await redis('DEL', `seckill:stock:${seededActivities[11]}`)
  await refresh()
  let listRequests = 0
  page.on('request', request => { if (new URL(request.url()).pathname === '/api/merchant/activities') listRequests++ })
  await page.getByRole('link', { name: '← 返回工作台', exact: true }).click()
  await page.getByRole('heading', { name: '商家工作台', exact: true }).waitFor()
  const atUnmount = listRequests
  await new Promise(resolve => setTimeout(resolve, 1900))
  check(listRequests === atUnmount, 'leaving the page cancels the pending activity poll')
  check(errors.length === 0, 'no uncaught browser errors')
  report.passed = true
} catch (error) {
  report.failure = error instanceof Error ? error.message.split('\n')[0].slice(0, 180) : 'UI verification failed'
  if (mainPage) {
    await mainPage.screenshot({ path: output.replace(/\.json$/, '') + '-failure.png', fullPage: true, animations: 'disabled' }).catch(() => {})
    report.controls = await mainPage.locator('[role="combobox"]').evaluateAll(nodes => nodes.map(node => ({
      id: node.id, label: node.getAttribute('aria-label'), expanded: node.getAttribute('aria-expanded'),
    }))).catch(() => [])
  }
  process.exitCode = 1
} finally {
  if (browser) await browser.close().catch(() => {})
  let clean = true
  for (const token of sessions) {
    try { const r = await api('/user/logout', token, 'POST'); if (r.status !== 200 && r.status !== 401) clean = false } catch { clean = false }
  }
  if (seeded) {
    try {
      const owned = sql(`SELECT id FROM tb_voucher WHERE shop_id IN (${shops.join(',')}) ORDER BY id`).split(/\s+/).filter(Boolean)
      if (owned.length) {
        sql(`START TRANSACTION;
          DELETE FROM tb_outbox WHERE aggregate_type='ACTIVITY' AND aggregate_id IN (${owned.map(value => `'${value}'`).join(',')});
          DELETE FROM tb_audit_log WHERE merchant_id IN (${merchants.join(',')});
          DELETE FROM tb_seckill_voucher WHERE voucher_id IN (${owned.join(',')});
          DELETE FROM tb_voucher WHERE id IN (${owned.join(',')}); COMMIT;`)
        // Delete durable producers before keys, and wait for any worker's row
        // lock to drain. A late CLOSE cannot recreate a gate after cleanup.
        for (const activity of owned) {
          const keys = ['gate', 'stock', 'begin', 'end'].map(part => `seckill:${part}:${activity}`)
          await redis('DEL', ...keys)
          if (await redis('EXISTS', ...keys) !== 0) clean = false
        }
      }
      sql(`START TRANSACTION;
        DELETE FROM tb_member_shop_scope WHERE member_id IN (${members.join(',')});
        DELETE FROM tb_merchant_member WHERE id IN (${members.join(',')});
        DELETE FROM tb_shop WHERE id IN (${shops.join(',')});
        DELETE FROM tb_merchant WHERE id IN (${merchants.join(',')});
        DELETE FROM tb_user WHERE id IN (${users.join(',')}); COMMIT;`)
      clean &&= sql(`SELECT (SELECT COUNT(*) FROM tb_shop WHERE id IN (${shops.join(',')}))+(SELECT COUNT(*) FROM tb_voucher WHERE shop_id IN (${shops.join(',')}))+(SELECT COUNT(*) FROM tb_merchant WHERE id IN (${merchants.join(',')}))`) === '0'
    } catch { clean = false }
  }
  report.cleanup = clean
  if (!clean) { report.passed = false; process.exitCode = 1 }
  await writeFile(output, JSON.stringify(report, null, 2) + '\n')
  process.stdout.write(JSON.stringify({ passed: report.passed, checks: report.checks.length, cleanup: report.cleanup, output }) + '\n')
}
