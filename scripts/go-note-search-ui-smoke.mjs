// Actual Vue -> Go -> MySQL/Kafka/Elasticsearch browser checks. Successful API
// responses are never mocked. SQL/Redis/ES cleanup is restricted to owned IDs.
import assert from 'node:assert/strict'
import { randomBytes, randomInt } from 'node:crypto'
import { spawnSync } from 'node:child_process'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { createConnection } from 'node:net'
import { dirname, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

for (const key of ['API_BASE', 'WEB_BASE', 'MYSQL_HOST', 'MYSQL_PORT', 'MYSQL_DATABASE', 'MYSQL_USER', 'MYSQL_PASSWORD', 'REDIS_HOST', 'REDIS_PORT', 'ES_URL', 'ES_INDEX', 'KAFKA_CONTAINER']) {
  if (!process.env[key]) throw new Error(`Explicit isolated ${key} is required`)
}
const specifier = process.env.PLAYWRIGHT_MODULE || 'playwright'
const { chromium } = await import(/^[A-Za-z]:[\\/]/.test(specifier) || specifier.startsWith('.') ? pathToFileURL(resolve(specifier)).href : specifier)
const API = process.env.API_BASE.replace(/\/$/, ''), WEB = process.env.WEB_BASE.replace(/\/$/, '')
const ES = process.env.ES_URL.replace(/\/$/, ''), INDEX = process.env.ES_INDEX
assert.match(INDEX, /^[a-z][a-z0-9_-]{0,62}$/, 'explicit isolated search index required')
const kafkaContainer = process.env.KAFKA_CONTAINER, kafkaTopic = process.env.KAFKA_TOPIC || 'community.events'
assert.match(kafkaContainer, /^[A-Za-z0-9][A-Za-z0-9_.-]+$/, 'explicit isolated Kafka container required')
const kafkaInternalPort = process.env.KAFKA_INTERNAL_PORT || '9094'
assert.ok(/^\d+$/.test(kafkaInternalPort) && Number(kafkaInternalPort) >= 1 && Number(kafkaInternalPort) <= 65535, 'valid dedicated Kafka internal listener port required')
const output = resolve(process.env.UI_EVIDENCE || 'note-search-ui.json')
await mkdir(dirname(output), { recursive: true })
const cleanupFrom = process.env.UI_CLEANUP_FROM ? JSON.parse(await readFile(resolve(process.env.UI_CLEANUP_FROM), 'utf8')).fixture : null
if (cleanupFrom) assert.ok(Array.isArray(cleanupFrom.notes) && cleanupFrom.notes.length <= 50 && Array.isArray(cleanupFrom.shops) && cleanupFrom.shops.length === 2 && [cleanupFrom.user, ...cleanupFrom.shops, ...cleanupFrom.notes].every(id => typeof id === 'string' && /^[1-9]\d{0,19}$/.test(id)), 'controlled cleanup requires exact synthetic fixture IDs')
const suffix = randomBytes(5).toString('hex'), prefix = `NS076${suffix}`
const user = cleanupFrom?.user || String(9007199254740993n + BigInt(randomInt(40000000, 50000000)) * 100n)
const merchant = user, shops = cleanupFrom?.shops || [String(BigInt(user) + 1n), String(BigInt(user) + 2n)]
const phone = cleanupFrom ? sql(`SELECT phone FROM tb_user WHERE id=${user}`) : `138${randomInt(10000000, 100000000)}`
const notes = cleanupFrom?.notes || [], tokens = []
const report = { task: 'GO-076', passed: false, checks: [], cleanup: false, fixture: { user, shops, notes }, browserTimezone: process.env.UI_TIMEZONE || 'Asia/Shanghai' }
let browser, context, page, seeded = Boolean(cleanupFrom)

function sql(query) {
  const result = spawnSync(process.env.MYSQL_BIN || 'mysql', ['--protocol=TCP', `--host=${process.env.MYSQL_HOST}`, `--port=${process.env.MYSQL_PORT}`, `--user=${process.env.MYSQL_USER}`, '--default-character-set=utf8mb4', '--batch', '--skip-column-names', process.env.MYSQL_DATABASE], {
    input: `SET NAMES utf8mb4; SET time_zone='+08:00'; ${query}`, encoding: 'utf8', env: { ...process.env, MYSQL_PWD: process.env.MYSQL_PASSWORD },
  })
  if (result.error || result.status !== 0) throw new Error('Owned SQL operation failed; credential/statement diagnostics omitted')
  return result.stdout.trim()
}
async function api(path, { token, method = 'GET', body } = {}) {
  const response = await fetch(API + path, { method, signal: AbortSignal.timeout(20000), headers: { ...(token ? { authorization: token } : {}), 'Content-Type': 'application/json' }, ...(body ? { body: JSON.stringify(body) } : {}) })
  const payload = await response.json()
  assert.ok(response.ok && payload.success, 'actual API operation did not succeed')
  return payload.data
}
function check(value, message) { assert.ok(value, message); report.checks.push(message) }
async function until(condition, description, timeout = 60000) {
  const end = Date.now() + timeout
  while (Date.now() < end) {
    if (await condition()) return
    await new Promise(resolveWait => setTimeout(resolveWait, 250))
  }
  throw new Error(`Condition not reached: ${description}`)
}
async function searchAll(q, shopId, sort = 'recent') {
  const rows = [], seen = new Set()
  let cursor
  for (let i = 0; i < 10; i++) {
    const query = new URLSearchParams({ q, sort, limit: '10', ...(shopId ? { shopId } : {}), ...(cursor ? { cursor } : {}) })
    const data = await api(`/notes/search?${query}`)
    for (const item of data.items) {
      assert.ok(!seen.has(item.id), 'actual API pagination must not repeat an ID')
      seen.add(item.id); rows.push(item)
    }
    if (!data.nextCursor) return rows
    cursor = data.nextCursor
  }
  throw new Error('Owned search did not terminate within ten pages')
}
function redisDelete(keys) {
  if (!keys.length) return Promise.resolve()
  const database = process.env.REDIS_DATABASE || '0'
  if (!/^(?:[0-9]|1[0-5])$/.test(database)) return Promise.reject(new Error('Invalid isolated Redis database'))
  return new Promise((resolveDelete, reject) => {
    const socket = createConnection({ host: process.env.REDIS_HOST, port: Number(process.env.REDIS_PORT) })
    let buffer = '', replies = 0
    const commands = [...(process.env.REDIS_PASSWORD ? [['AUTH', process.env.REDIS_PASSWORD]] : []), ['SELECT', database], ['DEL', ...keys]]
    const done = error => { socket.destroy(); error ? reject(new Error('Owned Redis cleanup failed; diagnostics omitted')) : resolveDelete() }
    socket.setTimeout(5000, () => done(true)); socket.once('error', () => done(true))
    socket.on('connect', () => socket.write(commands.map(args => `*${args.length}\r\n${args.map(value => `$${Buffer.byteLength(value)}\r\n${value}\r\n`).join('')}`).join('')))
    socket.on('data', chunk => {
      buffer += chunk.toString('utf8')
      while (buffer.includes('\r\n')) {
        const at = buffer.indexOf('\r\n'), line = buffer.slice(0, at); buffer = buffer.slice(at + 2)
        if (!/^(?:\+OK|:\d+)$/.test(line)) return done(true)
        if (++replies === commands.length) return done(false)
      }
    })
  })
}
function offsetsCaughtUp() {
  let partitions = 0
  for (const group of ['community.follow-v1', 'community.feed-v1', 'community.search-v1']) {
    const result = spawnSync('docker', ['exec', kafkaContainer, '/opt/kafka/bin/kafka-consumer-groups.sh', '--bootstrap-server', `localhost:${kafkaInternalPort}`, '--group', group, '--describe'], { encoding: 'utf8', timeout: 15000, maxBuffer: 1 << 20 })
    if (result.error || result.status !== 0) return false
    const rows = result.stdout.split(/\r?\n/).map(line => line.trim().split(/\s+/)).filter(columns => columns[0] === group && columns[1] === kafkaTopic)
    if (!rows.length || rows.some(columns => !/^\d+$/.test(columns[2]) || !/^\d+$/.test(columns[3]) || !/^\d+$/.test(columns[4]) || columns[5] !== '0' || BigInt(columns[3]) !== BigInt(columns[4]))) return false
    partitions += rows.length
  }
  report.offsetBarrier = { groups: 3, partitions, caughtUp: true }
  return true
}

class NoteSearchPage {
  constructor(page) {
    this.page = page
    this.keyword = page.getByRole('textbox', { name: '关键词', exact: true })
    this.shop = page.getByRole('textbox', { name: '门店 ID（可选）', exact: true })
    this.sort = page.getByRole('combobox', { name: '排序', exact: true })
    this.cards = page.getByRole('list', { name: '笔记搜索结果', exact: true }).getByRole('listitem')
  }
  response(q, { cursor } = {}) {
    return this.page.waitForResponse(response => {
      const url = new URL(response.url())
      return url.pathname === '/api/notes/search' && url.searchParams.get('q') === q && (cursor === undefined || url.searchParams.get('cursor') === cursor)
    })
  }
  async submit(q, shop = '', sort = 'relevance') {
    await this.keyword.fill(q); await this.shop.fill(shop); await this.sort.selectOption(sort)
    const response = this.response(q, { cursor: null })
    await this.page.getByRole('button', { name: '搜索', exact: true }).click()
    const body = await (await response).json()
    assert.ok(body.success, 'browser must receive a real successful search response')
    return body.data
  }
  async count(expected) {
    await this.page.waitForFunction(n => (document.querySelector('[aria-label="笔记搜索结果"]')?.children.length || 0) === n, expected)
  }
  async ids() { return this.cards.getByRole('heading', { level: 3 }).getByRole('link').evaluateAll(links => links.map(link => new URL(link.href).pathname.split('/').pop())) }
}

try {
  if (cleanupFrom) {
    report.controlledCleanupOnly = true
    report.passed = true
  } else {
  sql(`START TRANSACTION;
    INSERT INTO tb_user(id,phone,password,nick_name,icon) VALUES(${user},'${phone}','x','${prefix}','');
    INSERT INTO tb_merchant(id,name,status) VALUES(${merchant},'${prefix}','ACTIVE');
    INSERT INTO tb_shop(id,merchant_id,name,type_id,images,address,x,y) VALUES(${shops[0]},${merchant},'桂花小院-${prefix}',1,'','合成地址A',0,0),(${shops[1]},${merchant},'竹影茶馆-${prefix}',1,'','合成地址B',0,0);
    COMMIT;`)
  seeded = true
  const sent = await api(`/user/code?phone=${phone}`, { method: 'POST' })
  assert.equal(sent.mode, 'development', 'development-only synthetic login required')
  const token = await api('/user/login', { method: 'POST', body: { phone, code: sent.code } })
  tokens.push(token)
  for (let i = 0; i < 13; i++) {
    const note = await api('/notes', { token, method: 'POST', body: {
      shopId: shops[i < 11 ? 0 : 1], title: `${prefix} 桂花小院探店 ${String(i).padStart(2, '0')}${i === 0 ? ' <b>安全文本</b>' : ''}`,
      content: `${prefix} 手冲咖啡与桂花香气。${i === 0 ? '铜壶慢煮限定正文 <img src=x onerror="window.__noteSearchXSS=1">' : '安静的到店体验。'}`, images: [],
    } })
    assert.equal(typeof note.id, 'string'); assert.match(note.id, /^[1-9]\d{0,19}$/); notes.push(note.id)
  }
  await until(async () => {
    try { return (await searchAll(prefix)).length === notes.length } catch { return false }
  }, 'all actual published notes become searchable')
  check(true, '13 actual authenticated publications become searchable through the running event pipeline')
  const expectedRecent = sql(`SELECT id FROM tb_blog WHERE id IN(${notes.join(',')}) ORDER BY create_time DESC,id DESC`).split(/\s+/)
  browser = await chromium.launch({ ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}), headless: true })
  context = await browser.newContext({ viewport: { width: 1280, height: 900 }, timezoneId: report.browserTimezone })
  await context.tracing.start({ screenshots: true, snapshots: true })
  page = await context.newPage()
  const uncaught = []
  page.on('pageerror', () => uncaught.push('uncaught browser error'))
  await page.goto(WEB)
  await page.getByRole('navigation', { name: '主导航' }).getByRole('link', { name: '搜索笔记', exact: true }).click()
  await page.getByRole('heading', { name: '搜索笔记', exact: true }).waitFor()
  const ui = new NoteSearchPage(page)
  check((await page.evaluate(() => localStorage.getItem('cityloop.token'))) === null && new URL(page.url()).pathname === '/notes/search', 'anonymous navigation opens the public search route before the dynamic note route')
  const first = await ui.submit(prefix, '', 'recent')
  await ui.count(10)
  check(first.items.length === 10 && typeof first.nextCursor === 'string', 'first real browser page exposes ten results and an opaque next cursor without an exact total')
  check(await ui.cards.getByText(`作者 #${user}`, { exact: true }).count() === 10, 'greater-than-2^53 author IDs remain exact decimal strings in all first-page cards')
  const more = ui.response(prefix, { cursor: first.nextCursor })
  await page.getByRole('button', { name: '加载更多', exact: true }).click(); await more; await ui.count(13)
  check(JSON.stringify(await ui.ids()) === JSON.stringify(expectedRecent), 'two pages match independent MySQL publication-time/ID order without duplicates or missing notes')
  await page.getByText('已经看完本次搜索结果了', { exact: true }).waitFor()
  await page.screenshot({ path: output.replace(/\.json$/, '') + '-results.png', fullPage: true })

  await ui.submit(prefix, shops[1], 'relevance'); await ui.count(2)
  check((await ui.cards.getByRole('link', { name: `查看门店 #${shops[1]}`, exact: true }).count()) === 2, 'shop filter preserves a greater-than-2^53 ID exactly and restricts results to that shop')
  const shared = new URL(page.url())
  check(shared.searchParams.get('q') === prefix && shared.searchParams.get('shopId') === shops[1] && shared.searchParams.get('sort') === 'relevance', 'shareable URL preserves keyword, canonical shop ID and sorting')
  await page.reload(); await ui.count(2)
  check(await ui.shop.inputValue() === shops[1] && await ui.sort.inputValue() === 'relevance', 'reload restores filters and a fresh first page from the shared URL')
  await ui.cards.filter({ has: page.getByRole('heading', { name: `${prefix} 桂花小院探店 12`, exact: true }) }).getByRole('link', { name: `查看门店 #${shops[1]}`, exact: true }).click()
  await page.getByRole('heading', { name: `竹影茶馆-${prefix}`, exact: true }).waitFor()
  check(new URL(page.url()).pathname === `/shops/${shops[1]}`, 'shop navigation retains the exact large string ID')
  await page.goBack(); await ui.count(2)

  await ui.submit('铜壶慢煮', shops[0], 'relevance'); await ui.count(1)
  check((await ui.cards.textContent()).includes('铜壶慢煮限定正文'), 'Chinese content-only search finds the public note independently of title words')
  check(await page.evaluate(() => window.__noteSearchXSS) === undefined && await ui.cards.locator('b, img').count() === 0, 'title and content markup remain escaped text and execute no handler')
  const detailLink = ui.cards.getByRole('heading', { level: 3 }).getByRole('link')
  const detailPath = await detailLink.getAttribute('href'); await detailLink.click()
  await page.waitForURL(url => url.pathname === detailPath)
  await page.getByRole('heading', { name: `${prefix} 桂花小院探店 00 <b>安全文本</b>`, exact: true }).waitFor()
  check(new URL(page.url()).pathname === detailPath, 'a public search result opens its real note detail')
  await page.goBack(); await ui.count(1)
  await ui.submit(`ABSENT${randomBytes(8).toString('hex')}`, shops[0], 'recent')
  await page.getByText('没有找到相关笔记，换个关键词或门店试试。', { exact: true }).waitFor()
  check(await ui.cards.count() === 0, 'no-match search shows an explicit empty state')

  await ui.submit(prefix, '', 'recent'); await ui.count(10)
  await ui.keyword.fill('超'.repeat(101))
  await page.getByRole('alert').filter({ hasText: '搜索关键词最多 100 字。' }).waitFor()
  check(await ui.cards.count() === 0 && await page.getByRole('button', { name: '加载更多', exact: true }).count() === 0, 'invalid new input immediately clears old results and pagination')
  await ui.keyword.fill(prefix); await ui.shop.fill('01'); await page.getByRole('button', { name: '搜索', exact: true }).click()
  await page.getByRole('alert').filter({ hasText: '请输入有效的门店 ID。' }).waitFor()
  check(await ui.cards.count() === 0, 'noncanonical store input is rejected without retaining old results')

  // Hold only the outbound request, then let the product abort it on a filter
  // edit. The route never fabricates a successful body or resolves stale JSON.
  let releaseHeld, notifyHeld
  const held = new Promise(resolveHeld => { notifyHeld = resolveHeld })
  const gate = new Promise(resolveGate => { releaseHeld = resolveGate })
  await page.route('**/api/notes/search?**', async route => {
    notifyHeld(); await gate
    await route.continue().catch(() => {})
  })
  await ui.keyword.fill(prefix); await ui.shop.fill(''); await page.getByRole('button', { name: '搜索', exact: true }).click()
  await held
  const aborted = page.waitForEvent('requestfailed', { predicate: request => new URL(request.url()).pathname === '/api/notes/search' })
  await ui.keyword.fill('超'.repeat(101)); releaseHeld(); await aborted
  await page.unroute('**/api/notes/search?**')
  await page.getByRole('alert').filter({ hasText: '搜索关键词最多 100 字。' }).waitFor()
  check(await ui.cards.count() === 0, 'editing filters aborts the pending request and cannot restore stale results or cursors')

  const expiring = await ui.submit(prefix, '', 'recent'); await ui.count(10)
  const ownCursor = JSON.parse(Buffer.from(expiring.nextCursor.split('.')[0], 'base64url').toString('utf8'))
  assert.equal(typeof ownCursor.pit, 'string', 'own cursor must identify its actual read snapshot')
  const closed = await fetch(`${ES}/_pit`, { method: 'DELETE', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ id: ownCursor.pit }), signal: AbortSignal.timeout(10000) })
  assert.ok(closed.ok && (await closed.json()).num_freed >= 1, 'explicitly close only this browser query snapshot')
  const expiredResponse = ui.response(prefix, { cursor: expiring.nextCursor })
  await page.getByRole('button', { name: '加载更多', exact: true }).click()
  check((await expiredResponse).status() === 400, 'an expired owned snapshot is rejected by the actual API')
  await page.getByRole('alert').filter({ hasText: '分页已失效，请重新搜索' }).waitFor()
  check(await ui.cards.count() === 0, 'expired pagination clears stale results and the unusable cursor')
  const restarted = ui.response(prefix, { cursor: null })
  await page.getByRole('button', { name: '重新搜索', exact: true }).click()
  check((await restarted).status() === 200, 'restart performs a real first-page request with a new snapshot')
  await ui.count(10)

  await page.route('**/api/notes/search?**', route => route.abort('failed'))
  await ui.keyword.fill(prefix); await ui.shop.fill(''); await page.getByRole('button', { name: '搜索', exact: true }).click()
  await page.getByRole('alert').filter({ hasText: '笔记搜索暂时不可用，请稍后重试' }).waitFor()
  check(await ui.cards.count() === 0, 'actual network failure shows the friendly unavailable state')
  await page.unroute('**/api/notes/search?**')
  const retried = ui.response(prefix, { cursor: null })
  await page.getByRole('button', { name: '重试', exact: true }).click()
  const retryPage = (await (await retried).json()).data; await ui.count(10)
  check(true, 'retry performs a real successful request without mocked JSON')
  await page.route('**/api/notes/search?**', route => route.abort('failed'))
  await page.getByRole('button', { name: '加载更多', exact: true }).click()
  await page.getByRole('button', { name: '重试加载', exact: true }).waitFor()
  check(await ui.cards.count() === 10, 'load-more transport failure preserves already displayed notes')
  await page.unroute('**/api/notes/search?**')
  const retriedMore = ui.response(prefix, { cursor: retryPage.nextCursor })
  await page.getByRole('button', { name: '重试加载', exact: true }).click(); await retriedMore; await ui.count(13)
  check(true, 'load-more retry resumes the failed cursor instead of skipping a page')
  check(!/Elasticsearch|Kafka|offset|索引|投影/.test(await page.getByRole('main').innerText()), 'normal search UI contains product text without implementation details')
  check(uncaught.length === 0, 'no uncaught browser exceptions in the exercised search flow')
  await context.tracing.stop({ path: output.replace(/\.json$/, '') + '-trace.zip' })
  report.passed = true
  }
} catch (error) {
  report.failure = error instanceof Error ? error.message.split('\n')[0].slice(0, 180) : 'search UI verification failed'
  if (page) await page.screenshot({ path: output.replace(/\.json$/, '') + '-failure.png', fullPage: true }).catch(() => {})
  if (context) await context.tracing.stop({ path: output.replace(/\.json$/, '') + '-failure-trace.zip' }).catch(() => {})
  process.exitCode = 1
} finally {
  if (browser) await browser.close().catch(() => {})
  let clean = true
  for (const token of tokens) { try { await api('/user/logout', { token, method: 'POST' }) } catch { clean = false } }
  try { await redisDelete([...tokens.map(token => 'login:token:' + token), 'login:code:' + phone, 'login:code:throttle:' + phone]) } catch { clean = false }
  if (seeded) {
    let cleanupStage = 'source receipts'
    try {
      if (notes.length) {
        const noteIDs = notes.join(','), events = notes.map(id => `'NOTE:${id}:1:PUBLISHED'`).join(',')
        await until(async () => sql(`SELECT COUNT(*) FROM tb_community_outbox WHERE event_id IN(${events}) AND delivery_status<>'DONE'`) === '0' && Number(sql(`SELECT COUNT(*) FROM tb_community_projection_event WHERE event_id IN(${events})`)) >= notes.length * 3, 'owned outbox DONE and all three consumer receipts before cleanup')
        cleanupStage = 'Kafka committed offset barrier'
        await until(async () => offsetsCaughtUp(), 'all three dedicated consumer groups have defined committed offsets equal to topic log ends')
        cleanupStage = 'exact owned search documents'
        for (const id of notes) {
          const response = await fetch(`${ES}/${INDEX}/_doc/${id}?refresh=wait_for`, { method: 'DELETE', signal: AbortSignal.timeout(10000) })
          if (![200, 404].includes(response.status)) throw new Error('exact owned search document cleanup failed')
        }
        cleanupStage = 'exact owned projection and note rows'
        sql(`START TRANSACTION;
          DELETE FROM tb_community_consumer_failure WHERE JSON_VALID(CAST(message_body AS CHAR CHARACTER SET utf8mb4)) AND JSON_UNQUOTE(JSON_EXTRACT(CAST(message_body AS CHAR CHARACTER SET utf8mb4),'$.eventId')) IN(${events});
          DELETE FROM tb_community_projection_event WHERE event_id IN(${events});
          DELETE FROM tb_community_note_projection WHERE note_id IN(${noteIDs});
          DELETE FROM tb_note_search_state WHERE note_id IN(${noteIDs});
          DELETE FROM tb_community_outbox WHERE event_id IN(${events});
          DELETE FROM tb_blog_like WHERE blog_id IN(${noteIDs}); DELETE FROM tb_blog WHERE id IN(${noteIDs}); COMMIT;`)
      }
      cleanupStage = 'exact owned merchant and user rows'
      sql(`START TRANSACTION; DELETE FROM tb_shop WHERE id IN(${shops.join(',')}); DELETE FROM tb_merchant WHERE id=${merchant}; DELETE FROM tb_user WHERE id=${user}; COMMIT;`)
      clean &&= sql(`SELECT (SELECT COUNT(*) FROM tb_user WHERE id=${user})+(SELECT COUNT(*) FROM tb_shop WHERE id IN(${shops.join(',')}))+(SELECT COUNT(*) FROM tb_blog WHERE id IN(${notes.length ? notes.join(',') : '0'}))`) === '0'
    } catch { clean = false; report.cleanupFailure = `Cleanup failed at ${cleanupStage}; retained fixture IDs are recorded for controlled recovery` }
  }
  report.cleanup = clean
  if (!clean) { report.passed = false; process.exitCode = 1 }
  report.recordedAt = new Date().toISOString()
  await writeFile(output, JSON.stringify(report, null, 2) + '\n')
  process.stdout.write(JSON.stringify({ passed: report.passed, checks: report.checks.length, cleanup: report.cleanup, output }) + '\n')
}
