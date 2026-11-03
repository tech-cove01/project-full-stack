// Pure generation/consumer checks. No SQL, Go, API, broker, cloud or load.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import { AUDIT_CONTRACT, SOURCE_VALID_SQL, EXPECTED_HASH_SQL, auditSQL,
  auditContractMatches, auditErrors, baselineAuditReference, baselineAuditCount } from './load/cap10-full-audit.mjs'

const manifest = { userIds: ['1', '2'], multiNoteIds: ['101', '102'] }
const fixtureSHA = 'f'.repeat(64)
function state() {
  return { auditContract: { ...AUDIT_CONTRACT }, fixtureManifestSHA256: fixtureSHA,
    observerPolicy: { mode: 'like-vector-v1', fullAuditTimeoutMs: 120000, scope: 'full-snapshot-audit-only' },
    controlRows: { projections: [{ projection: 'follow-v1', rebuiltAt: null }, { projection: 'feed-v1', rebuiltAt: '2026-11-03 12:30:01.123000' }],
      search: { name: 'search-v1', indexAlias: '', activeGeneration: '', rebuiltAt: null } },
    sourceWatermark: { maxOutboxId: '2', diagnosticOnly: true },
    facts: manifest.multiNoteIds.map((noteId, i) => ({ noteId, userId: manifest.userIds[i], version: 1, active: 1 })),
    notes: manifest.multiNoteIds.map(noteId => ({ noteId, liked: 1, activeRelations: 1 })),
    events: manifest.multiNoteIds.map((noteId, i) => ({ noteId, userId: manifest.userIds[i], count: 1, distinctVersions: 1, minVersion: 1, maxVersion: 1, invalid: 0 })),
    delivery: [{ status: 'DONE', count: 2 }],
    receipts: ['follow-v1', 'feed-v1', 'search-v1'].map(projection => ({ projection, state: 'IGNORED', count: 2, invalidHash: 0, invalidProjection: 0, invalidState: 0 })),
    parked: [], transitions: 2, eventCount: 2 }
}
let checks = 0
async function check(name, run) { await run(); checks++; console.log('PASS ' + name) }

await check('same attributed cohort scope drives events delivery and receipts inside one read-only RR', () => {
  const sql = auditSQL(manifest.multiNoteIds)
  assert.equal((sql.match(/o\.aggregate_id IN \('101','102'\) AND/g) || []).length, 3)
  assert.equal((sql.match(/o\.aggregate_type='BLOG' OR o\.event_type='LIKE_CHANGED' OR o\.event_id LIKE 'LIKE:%'/g) || []).length, 3)
  assert.match(sql, /START TRANSACTION WITH CONSISTENT SNAPSHOT, READ ONLY;/)
  assert.equal((sql.match(/COMMIT;/g) || []).length, 1)
  assert.equal(/SELECT o\.\*/.test(sql), false)
  assert.match(sql, /hash_values AS \(SELECT o\.event_id,CASE WHEN/)
  assert.match(sql, /LIMIT 18446744073709551615\),/)
  assert.match(sql, /SELECT DISTINCT projection,HEX\(projection\)/)
  assert.match(sql, /r\.projection=p\.projection AND BINARY r\.projection=BINARY p\.projection/)
  assert.match(sql, /GROUP BY r\.projection,r\.state,HEX\(r\.projection\),HEX\(r\.state\)/)
  assert.match(SOURCE_VALID_SQL, /JSON_TYPE\(o\.payload\)='OBJECT' AND JSON_LENGTH\(o\.payload\)=4/)
  assert.match(EXPECTED_HASH_SQL, /THEN UNHEX\(SHA2\(CONCAT\(/)
  assert.match(EXPECTED_HASH_SQL, /ELSE NULL END$/)
})
await check('only canonical unique bounded SQL note IDs are interpolated', () => {
  assert.ok(auditSQL(['18446744073709551615']).includes("'18446744073709551615'"))
  for (const ids of [[], ['0'], ['01'], ['1\n'], ['１'], [1], ["1'); DROP TABLE x; --"], ['18446744073709551616'], ['1', '1']]) assert.throws(() => auditSQL(ids))
})
await check('complete valid raw audit has no semantic error and does not mutate its input', () => {
  const value = state(), before = structuredClone(value)
  assert.deepEqual(auditErrors(manifest, value, fixtureSHA), [])
  assert.deepEqual(value, before)
  assert.equal(auditContractMatches({ ...AUDIT_CONTRACT }), true)
  for (const marker of [null, [], {}, { ...AUDIT_CONTRACT, extra: 1 }, { ...AUDIT_CONTRACT, hashContract: 'other' }]) assert.equal(Boolean(auditContractMatches(marker)), false)
})
await check('unknown projection raw aliases state and hash flags cannot hide behind correct counts', () => {
  const changes = [v => { v.receipts[0].invalidHash = 1 },
    v => { v.receipts.push({ ...v.receipts[0], projection: 'unknown-v1' }) },
    v => { v.receipts[0].projection += ' ' }, v => { v.receipts[0].projection = 'FOLLOW-V1' },
    v => { v.receipts[0].state = 'APPLIED' }, v => { v.receipts[0].state += ' ' },
    v => { v.receipts.push({ ...v.receipts[0] }) }]
  for (const change of changes) { const value = state(); change(value); assert.ok(auditErrors(manifest, value, fixtureSHA).length) }
  for (const invalid of [null, true, -1, NaN, 3, Number.MAX_SAFE_INTEGER + 1]) {
    const value = state(); value.receipts[0].invalidHash = invalid
    assert.ok(auditErrors(manifest, value, fixtureSHA).length)
  }
})
await check('source invalidity and missing or extra version-zero pairs are semantic failures', () => {
  for (const change of [v => { v.events[0].invalid = 1 }, v => { v.events[0].userId = '9' },
    v => { v.facts.pop() }, v => { v.facts[0].userId = '9' },
    v => { v.notes[0].liked = true }, v => { v.events[0].count = null },
    v => { v.delivery[0].status = 'DONE ' }, v => { v.delivery[0].count = 1 },
    v => { v.parked.push({ projection: 'follow-v1', state: 'PARKED', count: 1 }) }]) {
    const value = state(); change(value); assert.ok(auditErrors(manifest, value, fixtureSHA).length)
  }
  const empty = state(); empty.facts = []; empty.events = []; empty.delivery = []; empty.receipts = []
  empty.transitions = 0; empty.eventCount = 0
  assert.ok(auditErrors(manifest, empty, fixtureSHA).length, 'never-present v0 rows must not disappear from a READY cohort')
})
await check('original fixture hash nullable control strings and diagnostic watermark are exact', () => {
  for (const change of [v => { v.fixtureManifestSHA256 = 'e'.repeat(64) },
    v => { v.controlRows.projections[1].rebuiltAt = '2026-02-30 12:30:01.123000' },
    v => { v.controlRows.projections[1].rebuiltAt += '\n' },
    v => { v.controlRows.projections[1] = { ...v.controlRows.projections[0] } },
    v => { v.controlRows.search.activeGeneration = null },
    v => { v.sourceWatermark.maxOutboxId = 2 }, v => { v.sourceWatermark.maxOutboxId = '02' },
    v => { v.sourceWatermark.diagnosticOnly = false }]) {
    const value = state(); change(value); assert.ok(auditErrors(manifest, value, fixtureSHA).length)
  }
})
await check('actual consumer failure enum accepts RESOLVED history while PARKED and guessed replay aliases fail', () => {
  const source = readFileSync('infra/mysql/migrations/010-community-consumer-recovery.sql', 'utf8')
  assert.match(source, /CHECK\s*\(state IN\s*\('PARKED',\s*'RESOLVED'\)\)/)
  const resolved = state(); resolved.parked = [{ projection: 'search-v1', state: 'RESOLVED', count: 1 }]
  assert.deepEqual(auditErrors(manifest, resolved, fixtureSHA), [])
  for (const status of ['PARKED', 'REPLAYED', 'resolved', 'RESOLVED ']) {
    const value = structuredClone(resolved); value.parked[0].state = status
    assert.ok(auditErrors(manifest, value, fixtureSHA).length)
  }
})

const helper = readFileSync('scripts/cap10-like.mjs', 'utf8')
const facts = helper.match(/^function factErrors[\s\S]*?^}/m)[0]
const reconcile = helper.match(/^async function reconcile[\s\S]*?^}/m)[0]
function context(value, mode = 'like-vector-v1') {
  const target = { observationPolicy: { mode }, auditErrors, modeNotes: m => m.multiNoteIds, fixtureManifestHashes: new WeakMap([[manifest, fixtureSHA]]),
    snapshot: async () => structuredClone(value), pause: async () => {}, derived: async () => ({ errors: [] }) }
  vm.runInNewContext(facts + '\n' + reconcile + '\nglobalThis.factErrors = factErrors; globalThis.reconcile = reconcile', target)
  return target
}
await check('actual common fact validator and reconcile consume wrong-hash and extra-projection flags', async () => {
  for (const change of [v => { v.receipts[0].invalidHash = 1 }, v => { v.receipts.push({ ...v.receipts[0], projection: 'extra-v1' }) },
    v => { delete v.auditContract }]) {
    const value = state(); change(value); const actual = context(value)
    assert.ok(actual.factErrors(manifest, 'multi', value).length)
    const result = await actual.reconcile(manifest, 'multi', 0)
    assert.equal(result.valid, false); assert.equal(result.factsValid, false)
  }
})
await check('missing valid receipts remain recovery evidence and legacy fact validation keeps its original behavior', async () => {
  const value = state(); value.receipts.pop()
  assert.deepEqual(auditErrors(manifest, value, fixtureSHA), [])
  const result = await context(value).reconcile(manifest, 'multi', 0)
  assert.equal(result.valid, false); assert.equal(result.factsValid, true); assert.equal(result.downstreamConverged, false)
  value.receipts[0].invalidHash = 1
  assert.equal(context(value, 'full-history-v1').factErrors(manifest, 'multi', value).length, 0)
})
await check('new-mode setup subtracts bound complete audit history rather than a vector delta or unverified alias', () => {
  const ready = { baselineAudit: { path: 'S20-preparation/current-reconcile/reconcile.json', sha256: 'a'.repeat(64) },
    baselineAuditEventCount: 2, baselineCountSource: 'bound-node-full-reconciliation', baseline: { sql: { counts: { events: 0 } } } }
  const proof = { valid: true, factsValid: true, downstreamConverged: true, errors: [], facts: state(), derived: { available: true, errors: [] } }
  assert.equal(baselineAuditCount(ready, proof, 'a'.repeat(64), manifest, fixtureSHA), 2)
  for (const change of [v => { v.baselineAudit.path = '../reconcile.json' }, v => { v.baselineAudit.path = '/absolute.json' },
    v => { v.baselineAudit.path = 'S20-preparation/audit.json' },
    v => { v.baselineAudit.path = 'a\n/reconcile.json' }, v => { v.baselineAuditEventCount = 0 },
    v => { v.baselineCountSource = 'old-vector-delta' }]) {
    const value = structuredClone(ready); change(value)
    assert.throws(() => baselineAuditCount(value, proof, 'a'.repeat(64), manifest, fixtureSHA))
  }
  assert.throws(() => baselineAuditCount(ready, proof, 'b'.repeat(64), manifest, fixtureSHA))
  const incomplete = structuredClone(proof); incomplete.facts.receipts.pop()
  assert.throws(() => baselineAuditCount(ready, incomplete, 'a'.repeat(64), manifest, fixtureSHA))
  const initial = structuredClone(proof)
  initial.facts.facts.forEach(row => { row.version = 0; row.active = 0 })
  initial.facts.notes.forEach(row => { row.liked = 0; row.activeRelations = 0 })
  initial.facts.events = []; initial.facts.delivery = []; initial.facts.receipts = []
  initial.facts.transitions = 0; initial.facts.eventCount = 0
  assert.equal(baselineAuditCount({ ...ready, baselineAuditEventCount: 0 }, initial, 'a'.repeat(64), manifest, fixtureSHA), 0)
  assert.deepEqual(baselineAuditReference(ready), ready.baselineAudit)
})
console.log(JSON.stringify({ checks, failed: 0, sqlExecuted: false, goExecuted: false,
  budgetVerified: false, newOracleVariantsPending: true, loadRuns: 0 }))
