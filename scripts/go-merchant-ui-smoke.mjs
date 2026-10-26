// Real Vue -> Go -> MySQL/Redis. Changes and cleanup target fixture IDs only.
import assert from 'node:assert/strict'
import { randomBytes, randomInt } from 'node:crypto'
import { spawnSync } from 'node:child_process'
import { mkdir, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'

for (const key of ['MYSQL_HOST', 'MYSQL_PORT', 'MYSQL_DATABASE', 'MYSQL_USER', 'MYSQL_PASSWORD']) {
  if (!process.env[key]) throw new Error(`Explicit isolated ${key} is required`)
}
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const WEB = process.env.WEB_BASE || 'http://127.0.0.1:5174'
const API = process.env.API_BASE || 'http://127.0.0.1:8082'
const output = resolve(process.env.UI_EVIDENCE || 'go-merchant-ui-result.json')
await mkdir(dirname(output), { recursive: true })
const prefix = `GOM${randomBytes(4).toString('hex')}`
const base = 9007199254740993n + BigInt(randomInt(1, 10000000)) * 100n
const id = n => String(base + BigInt(n))
const merchants = [id(0), id(1), id(2)]
const shops = [id(0), id(1), id(2), id(3), id(4)]
const users = [id(0), id(1), id(2), id(3)]
const members = [id(0), id(1), id(2), id(3)]
const phones = new Set()
while (phones.size < users.length) phones.add(`137${randomInt(10000000, 100000000)}`)
const phoneList = [...phones]
const sessions = []
const report = { task: 'GO-020', acceptance: ['P1-6 (workspace subset)', 'MIG-GO-03'], passed: false, checks: [], fixture: { merchants: 3, shops: 5, users: 4 }, cleanup: false }
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
async function api(path, token, method = 'GET') {
  const response = await fetch(`${API}${path}`, { method, headers: token ? { authorization: token } : {} })
  return { status: response.status, body: await response.json() }
}
let browser
let seeded = false
try {
  const health = await api('/actuator/health')
  const proxied = await (await fetch(`${WEB}/api/actuator/health`)).json()
  check(health.status === 200 && health.body.runtime === 'go' && proxied.runtime === 'go'
    && health.body.service === 'project-full-stack-go' && proxied.version === health.body.version,
  'direct and proxied requests identify the same Go service')
  report.service = proxied
  check((await api('/merchant/workspace')).status === 401, 'anonymous workspace request is 401')
  sql(`SET NAMES utf8mb4; SET time_zone='+08:00'; START TRANSACTION;
    INSERT INTO tb_user(id,phone,nick_name) VALUES ${users.map((user, i) => `(${user},'${phoneList[i]}','${prefix} 用户${i}')`).join(',')};
    INSERT INTO tb_merchant(id,name) VALUES(${merchants[0]},'${prefix} 商家A'),(${merchants[1]},'${prefix} 商家B'),(${merchants[2]},'${prefix} 商家C');
    INSERT INTO tb_shop(id,merchant_id,name,type_id,address,x,y) VALUES
      (${shops[0]},${merchants[0]},'${prefix} A1',1,'A1地址',120,30),
      (${shops[1]},${merchants[0]},'${prefix} A2',1,'A2地址',120,30),
      (${shops[2]},${merchants[1]},'${prefix} B1',1,'B1地址',120,30),
      (${shops[3]},${merchants[1]},'${prefix} B2',1,'B2地址',120,30),
      (${shops[4]},${merchants[2]},'${prefix} C1',1,'C1地址',120,30);
    INSERT INTO tb_merchant_member(id,merchant_id,user_id,role_code) VALUES
      (${members[0]},${merchants[0]},${users[0]},'OWNER'),
      (${members[1]},${merchants[1]},${users[0]},'STAFF'),
      (${members[2]},${merchants[0]},${users[1]},'STAFF'),
      (${members[3]},${merchants[0]},${users[2]},'MANAGER');
    INSERT INTO tb_member_shop_scope(member_id,shop_id) VALUES
      (${members[1]},${shops[2]}),(${members[2]},${shops[0]}),(${members[2]},${shops[3]});
    COMMIT;`)
  seeded = true
  browser = await chromium.launch({ ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}), headless: true })
  const errors = []
  async function login(index) {
    const context = await browser.newContext({ viewport: { width: 1280, height: 1000 } })
    const page = await context.newPage()
    page.on('pageerror', () => errors.push('uncaught page error'))
    await page.goto(`${WEB}/merchant`)
    await page.getByRole('heading', { name: '手机号登录', exact: true }).waitFor()
    await page.getByPlaceholder('请输入手机号').fill(phoneList[index])
    await page.getByRole('button', { name: '获取验证码', exact: true }).click()
    await page.waitForFunction(() => /^\d{6}$/.test(document.querySelector('input[placeholder="6 位验证码"]')?.value || ''))
    const response = page.waitForResponse(r => new URL(r.url()).pathname === '/api/merchant/workspace')
    await page.getByRole('button', { name: '登录并继续', exact: true }).click()
    const body = await (await response).json()
    const token = await page.evaluate(() => localStorage.getItem('cityloop.token'))
    if (typeof token === 'string') sessions.push(token)
    check(body.success && typeof token === 'string' && /^[a-f0-9]{32}$/.test(token), `fixture user ${index} logs in through real Vue`)
    return { page, token, body }
  }
  const mixed = await login(0)
  const page = mixed.page
  const section = merchant => page.locator(`.merchant-workspace[data-merchant-id="${merchant}"]`)
  const row = shop => page.locator(`.merchant-shop[data-shop-id="${shop}"]`)
  await row(shops[2]).waitFor()
  check(mixed.body.data.merchants.length === 2
    && mixed.body.data.merchants[0].id === merchants[0]
    && mixed.body.data.merchants[0].role === 'OWNER'
    && mixed.body.data.merchants[1].role === 'STAFF'
    && mixed.body.data.merchants[0].shops.map(s => s.id).join(',') === shops.slice(0, 2).join(',')
    && mixed.body.data.merchants[1].shops.map(s => s.id).join(',') === shops[2],
  'mixed OWNER-A and STAFF-B see exactly A1 A2 B1 with string IDs')
  check(await section(merchants[0]).getByText('管理门店', { exact: true }).count() === 2
    && await section(merchants[1]).getByText('授权门店', { exact: true }).count() === 1
    && await row(shops[3]).count() === 0 && await row(shops[4]).count() === 0,
  'roles and scope labels remain specific to each merchant')
  check(await page.getByRole('link', { name: '活动管理', exact: true }).getAttribute('href') === '/merchant/activities',
    'GO-021 exposes the activities entry')
  for (const label of ['订单与核销 · 稍后开放', '公开规则 · 稍后开放']) {
    check(await page.getByRole('button', { name: label, exact: true }).isDisabled(), `unavailable entry ${label} is disabled`)
  }
  const forged = await api(`/merchant/workspace?userId=${users[2]}&merchantId=${merchants[2]}&shopId=${shops[3]}&role=OWNER`, mixed.token)
  check(forged.status === 200 && JSON.stringify(forged.body.data) === JSON.stringify(mixed.body.data), 'client user merchant shop and role parameters cannot change authorization')
  await page.screenshot({ path: output.replace(/\.json$/, '') + '-mixed.png', fullPage: true, animations: 'disabled' })
  report.roleRendering = await page.locator('.merchant-role').evaluateAll(tags => tags.map(tag => {
    const style = getComputedStyle(tag)
    const bounds = tag.getBoundingClientRect()
    return { text: tag.textContent, color: style.color, fontSize: style.fontSize, width: bounds.width, height: bounds.height }
  }))
  async function refresh() {
    const response = page.waitForResponse(r => new URL(r.url()).pathname === '/api/merchant/workspace')
    await page.getByRole('button', { name: '刷新授权', exact: true }).click()
    return (await response).json()
  }
  sql(`UPDATE tb_merchant_member SET status='DISABLED' WHERE id=${members[0]};`)
  let body = await refresh()
  await section(merchants[0]).waitFor({ state: 'detached' })
  check(body.data.merchants.length === 1 && body.data.merchants[0].id === merchants[1]
    && (await api('/user/me', mixed.token)).status === 200,
  'disabled A membership immediately disappears while the original login remains valid')
  sql(`DELETE FROM tb_member_shop_scope WHERE member_id=${members[1]} AND shop_id=${shops[2]};`)
  body = await refresh()
  await section(merchants[1]).locator('.merchant-empty-shops').waitFor()
  check(body.data.merchants[0].shops.length === 0 && await row(shops[2]).count() === 0, 'scope revocation removes the old shop and keeps an empty membership')
  sql(`INSERT INTO tb_member_shop_scope(member_id,shop_id) VALUES(${members[1]},${shops[2]});
    UPDATE tb_merchant_member SET role_code='OWNER' WHERE id=${members[1]};`)
  body = await refresh()
  await row(shops[3]).waitFor()
  check(body.data.merchants[0].role === 'OWNER' && body.data.merchants[0].shops.length === 2, 'role change is loaded without a new token')
  sql(`UPDATE tb_merchant_member SET role_code='STAFF' WHERE id=${members[1]};`)
  body = await refresh()
  await row(shops[3]).waitFor({ state: 'detached' })
  check(body.data.merchants[0].role === 'STAFF' && body.data.merchants[0].shops.length === 1, 'downgrade to STAFF immediately removes the unscoped shop')
  sql(`UPDATE tb_merchant SET status='DISABLED' WHERE id=${merchants[1]};`)
  body = await refresh()
  await page.getByText('当前账号还没有商家或门店授权', { exact: true }).waitFor()
  check(body.data.merchants.length === 0 && (await api('/user/me', mixed.token)).status === 200, 'disabled merchant removes business authorization without revoking the original login')
  sql(`UPDATE tb_merchant_member SET status='ACTIVE' WHERE id=${members[0]};
    UPDATE tb_merchant SET status='ACTIVE' WHERE id=${merchants[1]};`)
  await refresh()
  await row(shops[0]).waitFor()
  await page.route('**/api/merchant/workspace*', route => route.abort('failed'))
  await page.getByRole('button', { name: '刷新授权', exact: true }).click()
  await page.getByText('暂时无法加载工作台', { exact: true }).waitFor()
  check(await page.locator('.merchant-workspace').count() === 0, 'failed refresh clears the previous authorization display')
  await page.unroute('**/api/merchant/workspace*')
  const retry = page.waitForResponse(r => new URL(r.url()).pathname === '/api/merchant/workspace')
  await page.getByRole('button', { name: '重新加载', exact: true }).click()
  await retry
  await row(shops[0]).waitFor()
  check(true, 'retry restores current authorization from the real Go API')

  const staff = await login(1)
  check(staff.body.data.merchants.length === 1
    && staff.body.data.merchants[0].shops.map(s => s.id).join(',') === shops[0],
  'STAFF-A sees A1 only and a dirty scope to B2 grants nothing')
  const manager = await login(2)
  check(manager.body.data.merchants.length === 1 && manager.body.data.merchants[0].role === 'MANAGER'
    && manager.body.data.merchants[0].shops.map(s => s.id).join(',') === shops.slice(0, 2).join(','),
  'MANAGER-A sees both A shops and no other merchant')
  const ordinary = await login(3)
  await ordinary.page.getByText('当前账号还没有商家或门店授权', { exact: true }).waitFor()
  check(ordinary.body.data.merchants.length === 0, 'ordinary authenticated user sees an empty workspace')
  await api('/user/logout', mixed.token, 'POST')
  await page.getByRole('button', { name: '刷新授权', exact: true }).click()
  await page.waitForURL(/\/login(?:\?|$)/)
  check(await page.evaluate(() => !localStorage.getItem('cityloop.token')), 'revoked login triggers 401 and clears the client identity')
  check(errors.length === 0, 'no uncaught browser errors')
  report.passed = true
} catch (error) {
  // Do not dump phone numbers, code fields, body, headers or session values.
  report.failure = error instanceof Error ? error.message.split('\n')[0].slice(0, 180) : 'UI verification failed'
  process.exitCode = 1
} finally {
  if (browser) await browser.close().catch(() => {})
  let sessionsCleaned = true
  for (const token of sessions) {
    try {
      const reply = await api('/user/logout', token, 'POST')
      if (reply.status !== 200 && reply.status !== 401) sessionsCleaned = false
    } catch { sessionsCleaned = false }
  }
  if (seeded) {
    try {
      sql(`START TRANSACTION;
        DELETE FROM tb_member_shop_scope WHERE member_id IN (${members.join(',')});
        DELETE FROM tb_merchant_member WHERE id IN (${members.join(',')});
        DELETE FROM tb_shop WHERE id IN (${shops.join(',')});
        DELETE FROM tb_merchant WHERE id IN (${merchants.join(',')});
        DELETE FROM tb_user WHERE id IN (${users.join(',')}); COMMIT;`)
      report.cleanup = sessionsCleaned
    } catch { report.cleanup = false }
    if (!report.cleanup) { report.passed = false; process.exitCode = 1 }
  }
  report.recordedAt = new Date().toISOString()
  await writeFile(output, JSON.stringify(report, null, 2) + '\n')
  console.log(JSON.stringify({ passed: report.passed, checks: report.checks.length, cleanup: report.cleanup, output }))
}
