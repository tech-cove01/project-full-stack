// Real Vue/Go/MySQL browser checks. SQL fixtures are removed by exact IDs.
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
const output = resolve(process.env.UI_EVIDENCE || 'go-shop-ui-result.json')
await mkdir(dirname(output), { recursive: true })
const prefix = `GOUI${randomBytes(4).toString('hex')}`
const base = 9007199254740993n + BigInt(randomInt(1, 10000000)) * 100n
const shopIDs = Array.from({ length: 53 }, (_, i) => String(base + BigInt(i)))
const types = [String(base), String(base + 1n)]
const vouchers = Array.from({ length: 7 }, (_, i) => String(base + BigInt(i)))
const report = { task: 'GO-011', acceptance: ['MIG-GO-02', 'MIG-GO-03', 'PRODUCT-01'], passed: false, checks: [], fixture: { prefix, shops: 53, types: 2 }, cleanup: false }
const check = (value, description) => {
  assert.ok(value, description)
  report.checks.push(description)
}
function sql(statement) {
  const result = spawnSync(process.env.MYSQL_BIN || 'mysql', [
    '--protocol=TCP', `--host=${process.env.MYSQL_HOST}`, `--port=${process.env.MYSQL_PORT}`,
    `--user=${process.env.MYSQL_USER}`, '--default-character-set=utf8mb4',
    '--batch', '--skip-column-names', process.env.MYSQL_DATABASE,
  ], { input: statement, encoding: 'utf8', env: { ...process.env, MYSQL_PWD: process.env.MYSQL_PASSWORD } })
  if (result.error || result.status !== 0) throw new Error('Isolated fixture SQL failed; credentials and SQL diagnostics are omitted')
  return result.stdout.trim()
}
async function api(path) {
  const response = await fetch(`${API}${path}`)
  return { status: response.status, body: await response.json() }
}
let browser
let seeded = false
try {
  const health = await api('/actuator/health')
  check(health.status === 200 && health.body.service === 'project-full-stack-go' && health.body.runtime === 'go', 'direct API identifies Go before creating fixtures')
  const proxied = await (await fetch(`${WEB}/api/actuator/health`)).json()
  check(proxied.runtime === 'go' && proxied.service === health.body.service && proxied.version === health.body.version, 'Vite proxy identifies the same Go service')
  report.service = health.body
  const shopRows = shopIDs.map((id, i) => `(${id},1,'${prefix} 咖啡店 ${String(i).padStart(2, '0')}',${types[i < 27 ? 0 : 1]},'','验收街区','验收路',120.15,30.28,30,48,'09:00-21:00')`).join(',')
  const voucherRows = vouchers.map((id, i) => `(${id},${shopIDs[0]},'${prefix} 券${i}',1900,3000,1,${i === 6 ? 0 : 1})`).join(',')
  const seckillRows = vouchers.map((id, i) => {
    const state = i === 4 ? 'INITIALIZING' : i === 5 ? 'OFFLINE' : 'ACTIVE'
    const begin = i === 0 ? 'DATE_SUB(NOW(),INTERVAL 30 MINUTE)' : i === 1 ? 'DATE_ADD(NOW(),INTERVAL 1 DAY)' : 'DATE_SUB(NOW(),INTERVAL 2 DAY)'
    const end = i === 0 ? 'DATE_ADD(NOW(),INTERVAL 30 MINUTE)' : i === 2 ? 'DATE_SUB(NOW(),INTERVAL 1 DAY)' : 'DATE_ADD(NOW(),INTERVAL 3 DAY)'
    return `(${id},1,${i === 3 ? 0 : 10},10,'${state}',1,${begin},${end},DATE_SUB(NOW(),INTERVAL 2 DAY),DATE_ADD(NOW(),INTERVAL 4 DAY))`
  }).join(',')
  sql(`SET NAMES utf8mb4; SET time_zone='+08:00'; START TRANSACTION;
    INSERT INTO tb_shop_type(id,name,icon,sort) VALUES(${types[0]},'${prefix} 分类A','',100),(${types[1]},'${prefix} 分类B','',100);
    INSERT INTO tb_shop(id,merchant_id,name,type_id,images,area,address,x,y,avg_price,score,open_hours) VALUES ${shopRows};
    INSERT INTO tb_voucher(id,shop_id,title,pay_value,actual_value,type,status) VALUES ${voucherRows};
    INSERT INTO tb_seckill_voucher(voucher_id,merchant_id,stock,total_stock,publish_status,publish_version,begin_time,end_time,redeem_begin_time,redeem_end_time) VALUES ${seckillRows};
    COMMIT;`)
  seeded = true
  browser = await chromium.launch({ ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}), headless: true })
  const timezoneId = process.env.UI_TIMEZONE || 'Asia/Shanghai'
  report.browserTimezone = timezoneId
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, timezoneId })
  const page = await context.newPage()
  const pageErrors = []
  page.on('pageerror', () => pageErrors.push('uncaught page error'))
  const cards = page.locator('.shop-card')
  const search = page.getByPlaceholder('搜索店铺名称')
  const more = () => page.getByRole('button', { name: /加载更多店铺|重试加载店铺/ })
  const count = n => page.waitForFunction(expected => document.querySelectorAll('.shop-card').length === expected, n)
  async function submit(name) {
    await search.fill(name)
    const response = page.waitForResponse(r => new URL(r.url()).pathname === '/api/shop/of/name' && new URL(r.url()).searchParams.get('current') === '1')
    await page.getByRole('button', { name: '找好店', exact: true }).click()
    return (await response).json()
  }
  async function loadAll(expected) {
    while (await cards.count() < expected) {
      const before = await cards.count()
      await more().click()
      await count(Math.min(before + 10, expected))
    }
    await page.getByText('已经看完所有店铺了', { exact: true }).waitFor()
  }
  await page.goto(WEB)
  const categories = page.getByRole('group', { name: '店铺分类' })
  await categories.waitFor()
  const body = await submit(prefix)
  await count(10)
  check(body.success && body.total === '53' && body.data.every(row => typeof row.id === 'string' && row.avgPrice === '30'), 'first page returns string total, exact IDs and avgPrice in yuan')
  await search.fill(`${prefix} 尚未提交`)
  await more().click()
  await count(20)
  check(true, 'unsubmitted input does not change load-more filters')
  await loadAll(53)
  const loaded = await cards.evaluateAll(nodes => nodes.map(node => node.getAttribute('href').split('/').pop()))
  check(JSON.stringify(loaded) === JSON.stringify(shopIDs), '53 shops across six pages have no missing or duplicate IDs and stable order')

  await search.fill(prefix)
  await categories.getByRole('button', { name: `${prefix} 分类A`, exact: true }).click()
  await count(10)
  await loadAll(27)
  const filtered = await cards.evaluateAll(nodes => nodes.map(node => node.getAttribute('href').split('/').pop()))
  check(JSON.stringify(filtered) === JSON.stringify(shopIDs.slice(0, 27)), 'category plus submitted name uses one backend intersection and resets pagination')
  await submit(`${prefix} 不存在`)
  await page.getByText('没有找到相关店铺，换个关键词试试', { exact: true }).waitFor()
  check(await cards.count() === 0, 'no-match search shows the empty state')

  await search.fill(prefix)
  await categories.getByRole('button', { name: '全部', exact: true }).click()
  await count(10)
  await context.setOffline(true)
  await more().click()
  await page.getByRole('alert').filter({ hasText: '无法连接服务' }).waitFor()
  check(await cards.count() === 10, 'failed next page preserves existing results')
  await context.setOffline(false)
  const retryResponse = page.waitForResponse(r => new URL(r.url()).pathname === '/api/shop/of/name' && new URL(r.url()).searchParams.get('current') === '2')
  await more().click()
  await retryResponse
  await count(20)
  check(true, 'load-more retry requests the failed page instead of skipping it')

  // Selective network aborts inject transport failures; successful retries hit
  // the real Go API and real database. No successful API response is mocked.
  await page.route('**/api/shop-type/list', route => route.abort('failed'))
  await page.reload()
  await page.getByRole('button', { name: '重试分类', exact: true }).waitFor()
  await page.locator('.shop-card').first().waitFor()
  await page.unroute('**/api/shop-type/list')
  await page.getByRole('button', { name: '重试分类', exact: true }).click()
  await categories.getByRole('button', { name: `${prefix} 分类A`, exact: true }).waitFor()
  check(true, 'category failure leaves all-shops browsing usable and supports a real retry')

  await page.route('**/api/voucher/list/*', route => route.abort('failed'))
  await page.goto(`${WEB}/shops/${shopIDs[0]}`)
  await page.getByRole('heading', { name: `${prefix} 咖啡店 00`, exact: true }).waitFor()
  await page.getByRole('button', { name: '重试优惠', exact: true }).waitFor()
  await page.unroute('**/api/voucher/list/*')
  await page.getByRole('button', { name: '重试优惠', exact: true }).click()
  await page.locator('.voucher-card').nth(3).waitFor()
  check(await page.locator('.voucher-card').count() === 4, 'voucher retry preserves shop detail and hides INITIALIZING, OFFLINE and disabled vouchers')
  check((await page.locator('.voucher-card').first().getByRole('button').innerText()).trim() === '登录后抢购', 'Shanghai sale window is interpreted correctly in the configured browser timezone')
  await page.getByRole('button', { name: '尚未开始', exact: true }).waitFor()
  await page.getByRole('button', { name: '活动已结束', exact: true }).waitFor()
  await page.getByRole('button', { name: '已抢完', exact: true }).waitFor()
  check((await api(`/voucher/list/${shopIDs[0]}`)).body.data.every(v => v.payValue === '1900' && v.actualValue === '3000'), 'voucher cents stay exact strings and availability states remain visible')
  check(pageErrors.length === 0, 'no uncaught browser errors')
  await page.screenshot({ path: output.replace(/\.json$/, '') + '.png', fullPage: true })
  report.passed = true
} catch (error) {
  report.failure = error instanceof Error ? error.message.split('\n')[0].slice(0, 200) : 'UI check failed'
  process.exitCode = 1
} finally {
  if (browser) await browser.close().catch(() => {})
  if (seeded) {
    try {
      sql(`START TRANSACTION;
        DELETE FROM tb_seckill_voucher WHERE voucher_id IN (${vouchers.join(',')});
        DELETE FROM tb_voucher WHERE id IN (${vouchers.join(',')});
        DELETE FROM tb_shop WHERE id IN (${shopIDs.join(',')});
        DELETE FROM tb_shop_type WHERE id IN (${types.join(',')}); COMMIT;`)
      report.cleanup = true
    } catch {
      report.cleanup = false
      report.passed = false
      process.exitCode = 1
    }
  }
  report.recordedAt = new Date().toISOString()
  await writeFile(output, JSON.stringify(report, null, 2) + '\n')
  console.log(JSON.stringify({ passed: report.passed, checks: report.checks.length, cleanup: report.cleanup, output }))
}
