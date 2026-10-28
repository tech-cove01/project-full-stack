// GO-076c: actual ES container stop/start while the API and anonymous Vue page
// stay live. Only the explicitly named GO076 project may be faulted. Successful
// responses are never mocked, and Kafka offsets are never changed by this file.
import assert from 'node:assert/strict'
import { randomBytes, randomInt } from 'node:crypto'
import { spawnSync } from 'node:child_process'
import { access, mkdir, readdir, readFile, writeFile } from 'node:fs/promises'
import { createConnection } from 'node:net'
import { resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

for (const key of ['API_BASE', 'WEB_BASE', 'MYSQL_HOST', 'MYSQL_PORT', 'MYSQL_DATABASE', 'MYSQL_USER', 'MYSQL_PASSWORD', 'REDIS_HOST', 'REDIS_PORT', 'ES_URL', 'ES_INDEX', 'ES_CONTAINER', 'KAFKA_CONTAINER', 'KAFKA_TOPIC', 'GO_SEARCH_BINARY', 'PLAYWRIGHT_MODULE', 'CHROME_PATH', 'FAULT_EVIDENCE_DIR']) {
  assert.ok(process.env[key], `Explicit dedicated ${key} is required`)
}
const API = process.env.API_BASE.replace(/\/$/, ''), WEB = process.env.WEB_BASE.replace(/\/$/, '')
const ES = process.env.ES_URL.replace(/\/$/, ''), INDEX = process.env.ES_INDEX
const esContainer = process.env.ES_CONTAINER, kafkaContainer = process.env.KAFKA_CONTAINER, topic = process.env.KAFKA_TOPIC
assert.equal(esContainer, 'project-full-stack-go076-elasticsearch-1', 'only the dedicated GO076 ES container may be stopped')
assert.equal(kafkaContainer, 'project-full-stack-go076-kafka-1', 'only the dedicated GO076 Kafka container may be inspected')
for (const [raw, port] of [[API, '8086'], [WEB, '5176'], [ES, '9202']]) {
  const url = new URL(raw)
  assert.ok(url.protocol === 'http:' && url.hostname === '127.0.0.1' && url.port === port && url.pathname === '/' && !url.search && !url.hash && !url.username && !url.password, 'explicit dedicated loopback endpoints required')
}
assert.equal(process.env.MYSQL_HOST, '127.0.0.1'); assert.equal(process.env.MYSQL_PORT, '3332')
assert.equal(process.env.REDIS_HOST, '127.0.0.1'); assert.equal(process.env.REDIS_PORT, '6404')
assert.equal(INDEX, 'community-notes-v1'); assert.equal(topic, 'community.events')
const out = resolve(process.env.FAULT_EVIDENCE_DIR)
const previousFiles = await readdir(out).catch(error => { if (error.code === 'ENOENT') return []; throw error })
assert.equal(previousFiles.length, 0, 'choose a new evidence directory; retain prior failed rounds')
await mkdir(out, { recursive: true })
const binary = resolve(process.env.GO_SEARCH_BINARY)
await access(binary); await access(process.env.CHROME_PATH)
const specifier = process.env.PLAYWRIGHT_MODULE
const { chromium } = await import(/^[A-Za-z]:[\\/]/.test(specifier) || specifier.startsWith('.') ? pathToFileURL(resolve(specifier)).href : specifier)
const prefix = 'ESFAULT076' + randomBytes(5).toString('hex'), phone = `139${randomInt(10000000, 100000000)}`
const fixture = { user: '', merchant: '', shop: '', notes: [] }, tokens = []
const report = { task: 'GO-076c', passed: false, checks: [], fixture, cleanup: false, esRestored: false, phases: [], failures: [] }
let browser, context, page, seeded = false, restoreRequired = false, stage = 'preflight'
const cleanupErrors = []

function check(value, description) { assert.ok(value, description); report.checks.push(description) }
function phase(name) { stage = name; report.phases.push({ name, at: new Date().toISOString() }); process.stdout.write(JSON.stringify({ phase: name, checks: report.checks.length }) + '\n') }
function docker(args, timeout = 45000) {
  const result = spawnSync('docker', args, { encoding: 'utf8', timeout, maxBuffer: 1 << 20 })
  if (result.error || result.status !== 0) throw new Error('Dedicated Docker operation failed; diagnostics omitted')
  return result.stdout.trim()
}
function inspect(name, service) {
  const labels = JSON.parse(docker(['inspect', '--format', '{{json .Config.Labels}}', name]))
  assert.equal(labels['com.docker.compose.project'], 'project-full-stack-go076')
  assert.equal(labels['com.docker.compose.service'], service)
  const state = JSON.parse(docker(['inspect', '--format', '{{json .State}}', name]))
  return { running: state.Running, oom: state.OOMKilled, exitCode: state.ExitCode, health: state.Health?.Status || 'none' }
}
function sql(statement) {
  const result = spawnSync(process.env.MYSQL_BIN || 'mysql', ['--protocol=TCP', `--host=${process.env.MYSQL_HOST}`, `--port=${process.env.MYSQL_PORT}`, `--user=${process.env.MYSQL_USER}`, '--default-character-set=utf8mb4', '--batch', '--skip-column-names', process.env.MYSQL_DATABASE], {
    input: `SET NAMES utf8mb4; SET time_zone='+08:00'; ${statement}`, encoding: 'utf8', env: { ...process.env, MYSQL_PWD: process.env.MYSQL_PASSWORD }, timeout: 15000,
  })
  if (result.error || result.status !== 0) throw new Error('Owned SQL operation failed; diagnostics omitted')
  return result.stdout.trim()
}
async function api(path, { method = 'GET', body, token } = {}) {
  const response = await fetch(API + path, { method, signal: AbortSignal.timeout(15000), headers: { 'Content-Type': 'application/json', ...(token ? { authorization: token } : {}) }, ...(body ? { body: JSON.stringify(body) } : {}) })
  const payload = await response.json()
  assert.ok(response.ok && payload.success, 'actual authenticated API operation failed')
  return payload.data
}
async function until(condition, description, timeout = 90000) {
  const deadline = Date.now() + timeout
  while (Date.now() < deadline) {
    if (await condition()) return
    await new Promise(resolveWait => setTimeout(resolveWait, 250))
  }
  throw new Error(`Condition not reached: ${description}`)
}
async function es(path, options = {}) {
  const response = await fetch(ES + path, { ...options, signal: AbortSignal.timeout(12000), headers: { 'Content-Type': 'application/json', ...(options.headers || {}) } })
  assert.ok(response.ok, 'actual ES operation did not succeed')
  return response.json()
}
async function aliasGeneration() {
  const aliases = await es('/_alias/' + INDEX), names = Object.keys(aliases)
  assert.equal(names.length, 1, 'exactly one actual write generation is required')
  assert.ok(names[0].startsWith(INDEX + '-build-'), 'controlled search generation required')
  return names[0]
}
function events(ids = fixture.notes) { return ids.map(id => `'NOTE:${id}:1:PUBLISHED'`).join(',') }
function ownsFailure(ids = fixture.notes) {
  return `projection='search-v1' AND topic='${topic}' AND message_key IN(${ids.map(id => `'${id}'`).join(',') || "''"})`
}
function ownedFailures(state = 'PARKED', ids = fixture.notes) {
  if (!ids.length) return []
  return sql(`SELECT topic,partition_id,source_offset FROM tb_community_consumer_failure WHERE ${ownsFailure(ids)} AND state='${state}' ORDER BY partition_id,source_offset`).split(/\r?\n/).filter(Boolean).map(line => {
    const [sourceTopic, partition, offset] = line.split(/\s+/)
    assert.equal(sourceTopic, topic); assert.match(partition, /^\d+$/); assert.match(offset, /^\d+$/)
    return { topic: sourceTopic, partition: Number(partition), offset }
  })
}
function replayOwned() {
  for (const source of ownedFailures()) {
    const run = spawnSync(binary, ['replay', source.topic, String(source.partition), source.offset], { encoding: 'utf8', env: process.env, timeout: 40000 })
    assert.ok(!run.error && run.status === 0, 'exact source replay failed; diagnostics omitted')
    const result = JSON.parse(run.stdout.trim())
    assert.equal(result.projection, 'search-v1'); assert.equal(result.result, 'committed')
  }
}
function offsetSnapshot() {
  const rows = []
  for (const group of ['community.follow-v1', 'community.feed-v1', 'community.search-v1']) {
    const raw = docker(['exec', kafkaContainer, '/opt/kafka/bin/kafka-consumer-groups.sh', '--bootstrap-server', 'localhost:9094', '--group', group, '--describe'], 20000)
    const partitions = raw.split(/\r?\n/).map(line => line.trim().split(/\s+/)).filter(columns => columns[0] === group && columns[1] === topic)
    if (!partitions.length) return null
    for (const columns of partitions) {
      if (!columns.slice(2, 6).every(value => /^\d+$/.test(value))) return null
      rows.push({ group, partition: Number(columns[2]), committed: columns[3], logEnd: columns[4], lag: columns[5] })
    }
  }
  return rows
}
async function offsetBarrier() {
  let snapshot
  await until(async () => {
    try { snapshot = offsetSnapshot(); return snapshot && snapshot.every(row => row.lag === '0' && row.committed === row.logEnd) } catch { return false }
  }, 'all three committed groups equal the dedicated topic log end')
  return snapshot
}
async function restoreES() {
  if (!restoreRequired) return
  if (!inspect(esContainer, 'elasticsearch').running) docker(['start', esContainer])
  await until(async () => {
    try { const health = await es('/_cluster/health?wait_for_status=yellow&timeout=2s'); return health.status === 'green' || health.status === 'yellow' } catch { return false }
  }, 'same ES container returns ready')
  report.esRestored = true
  restoreRequired = false
}
async function publish(token, count) {
  for (let i = 0; i < count; i++) {
    const number = fixture.notes.length + 1
    const view = await api('/notes', { token, method: 'POST', body: { shopId: fixture.shop, title: `${prefix} 故障恢复笔记 ${number}`, content: `${prefix} 公开合成探店记录，证明搜索故障不阻止笔记发布。`, images: [] } })
    assert.equal(typeof view.id, 'string'); assert.match(view.id, /^[1-9]\d{0,19}$/); fixture.notes.push(view.id)
  }
}
async function searchOwn() {
  return api('/notes/search?' + new URLSearchParams({ q: prefix, shopId: fixture.shop, sort: 'recent', limit: '10' }))
}
class FaultSearchPage {
  constructor(page) {
    this.page = page
    this.keyword = page.getByRole('textbox', { name: '关键词', exact: true })
    this.shop = page.getByRole('textbox', { name: '门店 ID（可选）', exact: true })
    this.cards = page.getByRole('list', { name: '笔记搜索结果', exact: true }).getByRole('listitem')
  }
  response() { return this.page.waitForResponse(response => { const url = new URL(response.url()); return url.pathname === '/api/notes/search' && url.searchParams.get('q') === prefix }) }
  async submit() {
    await this.keyword.fill(prefix); await this.shop.fill(fixture.shop)
    const response = this.response()
    await this.page.getByRole('button', { name: '搜索', exact: true }).click()
    return response
  }
  async count(expected) { await until(async () => await this.cards.count() === expected, 'anonymous visible result count', 20000) }
}
function redisDelete(keys) {
  const database = process.env.REDIS_DATABASE || '0'
  assert.match(database, /^(?:[0-9]|1[0-5])$/)
  return new Promise((resolveDelete, reject) => {
    const socket = createConnection({ host: process.env.REDIS_HOST, port: Number(process.env.REDIS_PORT) })
    const commands = [...(process.env.REDIS_PASSWORD ? [['AUTH', process.env.REDIS_PASSWORD]] : []), ['SELECT', database], ['DEL', ...keys]]
    let buffer = '', replies = 0, completed = false
    const done = failed => { if (completed) return; completed = true; socket.destroy(); failed ? reject(new Error('Owned Redis cleanup failed')) : resolveDelete() }
    socket.setTimeout(5000, () => done(true)); socket.once('error', () => done(true))
    socket.once('connect', () => socket.write(commands.map(args => `*${args.length}\r\n${args.map(value => `$${Buffer.byteLength(value)}\r\n${value}\r\n`).join('')}`).join('')))
    socket.on('data', chunk => { buffer += chunk.toString('utf8'); while (buffer.includes('\r\n')) { const at = buffer.indexOf('\r\n'), line = buffer.slice(0, at); buffer = buffer.slice(at + 2); if (!/^(?:\+OK|:\d+)$/.test(line)) return done(true); if (++replies === commands.length) return done(false) } })
  })
}

try {
  phase('verify-dedicated-running-dependencies')
  report.initialES = inspect(esContainer, 'elasticsearch'); check(report.initialES.running && !report.initialES.oom, 'the exact dedicated ES container is running before fault injection')
  check(inspect(kafkaContainer, 'kafka').running, 'the dedicated Kafka container is running')
  report.initialGeneration = await aliasGeneration()
  const ids = sql(`START TRANSACTION;
    INSERT INTO tb_user(phone,nick_name) VALUES('${phone}','${prefix}'); SET @fault_user=LAST_INSERT_ID();
    INSERT INTO tb_merchant(name,status) VALUES('${prefix}','ACTIVE'); SET @fault_merchant=LAST_INSERT_ID();
    INSERT INTO tb_shop(merchant_id,name,type_id,address,x,y) VALUES(@fault_merchant,'故障恢复门店-${prefix}',1,'合成地址',0,0); SET @fault_shop=LAST_INSERT_ID();
    INSERT INTO tb_merchant_member(merchant_id,user_id,role_code,status) VALUES(@fault_merchant,@fault_user,'OWNER','ACTIVE');
    SELECT @fault_user; SELECT @fault_merchant; SELECT @fault_shop; COMMIT;`).split(/\s+/)
  assert.equal(ids.length, 3); ids.forEach(id => assert.match(id, /^[1-9]\d{0,19}$/))
  ;[fixture.user, fixture.merchant, fixture.shop] = ids; seeded = true
  const code = await api(`/user/code?phone=${phone}`, { method: 'POST' }); assert.equal(code.mode, 'development')
  const token = await api('/user/login', { method: 'POST', body: { phone, code: code.code } }); tokens.push(token)
  phase('publish-two-before-outage')
  await publish(token, 2)
  await until(async () => { try { return (await searchOwn()).items.length === 2 } catch { return false } }, 'two actual notes become searchable')
  report.initialOffsets = await offsetBarrier()
  browser = await chromium.launch({ executablePath: process.env.CHROME_PATH, headless: true })
  context = await browser.newContext({ viewport: { width: 1280, height: 900 }, timezoneId: 'Asia/Shanghai' })
  await context.tracing.start({ screenshots: true, snapshots: true })
  page = await context.newPage(); page.setDefaultTimeout(20000)
  await page.goto(WEB + '/notes/search?' + new URLSearchParams({ q: prefix, shopId: fixture.shop, sort: 'recent' }))
  const ui = new FaultSearchPage(page); await ui.count(2)
  check(await page.evaluate(() => localStorage.getItem('cityloop.token')) === null, 'the browser and trace are anonymous and contain no authenticated setup requests')
  check(true, 'two actual pre-outage publications are visible in the real Vue page')
  phase('stop-only-dedicated-ES-container')
  restoreRequired = true
  docker(['stop', '--time', '30', esContainer])
  report.stoppedES = inspect(esContainer, 'elasticsearch')
  check(!report.stoppedES.running && !report.stoppedES.oom, 'physical stop leaves only the explicitly named ES container stopped without OOM')
  const downResponse = await ui.submit()
  check(downResponse.status() === 503 && !(await downResponse.json()).success, 'the real browser receives actual HTTP 503 while ES is physically stopped')
  await page.getByRole('alert').filter({ hasText: '笔记搜索暂时不可用，请稍后重试' }).waitFor()
  await page.getByRole('button', { name: '重试', exact: true }).waitFor()
  check(await ui.cards.count() === 0, 'the product shows a friendly retry state and clears stale search results')
  await page.screenshot({ path: resolve(out, 'search-es-down.png'), fullPage: true })
  phase('publish-during-physical-outage-and-park')
  await publish(token, 2)
  const outageIds = fixture.notes.slice(2)
  await until(async () => sql(`SELECT COUNT(*) FROM tb_community_outbox WHERE event_id IN(${events()}) AND delivery_status='DONE'`) === '4' && ownedFailures('PARKED', outageIds).length === 2, 'Kafka delivery plus exact durable search parking')
  const outageOffsets = await offsetBarrier()
  report.outage = {
    facts: Number(sql(`SELECT COUNT(*) FROM tb_blog WHERE id IN(${fixture.notes.join(',')}) AND status='PUBLISHED'`)),
    deliveredOutbox: Number(sql(`SELECT COUNT(*) FROM tb_community_outbox WHERE event_id IN(${events()}) AND delivery_status='DONE'`)),
    followReceipts: Number(sql(`SELECT COUNT(*) FROM tb_community_projection_event WHERE projection='follow-v1' AND event_id IN(${events()})`)),
    feedApplied: Number(sql(`SELECT COUNT(*) FROM tb_community_projection_event WHERE projection='feed-v1' AND state='APPLIED' AND event_id IN(${events()})`)),
    searchApplied: Number(sql(`SELECT COUNT(*) FROM tb_community_projection_event WHERE projection='search-v1' AND state='APPLIED' AND event_id IN(${events()})`)),
    parkedRecoveryRecords: ownedFailures('PARKED', outageIds).length,
    sourceCoordinates: ownedFailures('PARKED', outageIds), committedOffsets: outageOffsets,
  }
  check(report.outage.facts === 4 && report.outage.deliveredOutbox === 4 && report.outage.followReceipts === 4 && report.outage.feedApplied === 4 && report.outage.searchApplied === 2 && report.outage.parkedRecoveryRecords === 2, 'publication and both existing consumer groups continue; search retains two durable recovery records')
  check(outageOffsets.every(row => row.lag === '0'), 'all three Kafka groups have zero uncommitted lag after durable parking; recovery backlog is the two parked records')
  phase('actual-trading-while-ES-stays-stopped')
  const tradeEvidence = resolve(out, 'trade-during-es-down.json')
  const trade = spawnSync(process.execPath, ['scripts/go-flow-demo.mjs', 'trade'], { env: { ...process.env, FLOW_EVIDENCE: tradeEvidence }, encoding: 'utf8', timeout: 90000 })
  const tradeReport = JSON.parse(await readFile(tradeEvidence, 'utf8'))
  report.trade = { exitCode: trade.status, passed: tradeReport.passed, checks: tradeReport.checks.length, orderStatus: tradeReport.flows?.trade?.orderStatus }
  check(!trade.error && trade.status === 0 && tradeReport.passed && tradeReport.checks.length === 9 && report.trade.orderStatus === 3, 'actual publish/admit/persist/pay/redeem/audit trading passes nine checks with ES physically stopped')
  phase('restart-same-container-and-replay-owned-sources')
  await restoreES()
  report.recoveredGeneration = await aliasGeneration()
  check(report.recoveredGeneration === report.initialGeneration, 'restarting the same ES container preserves the original write generation')
  for (const id of fixture.notes.slice(0, 2)) { const document = await es(`/${INDEX}/_doc/${id}`); check(document.found && document._version === 1 && document._source.id === id, 'pre-outage document persists after the physical restart') }
  replayOwned()
  await es('/' + INDEX + '/_refresh', { method: 'POST' })
  await until(async () => { try { const found = (await searchOwn()).items; return found.length === 4 && new Set(found.map(item => item.id)).size === 4 && found.every(item => fixture.notes.includes(item.id)) } catch { return false } }, 'four exact actual notes become searchable after replay')
  report.recovery = { parked: ownedFailures('PARKED').length, resolved: ownedFailures('RESOLVED').length, applied: Number(sql(`SELECT COUNT(*) FROM tb_community_projection_event WHERE projection='search-v1' AND event_id IN(${events()}) AND state='APPLIED'`)) }
  check(report.recovery.parked === 0 && report.recovery.resolved === 2 && report.recovery.applied === 4, 'exact replay resolves both failures and materializes each of the four note events once')
  const count = await es('/' + INDEX + '/_count', { method: 'POST', body: JSON.stringify({ query: { ids: { values: fixture.notes } } }) })
  check(count.count === 4, 'the actual alias contains exactly four unique owned documents')
  const retry = ui.response(); await page.getByRole('button', { name: '重试', exact: true }).click()
  const retryResponse = await retry
  check(retryResponse.status() === 200 && (await retryResponse.json()).data.items.length === 4, 'the existing retry button fetches real successful results after recovery without mocked JSON')
  await ui.count(4); await page.screenshot({ path: resolve(out, 'search-es-recovered.png'), fullPage: true })
  report.recoveredOffsets = await offsetBarrier()
  check(JSON.stringify(report.recoveredOffsets) === JSON.stringify(report.outage.committedOffsets), 'controlled failure replay changes no Kafka offsets')
  await context.tracing.stop({ path: resolve(out, 'search-es-fault-trace.zip') })
  report.passed = true
} catch (error) {
  report.failures.push({ phase: stage, kind: error?.name || 'Error', message: 'Fault verification failed; raw credential and driver diagnostics omitted' })
  if (page) await page.screenshot({ path: resolve(out, 'search-es-failure.png'), fullPage: true }).catch(() => {})
  if (context) await context.tracing.stop({ path: resolve(out, 'search-es-failure-trace.zip') }).catch(() => {})
  process.exitCode = 1
} finally {
  if (browser) await browser.close().catch(() => {})
  try { await restoreES(); if (!restoreRequired) report.esRestored = inspect(esContainer, 'elasticsearch').running } catch { cleanupErrors.push('same dedicated ES container recovery failed') }
  for (const token of tokens) { try { await api('/user/logout', { token, method: 'POST' }) } catch { cleanupErrors.push('owned session logout failed') } }
  try { await redisDelete([...tokens.map(token => 'login:token:' + token), 'login:code:' + phone, 'login:code:throttle:' + phone]) } catch { cleanupErrors.push('owned Redis authentication key cleanup failed') }
  if (seeded) {
    try {
      // Recover the journal if an HTTP result was lost after a committed write.
      const owned = sql(`SELECT id FROM tb_blog WHERE user_id=${fixture.user} AND shop_id=${fixture.shop}`).split(/\s+/).filter(Boolean)
      for (const id of owned) { assert.match(id, /^[1-9]\d{0,19}$/); if (!fixture.notes.includes(id)) fixture.notes.push(id) }
      if (fixture.notes.length) {
        await until(async () => sql(`SELECT COUNT(*) FROM tb_community_outbox WHERE event_id IN(${events()}) AND delivery_status<>'DONE'`) === '0', 'owned source relay is drained before cleanup')
        replayOwned()
        await until(async () => Number(sql(`SELECT COUNT(*) FROM tb_community_projection_event WHERE projection IN('follow-v1','feed-v1','search-v1') AND event_id IN(${events()})`)) === fixture.notes.length * 3, 'all owned durable consumer outcomes before cleanup')
        report.cleanupOffsets = await offsetBarrier()
        for (const id of fixture.notes) {
          const response = await fetch(`${ES}/${INDEX}/_doc/${id}?refresh=wait_for`, { method: 'DELETE', signal: AbortSignal.timeout(10000) })
          assert.ok([200, 404].includes(response.status), 'owned ES document cleanup failed')
        }
        sql(`START TRANSACTION;
          DELETE FROM tb_community_consumer_failure WHERE ${ownsFailure()};
          DELETE FROM tb_community_projection_event WHERE projection IN('follow-v1','feed-v1','search-v1') AND event_id IN(${events()});
          DELETE FROM tb_note_search_state WHERE note_id IN(${fixture.notes.join(',')});
          DELETE FROM tb_community_note_projection WHERE note_id IN(${fixture.notes.join(',')});
          DELETE FROM tb_community_outbox WHERE event_id IN(${events()});
          DELETE FROM tb_blog_like WHERE blog_id IN(${fixture.notes.join(',')});
          DELETE FROM tb_blog WHERE id IN(${fixture.notes.join(',')}) AND user_id=${fixture.user}; COMMIT;`)
      }
      sql(`START TRANSACTION; DELETE FROM tb_merchant_member WHERE merchant_id=${fixture.merchant} AND user_id=${fixture.user}; DELETE FROM tb_shop WHERE id=${fixture.shop} AND merchant_id=${fixture.merchant}; DELETE FROM tb_merchant WHERE id=${fixture.merchant}; DELETE FROM tb_user WHERE id=${fixture.user}; COMMIT;`)
      report.ownedRowsRemaining = Number(sql(`SELECT (SELECT COUNT(*) FROM tb_user WHERE id=${fixture.user})+(SELECT COUNT(*) FROM tb_shop WHERE id=${fixture.shop})+(SELECT COUNT(*) FROM tb_merchant WHERE id=${fixture.merchant})+(SELECT COUNT(*) FROM tb_blog WHERE id IN(${fixture.notes.join(',') || '0'}))`))
      assert.equal(report.ownedRowsRemaining, 0, 'owned SQL fixture remains')
    } catch { cleanupErrors.push('owned fixture cleanup failed; safe IDs retained in report for controlled recovery') }
  }
  report.cleanup = cleanupErrors.length === 0
  report.cleanupErrors = cleanupErrors
  report.recordedAt = new Date().toISOString()
  if (!report.cleanup || !report.esRestored) { report.passed = false; process.exitCode = 1 }
  await writeFile(resolve(out, 'fault-result.json'), JSON.stringify(report, null, 2) + '\n', { mode: 0o600 })
  process.stdout.write(JSON.stringify({ passed: report.passed, checks: report.checks.length, cleanup: report.cleanup, esRestored: report.esRestored }) + '\n')
}
