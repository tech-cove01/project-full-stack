// GO-073 isolated sustained transaction measurement. Never point at a shared DB.
// A new evidence directory and explicit connection settings are mandatory.
import { randomInt } from 'node:crypto'
import { spawn } from 'node:child_process'
import { mkdir, mkdtemp, readFile, unlink, rmdir, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import net from 'node:net'

const required = ['MYSQL_HOST', 'MYSQL_PORT', 'MYSQL_DATABASE', 'MYSQL_USER', 'MYSQL_PASSWORD', 'REDIS_HOST', 'REDIS_PORT', 'API_BASE', 'GO073_EVIDENCE']
for (const key of required) if (!process.env[key]) throw new Error(`explicit ${key} required`)
if (process.env.MYSQL_HOST !== '127.0.0.1' || process.env.REDIS_HOST !== '127.0.0.1' || !/^http:\/\/127\.0\.0\.1:\d+$/.test(process.env.API_BASE)) throw new Error('loopback isolated services required')
if (process.env.MYSQL_PORT !== '3420' || process.env.REDIS_PORT !== '6520' || process.env.MYSQL_DATABASE !== 'project_full_stack_go' || process.env.API_BASE !== 'http://127.0.0.1:8220' || !/^go073-trade-[0-9a-f]{40}$/.test(process.env.GO073_REVISION || '')) throw new Error('dedicated GO-073 trade services and full revision required')
const rate = Number(process.env.GO073_RATE || 5)
const warmSeconds = Number(process.env.GO073_WARM_SECONDS || 15)
const stableSeconds = Number(process.env.GO073_STABLE_SECONDS || 180)
if (![rate, warmSeconds, stableSeconds].every(Number.isSafeInteger) || rate < 1 || rate > 100 || warmSeconds < 1 || stableSeconds < 1) throw new Error('invalid load parameters')
const count = rate * (warmSeconds + 2) + 10 + rate * stableSeconds + 50
if (count > 30000) throw new Error('fixture limit exceeded')
const stock = count + 100
const dir = resolve(process.env.GO073_EVIDENCE)
await mkdir(dir, { recursive: false }) // never overwrite previous evidence
const temp = await mkdtemp(join(tmpdir(), 'go073-trade-'))
const inputPath = join(temp, 'sessions.json')
const base = process.env.API_BASE
const sqlBin = process.env.MYSQL_BIN || 'mysql'
const mysqlArgs = ['--protocol=TCP', `--host=${process.env.MYSQL_HOST}`, `--port=${process.env.MYSQL_PORT}`, `--user=${process.env.MYSQL_USER}`, '--default-character-set=utf8mb4', '--batch', '--skip-column-names', process.env.MYSQL_DATABASE]
const mysqlEnv = { ...process.env, MYSQL_PWD: process.env.MYSQL_PASSWORD }
const evidence = { task: 'GO-073', model: { rate, warmSeconds, stableSeconds, users: count, stock }, started: new Date().toISOString(), passed: false }
let activityId = null
let fixturePrefix = null, ownerPhone = null, fixtureName = null
let prefixSafe = false, ownerSafe = false, merchantSafe = false
let stopPoll = false
let monitor = null

function run(binary, args, options = {}) {
  return new Promise((resolveRun, reject) => {
    const child = spawn(binary, args, { ...options, windowsHide: true })
    let stdout = '', stderr = ''
    child.stdout?.on('data', chunk => { stdout += chunk })
    child.stderr?.on('data', chunk => { stderr += chunk })
    child.on('error', reject)
    child.on('close', code => resolveRun({ code, stdout, stderr }))
    if (options.input !== undefined) child.stdin.end(options.input)
  })
}
async function sql(query) {
  const result = await run(sqlBin, mysqlArgs, { env: mysqlEnv, input: `SET time_zone='+00:00'; ${query}` })
  if (result.code !== 0) throw new Error('isolated MySQL query failed')
  return result.stdout.trim()
}
async function api(method, path, body, token, key) {
  const headers = { 'Content-Type': 'application/json' }
  if (token) headers.authorization = token
  if (key) headers['Idempotency-Key'] = key
  const response = await fetch(base + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(10000) })
  return { status: response.status, body: await response.json() }
}
async function login(phone) {
  const code = await api('POST', `/user/code?phone=${phone}`)
  if (!code.body.success || code.body.data?.mode !== 'development') throw new Error('development code issuance failed')
  const loginResult = await api('POST', '/user/login', { phone, code: code.body.data.code })
  if (!loginResult.body.success || typeof loginResult.body.data !== 'string') throw new Error('development login failed')
  return loginResult.body.data
}
const pause = ms => new Promise(resolvePause => setTimeout(resolvePause, ms))
const percentile = (items, p) => {
  if (!items.length) return null
  const sorted = [...items].sort((a, b) => a - b)
  return sorted[Math.ceil(sorted.length * p) - 1]
}
// Small single-command RESP client; the instance is required to be loopback.
async function redis(...parts) {
  return new Promise((resolveRedis, reject) => {
    const socket = net.createConnection({ host: process.env.REDIS_HOST, port: Number(process.env.REDIS_PORT) })
    let buffer = Buffer.alloc(0)
    const request = `*${parts.length}\r\n` + parts.map(part => `$${Buffer.byteLength(String(part))}\r\n${part}\r\n`).join('')
    socket.setTimeout(5000, () => socket.destroy(new Error('redis timeout')))
    socket.on('connect', () => socket.write(request))
    socket.on('error', reject)
    function parse(position) {
      const end = buffer.indexOf('\r\n', position)
      if (end < 0) return null
      const type = String.fromCharCode(buffer[position]), head = buffer.subarray(position + 1, end).toString(), next = end + 2
      if (type === '+' || type === ':' || type === '-') return { value: type === ':' ? Number(head) : head, next }
      if (type === '$') { const len = Number(head); if (len < 0) return { value: null, next }; if (buffer.length < next + len + 2) return null; return { value: buffer.subarray(next, next + len).toString(), next: next + len + 2 } }
      if (type === '*') { const items = []; let at = next; for (let i = 0; i < Number(head); i++) { const item = parse(at); if (!item) return null; items.push(item.value); at = item.next }; return { value: items, next: at } }
      throw new Error('invalid Redis response')
    }
    socket.on('data', chunk => { buffer = Buffer.concat([buffer, chunk]); const result = parse(0); if (result) { socket.end(); resolveRedis(result.value) } })
  })
}

try {
  for (const [service, port, target] of [['mysql', '3420', '3306/tcp'], ['redis', '6520', '6379/tcp']]) {
    const inspect = await run('docker', ['inspect', `project-full-stack-go073-trade-${service}-1`, '--format', '{{json .Config.Labels}}|{{json .NetworkSettings.Ports}}|{{.State.Running}}'])
    if (inspect.code !== 0) throw new Error('dedicated container inspect failed')
    const [labelsRaw, portsRaw, running] = inspect.stdout.trim().split('|')
    const labels = JSON.parse(labelsRaw), ports = JSON.parse(portsRaw)
    if (labels['com.docker.compose.project'] !== 'project-full-stack-go073-trade' || labels['com.docker.compose.service'] !== service || running !== 'true' || !ports[target]?.some(item => item.HostIp === '127.0.0.1' && item.HostPort === port)) throw new Error('dedicated container ownership or port mismatch')
  }
  const health = await api('GET', '/health/ready')
  if (health.status !== 200 || health.body.status !== 'UP' || health.body.service !== 'project-full-stack-go' || health.body.version !== process.env.GO073_REVISION) throw new Error('dedicated API revision health mismatch')
  const prefix = `139${String(randomInt(1000, 10000)).padStart(4, '0')}`
  fixturePrefix = prefix
  if (count > 10000) throw new Error('user prefix supports at most 10000 users per run')
  if (Number(await sql(`SELECT COUNT(*) FROM tb_user WHERE phone LIKE '${prefix}%'`)) !== 0) throw new Error('fixture prefix already exists')
  prefixSafe = true
  ownerPhone = `138${randomInt(10000000, 100000000)}`
  if (Number(await sql(`SELECT COUNT(*) FROM tb_user WHERE phone='${ownerPhone}'`)) !== 0) throw new Error('owner phone collision')
  ownerSafe = true
  fixtureName = `go073-${randomInt(100000000, 1000000000)}`
  if (Number(await sql(`SELECT COUNT(*) FROM tb_merchant WHERE name='${fixtureName}'`)) !== 0) throw new Error('fixture name collision')
  if (Number(await sql(`SELECT COUNT(*) FROM tb_user WHERE nick_name IN ('${fixtureName}','${fixtureName}-owner')`)) !== 0) throw new Error('fixture nickname collision')
  merchantSafe = true
  evidence.fixture = { fixtureName, ownerNick: `${fixtureName}-owner`, temporaryCredentialFile: inputPath }
  await writeFile(join(dir, 'fixture-progress.json'), JSON.stringify(evidence.fixture, null, 2) + '\n')
  const ids = (await sql(`INSERT INTO tb_user(phone,password,nick_name,icon) VALUES('${ownerPhone}','x','${fixtureName}-owner',''); SET @owner=LAST_INSERT_ID(); INSERT INTO tb_merchant(name,status) VALUES('${fixtureName}','ACTIVE'); SET @merchant=LAST_INSERT_ID(); INSERT INTO tb_shop(merchant_id,name,type_id,images,address,x,y,open_hours) VALUES(@merchant,'${fixtureName}',1,'','synthetic',0,0,'09:00-21:00'); SET @shop=LAST_INSERT_ID(); INSERT INTO tb_merchant_member(merchant_id,user_id,role_code,status) VALUES(@merchant,@owner,'OWNER','ACTIVE'); SELECT @owner,@merchant,@shop;`)).split('\t')
  if (ids.length !== 3 || ids.some(id => !/^[1-9][0-9]*$/.test(id))) throw new Error('fixture creation failed')
  const [ownerId, merchantId, shopId] = ids
  Object.assign(evidence.fixture, { ownerId, merchantId, shopId })
  await writeFile(join(dir, 'fixture-progress.json'), JSON.stringify(evidence.fixture, null, 2) + '\n')
  const owner = await login(ownerPhone)
  const now = Date.now(), iso = value => new Date(Math.floor(value / 1000) * 1000).toISOString()
  const created = await api('POST', '/merchant/activities', { shopId, title: 'GO-073 isolated trade', payValue: '1900', actualValue: '2500', totalStock: stock, beginTime: iso(now - 60000), endTime: iso(now + 3600000), redeemBeginTime: iso(now - 60000), redeemEndTime: iso(now + 7200000) }, owner, `go073-${prefix}`)
  if (!created.body.success || !/^[1-9][0-9]*$/.test(created.body.data?.id || '')) throw new Error('activity creation failed')
  activityId = created.body.data.id
  evidence.fixture.activityId = activityId
  await writeFile(join(dir, 'fixture-progress.json'), JSON.stringify(evidence.fixture, null, 2) + '\n')
  let ready = false
  for (let i = 0; i < 120; i++) { const list = await api('GET', '/merchant/activities?current=1&pageSize=50', undefined, owner); if ((list.body.data || []).some(item => item.id === activityId && item.ready && item.publishStatus === 'ACTIVE')) { ready = true; break }; await pause(250) }
  if (!ready) throw new Error('activity gate did not become ready')
  const tokens = new Array(count), phones = Array.from({ length: count }, (_, i) => prefix + String(i).padStart(4, '0'))
  // Tag synthetic accounts so a forced interruption remains recoverable without
  // persisting phone numbers. Login still uses the application's real flow.
  await sql(`INSERT INTO tb_user(phone,password,nick_name,icon) VALUES ${phones.map(phone => `('${phone}','x','${fixtureName}','')`).join(',')}`)
  const userRows = (await sql(`SELECT phone,id FROM tb_user WHERE nick_name='${fixtureName}'`)).split(/\r?\n/).map(line => line.split('\t'))
  const userByPhone = new Map(userRows)
  const userIds = phones.map(phone => userByPhone.get(phone))
  if (userRows.length !== count || userIds.some(id => !id) || new Set(userIds).size !== count) throw new Error('fixture users are not unique')
  evidence.fixture.actualUsers = count
  evidence.fixture.userIds = userIds
  await writeFile(join(dir, 'fixture-progress.json'), JSON.stringify(evidence.fixture, null, 2) + '\n')
  for (let start = 0; start < count; start += 20) {
    await Promise.all(phones.slice(start, start + 20).map(async (phone, index) => { tokens[start + index] = await login(phone) }))
  }
  if (new Set(tokens).size !== count) throw new Error('fixture sessions are not unique')
  await writeFile(inputPath, JSON.stringify({ baseUrl: base, activityId, tokens }), { mode: 0o600, flag: 'wx' })
  const observed = new Map(), polls = []
  let pollError = null
  monitor = (async () => {
    while (!stopPoll) {
      const start = Date.now()
      try {
        const rows = await sql(`SELECT id,user_id,status FROM tb_voucher_order WHERE voucher_id=${activityId}`)
        const end = Date.now()
        let newCount = 0
        for (const line of rows ? rows.split(/\r?\n/) : []) {
          const [id, userId, status] = line.split('\t')
          if (!observed.has(id)) { observed.set(id, { id, userId, status: Number(status), firstObservedAt: end, queryStartedAt: start }); newCount++ }
        }
        polls.push({ start, end, count: observed.size, newCount })
      } catch (error) { pollError = String(error.message); break }
      await pause(Math.max(0, 1000 - (Date.now() - start)))
    }
  })()
  const k6Env = { ...process.env, GO073_INPUT: inputPath, GO073_RATE: String(rate), GO073_WARM_SECONDS: String(warmSeconds), GO073_STABLE_SECONDS: String(stableSeconds) }
  const loadStart = Date.now()
  const k6 = await run('k6', ['run', '--quiet', '--console-output', join(dir, 'requests.log'), '--summary-export', join(dir, 'k6-summary.json'), 'scripts/load/go073-trade.js'], { env: k6Env })
  const loadEnd = Date.now()
  await writeFile(join(dir, 'k6-output.txt'), k6.stdout + '\n' + k6.stderr)
  if (k6.code !== 0) throw new Error(`k6 exited ${k6.code}`)
  const log = await readFile(join(dir, 'requests.log'), 'utf8')
  const requests = log.trim().split(/\r?\n/).filter(Boolean).map(line => {
    const match = line.match(/\bmsg=("(?:\\.|[^"\\])*")/)
    if (!match) throw new Error('unexpected k6 request log format')
    return JSON.parse(JSON.parse(match[1]))
  })
  const unique = new Map()
  for (const item of requests) if (item.category === 'success' && !unique.has(item.orderId)) unique.set(item.orderId, item)
  const drainStart = Date.now()
  while (!pollError && Date.now() - drainStart < 90000 && [...unique.keys()].some(id => !observed.has(id))) await pause(500)
  stopPoll = true
  await monitor
  if (pollError) throw new Error(`order observation failed: ${pollError}`)
  const rows = (await sql(`SELECT id,user_id,status FROM tb_voucher_order WHERE voucher_id=${activityId}`)).split(/\r?\n/).filter(Boolean).map(line => { const [id, userId, status] = line.split('\t'); return { id, userId, status: Number(status) } })
  const rowById = new Map(rows.map(row => [row.id, row]))
  const augmented = requests.map(item => ({ ...item, expectedUserId: userIds[item.index], observed: observed.get(item.orderId) || null, final: rowById.get(item.orderId) || null }))
  const summary = {}
  for (const phase of ['warm', 'stable']) {
    const items = augmented.filter(item => item.phase === phase), seen = new Set()
    let repeatedSuccess = 0
    const accepted = items.filter(item => { if (item.category !== 'success') return false; if (seen.has(item.orderId)) { repeatedSuccess++; return false }; seen.add(item.orderId); return true })
    const final = accepted.filter(item => item.final), matched = final.filter(item => item.final.userId === item.expectedUserId)
    const delays = accepted.filter(item => item.observed).map(item => item.observed.firstObservedAt - item.ended)
    const categories = Object.fromEntries(['success', 'sold_out', 'duplicate', 'other_business', 'rate_limited', 'system_error'].map(key => [key, items.filter(item => item.category === key).length]))
    summary[phase] = { requests: items.length, categories, repeatedSuccess, uniqueAccepted: accepted.length, durableOrders: final.length, matchingUsers: matched.length,
      unresolved: accepted.length - final.length, acceptanceToOrderRatio: accepted.length ? final.length / accepted.length : null,
      systemErrorRate: items.length ? categories.system_error / items.length : null,
      admissionMs: { p95: percentile(items.map(item => item.durationMs), .95), p99: percentile(items.map(item => item.durationMs), .99) },
      acceptedAdmissionMs: { p95: percentile(accepted.map(item => item.durationMs), .95), p99: percentile(accepted.map(item => item.durationMs), .99) },
      observationDelayMs: { count: delays.length, negativeCount: delays.filter(value => value < 0).length, p95Raw: percentile(delays, .95), p99Raw: percentile(delays, .99), p95Clamped: percentile(delays.map(value => Math.max(0, value)), .95), p99Clamped: percentile(delays.map(value => Math.max(0, value)), .99) },
      cohortOrdersPerSecond: final.length / (phase === 'warm' ? warmSeconds : stableSeconds) }
  }
  const previousIntervals = polls.slice(1).map((poll, index) => poll.start - polls[index].start)
  const mysqlStock = Number(await sql(`SELECT stock FROM tb_seckill_voucher WHERE voucher_id=${activityId}`))
  const redisStock = Number(await redis('GET', `seckill:stock:${activityId}`))
  const redisAccepted = await redis('HLEN', `seckill:accepted:${activityId}`)
  const settleStarted = Date.now(), settle = []
  let pending, streamGroups, group
  do {
    pending = await redis('XPENDING', 'stream.orders', 'g1')
    const groups = await redis('XINFO', 'GROUPS', 'stream.orders')
    streamGroups = groups.map(row => Object.fromEntries(Array.from({ length: row.length / 2 }, (_, index) => [row[index * 2], row[index * 2 + 1]])))
    group = streamGroups.find(item => item.name === 'g1')
    settle.push({ at: Date.now(), pending: pending[0], lag: group?.lag ?? null, lastDeliveredId: group?.['last-delivered-id'] ?? null })
    if (pending[0] === 0 && group?.lag === 0) break
    await pause(250)
  } while (Date.now() - settleStarted < 30000)
  const streamLength = await redis('XLEN', 'stream.orders')
  const reviews = Number(await sql(`SELECT COUNT(*) FROM tb_order_review WHERE voucher_id=${activityId} AND status <> 'RESOLVED'`))
  const rejections = Number(await sql(`SELECT COUNT(*) FROM tb_outbox WHERE aggregate_type='RESERVATION' AND JSON_UNQUOTE(JSON_EXTRACT(payload,'$.voucherId'))='${activityId}'`))
  const k6Summary = JSON.parse(await readFile(join(dir, 'k6-summary.json'), 'utf8'))
  const firstStable = augmented.find(item => item.phase === 'stable')?.started
  const stableIds = new Set(augmented.filter(item => item.phase === 'stable' && item.category === 'success').map(item => item.orderId))
  const stableWindowObserved = firstStable === undefined ? null : [...observed.values()].filter(item => stableIds.has(item.id) && item.firstObservedAt >= firstStable && item.firstObservedAt < firstStable + stableSeconds * 1000).length
  const dropped = k6Summary.metrics?.dropped_iterations?.count
  if (!Number.isFinite(dropped)) throw new Error('k6 did not export dropped iteration count')
  evidence.result = { loadStart, loadEnd, phases: summary, droppedIterations: dropped,
    poll: { count: polls.length, intervalP95Ms: percentile(previousIntervals, .95), intervalMaxMs: previousIntervals.length ? Math.max(...previousIntervals) : null, queryP95Ms: percentile(polls.map(item => item.end - item.start), .95), queryMaxMs: polls.length ? Math.max(...polls.map(item => item.end - item.start)) : null },
    stableWindow: { approximateStartFromFirstRequest: firstStable, durationSeconds: stableSeconds, stableCohortOrdersObserved: stableWindowObserved, stableCohortObservedPerSecond: stableWindowObserved === null ? null : stableWindowObserved / stableSeconds },
    reconciliation: { durableRows: rows.length, uniqueSuccessIds: unique.size, mysqlStock, redisStock, redisAccepted, pending, streamLength, streamGroups, reviews, rejections, unexpectedOrderIds: rows.filter(row => !unique.has(row.id)).length } }
  evidence.reconciliationPassed = summary.stable.requests > 0 && summary.stable.uniqueAccepted > 0 && summary.warm.unresolved === 0 && summary.stable.unresolved === 0 && summary.warm.matchingUsers === summary.warm.uniqueAccepted && summary.stable.matchingUsers === summary.stable.uniqueAccepted && summary.warm.uniqueAccepted + summary.stable.uniqueAccepted === unique.size && rows.length === unique.size && mysqlStock === stock - rows.length && redisStock === mysqlStock && redisAccepted === rows.length && reviews === 0 && rejections === 0 && pending[0] === 0 && group?.lag === 0
  evidence.measurementUsable = dropped === 0 && summary.warm.categories.system_error === 0 && summary.stable.categories.system_error === 0
  evidence.passed = evidence.reconciliationPassed && evidence.measurementUsable
  await writeFile(join(dir, 'requests.json'), JSON.stringify(augmented, null, 2) + '\n')
  await writeFile(join(dir, 'polls.json'), JSON.stringify(polls, null, 2) + '\n')
  await writeFile(join(dir, 'orders.json'), JSON.stringify(rows, null, 2) + '\n')
  await writeFile(join(dir, 'settle.json'), JSON.stringify(settle, null, 2) + '\n')
} catch (error) {
  evidence.error = String(error.message)
  // Preserve owned IDs even when setup failed halfway; never persist phones or tokens.
  try {
    if (ownerSafe && prefixSafe) {
      evidence.recovery = {
        ownerIds: merchantSafe ? (await sql(`SELECT id FROM tb_user WHERE nick_name='${fixtureName}-owner'`)).split(/\r?\n/).filter(Boolean) : [],
        userIds: merchantSafe ? (await sql(`SELECT id FROM tb_user WHERE nick_name='${fixtureName}'`)).split(/\r?\n/).filter(Boolean) : [],
        merchantIds: merchantSafe ? (await sql(`SELECT id FROM tb_merchant WHERE name='${fixtureName}'`)).split(/\r?\n/).filter(Boolean) : [],
        activityId,
      }
    }
  } catch { evidence.recoveryError = 'could not query fixture identifiers; preserve isolated volume for inspection' }
} finally {
  stopPoll = true
  if (monitor) await monitor
  // The isolated data volume is intentionally preserved for independent audit.
  // Temporary bearer credentials are removed even if the measurement fails.
  try { await unlink(inputPath) } catch (error) { if (error.code !== 'ENOENT') throw error }
  await rmdir(temp)
  evidence.finished = new Date().toISOString()
  await writeFile(join(dir, 'result.json'), JSON.stringify(evidence, null, 2) + '\n')
}
console.log(JSON.stringify({ task: evidence.task, passed: evidence.passed, reconciliationPassed: evidence.reconciliationPassed, measurementUsable: evidence.measurementUsable, result: evidence.result, error: evidence.error || null }))
process.exit(evidence.passed ? 0 : 1)
