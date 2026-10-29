// GO-073 isolated event measurement. Never point this at shared infrastructure.
import assert from 'node:assert/strict'
import { randomInt, randomBytes } from 'node:crypto'
import { spawnSync } from 'node:child_process'
import { writeFileSync, existsSync } from 'node:fs'

const expected = { MYSQL_HOST: '127.0.0.1', MYSQL_PORT: '3431', MYSQL_DATABASE: 'project_full_stack_go', REDIS_HOST: '127.0.0.1', REDIS_PORT: '6531', API_BASE: 'http://127.0.0.1:8231', ES_URL: 'http://127.0.0.1:9231', ES_INDEX: 'go073-event-notes', KAFKA_TOPIC: 'community.events', KAFKA_CONTAINER: 'project-full-stack-go073-independent-events-kafka-1', ES_CONTAINER: 'project-full-stack-go073-independent-events-elasticsearch-1' }
for (const [key, value] of Object.entries(expected)) assert.equal(process.env[key], value, `${key} must name the isolated GO-073 environment`)
assert.ok(process.env.MYSQL_USER && process.env.MYSQL_PASSWORD && process.env.GO_SEARCH_BINARY && process.env.EVENT_OUTPUT)
assert.ok(!existsSync(process.env.EVENT_OUTPUT), 'preserve prior output; choose a new file')
const duration = Number(process.env.EVENT_DURATION_SECONDS || 180)
assert.ok(Number.isInteger(duration) && duration >= 120 && duration <= 600)
const apiBase = expected.API_BASE, es = expected.ES_URL, index = expected.ES_INDEX
const phone = `139${randomInt(10000000, 100000000)}`
const prefix = 'go073e' + randomBytes(6).toString('hex')
const data = { task: 'GO-073 events', durationSeconds: duration, prefix, startedAt: new Date().toISOString(), notes: [], failures: [], snapshots: [], visibility: [], timings: {}, success: false }
let token = '', restore = false
const pause = ms => new Promise(r => setTimeout(r, ms))
function docker(args, timeout = 45000) {
  const r = spawnSync('docker', args, { encoding: 'utf8', timeout })
  if (r.status !== 0 || r.error) throw new Error(`docker ${args[0]} failed: ${r.stderr?.slice(0, 300) || r.error?.message}`)
  return r.stdout.trim()
}
function verifyContainer(name, service) {
  const labels = JSON.parse(docker(['inspect', '--format', '{{json .Config.Labels}}', name]))
  assert.equal(labels['com.docker.compose.project'], 'project-full-stack-go073-independent-events')
  assert.equal(labels['com.docker.compose.service'], service)
}
function sql(q) {
  const r = spawnSync(process.env.MYSQL_BIN || 'mysql', ['--protocol=TCP', '--host=127.0.0.1', `--port=${expected.MYSQL_PORT}`, `--user=${process.env.MYSQL_USER}`, '--batch', '--skip-column-names', '--default-character-set=utf8mb4', expected.MYSQL_DATABASE], { input: `SET time_zone='+08:00'; ${q}`, encoding: 'utf8', env: { ...process.env, MYSQL_PWD: process.env.MYSQL_PASSWORD }, timeout: 15000 })
  if (r.error || r.status !== 0) throw new Error(`SQL failed: ${r.stderr?.slice(0, 300) || r.error?.message}`)
  return r.stdout.trim()
}
async function request(path, options = {}) {
  const response = await fetch(apiBase + path, { method: options.method || 'GET', headers: { 'Content-Type': 'application/json', ...(options.auth ? { authorization: token } : {}) }, ...(options.body ? { body: JSON.stringify(options.body) } : {}), signal: AbortSignal.timeout(15000) })
  const body = await response.json()
  if (!response.ok || !body.success) throw new Error(`API ${path.split('?')[0]} HTTP ${response.status} success=${body.success}`)
  return body.data
}
async function esCall(path, options = {}) {
  const response = await fetch(es + path, { ...options, headers: { 'Content-Type': 'application/json' }, signal: AbortSignal.timeout(10000) })
  if (!response.ok) throw new Error(`ES ${path} HTTP ${response.status}`)
  return response.json()
}
async function until(fn, timeout = 120000) {
  const end = Date.now() + timeout
  while (Date.now() < end) { if (await fn()) return; await pause(500) }
  throw new Error('timed out waiting for convergence')
}
function idList(notes = data.notes, quoted = false) {
  return notes.length ? notes.map(n => { assert.match(n.id, /^[1-9]\d*$/); return quoted ? `'${n.id}'` : n.id }).join(',') : quoted ? "'0'" : '0'
}
function snapshot(label) {
  const ids = idList(), strings = idList(data.notes, true), query = `SELECT 'facts',COUNT(*) FROM tb_blog WHERE id IN(${ids}); SELECT 'outbox',delivery_status,COUNT(*) FROM tb_community_outbox WHERE aggregate_id IN(${strings}) GROUP BY delivery_status; SELECT 'receipt',projection,state,COUNT(*) FROM tb_community_projection_event WHERE event_id IN(SELECT event_id FROM tb_community_outbox WHERE aggregate_id IN(${strings})) GROUP BY projection,state; SELECT 'failure',state,COUNT(*) FROM tb_community_consumer_failure WHERE projection='search-v1' AND CAST(message_key AS CHAR) IN(${strings}) GROUP BY state; SELECT 'like',COALESCE(SUM(liked),0),(SELECT COUNT(*) FROM tb_blog_like WHERE active=1 AND blog_id IN(${ids})) FROM tb_blog WHERE id IN(${ids});`
  const rawSQL = sql(query), offsets = {}
  for (const group of ['community.follow-v1', 'community.feed-v1', 'community.search-v1']) offsets[group] = docker(['exec', expected.KAFKA_CONTAINER, '/opt/kafka/bin/kafka-consumer-groups.sh', '--bootstrap-server', 'localhost:9094', '--group', group, '--describe'], 25000)
  const parsedOffsets = Object.fromEntries(Object.entries(offsets).map(([group, raw]) => {
    const line = raw.split(/\r?\n/).map(row => row.trim().split(/\s+/)).find(row => row[0] === group && row[1] === expected.KAFKA_TOPIC)
    assert.ok(line && line.slice(2, 6).every(x => /^\d+$/.test(x)), `${group} must have actual numeric offset and lag`)
    return [group, { partition: Number(line[2]), committed: Number(line[3]), end: Number(line[4]), lag: Number(line[5]) }]
  }))
  const result = { label, at: new Date().toISOString(), rawSQL, offsets, parsedOffsets }; data.snapshots.push(result); return result
}
async function offsetBarrier(label) {
  let value
  await until(async () => {
    try { value = snapshot(label); return Object.values(value.parsedOffsets).every(x => x.lag === 0 && x.committed === x.end) } catch { return false }
  })
  return value
}
async function publish(phase, ordinal) {
  const started = Date.now()
  const note = await request('/notes', { method: 'POST', auth: true, body: { shopId: data.shopId, title: `${prefix} synthetic ${phase} ${ordinal}`, content: `${prefix} synthetic event measurement`, images: [] } })
  assert.match(note.id, /^[1-9]\d*$/)
  const entry = { id: note.id, phase, started, completed: Date.now() }; data.notes.push(entry)
  try { await request(`/notes/${note.id}/like`, { method: 'PUT', auth: true, body: { liked: true } }); entry.like = true } catch (e) { entry.like = false; data.failures.push({ phase, operation: 'like', message: e.message }) }
  return entry
}
async function actualVisible(notes) {
  if (!notes.length) return []
  const r = await esCall(`/${index}/_search`, { method: 'POST', body: JSON.stringify({ size: notes.length, query: { ids: { values: notes.map(n => n.id) } }, _source: false, version: true }) })
  return r.hits.hits.map(h => ({ id: h._id, version: h._version }))
}
async function observeUnseen(seen, phase) {
  const hits = await actualVisible(data.notes.filter(n => !seen.has(n.id)))
  const observedAt = Date.now()
  for (const h of hits) {
    seen.add(h.id); const n = data.notes.find(x => x.id === h.id)
    data.visibility.push({ id: h.id, phase: n.phase, observationPhase: phase, version: h.version, observedAt, fromRequestStartMs: observedAt - n.started })
  }
}
async function waitHealthy() { await until(async () => { try { return ['green', 'yellow'].includes((await esCall('/_cluster/health')).status) } catch { return false } }) }
try {
  verifyContainer(expected.ES_CONTAINER, 'elasticsearch'); verifyContainer(expected.KAFKA_CONTAINER, 'kafka')
  const alias = await esCall('/_alias/' + index); data.generation = Object.keys(alias); assert.equal(data.generation.length, 1)
  data.shopId = sql('SELECT id FROM tb_shop ORDER BY id LIMIT 1'); assert.match(data.shopId, /^[1-9]\d*$/)
  const code = await request(`/user/code?phone=${phone}`, { method: 'POST' }); token = await request('/user/login', { method: 'POST', body: { phone, code: code.code } })
  assert.ok(token); data.userId = sql(`SELECT id FROM tb_user WHERE phone='${phone}'`); assert.match(data.userId, /^[1-9]\d*$/)
  data.timings.warmupStart = new Date().toISOString()
  for (let i = 0; i < 10; i++) { await publish('warmup', i); await pause(1000) }
  await until(async () => (await actualVisible(data.notes)).length === data.notes.length)
  data.timings.warmupEnd = new Date().toISOString()
  // One event pair per second in the stable window. Schedule against the start
  // time, preserving missed slots instead of pretending the rate was achieved.
  data.timings.stableStart = new Date().toISOString(); const start = Date.now(), seen = new Set()
  for (let i = 0; i < duration; i++) {
    const target = start + i * 1000; if (Date.now() < target) await pause(target - Date.now())
    try { await publish('stable', i) } catch (e) { data.failures.push({ phase: 'stable', ordinal: i, operation: 'publish', message: e.message }) }
    if (i % 5 === 4) {
      try { await observeUnseen(seen, 'stable') } catch (e) { data.failures.push({ phase: 'stable', operation: 'visibility', message: e.message }) }
    }
  }
  data.timings.stableEnd = new Date().toISOString(); data.timings.stableElapsedMs = Date.now() - start
  await until(async () => Number(sql(`SELECT COUNT(*) FROM tb_community_outbox WHERE aggregate_id IN(${idList(data.notes, true)}) AND delivery_status<>'DONE'`)) === 0)
  await until(async () => { await observeUnseen(seen, 'tail'); return seen.size === data.notes.length })
  await until(async () => Number(sql(`SELECT COUNT(*) FROM tb_community_projection_event WHERE event_id IN(SELECT event_id FROM tb_community_outbox WHERE aggregate_id IN(${idList(data.notes, true)}))`)) === data.notes.length * 6)
  await offsetBarrier('before-es-stop')
  data.timings.stopRequested = new Date().toISOString(); restore = true; docker(['stop', '--time', '30', expected.ES_CONTAINER]); data.timings.stopped = new Date().toISOString()
  const outage = []; for (let i = 0; i < 10; i++) outage.push(await publish('outage', i))
  await until(async () => Number(sql(`SELECT COUNT(*) FROM tb_community_consumer_failure WHERE projection='search-v1' AND state='PARKED' AND CAST(message_key AS CHAR) IN(${idList(outage, true)})`)) === outage.length)
  await offsetBarrier('es-down-parked')
  data.timings.startRequested = new Date().toISOString(); docker(['start', expected.ES_CONTAINER]); await waitHealthy(); data.timings.esHealthy = new Date().toISOString()
  const generation = Object.keys(await esCall('/_alias/' + index)); assert.deepEqual(generation, data.generation)
  const coordinates = sql(`SELECT topic,partition_id,source_offset FROM tb_community_consumer_failure WHERE projection='search-v1' AND state='PARKED' AND CAST(message_key AS CHAR) IN(${idList(outage, true)}) ORDER BY partition_id,source_offset`).split(/\r?\n/).map(x => x.split(/\s+/))
  assert.equal(coordinates.length, outage.length); data.replays = []
  for (const [topic, partition, offset] of coordinates) {
    assert.equal(topic, 'community.events'); assert.match(partition, /^\d+$/); assert.match(offset, /^\d+$/)
    const r = spawnSync(process.env.GO_SEARCH_BINARY, ['replay', topic, partition, offset], { env: process.env, encoding: 'utf8', timeout: 40000 })
    if (r.status !== 0 || r.error) throw new Error(`replay failed ${partition}/${offset}`)
    data.replays.push({ topic, partition, offset, output: r.stdout.trim() })
  }
  await esCall(`/${index}/_refresh`, { method: 'POST' }); await until(async () => (await actualVisible(data.notes)).length === data.notes.length)
  data.timings.converged = new Date().toISOString(); data.visibleFinal = await actualVisible(data.notes)
  assert.ok(data.visibleFinal.every(hit => hit.version === 1)); assert.equal(new Set(data.visibleFinal.map(hit => hit.id)).size, data.notes.length)
  await until(async () => Number(sql(`SELECT COUNT(*) FROM tb_community_projection_event WHERE event_id IN(SELECT event_id FROM tb_community_outbox WHERE aggregate_id IN(${idList(data.notes, true)}))`)) === data.notes.length * 6)
  await offsetBarrier('recovered')
  const mismatches = Number(sql(`SELECT COUNT(*) FROM tb_blog b WHERE b.id IN(${idList()}) AND (b.liked<>(SELECT COUNT(*) FROM tb_blog_like l WHERE l.blog_id=b.id AND l.active=1) OR b.liked<>1 OR b.publish_version<>1)`))
  const resolved = Number(sql(`SELECT COUNT(*) FROM tb_community_consumer_failure WHERE projection='search-v1' AND state='RESOLVED' AND CAST(message_key AS CHAR) IN(${idList(outage, true)})`))
  const parked = Number(sql(`SELECT COUNT(*) FROM tb_community_consumer_failure WHERE projection='search-v1' AND state='PARKED' AND CAST(message_key AS CHAR) IN(${idList(data.notes, true)})`))
  const stateVersions = Number(sql(`SELECT COUNT(*) FROM tb_note_search_state WHERE note_id IN(${idList()}) AND version=1`))
  const wrongOutbox = Number(sql(`SELECT COUNT(*) FROM tb_blog b WHERE b.id IN(${idList()}) AND ((SELECT COUNT(*) FROM tb_community_outbox o WHERE o.aggregate_id=CAST(b.id AS CHAR) AND o.event_type='NOTE_PUBLISHED' AND o.aggregate_version=1 AND o.delivery_status='DONE')<>1 OR (SELECT COUNT(*) FROM tb_community_outbox o WHERE o.aggregate_id=CAST(b.id AS CHAR) AND o.event_type='LIKE_CHANGED' AND o.aggregate_version=1 AND o.delivery_status='DONE')<>1)`))
  const wrongReceipt = Number(sql(`SELECT COUNT(*) FROM tb_community_outbox o CROSS JOIN (SELECT 'follow-v1' AS projection UNION ALL SELECT 'feed-v1' UNION ALL SELECT 'search-v1') g LEFT JOIN tb_community_projection_event p ON p.event_id=o.event_id AND p.projection=g.projection WHERE o.aggregate_id IN(${idList(data.notes, true)}) AND (p.event_id IS NULL OR p.state<>CASE WHEN o.event_type='NOTE_PUBLISHED' AND g.projection IN('feed-v1','search-v1') THEN 'APPLIED' ELSE 'IGNORED' END)`))
  data.reconciliation = { facts: data.notes.length, likeOrVersionMismatches: mismatches, wrongOutbox, wrongReceipt, stateVersions, parked, resolved }
  assert.equal(mismatches, 0); assert.equal(wrongOutbox, 0); assert.equal(wrongReceipt, 0); assert.equal(stateVersions, data.notes.length); assert.equal(parked, 0); assert.equal(resolved, outage.length)
  restore = false
  data.projectionLatencyRaw = sql(`SELECT o.event_id,o.event_type,o.delivery_status,TIMESTAMPDIFF(MICROSECOND,o.create_time,o.completed_at),p.projection,p.state,TIMESTAMPDIFF(MICROSECOND,o.create_time,p.applied_at) FROM tb_community_outbox o LEFT JOIN tb_community_projection_event p ON p.event_id=o.event_id WHERE o.aggregate_id IN(${idList(data.notes, true)}) ORDER BY o.id,p.projection`)
  data.success = data.failures.length === 0
} catch (error) { data.failures.push({ phase: 'fatal', message: error.message }); process.exitCode = 1 }
finally {
  if (restore) { try { docker(['start', expected.ES_CONTAINER]); await waitHealthy(); data.finallyRestored = true } catch (error) { data.finallyRestored = false; data.failures.push({ phase: 'restore', message: error.message }) } }
  if (token) { try { await request('/user/logout', { method: 'POST', auth: true }); data.logout = true } catch { data.logout = false } }
  data.endedAt = new Date().toISOString()
  writeFileSync(process.env.EVENT_OUTPUT, JSON.stringify(data, null, 2) + '\n', { flag: 'wx' })
  process.stdout.write(JSON.stringify({ success: data.success, notes: data.notes.length, failures: data.failures.length, output: process.env.EVENT_OUTPUT }) + '\n')
  if (!data.success) process.exitCode = 1
}
