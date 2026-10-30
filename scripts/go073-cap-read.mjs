// GO-073-CAP read workload. Only the explicitly owned loopback environment is allowed.
// prepare/run/finish keep credentials outside the repository and retain SQL fixtures.
import assert from 'node:assert/strict'
import { randomBytes, randomInt, createHash } from 'node:crypto'
import { spawn } from 'node:child_process'
import { createWriteStream } from 'node:fs'
import { mkdir, mkdtemp, readFile, writeFile, unlink, rmdir } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { resolve, dirname, join } from 'node:path'
import net from 'node:net'

const owned = { project: 'project-full-stack-cap-read', mysql: '3451', redis: '6551', api: 'http://127.0.0.1:8251', database: 'project_full_stack_go' }
for (const name of ['MYSQL_USER', 'MYSQL_PASSWORD', 'CAP_SOURCE_SHA', 'CAP_READ_EXPECTED_REVISION']) assert.ok(process.env[name], `explicit ${name} required`)
assert.match(process.env.CAP_SOURCE_SHA, /^[0-9a-f]{40}$/)
assert.equal(process.env.CAP_READ_EXPECTED_REVISION, `capacity-${process.env.CAP_SOURCE_SHA}`, 'API revision must bind the declared API source SHA')
const fixtureSourceSha = process.env.CAP_READ_FIXTURE_SOURCE_SHA ?? process.env.CAP_SOURCE_SHA
assert.match(fixtureSourceSha, /^[0-9a-f]{40}$/, 'fixture provenance must be a full source SHA')
assert.equal(process.env.MYSQL_HOST, '127.0.0.1'); assert.equal(process.env.MYSQL_PORT, owned.mysql); assert.equal(process.env.MYSQL_DATABASE, owned.database)
assert.equal(process.env.REDIS_HOST, '127.0.0.1'); assert.equal(process.env.REDIS_PORT, owned.redis); assert.equal(process.env.API_BASE, owned.api)
const action = process.argv[2]
assert.ok(['prepare', 'run', 'finish'].includes(action), 'use prepare, run or finish')
if (action === 'prepare') assert.equal(fixtureSourceSha, process.env.CAP_SOURCE_SHA, 'a new fixture must originate from the current API source')
const pause = ms => new Promise(r => setTimeout(r, ms))
const sqlBin = process.env.MYSQL_BIN || 'mysql'

async function command(binary, args, { input, env = process.env, output, onStart, stopFile } = {}) {
  return new Promise((resolveCommand, reject) => {
    const child = spawn(binary, args, { env, windowsHide: true })
    let stdout = '', stderr = ''
    let interval = null, readingStop = false, externalStop = null
    const file = output ? createWriteStream(output, { flags: 'wx' }) : null
    Promise.resolve(onStart?.(child.pid)).catch(error => { child.kill(); reject(error) })
    if (stopFile) interval = setInterval(async () => {
      if (readingStop || externalStop) return
      readingStop = true
      try {
        const raw = await readFile(stopFile, 'utf8')
        let reason; try { reason = JSON.parse(raw) } catch { reason = { invalidStopFile: true } }
        externalStop = { at: Date.now(), reason }
        // The child was created by this invocation; no name-based/global kill.
        child.kill('SIGTERM')
      } catch (error) { if (error.code !== 'ENOENT') { externalStop = { at: Date.now(), reason: { stopReadError: true } }; child.kill('SIGTERM') } }
      finally { readingStop = false }
    }, 500)
    child.stdout.on('data', chunk => { if (file) { if (!file.write(chunk)) child.stdout.pause() } else stdout += chunk })
    child.stderr.on('data', chunk => { if (file) { if (!file.write(chunk)) child.stderr.pause() } else stderr += chunk })
    file?.on('drain', () => { child.stdout.resume(); child.stderr.resume() })
    file?.on('error', error => { child.kill(); reject(error) })
    child.on('error', error => { if (interval) clearInterval(interval); reject(error) })
    child.on('close', code => { if (interval) clearInterval(interval); if (file) file.end(() => resolveCommand({ code, externalStop })); else resolveCommand({ code, stdout, stderr, externalStop }) })
    child.stdin.end(input)
  })
}
async function sql(statement, observer = false) {
  const user = observer ? process.env.CAP_READ_OBSERVER_USER : process.env.MYSQL_USER
  const password = observer ? process.env.CAP_READ_OBSERVER_PASSWORD : process.env.MYSQL_PASSWORD
  assert.ok(user && password, observer ? 'explicit read observer MySQL credentials required' : 'MySQL credentials required')
  const result = await command(sqlBin, ['--protocol=TCP', '--host=127.0.0.1', '--port=' + owned.mysql, '--user=' + user, '--default-character-set=utf8mb4', '--batch', '--skip-column-names', owned.database], { input: `SET NAMES utf8mb4; SET time_zone='+08:00'; ${statement}`, env: { ...process.env, MYSQL_PWD: password } })
  if (result.code !== 0) throw new Error('owned fixture or observation SQL failed; diagnostics suppressed')
  return result.stdout.trim()
}
async function redis(...parts) {
  return new Promise((resolveRedis, reject) => {
    const socket = net.createConnection({ host: '127.0.0.1', port: Number(owned.redis) })
    let buffer = Buffer.alloc(0)
    socket.setTimeout(5000, () => socket.destroy(new Error('Redis timeout')))
    socket.on('connect', () => socket.write(`*${parts.length}\r\n` + parts.map(p => `$${Buffer.byteLength(String(p))}\r\n${p}\r\n`).join('')))
    socket.on('error', reject)
    socket.on('data', chunk => {
      buffer = Buffer.concat([buffer, chunk]); const at = buffer.indexOf('\r\n'); if (at < 0) return
      const kind = buffer.toString('ascii', 0, 1), head = buffer.toString('utf8', 1, at)
      if (kind === '-') { socket.destroy(); reject(new Error('Redis command failed')); return }
      if (kind === '$') { const n = Number(head); if (buffer.length < at + 2 + n + 2) return; socket.end(); resolveRedis(buffer.toString('utf8', at + 2, at + 2 + n)); return }
      if (kind === '+' || kind === ':') { socket.end(); resolveRedis(head) }
    })
  })
}
async function api(method, path, body, token) {
  const response = await fetch(owned.api + path, { method, headers: { 'Content-Type': 'application/json', ...(token ? { authorization: token } : {}) }, ...(body ? { body: JSON.stringify(body) } : {}), signal: AbortSignal.timeout(10000) })
  return { status: response.status, body: await response.json() }
}
async function guard() {
  for (const [service, target, port] of [['mysql', '3306/tcp', owned.mysql], ['redis', '6379/tcp', owned.redis]]) {
    const r = await command('docker', ['inspect', `${owned.project}-${service}-1`, '--format', '{{json .Config.Labels}}|{{json .NetworkSettings.Ports}}|{{.State.Running}}'])
    assert.equal(r.code, 0, 'owned container inspect failed')
    const [labelsText, portsText, running] = r.stdout.trim().split('|'), labels = JSON.parse(labelsText), ports = JSON.parse(portsText)
    assert.equal(labels['com.docker.compose.project'], owned.project); assert.equal(labels['com.docker.compose.service'], service); assert.equal(running, 'true')
    assert.ok(ports[target]?.some(p => p.HostIp === '127.0.0.1' && p.HostPort === port), 'owned port binding mismatch')
  }
  const health = await api('GET', '/health/ready')
  assert.equal(health.status, 200); assert.equal(health.body.status, 'UP'); assert.equal(health.body.service, 'project-full-stack-go'); assert.equal(health.body.version, process.env.CAP_READ_EXPECTED_REVISION)
}
const manifestPath = process.env.CAP_READ_FIXTURE ? resolve(process.env.CAP_READ_FIXTURE) : null
async function loadFixture() {
  assert.ok(manifestPath, 'explicit CAP_READ_FIXTURE required')
  const manifestBytes = await readFile(manifestPath)
  const manifest = JSON.parse(manifestBytes.toString('utf8'))
  assert.equal(manifest.project, owned.project); assert.equal(manifest.baseUrl, owned.api); assert.equal(manifest.sourceSha, fixtureSourceSha)
  assert.equal(manifest.expectedRevision, `capacity-${manifest.sourceSha}`, 'fixture revision must retain its original source binding')
  const runtime = JSON.parse(await readFile(manifest.credentialFile, 'utf8'))
  assert.equal(runtime.fixture, manifest.fixture); assert.ok(Array.isArray(runtime.readers) && runtime.readers.length <= 32)
  return { manifest, runtime, manifestSHA256: createHash('sha256').update(manifestBytes).digest('hex') }
}
function parseCommands(text) {
  return Object.fromEntries(text.split(/\r?\n/).filter(x => x.startsWith('cmdstat_')).map(line => { const [name, values] = line.split(':'); return [name.slice(8), Object.fromEntries(values.split(',').map(kv => { const [k, v] = kv.split('='); return [k, Number(v)] }))] }))
}
const mysqlStatusNames = ['Uptime', 'Questions', 'Queries', 'Com_select', 'Com_insert', 'Com_update', 'Com_delete', 'Com_commit', 'Com_stmt_execute', 'Com_stmt_prepare', 'Com_stmt_close', 'Com_stmt_reprepare', 'Performance_schema_digest_lost', 'Performance_schema_prepared_statements_lost']
const mysqlStatementEvents = ['statement/com/Execute', 'statement/com/Prepare', 'statement/sql/select', 'statement/sql/insert', 'statement/sql/update', 'statement/sql/delete', 'statement/sql/commit']
function unsignedCounter(text) {
  assert.match(text, /^(0|[1-9]\d*)$/, 'invalid unsigned counter')
  const value = Number(text)
  assert.ok(Number.isSafeInteger(value), 'counter exceeds exact integer range')
  return value
}
function parseStatusRows(text) {
  const rawRows = text.split(/\r?\n/).filter(Boolean).map(line => {
    const fields = line.split('\t')
    assert.equal(fields.length, 2, 'unexpected SHOW GLOBAL STATUS row')
    return { name: fields[0], value: fields[1] }
  })
  const counters = {}
  for (const { name, value } of rawRows) {
    assert.ok(mysqlStatusNames.includes(name) && !Object.hasOwn(counters, name), 'unexpected or duplicate status counter')
    counters[name] = unsignedCounter(value)
  }
  assert.deepEqual(Object.keys(counters).sort(), [...mysqlStatusNames].sort(), 'missing required SHOW GLOBAL STATUS counter')
  return { rawRows, counters }
}
function parseStatementEvents(text) {
  const rows = text.split(/\r?\n/).filter(Boolean).map(line => {
    const fields = line.split('\t')
    assert.equal(fields.length, 6, 'unexpected statement event row')
    const [event, count, picoseconds, errors, examined, sent] = fields
    assert.match(picoseconds, /^(0|[1-9]\d*)$/, 'invalid event timer')
    return { event, count: unsignedCounter(count), picoseconds, errors: unsignedCounter(errors), examined: unsignedCounter(examined), sent: unsignedCounter(sent) }
  })
  assert.deepEqual(rows.map(x => x.event).sort(), [...mysqlStatementEvents].sort(), 'missing or duplicate statement event')
  return rows
}
async function snapshot(label) {
  const started = Date.now()
  const digest = await sql(`SELECT DIGEST, DIGEST_TEXT, COUNT_STAR, SUM_TIMER_WAIT, SUM_ROWS_EXAMINED, SUM_ROWS_SENT FROM performance_schema.events_statements_summary_by_digest WHERE SCHEMA_NAME='${owned.database}' AND DIGEST IS NOT NULL AND DIGEST_TEXT NOT LIKE '%performance_schema%' ORDER BY DIGEST`, true)
  // P_S global_status omits Com_* counters. Binary prepared executions also do
  // not populate our digest view; neither absence may be interpreted as zero.
  const statusStartedAt = Date.now()
  const showGlobalStatusRaw = await sql(`SHOW GLOBAL STATUS WHERE Variable_name IN (${mysqlStatusNames.map(name => `'${name}'`).join(',')})`, true)
  const statusEndedAt = Date.now()
  const globalStatus = { ...parseStatusRows(showGlobalStatusRaw), raw: showGlobalStatusRaw, startedAt: statusStartedAt, endedAt: statusEndedAt }
  const eventRaw = await sql(`SELECT EVENT_NAME,COUNT_STAR,SUM_TIMER_WAIT,SUM_ERRORS,SUM_ROWS_EXAMINED,SUM_ROWS_SENT FROM performance_schema.events_statements_summary_global_by_event_name WHERE EVENT_NAME IN (${mysqlStatementEvents.map(name => `'${name}'`).join(',')}) ORDER BY EVENT_NAME`, true)
  const redisRaw = await redis('INFO', 'commandstats')
  return { label, started, ended: Date.now(), globalStatus, statementEvents: parseStatementEvents(eventRaw), statementEventsRaw: eventRaw, digestLost: globalStatus.counters.Performance_schema_digest_lost, digests: digest.split(/\r?\n/).filter(Boolean).map(row => { const [digest, text, count, picoseconds, examined, sent] = row.split('\t'); return { digest, text, count: unsignedCounter(count), picoseconds, examined: unsignedCounter(examined), sent: unsignedCounter(sent) } }), redis: parseCommands(redisRaw) }
}
function deltaSnapshots(before, after) {
  const old = new Map(before.digests.map(x => [x.digest, x]))
  const sql = after.digests.map(row => ({ ...row, count: row.count - (old.get(row.digest)?.count || 0), examined: row.examined - (old.get(row.digest)?.examined || 0), sent: row.sent - (old.get(row.digest)?.sent || 0), timerPicoseconds: String(BigInt(row.picoseconds) - BigInt(old.get(row.digest)?.picoseconds || '0')) }))
  const globalCommands = Object.fromEntries(mysqlStatusNames.map(name => {
    assert.ok(Object.hasOwn(before.globalStatus.counters, name) && Object.hasOwn(after.globalStatus.counters, name), 'missing snapshot status counter')
    return [name, after.globalStatus.counters[name] - before.globalStatus.counters[name]]
  }))
  const statusElapsedMs = (after.globalStatus.startedAt + after.globalStatus.endedAt - before.globalStatus.startedAt - before.globalStatus.endedAt) / 2
  const statusUncertaintyMs = 2000 + after.globalStatus.endedAt - after.globalStatus.startedAt + before.globalStatus.endedAt - before.globalStatus.startedAt
  const uptimeConsistent = statusElapsedMs >= 0 && globalCommands.Uptime >= 0 && Math.abs(globalCommands.Uptime * 1000 - statusElapsedMs) <= statusUncertaintyMs
  const priorEvents = new Map(before.statementEvents.map(row => [row.event, row]))
  const statementEvents = after.statementEvents.map(row => {
    const previous = priorEvents.get(row.event)
    assert.ok(previous, 'statement event disappeared from snapshot')
    return { event: row.event, count: row.count - previous.count, errors: row.errors - previous.errors, examined: row.examined - previous.examined, sent: row.sent - previous.sent, timerPicoseconds: String(BigInt(row.picoseconds) - BigInt(previous.picoseconds)) }
  })
  const redis = Object.fromEntries(Object.entries(after.redis).map(([name, fields]) => [name, { calls: fields.calls - (before.redis[name]?.calls || 0), usec: fields.usec - (before.redis[name]?.usec || 0), failed: (fields.failed_calls || 0) - (before.redis[name]?.failed_calls || 0) }]))
  return { globalCommands, statementEvents, sql: sql.filter(row => row.count > 0), redis: Object.fromEntries(Object.entries(redis).filter(([, values]) => values.calls > 0)), scope: 'whole isolated instance between sequential snapshots; includes API/background/helper/observer SQL and Redis commands, not per-endpoint or public-count cache DB loads', commandCountersOverlap: 'Com_select and Com_stmt_execute overlap; report separately, never add them as SQL total', digestCoverage: 'auxiliary only: binary prepared SQL is absent from this digest view; empty SQL digest delta does not mean no database access', publicCountDatabaseLoads: null, uptimeConsistent, statusElapsedMs, statusUncertaintyMs, counterResets: !uptimeConsistent || Object.values(globalCommands).some(x => x < 0) || statementEvents.some(x => x.count < 0 || x.errors < 0 || x.examined < 0 || x.sent < 0 || BigInt(x.timerPicoseconds) < 0n) || sql.some(x => x.count < 0 || x.examined < 0 || x.sent < 0 || BigInt(x.timerPicoseconds) < 0n) || Object.values(redis).some(x => x.calls < 0 || x.usec < 0 || x.failed < 0), lostDigestsDelta: after.digestLost - before.digestLost, preparedStatementsLostDelta: globalCommands.Performance_schema_prepared_statements_lost, missingAfterDigests: before.digests.filter(x => !after.digests.some(y => y.digest === x.digest)).map(x => x.digest) }
}

await guard()
if (action === 'prepare') {
  assert.ok(process.env.CAP_READ_FIXTURE_DIR, 'explicit new CAP_READ_FIXTURE_DIR required')
  const dir = resolve(process.env.CAP_READ_FIXTURE_DIR); await mkdir(dir, { recursive: false })
  const fixture = 'capread-' + randomBytes(5).toString('hex'), runtimeDir = await mkdtemp(join(tmpdir(), 'cap-read-')), credentialFile = join(runtimeDir, 'input.json')
  const manifest = { task: 'GO-073-CAP-03', fixture, project: owned.project, baseUrl: owned.api, sourceSha: process.env.CAP_SOURCE_SHA, expectedRevision: process.env.CAP_READ_EXPECTED_REVISION, credentialFile, pageSize: 20, authors: 32, readers: 32, notesPerAuthor: 200, noteCount: 6400, created: new Date().toISOString(), status: 'PREPARING' }
  await writeFile(join(dir, 'fixture.json'), JSON.stringify(manifest, null, 2) + '\n', { flag: 'wx' })
  const usedPhones = new Set(), phones = Array.from({ length: 64 }, () => { let phone; do { phone = '136' + randomInt(10000000, 100000000) } while (usedPhones.has(phone)); usedPhones.add(phone); return phone })
  const names = phones.map((_, i) => fixture + (i < 32 ? '-a' : '-r') + String(i % 32).padStart(2, '0'))
  assert.equal(await sql(`SELECT COUNT(*) FROM tb_user WHERE nick_name LIKE '${fixture}%' OR phone IN(${phones.map(p => `'${p}'`).join(',')})`), '0', 'fixture collision')
  assert.equal(await sql('SELECT COUNT(*) FROM tb_shop WHERE id=1'), '1', 'synthetic demo shop 1 required')
  await sql(`INSERT INTO tb_user(phone,password,nick_name,icon) VALUES ${phones.map((p, i) => `('${p}','x','${names[i]}','')`).join(',')}`)
  const userRows = (await sql(`SELECT id,nick_name FROM tb_user WHERE nick_name LIKE '${fixture}%' ORDER BY nick_name`)).split(/\r?\n/).map(line => line.split('\t'))
  const byName = new Map(userRows.map(([id, name]) => [name, id])), authorIds = names.slice(0, 32).map(n => byName.get(n)), readerIds = names.slice(32).map(n => byName.get(n))
  assert.ok([...authorIds, ...readerIds].every(x => /^[1-9]\d*$/.test(x || '')))
  Object.assign(manifest, { authorIds, readerIds }); await writeFile(join(dir, 'fixture.json'), JSON.stringify(manifest, null, 2) + '\n')
  const noteValues = []; for (let ordinal = 0; ordinal < 200; ordinal++) for (let a = 0; a < 32; a++) noteValues.push(`(1,${authorIds[a]},'${fixture}-${ordinal}-${a}','','${'x'.repeat(256)}',0,0,'PUBLISHED',1,NOW()-INTERVAL 1 HOUR,NOW()-INTERVAL 1 HOUR)`)
  await sql(`START TRANSACTION; INSERT INTO tb_follow(user_id,follow_user_id,active,version) VALUES ${readerIds.map((id, a) => `(${id},${authorIds[a]},1,1)`).join(',')}; INSERT INTO tb_blog(shop_id,user_id,title,images,content,liked,comments,status,publish_version,create_time,update_time) VALUES ${noteValues.join(',')}; COMMIT;`)
  const notes = (await sql(`SELECT id,user_id FROM tb_blog WHERE title LIKE '${fixture}%' ORDER BY create_time DESC,id DESC`)).split(/\r?\n/).map(line => line.split('\t'))
  assert.equal(notes.length, 6400)
  const readers = []
  await writeFile(credentialFile, JSON.stringify({ fixture, baseUrl: owned.api, pageSize: 20, readers }), { mode: 0o600, flag: 'wx' })
  for (let i = 0; i < 32; i++) {
    const phone = phones[32 + i], code = await api('POST', '/user/code?phone=' + phone)
    assert.equal(code.body.data?.mode, 'development')
    const login = await api('POST', '/user/login', { phone, code: code.body.data.code }); assert.equal(login.body.success, true); assert.equal(typeof login.body.data, 'string')
    const expectedIds = notes.filter(([, author]) => author === authorIds[i]).slice(0, 20).map(([id]) => id)
    readers.push({ userId: readerIds[i], authorId: authorIds[i], token: login.body.data, expectedIds })
    // Keep already-issued sessions recoverable if a later login or probe fails.
    await writeFile(credentialFile, JSON.stringify({ fixture, baseUrl: owned.api, pageSize: 20, readers }), { mode: 0o600 })
  }
  // Fixed data is inserted outside the measured window; no community events
  // were created. Kafka must remain disabled, and Feed source must be database.
  for (const reader of readers) { const result = await api('GET', '/feed/notes?limit=20', undefined, reader.token); assert.equal(result.body.data?.source, 'database'); assert.deepEqual(result.body.data.items.map(x => x.id), reader.expectedIds) }
  Object.assign(manifest, { status: 'READY', expectedPages: readers.map(({ token, ...r }) => r), noteIds: notes.map(([id]) => id), indexes: await sql('SHOW INDEX FROM tb_blog; SHOW INDEX FROM tb_follow; SHOW INDEX FROM tb_blog_like') })
  await writeFile(join(dir, 'fixture.json'), JSON.stringify(manifest, null, 2) + '\n')
  console.log(JSON.stringify({ prepared: true, manifest: join(dir, 'fixture.json'), readers: 32, notes: 6400 }))
} else if (action === 'finish') {
  const { manifest, runtime, manifestSHA256 } = await loadFixture()
  const outcomes = []; for (const reader of runtime.readers) { const r = await api('POST', '/user/logout', undefined, reader.token); outcomes.push(r.status === 200 && r.body.success === true) }
  assert.ok(outcomes.every(Boolean), 'a reader logout failed')
  await unlink(manifest.credentialFile); await rmdir(dirname(manifest.credentialFile))
  await writeFile(join(dirname(manifestPath), 'finish.json'), JSON.stringify({ at: new Date().toISOString(), logoutCount: outcomes.length, credentialFileRemoved: true, sqlFixtureRetained: true, apiSourceSha: process.env.CAP_SOURCE_SHA, apiExpectedRevision: process.env.CAP_READ_EXPECTED_REVISION, fixtureSourceSha: manifest.sourceSha, fixtureExpectedRevision: manifest.expectedRevision, fixtureManifestSHA256: manifestSHA256 }, null, 2) + '\n', { flag: 'wx' })
  console.log(JSON.stringify({ finished: true, readersLoggedOut: outcomes.length, sqlFixtureRetained: true }))
} else {
  const { manifest, runtime, manifestSHA256 } = await loadFixture()
  assert.equal(manifest.status, 'READY', 'fixture prepare did not complete'); assert.equal(runtime.readers.length, 32)
  const mode = process.env.CAP_READ_MODE, rate = Number(process.env.CAP_READ_RATE), seconds = Number(process.env.CAP_READ_SECONDS || 60), warmSeconds = Number(process.env.CAP_READ_WARM_SECONDS || 10), warmRate = Number(process.env.CAP_READ_WARM_RATE || Math.min(rate, 25)), preVUs = Number(process.env.CAP_READ_PRE_VUS || 64), maxVUs = Number(process.env.CAP_READ_MAX_VUS || 512)
  assert.ok(['hot', 'multi'].includes(mode)); assert.ok([rate, seconds, warmSeconds, warmRate, preVUs, maxVUs].every(Number.isInteger)); assert.ok(rate > 0 && rate <= 20000 && seconds >= 1 && seconds <= 600 && warmSeconds >= 1 && warmSeconds <= 60 && warmRate >= 1 && warmRate <= rate && preVUs >= 1 && maxVUs >= preVUs && maxVUs <= 4096)
  assert.ok(process.env.CAP_READ_EVIDENCE, 'explicit new CAP_READ_EVIDENCE required')
  const dir = resolve(process.env.CAP_READ_EVIDENCE); await mkdir(dir, { recursive: false })
  if (process.env.CAP_STOP_FILE) { try { await readFile(process.env.CAP_STOP_FILE); throw new Error('stop file already exists; preserve it and choose a new path') } catch (error) { if (error.code !== 'ENOENT') throw error } }
  const report = { task: 'GO-073-CAP-03', mode, rate, seconds, warmSeconds, warmRate, preVUs, maxVUs, fixture: manifest.fixture, apiSourceSha: process.env.CAP_SOURCE_SHA, apiExpectedRevision: process.env.CAP_READ_EXPECTED_REVISION, fixtureSourceSha: manifest.sourceSha, fixtureExpectedRevision: manifest.expectedRevision, fixtureManifestSHA256: manifestSHA256, pageSize: 20, activeReaders: mode === 'hot' ? 1 : 32, activeCountKeys: mode === 'hot' ? 20 : 640, thresholds: { actualOfferedRatioMin: .99, successfulFractionMin: .99, incompleteOrUnclassifiedMax: 0, droppedMax: 0, systemErrorRateMaxExclusive: .01, p95MsMax: 200, p99MsMax: 500, successfulP95MsMax: 200, successfulP99MsMax: 500, contentErrorsMax: 0 }, started: new Date().toISOString() }
  let beforeWarm = null
  async function runPhase(phase, duration) {
    if (process.env.CAP_STOP_FILE) {
      try { await readFile(process.env.CAP_STOP_FILE); report.phaseStartBlockedByStop = phase; throw new Error('resource stop already present; no new k6 phase started') }
      catch (error) { if (error.code !== 'ENOENT') throw error }
    }
    const env = { ...process.env, CAP_READ_INPUT: manifest.credentialFile, CAP_READ_MODE: mode, CAP_READ_RATE: String(phase === 'warm' ? warmRate : rate), CAP_READ_SECONDS: String(duration), CAP_READ_PHASE: phase, CAP_READ_PRE_VUS: String(preVUs), CAP_READ_MAX_VUS: String(maxVUs) }
    return command(process.env.K6_BIN || 'k6', ['run', '--quiet', '--summary-export', join(dir, `${phase}-summary.json`), 'scripts/load/go073-cap-read.js'], { env, output: join(dir, `${phase}-console.txt`), stopFile: process.env.CAP_STOP_FILE, onStart(pid) {
      const detail = JSON.stringify({ phase, helper_pid: process.pid, k6_pid: pid, k6_started_unix_ms: Date.now() }) + '\n'
      return Promise.all([writeFile(join(dir, `${phase}-process.json`), detail), writeFile(join(dir, 'process.json'), detail)])
    } })
  }
  try {
    const consumers = await sql("SELECT NAME,ENABLED FROM performance_schema.setup_consumers WHERE NAME IN('statements_digest','events_statements_current')", true)
    assert.ok(consumers.includes('statements_digest\tYES') && consumers.includes('events_statements_current\tYES'), 'performance_schema statement consumers required')
    report.performanceSchemaConsumers = consumers
    report.selectInstrument = await sql("SELECT ENABLED,TIMED FROM performance_schema.setup_instruments WHERE NAME='statement/sql/select'", true)
    assert.equal(report.selectInstrument, 'YES\tYES', 'timed statement/sql/select instrumentation required')
    beforeWarm = await snapshot('before-warm')
    const hashes = {}; for (const path of ['scripts/go073-cap-read.mjs', 'scripts/load/go073-cap-read.js']) hashes[path] = createHash('sha256').update(await readFile(path)).digest('hex')
    report.scriptSHA256 = hashes
    const warm = await runPhase('warm', warmSeconds); report.warmExit = warm.code; report.warmExternalStop = warm.externalStop
    assert.equal(report.warmExit, 0, 'warm probe failed; no stable workload started')
    assert.ok(!warm.externalStop, 'warm phase externally stopped; no stable workload started')
    const before = await snapshot('before-stable'); await writeFile(join(dir, 'before.json'), JSON.stringify(before, null, 2) + '\n')
    report.stableStartedAt = Date.now(); const stable = await runPhase('stable', seconds); report.stableExit = stable.code; report.stableExternalStop = stable.externalStop; report.stableEndedAt = Date.now(); report.stableCommandElapsedMs = report.stableEndedAt - report.stableStartedAt
    const after = await snapshot('after-stable'); const accessDelta = deltaSnapshots(before, after); await writeFile(join(dir, 'after.json'), JSON.stringify(after, null, 2) + '\n'); await writeFile(join(dir, 'access-delta.json'), JSON.stringify(accessDelta, null, 2) + '\n')
    assert.ok(!accessDelta.counterResets && accessDelta.lostDigestsDelta === 0 && accessDelta.preparedStatementsLostDelta === 0 && accessDelta.missingAfterDigests.length === 0, 'access counters reset, uptime inconsistent or instrumentation lost; no valid access count claim')
    const summary = JSON.parse(await readFile(join(dir, 'stable-summary.json'), 'utf8')), metric = name => { const value = summary.metrics?.[name]; assert.ok(value, 'missing required metric: ' + name); return value }
    const attempted = metric('cap_read_attempted').count, completed = metric('cap_read_completed').count, completedHttpRequests = metric('http_reqs').count, successful = metric('cap_read_success').count, systemErrors = metric('cap_read_system_errors').count, contentErrors = metric('cap_read_content_errors').count, httpErrors = metric('cap_read_http_errors').count, dropped = metric('dropped_iterations').count
    const latency = metric('cap_read_all_ms'), successfulLatency = summary.metrics?.cap_read_success_ms || null
    assert.ok([attempted, completed, completedHttpRequests, successful, systemErrors, contentErrors, httpErrors, dropped, latency['p(95)'], latency['p(99)']].every(Number.isFinite), 'required metric value missing')
    assert.ok(attempted >= completedHttpRequests && completedHttpRequests >= completed && completed >= successful, 'inconsistent started/HTTP/completed/success counts')
    if (successful > 0) assert.ok(successfulLatency && [successfulLatency['p(95)'], successfulLatency['p(99)']].every(Number.isFinite), 'successful request latency missing')
    report.result = { attempted, completedHttpRequests, completed, incompleteOrUnclassified: attempted - completed, completedButUnclassified: completedHttpRequests - completed, classificationCoverage: completed / attempted, successful, systemErrors, contentErrors, httpErrors, dropped, targetOffers: rate * seconds, actualOfferedRatio: attempted / (rate * seconds), issuedPerNominalSecond: attempted / seconds, successfulPerNominalSecond: successful / seconds, systemErrorRate: systemErrors / attempted, systemErrorRatePerCompleted: completed > 0 ? systemErrors / completed : null, allLatency: latency, successfulLatency, countingScope: 'attempted enters the HTTP call; completedHttpRequests is native http_reqs including transport failures; completed reaches terminal classification; unfinished calls are unknown, nominal-second rates retain the full planned denominator even after early stop', k6Metrics: { vus: summary.metrics?.vus, vusMax: summary.metrics?.vus_max, dataReceived: summary.metrics?.data_received } }
    if (successful > 0) assert.ok(accessDelta.globalCommands.Com_select > 0 && accessDelta.globalCommands.Com_stmt_execute > 0 && accessDelta.statementEvents.some(row => row.event === 'statement/com/Execute' && row.count > 0), 'successful Feed stage lacks measured instance SQL executions; no access count claim')
    report.meetsBudget = report.stableExit === 0 && !report.stableExternalStop && completed === attempted && completedHttpRequests === attempted && attempted / (rate * seconds) >= .99 && successful / attempted >= .99 && dropped === 0 && systemErrors / attempted < .01 && contentErrors === 0 && httpErrors / attempted < .01 && latency['p(95)'] <= 200 && latency['p(99)'] <= 500 && successfulLatency && successfulLatency['p(95)'] <= 200 && successfulLatency['p(99)'] <= 500
  } catch (error) { report.failure = error.message; report.meetsBudget = false }
  finally {
    report.finished = new Date().toISOString(); if (beforeWarm) await writeFile(join(dir, 'warm-before.json'), JSON.stringify(beforeWarm, null, 2) + '\n'); await writeFile(join(dir, 'result.json'), JSON.stringify(report, null, 2) + '\n')
  }
  console.log(JSON.stringify({ task: report.task, mode, rate, seconds, meetsBudget: report.meetsBudget, result: report.result, failure: report.failure || null }))
  process.exitCode = report.meetsBudget ? 0 : 1
}
