// CAP-10 real synchronous like writes. Only the fixed, owned loopback cohort.
import assert from 'node:assert/strict'
import { randomBytes, randomInt, createHash } from 'node:crypto'
import { spawn } from 'node:child_process'
import { createWriteStream } from 'node:fs'
import { mkdir, mkdtemp, readFile, writeFile, unlink, rmdir, lstat, realpath } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { resolve, dirname, join, basename, relative, sep } from 'node:path'
import net from 'node:net'
import { stateFromBody, classifyWrite, unknownBounds } from './load/cap10-like-contract.mjs'
import { nativeScenarioWindow } from './load/cap10-native-window.mjs'
import { collectDatabase } from './load/cap10-db-observe.mjs'
import { poolSize, fixturePoolSize } from './load/cap10-cohort-contract.mjs'
import { observerPolicy } from './load/cap10-observer-policy.mjs'
import { auditSQL, auditContractMatches, auditErrors, baselineAuditReference, baselineAuditCount } from './load/cap10-full-audit.mjs'
import { PHASE_FACTS, phaseSQL, phaseErrors, makePhaseFacts, vectorIdentity, controlsIdentity, acceptedSample, recoveryEvidence } from './load/cap10-phase-facts.mjs'

const owned = { project: 'project-full-stack-cap10', mysql: '3481', redis: '6581', api: 'http://127.0.0.1:8281', database: 'project_full_stack_go' }
const action = process.argv[2]
const finishedFixtureArgs = process.argv.slice(3)
let finishedFixtureManifestSHA256
if (finishedFixtureArgs.length) {
  assert.equal(action, 'reconcile', 'finished fixture mode is available only for read-only reconcile')
  assert.ok(finishedFixtureArgs.length === 2 && finishedFixtureArgs[0] === '--finished-fixture-sha256', 'use reconcile --finished-fixture-sha256 <original manifest SHA256>')
  assert.match(finishedFixtureArgs[1], /^[a-f0-9]{64}$/, 'exact original finished fixture SHA256 required')
  finishedFixtureManifestSHA256 = finishedFixtureArgs[1]
}
const observationPolicy = observerPolicy(process.env)
const vectorMode = observationPolicy.mode === 'like-vector-v1'
let windowBaseline = null
assert.ok(['prepare', 'probe', 'run', 'inspect', 'reconcile', 'finish'].includes(action), 'use prepare/probe/run/inspect/reconcile/finish')
assert.equal(process.env.CAP_COHORT, 'cap10')
for (const key of ['MYSQL_USER', 'MYSQL_PASSWORD', 'CAP_SOURCE_SHA', 'CAP_READ_EXPECTED_REVISION']) assert.ok(process.env[key], `explicit ${key} required`)
assert.match(process.env.CAP_SOURCE_SHA, /^[a-f0-9]{40}$/)
assert.equal(process.env.CAP_READ_EXPECTED_REVISION, 'capacity-' + process.env.CAP_SOURCE_SHA)
assert.equal(process.env.MYSQL_HOST, '127.0.0.1'); assert.equal(process.env.MYSQL_PORT, owned.mysql)
assert.equal(process.env.MYSQL_DATABASE, owned.database); assert.equal(process.env.REDIS_HOST, '127.0.0.1')
assert.equal(process.env.REDIS_PORT, owned.redis); assert.equal(process.env.API_BASE, owned.api)
const json = value => JSON.stringify(value, null, 2) + '\n'
const save = (path, value) => writeFile(path, json(value), { flag: 'wx' })
const hash = data => createHash('sha256').update(data).digest('hex')
const pause = ms => new Promise(r => setTimeout(r, ms))
async function capacitySnapshot() {
  assert.match(process.env.CAP_API_PID || '', /^[1-9]\d*$/)
  const startedAt = Date.now()
  const response = await fetch('http://127.0.0.1:8381/debug/capacity', { redirect: 'error', signal: AbortSignal.timeout(5000) })
  assert.equal(response.status, 200)
  const reader = response.body.getReader(), chunks = []; let size = 0
  while (true) {
    const part = await reader.read(); if (part.done) break
    size += part.value.length
    if (size > 2 * 1024 * 1024) { await reader.cancel(); throw new Error('capacity snapshot exceeds 2MiB bound') }
    chunks.push(Buffer.from(part.value))
  }
  const data = JSON.parse(Buffer.concat(chunks).toString('utf8'))
  assert.equal(data.pid, Number(process.env.CAP_API_PID), 'capacity API PID differs')
  return { startedAt, finishedAt: Date.now(), bytes: size, data, scope: 'bounded loopback capacity snapshot around only the native stable phase; not a profile or business request' }
}
const sqlBin = process.env.MYSQL_BIN || 'mysql'
const executionPlatform = process.platform === 'win32' ? 'windows' : process.platform === 'linux' ? 'linux' : null
assert.ok(executionPlatform, 'CAP10 supports only the declared Windows/Linux executors')
assert.equal(process.env.CAP10_PLATFORM, executionPlatform, 'CAP10 platform must match the native executor')
const batchRoot = resolve('docs/evidence/2026-11-01-cap10')
const evidenceRoot = executionPlatform === 'linux' ? join(batchRoot, 'linux') : batchRoot
assert.equal(process.env.CAP10_EVIDENCE, evidenceRoot)
assert.equal(dirname(process.env.CAP10_ENVIRONMENT_METADATA || ''), evidenceRoot)
assert.match(basename(process.env.CAP10_ENVIRONMENT_METADATA), /^environment(?:-[a-f0-9]{12})?\.json$/)
const metadataStat = await lstat(process.env.CAP10_ENVIRONMENT_METADATA)
assert.ok(metadataStat.isFile() && !metadataStat.isSymbolicLink(), 'regular owned environment metadata required')
assert.equal(await realpath(process.env.CAP10_ENVIRONMENT_METADATA), join(await realpath(evidenceRoot), basename(process.env.CAP10_ENVIRONMENT_METADATA)), 'environment metadata resolves outside its original namespace')
const resourceProfile = process.env.CAP10_RESOURCE_PROFILE ?? 'baseline'
assert.ok(['baseline', 'mysql8-v1'].includes(resourceProfile), 'explicit bounded resource profile required')
const resourceMetadata = JSON.parse(await readFile(process.env.CAP10_ENVIRONMENT_METADATA))
assert.ok(resourceMetadata && typeof resourceMetadata === 'object' && !Array.isArray(resourceMetadata), 'environment metadata object required')
assert.equal(Object.hasOwn(resourceMetadata, 'resourceProfile') ? resourceMetadata.resourceProfile : 'baseline', resourceProfile, 'private and public resource profiles differ')
if (executionPlatform === 'linux') {
  const raw = await readFile(join(batchRoot, 'environment-transition.json'))
  assert.equal(process.env.CAP10_TRANSITION_SHA256, hash(raw), 'Linux transition bytes differ')
  const transition = JSON.parse(raw)
  assert.equal(transition.version, 1); assert.equal(transition.batch, 'CAP-10')
  assert.equal(transition.fromPlatform, 'windows'); assert.equal(transition.toPlatform, 'linux')
  assert.equal(transition.switchCount, 1); assert.equal(transition.authorized, true)
  assert.ok(transition.hostLabel && transition.userAuthorizationReference, 'explicit host authorization required')
}
const privateRoot = resolve(process.env.CAP10_RUNTIME || '')
assert.ok(process.env.CAP10_RUNTIME, 'explicit private CAP10_RUNTIME required')
assert.equal(dirname(privateRoot), resolve('..')); assert.match(basename(privateRoot), /^\.runtime-fullstack-cap10-[a-f0-9]{12}$/)
assert.equal(resolve(tmpdir()), join(privateRoot, 'tmp'), 'TEMP/TMP must be the owned private runtime/tmp')
function under(path, root) { const rel = relative(root, resolve(path)); return rel !== '' && rel !== '..' && !rel.startsWith('..' + sep) && !resolve(path).startsWith('\\\\') && !rel.includes(':') }
function evidence(path) { assert.ok(under(path, evidenceRoot), 'new output must belong to the fixed CAP10 evidence root'); return resolve(path) }

async function command(binary, args, { input, env = process.env, output, onStart, stopFile, timeoutMs } = {}) {
  return new Promise((resolveCommand, reject) => {
    const child = spawn(binary, args, { env, windowsHide: true })
    let stdout = '', stderr = '', timer = null, checking = false, externalStop = null, timedOut = false
    const deadlineTimer = timeoutMs ? setTimeout(() => { timedOut = true; child.kill('SIGTERM') }, timeoutMs) : null
    const file = output ? createWriteStream(output, { flags: 'wx' }) : null
    Promise.resolve(onStart?.(child.pid)).catch(error => { child.kill(); reject(error) })
    if (stopFile) timer = setInterval(async () => {
      if (checking || externalStop) return
      checking = true
      try {
        const raw = await readFile(stopFile, 'utf8')
        let reason; try { reason = JSON.parse(raw) } catch { reason = { invalidStopFile: true } }
        externalStop = { at: Date.now(), reason }; child.kill('SIGTERM')
      } catch (error) { if (error.code !== 'ENOENT') { externalStop = { at: Date.now(), reason: { stopReadError: true } }; child.kill('SIGTERM') } }
      finally { checking = false }
    }, 500)
    child.stdout.on('data', chunk => { if (file) { if (!file.write(chunk)) child.stdout.pause() } else stdout += chunk })
    child.stderr.on('data', chunk => { if (file) { if (!file.write(chunk)) child.stderr.pause() } else stderr += chunk })
    file?.on('drain', () => { child.stdout.resume(); child.stderr.resume() })
    file?.on('error', error => { child.kill(); reject(error) })
    child.on('error', error => { if (timer) clearInterval(timer); if (deadlineTimer) clearTimeout(deadlineTimer); reject(error) })
    child.on('close', code => {
      if (timer) clearInterval(timer)
      if (deadlineTimer) clearTimeout(deadlineTimer)
      if (file) file.end(() => resolveCommand({ code, externalStop, timedOut }))
      else resolveCommand({ code, stdout, stderr, externalStop, timedOut })
    })
    child.stdin.end(input)
  })
}

async function sql(statement, observer = false, timeoutMs = 30000) {
  const user = observer ? process.env.CAP_READ_OBSERVER_USER : process.env.MYSQL_USER
  const password = observer ? process.env.CAP_READ_OBSERVER_PASSWORD : process.env.MYSQL_PASSWORD
  assert.ok(user && password, 'explicit SQL credentials required')
  const result = await command(sqlBin, ['--protocol=TCP', '--host=127.0.0.1', '--port=' + owned.mysql,
    '--user=' + user, '--default-character-set=utf8mb4', '--batch', '--skip-column-names', owned.database],
  { input: `SET NAMES utf8mb4; SET time_zone='+08:00'; ${statement}`, env: { ...process.env, MYSQL_PWD: password }, timeoutMs })
  if (result.code !== 0) {
    const failure = new Error(result.timedOut ? 'owned SQL timeout; commit outcome requires reconciliation' : 'owned fixture/observer SQL failed; credentials and statement body suppressed')
    if (observer) {
      const line = result.stderr.split(/\r?\n/).map(s => /^ERROR (\d+) \(([A-Z0-9]+)\)(?: at line (\d+))?: (.*)$/.exec(s)).find(Boolean)
      if (line) failure.observerSQLFailure = { code: Number(line[1]), state: line[2], line: line[3] ? Number(line[3]) : null, message: line[4].slice(0, 1000) }
    }
    throw failure
  }
  return result.stdout.trim()
}

// One bounded pipeline; values are ordinary synthetic IDs, never credentials.
async function redisCommands(commands) {
  return new Promise((resolveRedis, reject) => {
    const socket = net.createConnection({ host: '127.0.0.1', port: Number(owned.redis) })
    let buffer = Buffer.alloc(0), offset = 0; const replies = []
    const parse = at => {
      const end = buffer.indexOf('\r\n', at); if (end < 0) return null
      const kind = String.fromCharCode(buffer[at]), head = buffer.toString('utf8', at + 1, end), start = end + 2
      if (kind === '-') throw new Error('owned Redis command failed')
      if (kind === '+' || kind === ':') return { value: kind === ':' ? Number(head) : head, end: start }
      if (kind === '$') {
        const length = Number(head); if (length === -1) return { value: null, end: start }
        assert.ok(Number.isSafeInteger(length) && length >= 0 && length <= 16 << 20)
        if (buffer.length < start + length + 2) return null
        return { value: buffer.toString('utf8', start, start + length), end: start + length + 2 }
      }
      if (kind === '*') {
        const count = Number(head); assert.ok(Number.isSafeInteger(count) && count >= 0 && count <= 4096)
        let next = start; const values = []
        for (let i = 0; i < count; i++) { const item = parse(next); if (!item) return null; values.push(item.value); next = item.end }
        return { value: values, end: next }
      }
      throw new Error('unsupported Redis response')
    }
    socket.setTimeout(5000, () => socket.destroy(new Error('owned Redis observation timeout')))
    socket.on('connect', () => socket.write(commands.map(parts => `*${parts.length}\r\n` + parts.map(p => `$${Buffer.byteLength(String(p))}\r\n${p}\r\n`).join('')).join('')))
    socket.on('error', reject)
    socket.on('data', chunk => {
      try {
        buffer = Buffer.concat([buffer, chunk]); assert.ok(buffer.length <= 16 << 20, 'bounded Redis evidence exceeded')
        while (replies.length < commands.length) { const item = parse(offset); if (!item) break; replies.push(item.value); offset = item.end }
        if (replies.length === commands.length) { socket.end(); resolveRedis(replies) }
      } catch (error) { socket.destroy(); reject(error) }
    })
    socket.on('end', () => { if (replies.length !== commands.length) reject(new Error('incomplete Redis observation')) })
  })
}

async function api(method, path, body, token) {
  const response = await fetch(owned.api + path, { method, headers: { 'Content-Type': 'application/json', ...(token ? { authorization: token } : {}) },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }), redirect: 'error', signal: AbortSignal.timeout(10000) })
  return { status: response.status, body: await response.json() }
}

async function guard() {
  for (const [service, target, port] of [['mysql', '3306/tcp', owned.mysql], ['redis', '6379/tcp', owned.redis]]) {
    const result = await command('docker', ['inspect', `${owned.project}-${service}-1`, '--format', '{{.Id}}|{{json .Config.Labels}}|{{json .HostConfig.PortBindings}}|{{.State.Running}}'])
    assert.equal(result.code, 0, 'owned container inspect failed')
    const [id, labelsRaw, portsRaw, running] = result.stdout.trim().split('|'), labels = JSON.parse(labelsRaw), ports = JSON.parse(portsRaw)
    assert.match(id, /^[a-f0-9]{64}$/); assert.equal(labels['com.docker.compose.project'], owned.project)
    assert.equal(labels['com.docker.compose.service'], service); assert.equal(running, 'true')
    assert.ok(ports[target]?.some(p => p.HostIp === '127.0.0.1' && p.HostPort === port), 'owned port mismatch')
  }
  const health = await api('GET', '/health/ready')
  assert.equal(health.status, 200); assert.equal(health.body.status, 'UP')
  assert.equal(health.body.service, 'project-full-stack-go'); assert.equal(health.body.version, process.env.CAP_READ_EXPECTED_REVISION)
}

const fixturePath = process.env.CAP_LIKE_FIXTURE ? resolve(process.env.CAP_LIKE_FIXTURE) : null
const fixtureManifestHashes = new WeakMap()
async function loadFixture({ finishedManifestSHA256 } = {}) {
  assert.ok(fixturePath, 'explicit CAP_LIKE_FIXTURE required')
  evidence(fixturePath)
  const bytes = await readFile(fixturePath), manifest = JSON.parse(bytes)
  const count = fixturePoolSize(manifest)
  assert.equal(manifest.project, owned.project); assert.equal(manifest.baseUrl, owned.api)
  assert.equal(manifest.executionPlatform, executionPlatform, 'fixture belongs to another executor')
  assert.equal(manifest.transitionSHA256, process.env.CAP10_TRANSITION_SHA256 || null, 'fixture transition differs')
  assert.ok(manifest.status === 'READY' || (action === 'finish' && manifest.status === 'PREPARING'), 'new fixture must be ready')
  assert.match(manifest.sourceSha, /^[a-f0-9]{40}$/); assert.equal(manifest.expectedRevision, 'capacity-' + manifest.sourceSha)
  assert.ok(under(manifest.credentialFile, join(privateRoot, 'tmp')) && basename(manifest.credentialFile) === 'input.json', 'private fixture input must stay in owned runtime/tmp')
  if (action !== 'finish') {
    assert.equal(manifest.userIds.length, count); assert.equal(manifest.multiNoteIds.length, count)
    assert.equal(new Set(manifest.userIds).size, count); assert.equal(new Set(manifest.multiNoteIds).size, count)
    for (const id of [...manifest.userIds, ...manifest.multiNoteIds]) assert.match(id, /^[1-9]\d*$/)
  }
  const manifestSHA256 = hash(bytes)
  if (finishedManifestSHA256 !== undefined) {
    assert.equal(action, 'reconcile', 'closed identity cannot enter a write action')
    assert.match(finishedManifestSHA256, /^[a-f0-9]{64}$/)
    assert.equal(manifest.status, 'READY', 'partial fixture cannot use closed identity audit')
    assert.equal(manifestSHA256, finishedManifestSHA256, 'finished fixture manifest bytes differ')
    const originalStat = await lstat(fixturePath)
    assert.ok(originalStat.isFile() && !originalStat.isSymbolicLink(), 'regular original finished manifest required')
    const originalReal = await realpath(fixturePath), directoryReal = await realpath(dirname(fixturePath))
    assert.equal(originalReal, join(directoryReal, basename(fixturePath)))
    assert.ok(under(originalReal, await realpath(evidenceRoot)), 'finished fixture resolves outside owned evidence')
    const finishPath = join(dirname(fixturePath), 'finish.json'), finishStat = await lstat(finishPath)
    assert.ok(finishStat.isFile() && !finishStat.isSymbolicLink(), 'regular same-directory finish receipt required')
    assert.equal(await realpath(finishPath), join(directoryReal, 'finish.json'))
    const finishBytes = await readFile(finishPath), finish = JSON.parse(finishBytes)
    assert.ok(finish && typeof finish === 'object' && !Array.isArray(finish), 'finish receipt object required')
    assert.equal(finish.fixtureManifestSHA256, manifestSHA256, 'finish receipt belongs to another fixture')
    assert.ok(Number.isSafeInteger(finish.loggedOut) && finish.loggedOut === count, 'finish receipt must cover the exact original pool')
    assert.equal(finish.credentialFileRemoved, true); assert.equal(finish.sqlFixtureRetained, true)
    try {
      await lstat(manifest.credentialFile)
      assert.fail('finished fixture credential path still exists')
    } catch (error) { if (error.code !== 'ENOENT') throw error }
    fixtureManifestHashes.set(manifest, manifestSHA256)
    return { manifest, runtime: null, manifestSHA256, finishedFixtureAudit: {
      contract: 'finished-fixture-read-only-v1', fixtureManifestSHA256: manifestSHA256,
      finish: { path: relative(evidenceRoot, finishPath).split(sep).join('/'), sha256: hash(finishBytes) },
      loggedOut: count, credentialFileAbsent: true, sqlFixtureRetained: true,
      tokenAuthenticationAttempted: false, capacityEligible: false,
      scope: 'Original manifest identities and finish receipt only; complete SQL/Redis reconciliation, no session restoration or current-cohort admission.' } }
  }
  let runtime
  try { runtime = JSON.parse(await readFile(manifest.credentialFile, 'utf8')) }
  catch (error) {
    if (action !== 'finish' || manifest.status !== 'PREPARING' || error.code !== 'ENOENT') throw new Error('private fixture input unavailable or malformed; preserve its registered recovery path')
    runtime = { fixture: manifest.fixture, users: [] }
  }
  assert.equal(runtime.fixture, manifest.fixture)
  assert.ok(Array.isArray(runtime.users) && (runtime.users.length === count || (action === 'finish' && manifest.status === 'PREPARING' && runtime.users.length < count)))
  assert.deepEqual(runtime.users.map(u => u.userId), (manifest.userIds || []).slice(0, runtime.users.length))
  fixtureManifestHashes.set(manifest, manifestSHA256)
  return { manifest, runtime, manifestSHA256 }
}
const modeNotes = (manifest, mode) => { assert.equal(mode, 'multi'); return manifest.multiNoteIds }
function rows(raw) { return raw ? raw.split(/\r?\n/).filter(Boolean).map(line => JSON.parse(line)) : [] }

async function snapshot(manifest, mode, label) {
  const ids = modeNotes(manifest, mode).map(id => `'${id}'`).join(',')
  const start = Date.now()
  // All fact, counter and outbox checks share one read-only RR snapshot. The
  // pre/post phase windows remain sequential, never a per-request commit clock.
  const legacyStatement = `SET TRANSACTION ISOLATION LEVEL REPEATABLE READ; START TRANSACTION WITH CONSISTENT SNAPSHOT, READ ONLY;
SELECT JSON_OBJECT('_kind','fact','noteId',CAST(l.blog_id AS CHAR),'userId',CAST(l.user_id AS CHAR),'active',l.active,'version',l.version) FROM tb_blog_like l WHERE l.blog_id IN (${ids}) ORDER BY l.blog_id,l.user_id;
SELECT JSON_OBJECT('_kind','note','noteId',CAST(b.id AS CHAR),'liked',b.liked,'activeRelations',(SELECT COUNT(*) FROM tb_blog_like l WHERE l.blog_id=b.id AND l.active=1)) FROM tb_blog b WHERE b.id IN (${ids}) ORDER BY b.id;
SELECT JSON_OBJECT('_kind','event','noteId',g.note_id,'userId',g.user_id,'count',g.event_count,'distinctVersions',g.distinct_versions,'minVersion',g.min_version,'maxVersion',g.max_version,'invalid',g.invalid_count) FROM (SELECT o.aggregate_id AS note_id,JSON_UNQUOTE(JSON_EXTRACT(o.payload,'$.userId')) AS user_id,COUNT(*) AS event_count,COUNT(DISTINCT o.aggregate_version) AS distinct_versions,MIN(o.aggregate_version) AS min_version,MAX(o.aggregate_version) AS max_version,SUM(COALESCE(BINARY o.aggregate_type<>BINARY 'BLOG' OR BINARY o.event_id<>BINARY CONCAT('LIKE:',o.aggregate_id,':',JSON_UNQUOTE(JSON_EXTRACT(o.payload,'$.userId')),':',o.aggregate_version) OR JSON_TYPE(JSON_EXTRACT(o.payload,'$.blogId'))<>'STRING' OR JSON_TYPE(JSON_EXTRACT(o.payload,'$.userId'))<>'STRING' OR JSON_TYPE(JSON_EXTRACT(o.payload,'$.version'))<>'INTEGER' OR JSON_TYPE(JSON_EXTRACT(o.payload,'$.liked'))<>'BOOLEAN' OR BINARY JSON_UNQUOTE(JSON_EXTRACT(o.payload,'$.blogId'))<>BINARY o.aggregate_id OR BINARY JSON_UNQUOTE(JSON_EXTRACT(o.payload,'$.version'))<>BINARY CAST(o.aggregate_version AS CHAR) OR JSON_UNQUOTE(JSON_EXTRACT(o.payload,'$.liked'))<>IF(MOD(o.aggregate_version,2)=1,'true','false'),1)) AS invalid_count FROM tb_community_outbox o WHERE o.event_type='LIKE_CHANGED' AND o.aggregate_id IN (${ids}) GROUP BY o.aggregate_id,user_id) g ORDER BY g.note_id,g.user_id;
SELECT JSON_OBJECT('_kind','delivery','status',delivery_status,'count',COUNT(*)) FROM tb_community_outbox WHERE event_type='LIKE_CHANGED' AND aggregate_id IN (${ids}) GROUP BY delivery_status;
SELECT JSON_OBJECT('_kind','receipt','projection',r.projection,'state',r.state,'count',COUNT(*)) FROM tb_community_projection_event r JOIN tb_community_outbox o ON o.event_id=r.event_id WHERE o.event_type='LIKE_CHANGED' AND o.aggregate_id IN (${ids}) GROUP BY r.projection,r.state;
SELECT JSON_OBJECT('_kind','parked','projection',projection,'state',state,'count',COUNT(*)) FROM tb_community_consumer_failure GROUP BY projection,state; COMMIT;`
  const fullAudit = observationPolicy.mode === 'like-vector-v1'
  const raw = rows(await sql(fullAudit ? auditSQL(modeNotes(manifest, mode)) : legacyStatement, true, observationPolicy.fullAuditTimeoutMs))
  const take = kind => raw.filter(row => row._kind === kind).map(({ _kind, ...row }) => row)
  assert.ok(raw.every(row => ['fact', 'note', 'event', 'delivery', 'receipt', 'parked', 'audit', 'control-projection', 'control-search', 'watermark'].includes(row._kind)))
  const facts = take('fact'), notes = take('note'), events = take('event'), delivery = take('delivery'), receipts = take('receipt'), parked = take('parked')
  const audit = take('audit')
  assert.ok(fullAudit ? audit.length === 1 && auditContractMatches(audit[0]) : audit.length === 0, 'full audit contract marker differs')
  let auditMetadata = {}
  if (fullAudit) {
    const controls = take('control-projection'), search = take('control-search'), watermark = take('watermark')
    assert.ok(controls.length === 2 && new Set(controls.map(row => row.projection)).size === 2
      && controls.every(row => ['follow-v1', 'feed-v1'].includes(row.projection)) && search.length === 1 && watermark.length === 1,
    'complete full-audit control and watermark rows required')
    const fixtureManifestSHA256 = fixtureManifestHashes.get(manifest)
    assert.match(fixtureManifestSHA256 || '', /^[a-f0-9]{64}$/, 'original fixture bytes must be bound before audit')
    auditMetadata = { auditContract: audit[0], fixtureManifestSHA256,
      controlRows: { projections: ['follow-v1', 'feed-v1'].map(name => controls.find(row => row.projection === name)), search: search[0] },
      sourceWatermark: { ...watermark[0], diagnosticOnly: true } }
  } else assert.equal(take('control-projection').length + take('control-search').length + take('watermark').length, 0)
  return { label, startedAt: start, endedAt: Date.now(), observerPolicy: observationPolicy, facts, notes, events, delivery, receipts, parked,
    ...auditMetadata,
    transitions: facts.reduce((sum, row) => sum + row.version, 0), eventCount: events.reduce((sum, row) => sum + row.count, 0),
    scope: 'one read-only RR snapshot of the selected synthetic note pool; pre/post captures sequential; global consumer-failure rows include earlier stages in the owned DB' }
}

function factErrors(manifest, mode, state) {
  if (vectorMode && state.captureKind === PHASE_FACTS) return phaseErrors(manifest, state, fixtureManifestHashes.get(manifest), windowBaseline.proof.facts.controlRows)
  const errors = [], users = new Set(manifest.userIds), notes = new Set(modeNotes(manifest, mode))
  if (observationPolicy.mode === 'like-vector-v1') errors.push(...auditErrors(manifest, state, fixtureManifestHashes.get(manifest)))
  const actual = new Map(state.facts.map(r => [r.noteId + ':' + r.userId, r]))
  const events = new Map(state.events.map(r => [r.noteId + ':' + r.userId, r]))
  for (const row of state.facts) {
    if (!users.has(row.userId) || !notes.has(row.noteId) || (mode === 'multi' && manifest.multiNoteIds[manifest.userIds.indexOf(row.userId)] !== row.noteId)) errors.push('unexpected relation in owned note pool')
    const event = events.get(row.noteId + ':' + row.userId)
    if (row.version === 0 && manifest.relationshipSeed?.version === 0) {
      if (row.active !== 0 || event) errors.push('fixture version-zero relation has an effect or event')
      continue
    }
    if (!Number.isSafeInteger(row.version) || row.version < 1 || row.active !== row.version % 2) errors.push('relation state/version parity differs')
    if (!event || event.count !== row.version || event.distinctVersions !== row.version || event.minVersion !== 1 || event.maxVersion !== row.version || event.invalid !== 0) errors.push('relation transitions and unique LIKE events differ')
  }
  for (const event of state.events) if (!actual.has(event.noteId + ':' + event.userId)) errors.push('LIKE event lacks relation')
  if (state.notes.length !== notes.size || state.notes.some(n => n.liked !== n.activeRelations || n.liked < 0)) errors.push('public count differs from all active facts')
  if (state.transitions !== state.eventCount) errors.push('sum relation versions differs from LIKE event count')
  return [...new Set(errors)]
}

function changedParticipants(before, after) {
  const prior = new Map(before.facts.map(r => [r.noteId + ':' + r.userId, r.version]))
  const relations = after.facts.map(row => ({ noteId: row.noteId, userId: row.userId,
    beforeVersion: prior.get(row.noteId + ':' + row.userId) || 0, afterVersion: row.version }))
    .map(row => ({ ...row, mutations: row.afterVersion - row.beforeVersion })).filter(row => row.mutations > 0)
  const distribution = {}
  for (const row of relations) distribution[row.mutations] = (distribution[row.mutations] || 0) + 1
  return { relationships: relations.length, users: new Set(relations.map(r => r.userId)).size,
    notes: new Set(relations.map(r => r.noteId)).size, mutations: relations.reduce((s, r) => s + r.mutations, 0),
    mutationsPerRelationship: distribution, relations,
    scope: 'verified positive authoritative relation-version changes between these RR snapshots; configured pool is separate. Failed/unknown phases do not establish complete native participation.' }
}

function initialDistribution(manifest, state) {
  const count = fixturePoolSize(manifest)
  const byPair = new Map(state.facts.map(r => [r.noteId + ':' + r.userId, r]))
  const versions = {}, active = { active: 0, inactive: 0, missing: 0 }
  for (let i = 0; i < count; i++) {
    const row = byPair.get(manifest.multiNoteIds[i] + ':' + manifest.userIds[i])
    if (!row) { active.missing++; continue }
    active[row.active === 1 ? 'active' : 'inactive']++
    versions[row.version] = (versions[row.version] || 0) + 1
  }
  return { configuredRelationships: count, existing: count - active.missing, ...active, versionHistogram: versions,
    existingFraction: (count - active.missing) / count,
    scope: 'authoritative RR facts for the exact manifest-bound pairs; active/version distributions are recorded explicitly, never inferred from fixture hash' }
}

async function derived(manifest, mode, state) {
  const notes = modeNotes(manifest, mode)
  const replies = await redisCommands(notes.flatMap(id => [['SMEMBERS', 'community:likes:' + id], ['HGETALL', 'community:likes:versions:' + id]]))
  const errors = [], values = []
  for (let i = 0; i < notes.length; i++) {
    const id = notes[i], members = replies[i * 2], entries = replies[i * 2 + 1]
    assert.ok(Array.isArray(members) && Array.isArray(entries) && entries.length % 2 === 0)
    const versions = Object.fromEntries(Array.from({ length: entries.length / 2 }, (_, j) => [entries[j * 2], entries[j * 2 + 1]]))
    const facts = state.facts.filter(r => r.noteId === id), active = facts.filter(r => r.active === 1).map(r => r.userId).sort()
    const knownUsers = new Set(facts.filter(r => r.version > 0).map(r => r.userId))
    if (Object.keys(versions).some(user => !knownUsers.has(user) && !['__complete', '__count'].includes(user))) errors.push('Redis version hash contains a user absent from positive-version facts')
    if (JSON.stringify([...members].sort()) !== JSON.stringify(active) || facts.some(r => r.version === 0 ? versions[r.userId] !== undefined : versions[r.userId] !== String(r.version))) errors.push('Redis relation members/version differs from authoritative facts')
    values.push({ noteId: id, members, versions })
  }
  return { available: true, errors: [...new Set(errors)], notes: values,
    scope: 'selected note set membership and per-user version hash; __complete is optional and not treated as a count proof' }
}

async function reconcile(manifest, mode, waitSeconds) {
  const started = Date.now(); const timeline = []; let state, fact, projection
  do {
    state = await snapshot(manifest, mode, 'reconcile')
    fact = factErrors(manifest, mode, state)
    const expected = state.eventCount
    const groups = ['follow-v1', 'feed-v1', 'search-v1']
    const receiptErrors = groups.filter(group => state.receipts.filter(x => x.projection === group && x.state === 'IGNORED').reduce((s, x) => s + x.count, 0) !== expected || state.receipts.some(x => x.projection === group && x.state === 'APPLIED' && x.count !== 0))
    const pending = state.delivery.filter(x => x.status !== 'DONE').reduce((s, x) => s + x.count, 0)
    const parked = state.parked.filter(x => x.state === 'PARKED').reduce((s, x) => s + x.count, 0)
    timeline.push({ at: new Date().toISOString(), transitions: state.transitions, eventCount: expected, pending, parked, receiptErrors })
    if (fact.length || (!pending && !parked && !receiptErrors.length)) break
    await pause(1000)
  } while (Date.now() - started < waitSeconds * 1000)
  projection = await derived(manifest, mode, state)
  const last = timeline.at(-1), errors = [...fact, ...projection.errors]
  if (last.pending || last.parked || last.receiptErrors.length) errors.push('outbox or independent consumer receipts did not converge')
  return { startedAt: started, finishedAt: Date.now(), elapsedMs: Date.now() - started, valid: errors.length === 0,
    factsValid: fact.length === 0 && projection.errors.length === 0,
    downstreamConverged: !last.pending && !last.parked && last.receiptErrors.length === 0,
    errors, facts: state, derived: projection, timeline,
    limitations: 'Final convergence is not steady-state proof; the common stage observer must separately record last-minute backlog/lag and the 60s recovery deadline.' }
}

async function boundWindowBaseline(manifest) {
  const downstream = evidence(process.env.CAP10_DOWNSTREAM_EVIDENCE)
  const ready = JSON.parse(await readFile(join(downstream, 'ready.json'), 'utf8'))
  assert.equal(ready.ready, true)
  assert.deepEqual(ready.observerPolicy, observationPolicy)
  assert.deepEqual(ready.observerSamplingLimits, { loadBudgetMs: 4000, readTimeoutMs: 3000, cadenceSeconds: 5, nativeRecoverySeconds: 60 })
  const ref = baselineAuditReference(ready), path = evidence(join(evidenceRoot, ref.path)), detail = await lstat(path)
  assert.ok(detail.isFile() && !detail.isSymbolicLink() && detail.size <= 8 * 1024 * 1024)
  assert.equal(await realpath(path), path)
  const bytes = await readFile(path), proof = JSON.parse(bytes)
  const events = baselineAuditCount(ready, proof, hash(bytes), manifest, fixtureManifestHashes.get(manifest))
  assert.equal(vectorIdentity(manifest, proof.facts.facts).sha256, ready.baselineVectorSHA256)
  assert.equal(controlsIdentity(proof.facts.controlRows).sha256, ready.baselineControlsSHA256)
  return { ref, events, proof, ready, downstream }
}

async function phaseCapture(manifest, mode, label, timeoutMs = 30000) {
  if (!vectorMode) return snapshot(manifest, mode, label)
  assert.ok(windowBaseline, 'the original complete per-window baseline must be bound first')
  const startedAt = Date.now(), raw = rows(await sql(phaseSQL(manifest), true, timeoutMs))
  return makePhaseFacts(manifest, raw, { label, startedAt, endedAt: Date.now(), fixtureSHA: fixtureManifestHashes.get(manifest), expectedControls: windowBaseline.proof.facts.controlRows })
}

async function vectorWarmBefore(manifest, mode, dir) {
  windowBaseline = await boundWindowBaseline(manifest)
  const guard = await phaseCapture(manifest, mode, 'before-warm-facts')
  assert.equal(guard.factVectorSHA256, windowBaseline.ready.baselineVectorSHA256)
  assert.equal(guard.controlsSHA256, windowBaseline.ready.baselineControlsSHA256)
  assert.deepEqual(guard.notes, windowBaseline.proof.facts.notes)
  const path = join(dir, 'before-warm-facts.json'); await save(path, guard)
  return { ...structuredClone(windowBaseline.proof.facts),
    captureKind: 'bound-complete-baseline-with-fresh-fact-guard', baselineAudit: windowBaseline.ref,
    baselineAuditEventCount: windowBaseline.events, freshGuard: { path: path.slice(evidenceRoot.length + 1).split(sep).join('/'), sha256: hash(await readFile(path)),
      startedAt: guard.startedAt, endedAt: guard.endedAt, factVectorSHA256: guard.factVectorSHA256, controlsSHA256: guard.controlsSHA256 },
    scope: 'Original complete audit fields/timestamps retained; only the attached fact/note/control guard is fresh. No new source-history scan was performed.' }
}

async function phaseSetupReconcile(manifest, mode, capture, committedAt, deadline) {
  let accepted = null
  while (Date.now() <= deadline) {
    accepted = acceptedSample(await readFile(join(windowBaseline.downstream, 'timeline.ndjson')), capture,
      { baselineEvents: windowBaseline.events, notBeforeMs: committedAt })
    if (accepted) break
    await pause(100)
  }
  assert.ok(accepted && Date.now() <= deadline, 'setup requires one exact accepted fact/control vector plus fully clean downstream within60s')
  const projection = await derived(manifest, mode, capture), errors = factErrors(manifest, mode, capture).concat(projection.errors)
  return { captureKind: PHASE_FACTS, fullAuditPerformed: false, valid: errors.length === 0, factsValid: errors.length === 0,
    downstreamConverged: true, errors, facts: capture, derived: projection, acceptedSample: accepted,
    baselineAudit: windowBaseline.ref, baselineAuditEventCount: windowBaseline.events,
    scope: 'Fresh phase facts plus a complete accepted incremental sample and actual Redis derived read; this is not a complete history audit.' }
}

async function waitObserverRecovery(nativeEndMs) {
  const path = join(windowBaseline.downstream, 'summary.json'), deadline = Date.now() + 65000
  while (Date.now() <= deadline) {
    try {
      const bytes = await readFile(path), summary = JSON.parse(bytes), timeline = await readFile(join(windowBaseline.downstream, 'timeline.ndjson'))
      const value = recoveryEvidence(summary, timeline, nativeEndMs, windowBaseline.ref, windowBaseline.events)
      return { ...value, summarySHA256: hash(bytes), observerCompletionWaitLimitMs: 65000,
        observerCompletionWaitScope: 'Bounded wait for existing post-controller collection to close; native recovery remains exactly60s.' }
    } catch (error) {
      if (error.code !== 'ENOENT') throw error
    }
    await pause(100)
  }
  throw new Error('original observer terminal collection unavailable; a later full audit cannot substitute for recovery evidence')
}

async function stopPresent() {
  if (!process.env.CAP_STOP_FILE) return false
  try { await readFile(process.env.CAP_STOP_FILE); return true } catch (error) { if (error.code !== 'ENOENT') throw error; return false }
}

async function normalizeInitialState(manifest, mode, dir) {
  const count = fixturePoolSize(manifest)
  const startedAt = Date.now(), deadline = startedAt + 60000
  const before = await phaseCapture(manifest, mode, 'before-setup', Math.max(1, Math.min(30000, deadline - Date.now())))
  assert.deepEqual(factErrors(manifest, mode, before), [], 'setup starts only from valid facts')
  assert.equal(initialDistribution(manifest, before).existing, count, 'setup requires every manifest-bound exclusive pair')
  const ids = manifest.multiNoteIds.map(id => `'${id}'`).join(',')
  const pair = `l.user_id=CASE l.blog_id ${manifest.multiNoteIds.map((note, i) => `WHEN ${note} THEN ${manifest.userIds[i]}`).join(' ')} END`
  const raw = rows(await sql(`SET TRANSACTION ISOLATION LEVEL READ COMMITTED; START TRANSACTION;
SELECT JSON_OBJECT('_kind','locked-note','noteId',CAST(id AS CHAR)) FROM tb_blog WHERE id IN (${ids}) ORDER BY id FOR UPDATE;
SELECT JSON_OBJECT('_kind','locked-pair','noteId',CAST(l.blog_id AS CHAR),'userId',CAST(l.user_id AS CHAR),'active',l.active,'version',l.version) FROM tb_blog_like l WHERE l.blog_id IN (${ids}) AND ${pair} ORDER BY l.blog_id,l.user_id FOR UPDATE;
INSERT INTO tb_community_outbox(event_id,aggregate_type,aggregate_id,aggregate_version,event_type,payload)
SELECT CONCAT('LIKE:',l.blog_id,':',l.user_id,':',l.version+1),'BLOG',CAST(l.blog_id AS CHAR),l.version+1,'LIKE_CHANGED',JSON_OBJECT('blogId',CAST(l.blog_id AS CHAR),'userId',CAST(l.user_id AS CHAR),'liked',JSON_EXTRACT('false','$'),'version',l.version+1) FROM tb_blog_like l WHERE l.blog_id IN (${ids}) AND ${pair} AND l.active=1;
UPDATE tb_blog_like l SET active=0,version=version+1 WHERE l.blog_id IN (${ids}) AND ${pair} AND l.active=1;
UPDATE tb_blog b SET liked=(SELECT COUNT(*) FROM tb_blog_like l WHERE l.blog_id=b.id AND l.active=1) WHERE b.id IN (${ids});
COMMIT;`, false, Math.max(1, deadline - Date.now())))
  const committedAt = Date.now()
  await save(join(dir, 'setup-transaction.json'), { startedAt, committedAt, records: raw, countedAsHTTPMutation: false })
  const after = await phaseCapture(manifest, mode, 'after-setup-commit', Math.max(1, Math.min(30000, deadline - Date.now())))
  const expected = before.facts.filter(row => row.active === 1).length
  assert.equal(raw.filter(row => row._kind === 'locked-note').length, count)
  assert.equal(raw.filter(row => row._kind === 'locked-pair').length, count)
  assert.deepEqual(factErrors(manifest, mode, after), [], 'setup facts/events invalid')
  assert.equal(after.transitions - before.transitions, expected)
  if (!vectorMode) assert.equal(after.eventCount - before.eventCount, expected)
  const luaSourcePath = 'backend/internal/communitycache/bitmap.go'
  const source = await readFile(luaSourcePath, 'utf8')
  const lua = /var applyLike = redis.NewScript\(`([\s\S]*?)`\)/.exec(source)?.[1]
  assert.ok(lua, 'exact production Mark Lua unavailable; no substitute implementation')
  const calls = after.facts.filter(row => row.version > 0).map(row => ['EVAL', lua, '2', 'community:likes:' + row.noteId,
    'community:likes:versions:' + row.noteId, row.userId, String(row.version), '0'])
  calls.push(['DEL', ...manifest.multiNoteIds.map(id => 'community:counts:' + id)])
  await redisCommands(calls)
  // Same fixed local TTL as the application; no Feed/read workload is run here.
  await pause(2100)
  const audit = vectorMode ? await phaseSetupReconcile(manifest, mode, after, committedAt, deadline)
    : await reconcile(manifest, mode, Math.max(0, Math.floor((deadline - Date.now()) / 1000)))
  await save(join(dir, 'setup-reconcile.json'), audit)
  assert.ok(audit.valid, 'setup derived facts or receipts did not converge')
  const distribution = initialDistribution(manifest, audit.facts)
  assert.equal(distribution.existing, count); assert.equal(distribution.active, 0)
  assert.ok(process.env.CAP10_DOWNSTREAM_EVIDENCE, 'setup requires actual downstream baseline evidence')
  const downstream = evidence(process.env.CAP10_DOWNSTREAM_EVIDENCE)
  const ready = JSON.parse(await readFile(join(downstream, 'ready.json'), 'utf8'))
  let baselineEvents, baselineAudit
  if (observationPolicy.mode === 'like-vector-v1') {
    baselineAudit = baselineAuditReference(ready)
    const path = evidence(join(evidenceRoot, baselineAudit.path)), detail = await lstat(path)
    assert.ok(detail.isFile() && !detail.isSymbolicLink() && detail.size <= 8 * 1024 * 1024, 'bounded original baseline audit required')
    assert.equal(await realpath(path), path, 'baseline audit path alias rejected')
    const bytes = await readFile(path)
    baselineEvents = baselineAuditCount(ready, JSON.parse(bytes), hash(bytes), manifest, fixtureManifestHashes.get(manifest))
  } else baselineEvents = ready.baseline.sql.counts.events
  const expectedStageEvents = (vectorMode ? audit.facts.transitions : audit.facts.eventCount) - baselineEvents
  let clean = vectorMode ? { at: audit.acceptedSample.at } : null
  while (!clean && Date.now() <= deadline) {
    const lines = (await readFile(join(downstream, 'timeline.ndjson'), 'utf8')).trim().split(/\r?\n/)
    let last; try { last = JSON.parse(lines.at(-1)) } catch { await pause(100); continue }
    if (last.sql && last.kafka && !last.errors.length && !last.semanticViolations?.length &&
        Date.parse(last.sql.startedAt) >= committedAt && last.sql.counts.events === expectedStageEvents &&
        last.sql.counts.pending === 0 && last.sql.counts.processing === 0 && last.sql.counts.needsReview === 0 && last.sql.parked === 0 &&
        last.sql.receipts.length === 3 && last.sql.receipts.every(r => r.missing === 0 && r.applied === 0 && r.ignored === expectedStageEvents) &&
        last.kafka.groups.length === 3 && last.kafka.groups.every(g => g.initialized && g.lag === 0)) { clean = last; break }
    await pause(100)
  }
  assert.ok(clean && Date.now() <= deadline, 'setup did not return to the predefined full-chain baseline within 60s')
  const result = { startedAt, committedAt, completedAt: Date.now(), setupBudgetMs: 60000,
    generatedFactTransitions: expected, generatedEvents: expected, countedAsHTTPMutation: false,
    beforeTransitions: before.transitions, afterTransitions: after.transitions,
    ...(!vectorMode ? { beforeEvents: before.eventCount, afterEvents: after.eventCount } : { fullAuditPerformed: false, acceptedSample: audit.acceptedSample,
      observedStageEventCount: expectedStageEvents, observedStageEventCountSource: 'complete accepted Go vector sample; not a new history scan' }),
    ...(baselineAudit ? { baselineAudit, baselineAuditEventCount: baselineEvents, baselineCountSource: ready.baselineCountSource } : {}),
    beforeDistribution: initialDistribution(manifest, before), afterDistribution: distribution,
    markLuaSourcePath: luaSourcePath, markLuaSHA256: hash(lua), sourceFileSHA256: hash(source),
    localTTLWaitMs: 2100, observerCleanSampleAt: clean.at,
    scope: 'synthetic fixture normalization after warm, before the measured window: only real active->inactive flips append versions/events; no history rollback or API reset traffic' }
  await save(join(dir, 'setup.json'), result)
  return result
}

await guard()
if (action === 'prepare') {
  const count = poolSize(process.env.CAP_LIKE_POOL_SIZE === undefined ? 512 : Number(process.env.CAP_LIKE_POOL_SIZE))
  assert.ok(process.env.CAP_LIKE_FIXTURE_DIR, 'explicit new CAP_LIKE_FIXTURE_DIR required')
  const dir = evidence(process.env.CAP_LIKE_FIXTURE_DIR); await mkdir(dir, { recursive: false })
  const fixture = 'caplike-' + randomBytes(5).toString('hex'), privateDir = await mkdtemp(join(tmpdir(), 'cap-like-')), credentialFile = join(privateDir, 'input.json')
  const manifest = { task: 'CAP-10', fixture, project: owned.project, baseUrl: owned.api, sourceSha: process.env.CAP_SOURCE_SHA,
    executionPlatform, transitionSHA256: process.env.CAP10_TRANSITION_SHA256 || null,
    expectedRevision: process.env.CAP_READ_EXPECTED_REVISION, credentialFile, created: new Date().toISOString(), status: 'PREPARING',
    poolSize: count, users: count, multiNotes: count, multiRelations: count, bodyBytes: 256,
    relationshipSeed: { active: 0, version: 0, events: 0, syntheticFixtureOnly: true } }
  await save(join(dir, 'fixture.json'), manifest)
  const phones = new Set(); while (phones.size < count + 1) phones.add('137' + randomInt(10000000, 100000000))
  const phoneList = [...phones], names = phoneList.map((_, i) => fixture + (i === 0 ? '-author' : '-u' + String(i - 1).padStart(3, '0')))
  assert.equal(await sql(`SELECT COUNT(*) FROM tb_user WHERE nick_name LIKE '${fixture}%' OR phone IN (${phoneList.map(p => `'${p}'`).join(',')})`), '0', 'synthetic user collision')
  assert.equal(await sql('SELECT COUNT(*) FROM tb_shop WHERE id=1'), '1')
  await sql(`INSERT INTO tb_user(phone,password,nick_name,icon) VALUES ${phoneList.map((p, i) => `('${p}','x','${names[i]}','')`).join(',')};`)
  const userRows = (await sql(`SELECT id,nick_name FROM tb_user WHERE nick_name LIKE '${fixture}%' ORDER BY nick_name`)).split(/\r?\n/).map(s => s.split('\t'))
  const byName = new Map(userRows.map(([id, name]) => [name, id])), authorId = byName.get(names[0]), userIds = names.slice(1).map(n => byName.get(n))
  for (const id of [authorId, ...userIds]) assert.match(id || '', /^[1-9]\d*$/)
  await sql(`INSERT INTO tb_blog(shop_id,user_id,title,images,content,liked,comments,status,publish_version,create_time,update_time) VALUES ${Array.from({ length: count }, (_, i) => `(1,${authorId},'${fixture}-n${String(i).padStart(4, '0')}','','${'x'.repeat(256)}',0,0,'PUBLISHED',1,NOW()-INTERVAL 1 HOUR,NOW()-INTERVAL 1 HOUR)`).join(',')};`)
  const notes = (await sql(`SELECT id FROM tb_blog WHERE title LIKE '${fixture}%' ORDER BY title`)).split(/\r?\n/)
  assert.equal(notes.length, count)
  Object.assign(manifest, { authorId, userIds, multiNoteIds: notes })
  await sql(`INSERT INTO tb_blog_like(blog_id,user_id,active,version) VALUES ${notes.map((note, i) => `(${note},${userIds[i]},0,0)`).join(',')};`)
  await writeFile(join(dir, 'fixture.json'), json(manifest))
  const runtime = { fixture, baseUrl: owned.api, users: [] }
  await writeFile(credentialFile, json(runtime), { mode: 0o600, flag: 'wx' })
  for (let i = 0; i < count; i++) {
    const code = await api('POST', '/user/code?phone=' + phoneList[i + 1]); assert.equal(code.body.data?.mode, 'development')
    const login = await api('POST', '/user/login', { phone: phoneList[i + 1], code: code.body.data.code })
    assert.equal(login.body.success, true); assert.equal(typeof login.body.data, 'string')
    runtime.users.push({ userId: userIds[i], token: login.body.data })
    await writeFile(credentialFile, json(runtime), { mode: 0o600 })
  }
  manifest.status = 'READY'; await writeFile(join(dir, 'fixture.json'), json(manifest))
  console.log(JSON.stringify({ prepared: true, manifest: join(dir, 'fixture.json'), poolSize: count, users: count, notes: count, privateCredentialValuesExported: false }))
} else if (action === 'finish') {
  const { manifest, runtime, manifestSHA256 } = await loadFixture()
  let loggedOut = 0
  for (const user of runtime.users) { const result = await api('POST', '/user/logout', undefined, user.token); assert.equal(result.status, 200); assert.equal(result.body.success, true); loggedOut++ }
  try { await unlink(manifest.credentialFile); await rmdir(dirname(manifest.credentialFile)) }
  catch (error) { if (manifest.status !== 'PREPARING' || error.code !== 'ENOENT') throw error }
  await save(join(dirname(fixturePath), 'finish.json'), { at: new Date().toISOString(), loggedOut, credentialFileRemoved: true,
    sqlFixtureRetained: true, apiSourceSha: process.env.CAP_SOURCE_SHA, fixtureSourceSha: manifest.sourceSha, fixtureManifestSHA256: manifestSHA256 })
  console.log(JSON.stringify({ finished: true, likersLoggedOut: loggedOut, credentialInputRemoved: true, sqlFixtureRetained: true }))
} else {
  const { manifest, runtime, manifestSHA256, finishedFixtureAudit } = await loadFixture({ finishedManifestSHA256: finishedFixtureManifestSHA256 })
  const count = fixturePoolSize(manifest)
  const mode = process.env.CAP_LIKE_MODE || 'multi'; assert.equal(mode, 'multi')
  assert.ok(process.env.CAP_LIKE_EVIDENCE, 'explicit new CAP_LIKE_EVIDENCE required')
  const dir = evidence(process.env.CAP_LIKE_EVIDENCE); await mkdir(dir, { recursive: false })
  if (action === 'inspect') {
    const state = await snapshot(manifest, mode, 'pre-reservation-inspect')
    const projection = await derived(manifest, mode, state)
    const errors = [...factErrors(manifest, mode, state), ...projection.errors]
    const distribution = initialDistribution(manifest, state)
    if (process.env.CAP10_REQUIRE_ALL_EXISTING === '1' && distribution.existing !== count) errors.push('formal requires all manifest-bound relationships already existing')
    if (process.env.CAP10_REQUIRE_INITIAL_ACTIVE === '0' && distribution.active !== 0) errors.push('formal initial active distribution differs')
    const result = { at: new Date().toISOString(), valid: errors.length === 0, errors, distribution,
      facts: state, derived: projection, reservesCapacitySlot: false }
    await save(join(dir, 'inspect.json'), result)
    console.log(JSON.stringify({ valid: result.valid, distribution, errors, reservesCapacitySlot: false }))
    process.exitCode = result.valid ? 0 : 1
  } else if (action === 'reconcile') {
    const result = await reconcile(manifest, mode, 60)
    if (finishedFixtureAudit) result.finishedFixtureAudit = finishedFixtureAudit
    await save(join(dir, 'reconcile.json'), result)
    console.log(JSON.stringify({ valid: result.valid, errors: result.errors })); process.exitCode = result.valid ? 0 : 1
  } else if (action === 'probe') {
    const user = runtime.users[0], noteId = modeNotes(manifest, mode)[0], records = []
    const result = { started: new Date().toISOString(), passed: false, records, simulatedLostReceipt: false,
      realTransportTimeoutInjected: false, recoveredFromAuthoritativeGET: false,
      scope: 'bounded actual API mutation/no-op/receipt-loss-recovery probe; all partial API observations retained; no throughput or P95/P99 claim' }
    const get = async () => { const r = await api('GET', `/notes/${noteId}/like`, undefined, user.token); const s = stateFromBody(r.body, noteId); records.push({ method: 'GET', status: r.status, kind: 'fact-read', state: s }); assert.equal(r.status, 200); assert.ok(s); return s }
    const put = async (prior, wanted, expected) => { const r = await api('PUT', `/notes/${noteId}/like`, { liked: wanted }, user.token); const outcome = classifyWrite(r.status, r.body, noteId, prior, wanted); records.push({ method: 'PUT', status: r.status, kind: outcome.kind, wanted, previous: { liked: prior.liked, version: prior.version }, state: outcome.state }); assert.equal(outcome.kind, expected); return outcome.state }
    try {
      let prior = await get(); prior = await put(prior, !prior.liked, 'mutation')
      prior = await put(prior, prior.liked, 'noop') // intentional idempotency, not capacity mutation
      const discarded = await put(prior, !prior.liked, 'mutation')
      result.simulatedLostReceipt = true
      // Deliberate client receipt loss after an actual completed response.
      const recovered = await get(); assert.equal(recovered.version, discarded.version); assert.equal(recovered.liked, discarded.liked)
      result.recoveredFromAuthoritativeGET = true
      prior = await put(recovered, !recovered.liked, 'mutation'); result.finalState = prior
      result.reconcile = await reconcile(manifest, mode, 60); result.passed = result.reconcile.valid
    } catch (error) {
      result.failure = error.message
      if (error.observerSQLFailure) result.observerSQLFailure = error.observerSQLFailure
    } finally {
      result.finished = new Date().toISOString()
      await save(join(dir, 'probe.json'), result)
    }
    console.log(JSON.stringify({ passed: result.passed, mutations: records.filter(r => r.kind === 'mutation').length,
      intentionalNoops: records.filter(r => r.kind === 'noop').length, recoveredFromAuthoritativeGET: result.recoveredFromAuthoritativeGET, failure: result.failure || null }))
    process.exitCode = result.passed ? 0 : 1
  } else {
    const rate = Number(process.env.CAP_LIKE_RATE), seconds = Number(process.env.CAP_LIKE_SECONDS || 60), warmSeconds = Number(process.env.CAP_LIKE_WARM_SECONDS || 10), warmRate = Number(process.env.CAP_LIKE_WARM_RATE || Math.min(25, rate)), preVUs = Number(process.env.CAP_LIKE_PRE_VUS || count), maxVUs = Number(process.env.CAP_LIKE_MAX_VUS || count)
    assert.ok([rate, seconds, warmSeconds, warmRate, preVUs, maxVUs].every(Number.isInteger))
    assert.ok(rate >= 1 && rate <= 5000 && [60, 180].includes(seconds) && warmSeconds === 10 && warmRate === Math.min(25, rate) && preVUs === count && maxVUs === count)
    assert.ok(count === 512 || process.env.CAP10_LOAD_BACKEND === 'pts', 'larger cohorts require the explicitly configured PTS backend')
    assert.equal(await stopPresent(), false, 'stop file already exists; no new native phase started')
    const report = { task: 'CAP-10', mode, rate, seconds, warmSeconds, warmRate, preVUs, maxVUs, poolSize: count, fixture: manifest.fixture, observerPolicy: observationPolicy,
      executionPlatform, transitionSHA256: process.env.CAP10_TRANSITION_SHA256 || null,
      apiSourceSha: process.env.CAP_SOURCE_SHA, apiExpectedRevision: process.env.CAP_READ_EXPECTED_REVISION,
      fixtureSourceSha: manifest.sourceSha, fixtureManifestSHA256: manifestSHA256,
      configuredPool: { users: count, notes: modeNotes(manifest, mode).length, exclusiveRelationships: count, authors: 1, noteBodyBytes: 256 }, started: new Date().toISOString(),
      nativeScenario: { complete: false, scope: 'native timing not yet verified; no completed scenario claim' },
      thresholds: { actualOfferedRatioMin: .99, mutationFractionMin: .99, unexpectedNoopMax: 0, contentErrorsMax: 0,
        unknownMax: 0, droppedMax: 0, systemAndHTTPErrorRatesMaxExclusive: .01, p95MsMax: 200, p99MsMax: 500 },
      phaseProcesses: 'warm and stable use separate k6 processes with fresh authoritative RR hydration', initialDistributions: {},
      scope: 'real authenticated PUT mutations; no same-pair overlap. Correctness/unknown/noop abort the batch; authoritative facts are reconciled after settle, without resuming uncertain pairs. API invalidate/read/Mark remain in PUT latency. Latency is evaluated only after the complete target window.' }
    if (process.env.CAP10_LOAD_BACKEND === 'pts') {
      report.loadBackend = 'pts'
      report.phaseProcesses = 'warm and stable use separate Alibaba PTS ReportIds with the packet-bound managed engines and fresh authoritative RR hydration'
      report.generatorResourceScope = 'ECS Python collector plus Node helper <2GiB; each remote JMeter engine separately <6GiB, matching the collector safety limit. Same PTS topology is required for every comparison.'
    }
    let warmBefore = null, finalFullAuditAttempted = false, stableControllerStarted = false
    async function finalFullAudit(reason, nativeEndMs = null) {
      assert.equal(finalFullAuditAttempted, false, 'one final full-audit attempt only')
      finalFullAuditAttempted = true
      await save(join(dir, 'final-full-audit-attempt.json'), { reason, attemptedAt: Date.now(), baselineAudit: windowBaseline?.ref, noAutomaticRetry: true })
      const end = nativeEndMs ?? report.nativeScenario?.scheduledEndedAt
      if (stableControllerStarted && report.nativeScenario?.complete === true && Number.isFinite(end) && end > 0) report.nativeRecoveryEvidence = await waitObserverRecovery(end)
      else report.nativeRecoveryEvidence = { nativeRecoveryVerified: false, summaryPassed: false, scope: 'No registered complete stable native window; no recovery claim or wait for a done signal that requires helper exit.' }
      return reconcile(manifest, mode, 0)
    }
    async function runPhase(phase, duration, state) {
      assert.equal(await stopPresent(), false, 'resource stop blocks next native phase')
      const map = new Map(state.facts.map(r => [r.noteId + ':' + r.userId, r]))
      const input = { fixture: manifest.fixture, poolSize: count, baseUrl: owned.api, users: runtime.users, multiNoteIds: manifest.multiNoteIds,
        initial: runtime.users.map((user, i) => { const noteId = manifest.multiNoteIds[i]; return { noteId, userId: user.userId, ...(map.get(noteId + ':' + user.userId) || { active: 0, version: 0 }) } }) }
      const privateDir = await mkdtemp(join(tmpdir(), 'cap-like-phase-')), path = join(privateDir, 'input.json')
      const env = { ...process.env, CAP_LIKE_INPUT: path, CAP_LIKE_MODE: mode, CAP_LIKE_RATE: String(phase === 'warm' ? warmRate : rate), CAP_LIKE_SECONDS: String(duration), CAP_LIKE_PHASE: phase, CAP_LIKE_PRE_VUS: String(preVUs), CAP_LIKE_MAX_VUS: String(maxVUs) }
      try {
        // Register the exact cleanup path before a credential can reach disk.
        await save(join(dir, phase + '-private-input.json'), { path, parent: privateDir, credentialValuesExported: false })
        await writeFile(path, json(input), { mode: 0o600, flag: 'wx' })
        const pts = process.env.CAP10_LOAD_BACKEND === 'pts'
        const binary = pts ? process.env.CAP10_PYTHON_BIN : process.env.K6_BIN || 'k6'
        assert.ok(binary, 'explicit Python executable required for PTS controller')
        const argv = pts ? ['-B', '-X', 'utf8', 'scripts/pts/cap10-pts-phase.py', '--input', path,
          '--summary', join(dir, phase + '-summary.json'), '--evidence', join(dir, phase + '-pts')]
          : ['run', '--summary-export', join(dir, phase + '-summary.json'), 'scripts/load/cap10-like.js']
        return await command(binary, argv,
        { env, output: join(dir, phase + '-console.txt'), stopFile: process.env.CAP_STOP_FILE, onStart(pid) {
          const detail = json(pts ? { phase, backend: 'pts', helper_pid: process.pid, controller_pid: pid,
            controller_started_unix_ms: Date.now(), remoteEvidence: join(dir, phase + '-pts') }
            : { phase, helper_pid: process.pid, k6_pid: pid, k6_started_unix_ms: Date.now() })
          return Promise.all([save(join(dir, phase + '-process.json'), JSON.parse(detail)), writeFile(join(dir, 'process.json'), detail)]).then(result => {
            if (vectorMode && phase === 'stable' && Number.isSafeInteger(pid) && pid > 0) stableControllerStarted = true
            return result
          })
        } })
      } finally {
        try { await unlink(path) } catch (error) { if (error.code !== 'ENOENT') throw error }
        await rmdir(privateDir)
        await save(join(dir, phase + '-private-input-removed.json'), { path, removed: true, credentialValuesExported: false })
      }
    }
    try {
      report.scriptSHA256 = Object.fromEntries(await Promise.all(['scripts/cap10-like.mjs', 'scripts/load/cap10-like.js', 'scripts/load/cap10-like-contract.mjs', 'scripts/load/cap10-native-window.mjs', 'scripts/load/cap10-db-observe.mjs', 'scripts/load/cap10-cohort-contract.mjs'].map(async p => [p, hash(await readFile(p))])))
      warmBefore = vectorMode ? await vectorWarmBefore(manifest, mode, dir) : await snapshot(manifest, mode, 'before-warm'); await save(join(dir, 'warm-before.json'), warmBefore)
      report.initialDistributions.beforeWarm = initialDistribution(manifest, warmBefore)
      assert.deepEqual(factErrors(manifest, mode, warmBefore), [], 'starting facts invalid')
      if (process.env.CAP10_FORMAL === '1') assert.equal(report.initialDistributions.beforeWarm.existing, count, 'formal requires all manifest-bound relationships before warm')
      const warm = await runPhase('warm', warmSeconds, warmBefore); report.warmExit = warm.code; report.warmExternalStop = warm.externalStop
      assert.equal(warm.code, 0, 'warm failed; stable phase not started'); assert.equal(warm.externalStop, null)
      report.setup = await normalizeInitialState(manifest, mode, dir)
      const before = await phaseCapture(manifest, mode, 'before-stable'); await save(join(dir, 'before.json'), before)
      report.initialDistributions.beforeStable = initialDistribution(manifest, before)
      assert.equal(report.initialDistributions.beforeStable.existing, count); assert.equal(report.initialDistributions.beforeStable.active, 0)
      assert.deepEqual(factErrors(manifest, mode, before), [], 'warm facts invalid')
      if (vectorMode) {
        const accepted = acceptedSample(await readFile(join(windowBaseline.downstream, 'timeline.ndjson')), before, { baselineEvents: windowBaseline.events, notBeforeMs: report.setup.committedAt })
        assert.ok(accepted, 'stable hydration must match an accepted setup vector'); report.beforeStableAcceptedSample = accepted
      }
      await save(join(dir, 'stable-db-before.json'), await collectDatabase(statement => sql(statement, true)))
      await save(join(dir, 'stable-capacity-before.json'), await capacitySnapshot())
      report.stableStartedAt = Date.now(); const stable = await runPhase('stable', seconds, before)
      Object.assign(report, { stableExit: stable.code, stableExternalStop: stable.externalStop, stableEndedAt: Date.now() })
      report.stableWrapperStartedAt = report.stableStartedAt; report.stableWrapperEndedAt = report.stableEndedAt
      report.stableCommandElapsedMs = report.stableEndedAt - report.stableStartedAt
      await save(join(dir, 'stable-capacity-after.json'), await capacitySnapshot())
      await save(join(dir, 'stable-db-after.json'), await collectDatabase(statement => sql(statement, true)))
      let vectorNative = null, earlyUnknown = null, vectorScenario = null
      if (vectorMode) {
        vectorNative = JSON.parse(await readFile(join(dir, 'stable-summary.json'), 'utf8'))
        vectorScenario = process.env.CAP10_LOAD_BACKEND === 'pts' ? vectorNative.nativeScenario : nativeScenarioWindow({ metric: vectorNative.metrics?.cap_native_scenario_start_unix_ms,
          durationMetric: vectorNative.metrics?.cap_native_scenario_duration_ms, seconds, wrapperStartedAt: report.stableWrapperStartedAt, wrapperEndedAt: report.stableWrapperEndedAt,
          exit: stable.code, externalStop: stable.externalStop, metrics: vectorNative.metrics })
        report.nativeScenario = vectorScenario
        const count = name => { const value = vectorNative.metrics?.[name]?.count; assert.ok(Number.isSafeInteger(value) && value >= 0); return value }
        earlyUnknown = unknownBounds(count('cap_like_attempted'), count('cap_like_completed'), count('cap_like_effect_unknown'))
        if (stable.code !== 0 || stable.externalStop || earlyUnknown.unknownUpperBound > 0) { report.ambiguousRequestSettleWaitMs = 5500; await pause(5500) }
      }
      const after = await phaseCapture(manifest, mode, 'after-stable'); await save(join(dir, 'after.json'), after)
      report.verifiedChangedParticipants = { stable: changedParticipants(before, after),
        warmSetupAndStable: { ...changedParticipants(warmBefore, after), includesSyntheticSetupTransitions: true } }
      // Windows can terminate k6 before it writes a summary. Preserve the real
      // business reconciliation independently; missing native metrics still fail.
      if (!vectorMode && (stable.code !== 0 || stable.externalStop)) { report.ambiguousRequestSettleWaitMs = 5500; await pause(5500) }
      const final = vectorMode ? await finalFullAudit('after-native-recovery-evidence', vectorScenario?.scheduledEndedAt ?? null) : await reconcile(manifest, mode, 60); await save(join(dir, 'reconcile.json'), final)
      report.finalReconcileValid = final.valid
      report.finalFactsValid = final.factsValid; report.downstreamConverged = final.downstreamConverged
      const native = vectorMode ? vectorNative : JSON.parse(await readFile(join(dir, 'stable-summary.json'), 'utf8'))
      report.sourceEngine = native.sourceEngine || 'k6'
      report.nativeScenario = vectorMode ? vectorScenario : process.env.CAP10_LOAD_BACKEND === 'pts' ? native.nativeScenario : nativeScenarioWindow({ metric: native.metrics?.cap_native_scenario_start_unix_ms,
        durationMetric: native.metrics?.cap_native_scenario_duration_ms, seconds,
        wrapperStartedAt: report.stableWrapperStartedAt, wrapperEndedAt: report.stableWrapperEndedAt,
        exit: stable.code, externalStop: stable.externalStop, metrics: native.metrics })
      const metric = name => { const m = native.metrics?.[name]; assert.ok(m, 'missing native metric: ' + name); return m }
      const counter = name => { const value = metric(name).count; assert.ok(Number.isSafeInteger(value) && value >= 0, 'invalid native counter: ' + name); return value }
      const counts = { attempted: counter('cap_like_attempted'), completed: counter('cap_like_completed'), successful: counter('cap_like_success'), mutation: counter('cap_like_mutation'), noop: counter('cap_like_noop'), rejected: counter('cap_like_rejected'), effectUnknown: counter('cap_like_effect_unknown'), contentErrors: counter('cap_like_content_errors'), systemErrors: counter('cap_like_system_errors'), httpErrors: counter('cap_like_http_errors'), recoveryReads: counter('cap_like_recovery_reads'), recoveryFailed: counter('cap_like_recovery_failed'), retiredRelations: counter('cap_like_retired_relations'), dropped: counter('dropped_iterations'), nativeWriteHTTP: counter('http_reqs{name:like-write}'), generatorTimingViolations: process.env.CAP10_LOAD_BACKEND === 'pts' ? counter('cap_like_generator_timing_violations') : 0 }
      assert.ok(counts.attempted >= counts.nativeWriteHTTP && counts.nativeWriteHTTP >= counts.completed && counts.completed >= counts.successful && counts.successful >= counts.mutation)
      const allLatency = metric('cap_like_all_ms'), mutationLatency = native.metrics?.cap_like_mutation_ms || null
      for (const m of [allLatency, mutationLatency]) assert.ok(m && ['p(95)', 'p(99)', 'max'].every(k => Number.isFinite(m[k]) && m[k] >= 0), 'native latency missing')
      const { classificationUnknown, unknownLowerBound, unknownUpperBound, unknownIsExact, unknown } = unknownBounds(counts.attempted, counts.completed, counts.effectUnknown)
      const dbMutations = after.transitions - before.transitions, eventsAdded = vectorMode ? final.facts.eventCount - (windowBaseline.events + report.beforeStableAcceptedSample.stageEvents) : after.eventCount - before.eventCount
      if (vectorMode) {
        const observed = JSON.parse((await readFile(join(windowBaseline.downstream, 'timeline.ndjson'), 'utf8')).trim().split(/\r?\n/).at(-1))
        assert.equal(final.facts.eventCount, windowBaseline.events + observed.sql.counts.events, 'final independent source count differs from bound baseline plus terminal vector delta')
        assert.equal(observed.sql.digestContract, 'like-vector-digest-v1')
        assert.equal(observed.sql.factVectorSHA256, after.factVectorSHA256)
        assert.equal(observed.sql.controlsSHA256, after.controlsSHA256)
        assert.equal(vectorIdentity(manifest, final.facts.facts).sha256, after.factVectorSHA256)
        assert.equal(controlsIdentity(final.facts.controlRows).sha256, after.controlsSHA256)
        report.sourceCountProvenance = { baselineAudit: windowBaseline.ref, baselineEvents: windowBaseline.events, terminalStageEvents: observed.sql.counts.events, finalAuditedEvents: final.facts.eventCount, stableBeforeAcceptedSample: report.beforeStableAcceptedSample, intermediateSourceHistoryRead: false }
      }
      const warmNative = JSON.parse(await readFile(join(dir, 'warm-summary.json'), 'utf8'))
      const warmMutations = warmNative.metrics?.cap_like_mutation?.count
      assert.ok(Number.isSafeInteger(warmMutations) && warmMutations >= 0, 'warm mutation count unavailable')
      if (vectorMode) assert.equal(report.sourceCountProvenance.terminalStageEvents, warmMutations + report.setup.generatedFactTransitions + counts.mutation, 'warm/setup/stable deltas differ from terminal source evidence')
      report.phaseAccounting = { warmHTTPMutations: warmMutations, syntheticSetupTransitions: report.setup.generatedFactTransitions,
        stableKnownHTTPMutations: counts.mutation, stableDBTransitions: dbMutations,
        totalDBTransitions: after.transitions - warmBefore.transitions,
        warmDBTransitions: report.setup.beforeTransitions - warmBefore.transitions,
        scope: 'warm HTTP, synthetic SQL setup and stable HTTP are separate; combined version differences include all three' }
      report.phaseAccountingValid = report.phaseAccounting.warmDBTransitions === warmMutations
      assert.equal(report.phaseAccountingValid, true, 'warm HTTP and DB transitions differ')
      report.result = { ...counts, classificationUnknown, unknown, unknownIsExact, unknownLowerBound, unknownUpperBound, targetOffers: rate * seconds, actualOfferedRatio: counts.attempted / (rate * seconds), mutationFraction: counts.mutation / Math.max(1, counts.attempted), mutationPerNominalSecond: counts.mutation / seconds, httpPutPerNominalSecond: counts.attempted / seconds, allLatency, mutationLatency, recoveryLatency: native.metrics?.cap_like_recovery_ms || null,
        iterationLatency: native.metrics?.iteration_duration || null,
        classifiedIterationLatency: native.metrics?.cap_like_iteration_ms || null,
        dbMutations, eventsAdded, mutationCounterMatchesDB: dbMutations === counts.mutation && eventsAdded === counts.mutation,
        unknownScope: 'classificationUnknown and effectUnknown can overlap during abort sample loss. Distinct unknown is exact only when lower/upper coincide; otherwise unknown=null and bounds remain. Recovery never erases original uncertainty. Zero-unknown requires both counters zero.' }
      if (!vectorMode && unknownUpperBound > 0 && !report.ambiguousRequestSettleWaitMs) {
        report.ambiguousRequestSettleWaitMs = 5500; await pause(5500)
        const settled = await reconcile(manifest, mode, 60); await save(join(dir, 'uncertain-reconcile.json'), settled)
        report.finalReconcileValid = settled.valid
        report.finalFactsValid = settled.factsValid; report.downstreamConverged = settled.downstreamConverged
      }
      report.meetsAPIBudget = report.nativeScenario.complete && stable.code === 0 && !stable.externalStop && counts.attempted > 0 && counts.attempted === counts.nativeWriteHTTP && counts.attempted === counts.completed && counts.attempted / (rate * seconds) >= .99 && counts.mutation / counts.attempted >= .99 && counts.noop === 0 && counts.rejected === 0 && classificationUnknown === 0 && counts.effectUnknown === 0 && counts.dropped === 0 && counts.contentErrors === 0 && counts.systemErrors / counts.attempted < .01 && counts.httpErrors / counts.attempted < .01 && allLatency['p(95)'] <= 200 && allLatency['p(99)'] <= 500 && mutationLatency['p(95)'] <= 200 && mutationLatency['p(99)'] <= 500 && report.result.mutationCounterMatchesDB
      report.meetsAPIBudget &&= counts.generatorTimingViolations === 0
      report.meetsBudget = report.meetsAPIBudget && report.finalReconcileValid
      if (vectorMode) report.meetsBudget &&= report.nativeRecoveryEvidence?.nativeRecoveryVerified === true && report.nativeRecoveryEvidence.summaryPassed === true
      report.downstreamSteadyStateScope = 'final valid means convergence only; common stage lag/last-minute growth gate must additionally pass for a full-chain capacity claim'
    } catch (error) {
      report.failure = error.message; if (error.observerSQLFailure) report.observerSQLFailure = error.observerSQLFailure
      report.meetsBudget = false; report.meetsAPIBudget ??= false
      if (!Object.hasOwn(report, 'finalReconcileValid') && (!vectorMode || !finalFullAuditAttempted)) {
        try {
          report.failedPhaseSettleWaitMs = 5500; await pause(5500)
          const failed = vectorMode ? await finalFullAudit(stableControllerStarted ? 'failed-stable-once' : 'failed-before-stable-start-once') : await reconcile(manifest, mode, 60); await save(join(dir, 'failed-phase-reconcile.json'), failed)
          report.finalReconcileValid = failed.valid
          report.finalFactsValid = failed.factsValid; report.downstreamConverged = failed.downstreamConverged
        } catch { report.finalReconcileValid = false; report.failedPhaseReconcileUnavailable = true }
      }
    }
    finally { report.finished = new Date().toISOString(); await save(join(dir, 'result.json'), report) }
    console.log(JSON.stringify({ task: 'CAP-10', mode, rate, seconds, meetsAPIBudget: report.meetsAPIBudget, meetsBudget: report.meetsBudget, result: report.result, failure: report.failure || null }))
    process.exitCode = report.meetsBudget ? 0 : 1
  }
}
