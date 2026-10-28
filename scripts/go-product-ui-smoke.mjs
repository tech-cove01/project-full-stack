// Real Vue/Go/MySQL/Redis browser acceptance; exact, independently owned fixtures.
import assert from 'node:assert/strict'
import { randomBytes, randomInt } from 'node:crypto'
import { spawnSync } from 'node:child_process'
import { mkdir, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'

for (const key of ['API_BASE', 'WEB_BASE', 'MYSQL_HOST', 'MYSQL_PORT', 'MYSQL_DATABASE', 'MYSQL_USER', 'MYSQL_PASSWORD']) {
  if (!process.env[key]) throw new Error(`Explicit isolated ${key} required`)
}
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const API = process.env.API_BASE, WEB = process.env.WEB_BASE
const output = resolve(process.env.UI_EVIDENCE || 'product-ui.json')
await mkdir(dirname(output), { recursive: true })
const prefix = 'RV28' + randomBytes(5).toString('hex')
const base = 9007199254740993n + BigInt(randomInt(30000000, 40000000)) * 100n
const ids = [0n, 1n, 2n].map(n => String(base + n))
const [owner, author, reader] = ids
const shop = owner, merchant = owner, member = owner
const phones = ids.map(() => '138' + randomInt(10000000, 100000000))
const tokens = [], notes = [], documents = [], conversations = []
const report = { passed: false, checks: [], cleanup: false, zone: process.env.BROWSER_TIMEZONE || 'UTC' }
function sql(query) {
  const result = spawnSync(process.env.MYSQL_BIN || 'mysql', ['--protocol=TCP', `--host=${process.env.MYSQL_HOST}`, `--port=${process.env.MYSQL_PORT}`, `--user=${process.env.MYSQL_USER}`, '--default-character-set=utf8mb4', '--batch', '--skip-column-names', process.env.MYSQL_DATABASE], {
    input: `SET NAMES utf8mb4; SET time_zone='+08:00'; ${query}`, encoding: 'utf8', env: { ...process.env, MYSQL_PWD: process.env.MYSQL_PASSWORD },
  })
  if (result.status !== 0 || result.error) throw new Error('Isolated SQL failed; credential diagnostics omitted')
  return result.stdout.trim()
}
async function api(path, index = 0, method = 'GET', body) {
  const response = await fetch(API + path, { method, signal: AbortSignal.timeout(20000), headers: { 'Content-Type': 'application/json', ...(tokens[index] ? { authorization: tokens[index] } : {}) }, ...(body ? { body: JSON.stringify(body) } : {}) })
  const result = await response.json()
  assert.ok(response.ok && result.success, 'real API operation failed')
  return result.data
}
function check(value, message) { assert.ok(value, message); report.checks.push(message) }
let browser, page, seeded = false
try {
  sql(`START TRANSACTION;
    INSERT INTO tb_user(id,phone,nick_name) VALUES ${ids.map((id,i) => `(${id},'${phones[i]}','${prefix}-${i}')`).join(',')};
    INSERT INTO tb_merchant(id,name) VALUES(${merchant},'${prefix}');
    INSERT INTO tb_shop(id,merchant_id,name,type_id,address,x,y) VALUES(${shop},${merchant},'${prefix}',1,'review',120,30);
    INSERT INTO tb_merchant_member(id,merchant_id,user_id,role_code) VALUES(${member},${merchant},${owner},'OWNER'); COMMIT;`)
  seeded = true
  for (let i = 0; i < 3; i++) {
    const sent = await api(`/user/code?phone=${phones[i]}`, i, 'POST')
    const response = await fetch(API + '/user/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ phone: phones[i], code: sent.code }) })
    const body = await response.json(); assert.ok(body.success && typeof body.data === 'string')
    tokens.push(body.data)
  }
  report.capabilities = await api('/ai/capabilities', 2)
  browser = await chromium.launch({ executablePath: process.env.CHROME_PATH, headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1080 }, timezoneId: report.zone })
  await context.tracing.start({ screenshots: true, snapshots: true })
  page = await context.newPage()
  page.on('response', async response => {
    if (response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/ai/conversations') {
      const result = await response.json().catch(() => ({}))
      if (result.data?.id) conversations.push(result.data.id)
    }
  })
  const pageErrors = []
  page.on('pageerror', () => pageErrors.push('uncaught browser error'))
  async function as(index, path) {
    await page.goto(WEB)
    await page.evaluate(token => localStorage.setItem('cityloop.token', token), tokens[index])
    await page.goto(WEB + path)
  }
  report.step = 'native community controls'
  await as(1, '/notes/new')
  await page.getByRole('heading', { name: '发布探店笔记', exact: true }).waitFor()
  const title = page.getByPlaceholder('例如：这家咖啡店的核销很顺畅')
  check(await title.count() === 1 && await title.evaluate(node => node instanceof HTMLInputElement), 'note composer renders a native editable title control')
  await title.fill(prefix + ' 页面发布')
  await page.getByPlaceholder('写下你的到店体验').fill(prefix + ' 真实页面正文')
  const published = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/notes')
  await page.getByRole('button', { name: '发布', exact: true }).click()
  const publishedBody = await (await published).json(); notes.push(publishedBody.data.id)
  await page.waitForURL(url => url.pathname === '/notes/' + notes[0])
  await page.getByRole('heading', { name: prefix + ' 页面发布', exact: true }).waitFor()
  check(sql(`SELECT COUNT(*) FROM tb_blog WHERE id=${notes[0]} AND user_id=${author}`) === '1', 'publishing through Vue creates the exact author-owned note')
  const second = await api('/notes', 1, 'POST', { title: prefix + ' 第二篇', content: '第二篇内容', images: [] }); notes.push(second.id)
  await as(2, '/notes/' + notes[0])
  await page.getByRole('button', { name: '点赞', exact: true }).click()
  await page.getByRole('button', { name: '已点赞', exact: true }).waitFor()
  check(sql(`SELECT active FROM tb_blog_like WHERE blog_id=${notes[0]} AND user_id=${reader}`) === '1', 'Vue like writes the current user fact')
  await page.getByRole('button', { name: '关注作者', exact: true }).click()
  await page.getByRole('button', { name: '已关注', exact: true }).waitFor()
  await page.goto(WEB + '/feed')
  await page.getByRole('link', { name: prefix + ' 页面发布', exact: true }).waitFor()
  check(await page.getByRole('link', { name: prefix + ' 第二篇', exact: true }).count() === 1, 'following author populates the real Feed with both notes')
  await page.screenshot({ path: output.replace(/\.json$/, '') + '-feed.png', fullPage: true })
  report.step = 'note route identity window'
  let held, intercepted
  const observed = new Promise(resolve => { intercepted = resolve })
  await page.route(`**/api/notes/${notes[0]}/like`, async route => {
    const response = await route.fetch(); held = { route, response }; intercepted()
  })
  await page.goto(WEB + '/notes/' + notes[0])
  await observed
  await page.evaluate(async target => { const { default: router } = await import('/src/router.ts'); await router.push(target) }, '/notes/' + notes[1])
  await page.getByRole('heading', { name: prefix + ' 第二篇', exact: true }).waitFor()
  await page.getByRole('button', { name: '点赞', exact: true }).waitFor()
  await held.route.fulfill({ response: held.response }).catch(() => {})
  await page.unroute(`**/api/notes/${notes[0]}/like`)
  // The completed old request must never overwrite the next route's state.
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
  check(await page.getByRole('button', { name: '点赞', exact: true }).count() === 1 && await page.getByRole('button', { name: '已点赞', exact: true }).count() === 0,
    'late like state for the previous note cannot replace the next note state')
  report.step = 'merchant knowledge and assistant'
  await as(0, '/merchant/knowledge')
  // Header and empty state offer the same action; use the header entrypoint.
  const createRule = page.locator('.page-top').getByRole('button', { name: '新建公开规则', exact: true })
  await createRule.waitFor()
  await createRule.click()
  await page.getByPlaceholder('例如：街角咖啡停车规则').fill(prefix + ' 停车规则')
  await page.getByPlaceholder('只写可以被顾客公开看到的规则；不要填写私密资料。').fill('停车免费两小时，超时每小时五元。')
  const draft = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/merchant/knowledge')
  await page.getByRole('button', { name: '保存草稿', exact: true }).click()
  const docBody = await (await draft).json(); documents.push(docBody.data.id)
  const row = page.getByRole('row').filter({ hasText: prefix + ' 停车规则' })
  await row.getByRole('button', { name: '发布', exact: true }).click()
  await row.getByText('已发布 v1', { exact: true }).waitFor()
  await row.getByRole('button', { name: '重建', exact: true }).click()
  await page.getByText('已重建当前公开内容。', { exact: true }).waitFor()
  check(sql(`SELECT published_version FROM tb_ai_document WHERE id=${documents[0]}`) === '1', 'merchant UI creates publishes and rebuilds the current FAQ')
  await row.getByRole('button', { name: '编辑', exact: true }).click()
  check(await page.getByPlaceholder('只写可以被顾客公开看到的规则；不要填写私密资料。').inputValue() === '停车免费两小时，超时每小时五元。', 'editing an existing FAQ preserves its current draft body')
  await page.getByRole('dialog').getByRole('button', { name: '取消', exact: true }).click()
  await as(2, '/assistant?shopId=' + shop)
  const prompt = page.getByPlaceholder('例如：未支付取消后还能再买吗？街角咖啡停车怎么收费？')
  await prompt.waitFor()
  check(report.capabilities.ready === true && report.capabilities.mode === 'stub', 'assistant honestly identifies usable stub mode')
  await page.getByText('当前仅启用确定性 stub，未验证真实模型质量', { exact: true }).waitFor()
  // The deterministic lexical stub validates protocol/citations, not semantic
  // paraphrase quality. Use the published wording; real quality is GO-075.
  await prompt.fill('停车免费两小时，超时每小时五元，是吗？')
  await page.getByRole('button', { name: '发送', exact: true }).click()
  await page.getByRole('link').filter({ hasText: prefix + ' 停车规则' }).waitFor()
  await page.getByRole('button', { name: '发送', exact: true }).waitFor({ state: 'visible' })
  check(await page.getByText('停车免费两小时', { exact: false }).count() > 0, 'real stub SSE renders the published FAQ answer and its citation')
  await page.screenshot({ path: output.replace(/\.json$/, '') + '-assistant.png', fullPage: true })
  check(pageErrors.length === 0, 'no uncaught browser exceptions in the exercised community merchant and AI pages')
  report.passed = true
  await context.tracing.stop() // Do not persist traces containing real login headers.
} catch (error) {
  report.failure = error instanceof Error ? error.message.split('\n')[0].slice(0, 180) : 'browser verification failed'
  if (page) await page.screenshot({ path: output.replace(/\.json$/, '') + '-failure.png', fullPage: true }).catch(() => {})
  process.exitCode = 1
} finally {
  if (browser) await browser.close().catch(() => {})
  let clean = true
  for (const id of conversations) {
    try {
      const response = await fetch(API + '/ai/conversations/' + encodeURIComponent(id), { method: 'DELETE', headers: { authorization: tokens[2] } })
      if (![200,404].includes(response.status)) clean = false
    } catch { clean = false }
  }
  for (let i = 0; i < tokens.length; i++) {
    try { const response = await fetch(API + '/user/logout', { method: 'POST', headers: { authorization: tokens[i] } }); if (![200,401].includes(response.status)) clean = false } catch { clean = false }
  }
  if (seeded) {
    try {
      const blogs = sql(`SELECT id FROM tb_blog WHERE user_id IN (${ids.join(',')})`).split(/\s+/).filter(Boolean)
      const blogIDs = blogs.length ? blogs.join(',') : '0'
      sql(`START TRANSACTION;
        DELETE FROM tb_ai_chunk WHERE document_id IN(SELECT id FROM tb_ai_document WHERE shop_id=${shop});
        DELETE FROM tb_ai_document_version WHERE document_id IN(SELECT id FROM tb_ai_document WHERE shop_id=${shop});
        DELETE FROM tb_ai_document WHERE shop_id=${shop};
        DELETE FROM tb_audit_log WHERE merchant_id=${merchant};
        DELETE FROM tb_community_outbox WHERE (aggregate_type IN('NOTE','LIKE') AND aggregate_id IN (${blogIDs})) OR (aggregate_type='FOLLOW' AND aggregate_id IN (${reader},${author}));
        DELETE FROM tb_blog_like WHERE blog_id IN(${blogIDs}); DELETE FROM tb_blog WHERE id IN(${blogIDs});
        DELETE FROM tb_follow WHERE user_id IN(${ids.join(',')}) OR follow_user_id IN(${ids.join(',')});
        DELETE FROM tb_merchant_member WHERE id=${member}; DELETE FROM tb_shop WHERE id=${shop}; DELETE FROM tb_merchant WHERE id=${merchant}; DELETE FROM tb_user WHERE id IN(${ids.join(',')}); COMMIT;`)
      clean &&= sql(`SELECT (SELECT COUNT(*) FROM tb_user WHERE id IN(${ids.join(',')}))+(SELECT COUNT(*) FROM tb_blog WHERE id IN(${blogIDs}))+(SELECT COUNT(*) FROM tb_ai_document WHERE shop_id=${shop})`) === '0'
    } catch { clean = false }
  }
  report.cleanup = clean
  if (!clean) { report.passed = false; process.exitCode = 1 }
  await writeFile(output, JSON.stringify(report, null, 2) + '\n')
  process.stdout.write(JSON.stringify({ passed: report.passed, checks: report.checks.length, cleanup: report.cleanup }) + '\n')
}
