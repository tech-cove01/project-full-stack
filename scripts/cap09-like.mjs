// CAP-09 real synchronous like writes. Only the fixed, owned loopback cohort.
import assert from 'node:assert/strict'
import { randomBytes, randomInt, createHash } from 'node:crypto'
import { spawn } from 'node:child_process'
import { createWriteStream } from 'node:fs'
import { mkdir, mkdtemp, readFile, writeFile, unlink, rmdir } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { resolve, dirname, join, basename, relative, sep } from 'node:path'
import net from 'node:net'
import { stateFromBody, classifyWrite, unknownBounds } from './load/cap09-like-contract.mjs'
import { nativeScenarioWindow } from './load/cap09-native-window.mjs'

const owned = { project: 'project-full-stack-cap09', mysql: '3471', redis: '6571', api: 'http://127.0.0.1:8271', database: 'project_full_stack_go' }
const action = process.argv[2]
assert.ok(['prepare', 'probe', 'run', 'reconcile', 'finish'].includes(action), 'use prepare/probe/run/reconcile/finish')
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
const sqlBin = process.env.MYSQL_BIN || 'mysql'
const evidenceRoot = resolve('docs/evidence/2026-11-01-cap09')
const privateRoot = resolve(process.env.CAP09_RUNTIME || '')
assert.ok(process.env.CAP09_RUNTIME, 'explicit private CAP09_RUNTIME required')
assert.equal(dirname(privateRoot), resolve('..')); assert.match(basename(privateRoot), /^\.runtime-fullstack-cap09-[a-f0-9]{12}$/)
assert.equal(resolve(tmpdir()), join(privateRoot, 'tmp'), 'TEMP/TMP must be the owned private runtime/tmp')
function under(path, root) { const rel = relative(root, resolve(path)); return rel !== '' && rel !== '..' && !rel.startsWith('..' + sep) && !resolve(path).startsWith('\\\\') && !rel.includes(':') }
function evidence(path) { assert.ok(under(path, evidenceRoot), 'new output must belong to the fixed CAP09 evidence root'); return resolve(path) }

async function command(binary, args, { input, env = process.env, output, onStart, stopFile } = {}) {
  return new Promise((resolveCommand, reject) => {
    const child = spawn(binary, args, { env, windowsHide: true })
    let stdout = '', stderr = '', timer = null, checking = false, externalStop = null
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
    child.on('error', error => { if (timer) clearInterval(timer); reject(error) })
    child.on('close', code => {
      if (timer) clearInterval(timer)
      if (file) file.end(() => resolveCommand({ code, externalStop }))
      else resolveCommand({ code, stdout, stderr, externalStop })
    })
    child.stdin.end(input)
  })
}

async function sql(statement, observer = false) {
  const user = observer ? process.env.CAP_READ_OBSERVER_USER : process.env.MYSQL_USER
  const password = observer ? process.env.CAP_READ_OBSERVER_PASSWORD : process.env.MYSQL_PASSWORD
  assert.ok(user && password, 'explicit SQL credentials required')
  const result = await command(sqlBin, ['--protocol=TCP', '--host=127.0.0.1', '--port=' + owned.mysql,
    '--user=' + user, '--default-character-set=utf8mb4', '--batch', '--skip-column-names', owned.database],
  { input: `SET NAMES utf8mb4; SET time_zone='+08:00'; ${statement}`, env: { ...process.env, MYSQL_PWD: password } })
  if (result.code !== 0) {
    const failure = new Error('owned fixture/observer SQL failed; credentials and statement body suppressed')
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
    ...(body === undefined ? {} : { body: JSON.stringify(body) }), signal: AbortSignal.timeout(10000) })
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
async function loadFixture() {
  assert.ok(fixturePath, 'explicit CAP_LIKE_FIXTURE required')
  evidence(fixturePath)
  const bytes = await readFile(fixturePath), manifest = JSON.parse(bytes)
  assert.equal(manifest.project, owned.project); assert.equal(manifest.baseUrl, owned.api)
  assert.ok(manifest.status === 'READY' || (action === 'finish' && manifest.status === 'PREPARING'), 'new fixture must be ready')
  assert.match(manifest.sourceSha, /^[a-f0-9]{40}$/); assert.equal(manifest.expectedRevision, 'capacity-' + manifest.sourceSha)
  assert.ok(under(manifest.credentialFile, join(privateRoot, 'tmp')) && basename(manifest.credentialFile) === 'input.json', 'private fixture input must stay in owned runtime/tmp')
  let runtime
  try { runtime = JSON.parse(await readFile(manifest.credentialFile, 'utf8')) }
  catch (error) {
    if (action !== 'finish' || manifest.status !== 'PREPARING' || error.code !== 'ENOENT') throw new Error('private fixture input unavailable or malformed; preserve its registered recovery path')
    runtime = { fixture: manifest.fixture, users: [] }
  }
  assert.equal(runtime.fixture, manifest.fixture)
  assert.ok(runtime.users.length === 512 || (action === 'finish' && runtime.users.length < 512))
  assert.deepEqual(runtime.users.map(u => u.userId), (manifest.userIds || []).slice(0, runtime.users.length))
  if (action !== 'finish') {
    assert.equal(manifest.userIds.length, 512); assert.equal(manifest.multiNoteIds.length, 512)
    for (const id of [...manifest.userIds, ...manifest.multiNoteIds, manifest.hotNoteId]) assert.match(id, /^[1-9]\d*$/)
  }
  return { manifest, runtime, manifestSHA256: hash(bytes) }
}
const modeNotes = (manifest, mode) => mode === 'hot' ? [manifest.hotNoteId] : manifest.multiNoteIds
function rows(raw) { return raw ? raw.split(/\r?\n/).filter(Boolean).map(line => JSON.parse(line)) : [] }

async function snapshot(manifest, mode, label) {
  const ids = modeNotes(manifest, mode).map(id => `'${id}'`).join(',')
  const start = Date.now()
  // All fact, counter and outbox checks share one read-only RR snapshot. The
  // pre/post phase windows remain sequential, never a per-request commit clock.
  const raw = rows(await sql(`SET TRANSACTION ISOLATION LEVEL REPEATABLE READ; START TRANSACTION WITH CONSISTENT SNAPSHOT, READ ONLY;
SELECT JSON_OBJECT('_kind','fact','noteId',CAST(l.blog_id AS CHAR),'userId',CAST(l.user_id AS CHAR),'active',l.active,'version',l.version) FROM tb_blog_like l WHERE l.blog_id IN (${ids}) ORDER BY l.blog_id,l.user_id;
SELECT JSON_OBJECT('_kind','note','noteId',CAST(b.id AS CHAR),'liked',b.liked,'activeRelations',(SELECT COUNT(*) FROM tb_blog_like l WHERE l.blog_id=b.id AND l.active=1)) FROM tb_blog b WHERE b.id IN (${ids}) ORDER BY b.id;
SELECT JSON_OBJECT('_kind','event','noteId',g.note_id,'userId',g.user_id,'count',g.event_count,'distinctVersions',g.distinct_versions,'minVersion',g.min_version,'maxVersion',g.max_version,'invalid',g.invalid_count) FROM (SELECT o.aggregate_id AS note_id,JSON_UNQUOTE(JSON_EXTRACT(o.payload,'$.userId')) AS user_id,COUNT(*) AS event_count,COUNT(DISTINCT o.aggregate_version) AS distinct_versions,MIN(o.aggregate_version) AS min_version,MAX(o.aggregate_version) AS max_version,SUM(COALESCE(BINARY o.aggregate_type<>BINARY 'BLOG' OR BINARY o.event_id<>BINARY CONCAT('LIKE:',o.aggregate_id,':',JSON_UNQUOTE(JSON_EXTRACT(o.payload,'$.userId')),':',o.aggregate_version) OR JSON_TYPE(JSON_EXTRACT(o.payload,'$.blogId'))<>'STRING' OR JSON_TYPE(JSON_EXTRACT(o.payload,'$.userId'))<>'STRING' OR JSON_TYPE(JSON_EXTRACT(o.payload,'$.version'))<>'INTEGER' OR JSON_TYPE(JSON_EXTRACT(o.payload,'$.liked'))<>'BOOLEAN' OR BINARY JSON_UNQUOTE(JSON_EXTRACT(o.payload,'$.blogId'))<>BINARY o.aggregate_id OR BINARY JSON_UNQUOTE(JSON_EXTRACT(o.payload,'$.version'))<>BINARY CAST(o.aggregate_version AS CHAR) OR JSON_UNQUOTE(JSON_EXTRACT(o.payload,'$.liked'))<>IF(MOD(o.aggregate_version,2)=1,'true','false'),1)) AS invalid_count FROM tb_community_outbox o WHERE o.event_type='LIKE_CHANGED' AND o.aggregate_id IN (${ids}) GROUP BY o.aggregate_id,user_id) g ORDER BY g.note_id,g.user_id;
SELECT JSON_OBJECT('_kind','delivery','status',delivery_status,'count',COUNT(*)) FROM tb_community_outbox WHERE event_type='LIKE_CHANGED' AND aggregate_id IN (${ids}) GROUP BY delivery_status;
SELECT JSON_OBJECT('_kind','receipt','projection',r.projection,'state',r.state,'count',COUNT(*)) FROM tb_community_projection_event r JOIN tb_community_outbox o ON o.event_id=r.event_id WHERE o.event_type='LIKE_CHANGED' AND o.aggregate_id IN (${ids}) GROUP BY r.projection,r.state;
SELECT JSON_OBJECT('_kind','parked','projection',projection,'state',state,'count',COUNT(*)) FROM tb_community_consumer_failure GROUP BY projection,state; COMMIT;`, true))
  const take = kind => raw.filter(row => row._kind === kind).map(({ _kind, ...row }) => row)
  assert.ok(raw.every(row => ['fact', 'note', 'event', 'delivery', 'receipt', 'parked'].includes(row._kind)))
  const facts = take('fact'), notes = take('note'), events = take('event'), delivery = take('delivery'), receipts = take('receipt'), parked = take('parked')
  return { label, startedAt: start, endedAt: Date.now(), facts, notes, events, delivery, receipts, parked,
    transitions: facts.reduce((sum, row) => sum + row.version, 0), eventCount: events.reduce((sum, row) => sum + row.count, 0),
    scope: 'one read-only RR snapshot of the selected synthetic note pool; pre/post captures sequential; global consumer-failure rows include earlier stages in the owned DB' }
}

function factErrors(manifest, mode, state) {
  const errors = [], users = new Set(manifest.userIds), notes = new Set(modeNotes(manifest, mode))
  const actual = new Map(state.facts.map(r => [r.noteId + ':' + r.userId, r]))
  const events = new Map(state.events.map(r => [r.noteId + ':' + r.userId, r]))
  for (const row of state.facts) {
    if (!users.has(row.userId) || !notes.has(row.noteId) || (mode === 'multi' && manifest.multiNoteIds[manifest.userIds.indexOf(row.userId)] !== row.noteId)) errors.push('unexpected relation in owned note pool')
    if (!Number.isSafeInteger(row.version) || row.version < 1 || row.active !== row.version % 2) errors.push('relation state/version parity differs')
    const event = events.get(row.noteId + ':' + row.userId)
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

async function derived(manifest, mode, state) {
  const notes = modeNotes(manifest, mode)
  const replies = await redisCommands(notes.flatMap(id => [['SMEMBERS', 'community:likes:' + id], ['HGETALL', 'community:likes:versions:' + id]]))
  const errors = [], values = []
  for (let i = 0; i < notes.length; i++) {
    const id = notes[i], members = replies[i * 2], entries = replies[i * 2 + 1]
    assert.ok(Array.isArray(members) && Array.isArray(entries) && entries.length % 2 === 0)
    const versions = Object.fromEntries(Array.from({ length: entries.length / 2 }, (_, j) => [entries[j * 2], entries[j * 2 + 1]]))
    const facts = state.facts.filter(r => r.noteId === id), active = facts.filter(r => r.active === 1).map(r => r.userId).sort()
    if (JSON.stringify([...members].sort()) !== JSON.stringify(active) || facts.some(r => versions[r.userId] !== String(r.version))) errors.push('Redis relation members/version differs from authoritative facts')
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
    errors, facts: state, derived: projection, timeline,
    limitations: 'Final convergence is not steady-state proof; the common stage observer must separately record last-minute backlog/lag and the 60s recovery deadline.' }
}

async function stopPresent() {
  if (!process.env.CAP_STOP_FILE) return false
  try { await readFile(process.env.CAP_STOP_FILE); return true } catch (error) { if (error.code !== 'ENOENT') throw error; return false }
}

await guard()
if (action === 'prepare') {
  assert.ok(process.env.CAP_LIKE_FIXTURE_DIR, 'explicit new CAP_LIKE_FIXTURE_DIR required')
  const dir = evidence(process.env.CAP_LIKE_FIXTURE_DIR); await mkdir(dir, { recursive: false })
  const fixture = 'caplike-' + randomBytes(5).toString('hex'), privateDir = await mkdtemp(join(tmpdir(), 'cap-like-')), credentialFile = join(privateDir, 'input.json')
  const manifest = { task: 'CAP-09', fixture, project: owned.project, baseUrl: owned.api, sourceSha: process.env.CAP_SOURCE_SHA,
    expectedRevision: process.env.CAP_READ_EXPECTED_REVISION, credentialFile, created: new Date().toISOString(), status: 'PREPARING',
    users: 512, multiNotes: 512, hotNotes: 1, multiRelations: 512, hotRelations: 512, bodyBytes: 256 }
  await save(join(dir, 'fixture.json'), manifest)
  const phones = new Set(); while (phones.size < 513) phones.add('137' + randomInt(10000000, 100000000))
  const phoneList = [...phones], names = phoneList.map((_, i) => fixture + (i === 0 ? '-author' : '-u' + String(i - 1).padStart(3, '0')))
  assert.equal(await sql(`SELECT COUNT(*) FROM tb_user WHERE nick_name LIKE '${fixture}%' OR phone IN (${phoneList.map(p => `'${p}'`).join(',')})`), '0', 'synthetic user collision')
  assert.equal(await sql('SELECT COUNT(*) FROM tb_shop WHERE id=1'), '1')
  await sql(`INSERT INTO tb_user(phone,password,nick_name,icon) VALUES ${phoneList.map((p, i) => `('${p}','x','${names[i]}','')`).join(',')};`)
  const userRows = (await sql(`SELECT id,nick_name FROM tb_user WHERE nick_name LIKE '${fixture}%' ORDER BY nick_name`)).split(/\r?\n/).map(s => s.split('\t'))
  const byName = new Map(userRows.map(([id, name]) => [name, id])), authorId = byName.get(names[0]), userIds = names.slice(1).map(n => byName.get(n))
  for (const id of [authorId, ...userIds]) assert.match(id || '', /^[1-9]\d*$/)
  await sql(`INSERT INTO tb_blog(shop_id,user_id,title,images,content,liked,comments,status,publish_version,create_time,update_time) VALUES ${Array.from({ length: 513 }, (_, i) => `(1,${authorId},'${fixture}-n${String(i).padStart(3, '0')}','','${'x'.repeat(256)}',0,0,'PUBLISHED',1,NOW()-INTERVAL 1 HOUR,NOW()-INTERVAL 1 HOUR)`).join(',')};`)
  const notes = (await sql(`SELECT id FROM tb_blog WHERE title LIKE '${fixture}%' ORDER BY title`)).split(/\r?\n/)
  assert.equal(notes.length, 513)
  Object.assign(manifest, { authorId, userIds, multiNoteIds: notes.slice(0, 512), hotNoteId: notes[512] })
  await writeFile(join(dir, 'fixture.json'), json(manifest))
  const runtime = { fixture, baseUrl: owned.api, users: [] }
  await writeFile(credentialFile, json(runtime), { mode: 0o600, flag: 'wx' })
  for (let i = 0; i < 512; i++) {
    const code = await api('POST', '/user/code?phone=' + phoneList[i + 1]); assert.equal(code.body.data?.mode, 'development')
    const login = await api('POST', '/user/login', { phone: phoneList[i + 1], code: code.body.data.code })
    assert.equal(login.body.success, true); assert.equal(typeof login.body.data, 'string')
    runtime.users.push({ userId: userIds[i], token: login.body.data })
    await writeFile(credentialFile, json(runtime), { mode: 0o600 })
  }
  manifest.status = 'READY'; await writeFile(join(dir, 'fixture.json'), json(manifest))
  console.log(JSON.stringify({ prepared: true, manifest: join(dir, 'fixture.json'), users: 512, notes: 513, privateCredentialValuesExported: false }))
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
  const { manifest, runtime, manifestSHA256 } = await loadFixture()
  const mode = process.env.CAP_LIKE_MODE || 'multi'; assert.ok(['multi', 'hot'].includes(mode))
  assert.ok(process.env.CAP_LIKE_EVIDENCE, 'explicit new CAP_LIKE_EVIDENCE required')
  const dir = evidence(process.env.CAP_LIKE_EVIDENCE); await mkdir(dir, { recursive: false })
  if (action === 'reconcile') {
    const result = await reconcile(manifest, mode, 60); await save(join(dir, 'reconcile.json'), result)
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
    const rate = Number(process.env.CAP_LIKE_RATE), seconds = Number(process.env.CAP_LIKE_SECONDS || 45), warmSeconds = Number(process.env.CAP_LIKE_WARM_SECONDS || 10), warmRate = Number(process.env.CAP_LIKE_WARM_RATE || Math.min(25, rate)), preVUs = Number(process.env.CAP_LIKE_PRE_VUS || 256), maxVUs = Number(process.env.CAP_LIKE_MAX_VUS || 512)
    assert.ok([rate, seconds, warmSeconds, warmRate, preVUs, maxVUs].every(Number.isInteger))
    assert.ok(rate >= 1 && rate <= 5000 && seconds >= 1 && seconds <= 180 && warmSeconds >= 1 && warmSeconds <= 30 && warmRate >= 1 && warmRate <= rate && preVUs >= 1 && preVUs <= maxVUs && maxVUs <= 512)
    assert.equal(await stopPresent(), false, 'stop file already exists; no new native phase started')
    const report = { task: 'CAP-09', mode, rate, seconds, warmSeconds, warmRate, preVUs, maxVUs, fixture: manifest.fixture,
      apiSourceSha: process.env.CAP_SOURCE_SHA, apiExpectedRevision: process.env.CAP_READ_EXPECTED_REVISION,
      fixtureSourceSha: manifest.sourceSha, fixtureManifestSHA256: manifestSHA256,
      configuredPool: { users: 512, notes: modeNotes(manifest, mode).length, exclusiveRelationships: 512, authors: 1, noteBodyBytes: 256 }, started: new Date().toISOString(),
      nativeScenario: { complete: false, scope: 'native timing not yet verified; no completed scenario claim' },
      thresholds: { actualOfferedRatioMin: .99, mutationFractionMin: .99, unexpectedNoopMax: 0, contentErrorsMax: 0,
        unknownMax: 0, droppedMax: 0, systemAndHTTPErrorRatesMaxExclusive: .01, p95MsMax: 200, p99MsMax: 500 },
      scope: 'main attempts are real PUT state changes; pre-phase hydration is outside native workload. Non-mutation outcomes GET the fact and retire that relation for this phase, because one read can race a late unknown commit. Only a later phase after drain and fresh RR facts can resume it. Recovery GETs/cost are separate; API invalidate/read/Mark remain in PUT latency' }
    let warmBefore = null
    async function runPhase(phase, duration, state) {
      assert.equal(await stopPresent(), false, 'resource stop blocks next native phase')
      const map = new Map(state.facts.map(r => [r.noteId + ':' + r.userId, r]))
      const input = { fixture: manifest.fixture, baseUrl: owned.api, users: runtime.users, multiNoteIds: manifest.multiNoteIds, hotNoteId: manifest.hotNoteId,
        initial: runtime.users.map((user, i) => { const noteId = mode === 'hot' ? manifest.hotNoteId : manifest.multiNoteIds[i]; return { noteId, userId: user.userId, ...(map.get(noteId + ':' + user.userId) || { active: 0, version: 0 }) } }) }
      const privateDir = await mkdtemp(join(tmpdir(), 'cap-like-phase-')), path = join(privateDir, 'input.json')
      await writeFile(path, json(input), { mode: 0o600, flag: 'wx' })
      await save(join(dir, phase + '-private-input.json'), { path, parent: privateDir, credentialValuesExported: false })
      const env = { ...process.env, CAP_LIKE_INPUT: path, CAP_LIKE_MODE: mode, CAP_LIKE_RATE: String(phase === 'warm' ? warmRate : rate), CAP_LIKE_SECONDS: String(duration), CAP_LIKE_PHASE: phase, CAP_LIKE_PRE_VUS: String(preVUs), CAP_LIKE_MAX_VUS: String(maxVUs) }
      try {
        return await command(process.env.K6_BIN || 'k6', ['run', '--summary-export', join(dir, phase + '-summary.json'), 'scripts/load/cap09-like.js'],
        { env, output: join(dir, phase + '-console.txt'), stopFile: process.env.CAP_STOP_FILE, onStart(pid) {
          const detail = json({ phase, helper_pid: process.pid, k6_pid: pid, k6_started_unix_ms: Date.now() })
          return Promise.all([save(join(dir, phase + '-process.json'), JSON.parse(detail)), writeFile(join(dir, 'process.json'), detail)])
        } })
      } finally {
        await unlink(path); await rmdir(privateDir)
        await save(join(dir, phase + '-private-input-removed.json'), { path, removed: true, credentialValuesExported: false })
      }
    }
    try {
      report.scriptSHA256 = Object.fromEntries(await Promise.all(['scripts/cap09-like.mjs', 'scripts/load/cap09-like.js', 'scripts/load/cap09-like-contract.mjs', 'scripts/load/cap09-native-window.mjs'].map(async p => [p, hash(await readFile(p))])))
      warmBefore = await snapshot(manifest, mode, 'before-warm'); await save(join(dir, 'warm-before.json'), warmBefore)
      assert.deepEqual(factErrors(manifest, mode, warmBefore), [], 'starting facts invalid')
      const warm = await runPhase('warm', warmSeconds, warmBefore); report.warmExit = warm.code; report.warmExternalStop = warm.externalStop
      assert.equal(warm.code, 0, 'warm failed; stable phase not started'); assert.equal(warm.externalStop, null)
      const before = await snapshot(manifest, mode, 'before-stable'); await save(join(dir, 'before.json'), before)
      assert.deepEqual(factErrors(manifest, mode, before), [], 'warm facts invalid')
      report.stableStartedAt = Date.now(); const stable = await runPhase('stable', seconds, before)
      Object.assign(report, { stableExit: stable.code, stableExternalStop: stable.externalStop, stableEndedAt: Date.now() })
      report.stableWrapperStartedAt = report.stableStartedAt; report.stableWrapperEndedAt = report.stableEndedAt
      report.stableCommandElapsedMs = report.stableEndedAt - report.stableStartedAt
      const after = await snapshot(manifest, mode, 'after-stable'); await save(join(dir, 'after.json'), after)
      report.verifiedChangedParticipants = { stable: changedParticipants(before, after),
        warmAndStable: changedParticipants(warmBefore, after) }
      // Windows can terminate k6 before it writes a summary. Preserve the real
      // business reconciliation independently; missing native metrics still fail.
      if (stable.code !== 0 || stable.externalStop) { report.ambiguousRequestSettleWaitMs = 5500; await pause(5500) }
      const final = await reconcile(manifest, mode, 60); await save(join(dir, 'reconcile.json'), final)
      report.finalReconcileValid = final.valid
      const native = JSON.parse(await readFile(join(dir, 'stable-summary.json'), 'utf8'))
      report.nativeScenario = nativeScenarioWindow({ metric: native.metrics?.cap_native_scenario_start_unix_ms,
        durationMetric: native.metrics?.cap_native_scenario_duration_ms, seconds,
        wrapperStartedAt: report.stableWrapperStartedAt, wrapperEndedAt: report.stableWrapperEndedAt,
        exit: stable.code, externalStop: stable.externalStop })
      const metric = name => { const m = native.metrics?.[name]; assert.ok(m, 'missing native metric: ' + name); return m }
      const counter = name => { const value = metric(name).count; assert.ok(Number.isSafeInteger(value) && value >= 0, 'invalid native counter: ' + name); return value }
      const counts = { attempted: counter('cap_like_attempted'), completed: counter('cap_like_completed'), successful: counter('cap_like_success'), mutation: counter('cap_like_mutation'), noop: counter('cap_like_noop'), rejected: counter('cap_like_rejected'), effectUnknown: counter('cap_like_effect_unknown'), contentErrors: counter('cap_like_content_errors'), systemErrors: counter('cap_like_system_errors'), httpErrors: counter('cap_like_http_errors'), recoveryReads: counter('cap_like_recovery_reads'), recoveryFailed: counter('cap_like_recovery_failed'), retiredRelations: counter('cap_like_retired_relations'), dropped: counter('dropped_iterations'), nativeWriteHTTP: counter('http_reqs{name:like-write}') }
      assert.ok(counts.attempted >= counts.nativeWriteHTTP && counts.nativeWriteHTTP >= counts.completed && counts.completed >= counts.successful && counts.successful >= counts.mutation)
      const allLatency = metric('cap_like_all_ms'), mutationLatency = native.metrics?.cap_like_mutation_ms || null
      for (const m of [allLatency, mutationLatency]) assert.ok(m && ['p(95)', 'p(99)', 'max'].every(k => Number.isFinite(m[k]) && m[k] >= 0), 'native latency missing')
      const { classificationUnknown, unknownLowerBound, unknownUpperBound, unknownIsExact, unknown } = unknownBounds(counts.attempted, counts.completed, counts.effectUnknown)
      const dbMutations = after.transitions - before.transitions, eventsAdded = after.eventCount - before.eventCount
      report.result = { ...counts, classificationUnknown, unknown, unknownIsExact, unknownLowerBound, unknownUpperBound, targetOffers: rate * seconds, actualOfferedRatio: counts.attempted / (rate * seconds), mutationFraction: counts.mutation / Math.max(1, counts.attempted), mutationPerNominalSecond: counts.mutation / seconds, httpPutPerNominalSecond: counts.attempted / seconds, allLatency, mutationLatency, recoveryLatency: native.metrics?.cap_like_recovery_ms || null,
        dbMutations, eventsAdded, mutationCounterMatchesDB: dbMutations === counts.mutation && eventsAdded === counts.mutation,
        unknownScope: 'classificationUnknown and effectUnknown can overlap during abort sample loss. Distinct unknown is exact only when lower/upper coincide; otherwise unknown=null and bounds remain. Recovery never erases original uncertainty. Zero-unknown requires both counters zero.' }
      if (unknownUpperBound > 0 && !report.ambiguousRequestSettleWaitMs) {
        report.ambiguousRequestSettleWaitMs = 5500; await pause(5500)
        const settled = await reconcile(manifest, mode, 60); await save(join(dir, 'uncertain-reconcile.json'), settled)
        report.finalReconcileValid = settled.valid
      }
      report.meetsAPIBudget = report.nativeScenario.complete && stable.code === 0 && !stable.externalStop && counts.attempted > 0 && counts.attempted === counts.nativeWriteHTTP && counts.attempted === counts.completed && counts.attempted / (rate * seconds) >= .99 && counts.mutation / counts.attempted >= .99 && counts.noop === 0 && counts.rejected === 0 && classificationUnknown === 0 && counts.effectUnknown === 0 && counts.dropped === 0 && counts.contentErrors === 0 && counts.systemErrors / counts.attempted < .01 && counts.httpErrors / counts.attempted < .01 && allLatency['p(95)'] <= 200 && allLatency['p(99)'] <= 500 && mutationLatency['p(95)'] <= 200 && mutationLatency['p(99)'] <= 500 && report.result.mutationCounterMatchesDB
      report.meetsBudget = report.meetsAPIBudget && report.finalReconcileValid
      report.downstreamSteadyStateScope = 'final valid means convergence only; common stage lag/last-minute growth gate must additionally pass for a full-chain capacity claim'
    } catch (error) { report.failure = error.message; if (error.observerSQLFailure) report.observerSQLFailure = error.observerSQLFailure; report.meetsBudget = false; report.meetsAPIBudget ??= false }
    finally { report.finished = new Date().toISOString(); await save(join(dir, 'result.json'), report) }
    console.log(JSON.stringify({ task: 'CAP-09', mode, rate, seconds, meetsAPIBudget: report.meetsAPIBudget, meetsBudget: report.meetsBudget, result: report.result, failure: report.failure || null }))
    process.exitCode = report.meetsBudget ? 0 : 1
  }
}
