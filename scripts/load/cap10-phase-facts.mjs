import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { controlRowsValid } from './cap10-full-audit.mjs'

export const PHASE_FACTS = 'like-vector-phase-facts-v1'
export const DIGEST = 'like-vector-digest-v1'
const groups = ['follow-v1', 'feed-v1', 'search-v1']
const sha = raw => createHash('sha256').update(raw).digest('hex')
const exact = (value, names) => value && typeof value === 'object' && !Array.isArray(value)
  && Object.keys(value).sort().join(',') === [...names].sort().join(',')
const integer = value => Number.isSafeInteger(value) && value >= 0
const milliseconds = value => typeof value === 'number' && Number.isFinite(value) && value >= 0 && value <= Number.MAX_SAFE_INTEGER
function millisecondsNS(value) {
  assert.ok(milliseconds(value))
  const match = /^([0-9]+)(?:\.([0-9]{1,6}))?$/.exec(String(value)); assert.ok(match, 'bounded native milliseconds precision required')
  return BigInt(match[1]) * 1000000n + BigInt((match[2] || '').padEnd(6, '0'))
}
function timestampNS(value) {
  if (typeof value !== 'string') return null
  const match = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.([0-9]{1,9}))?Z$/.exec(value)
  if (!match) return null
  const seconds = Date.parse(match[1] + 'Z')
  if (!Number.isSafeInteger(seconds) || new Date(seconds).toISOString().slice(0, 19) !== match[1]) return null
  return BigInt(seconds) * 1000000n + BigInt((match[2] || '').padEnd(9, '0'))
}
const id = value => typeof value === 'string' && /^[1-9][0-9]{0,19}$/.test(value) && BigInt(value) <= 18446744073709551615n
export const goJSON = value => JSON.stringify(value, (_key, item) => {
  if (typeof item === 'string') assert.ok(item.isWellFormed(), 'unpaired surrogate cannot share a Go UTF8 identity')
  return item
}).replace(/[<>&\u2028\u2029]/g, char => ({ '<': '\\u003c', '>': '\\u003e', '&': '\\u0026', '\u2028': '\\u2028', '\u2029': '\\u2029' })[char])

export function vectorIdentity(manifest, facts) {
  assert.ok(Array.isArray(manifest.multiNoteIds) && manifest.multiNoteIds.length === manifest.userIds.length)
  assert.ok(Array.isArray(facts) && facts.length === manifest.multiNoteIds.length)
  const byNote = new Map()
  for (const row of facts) { assert.ok(!byNote.has(row.noteId)); byNote.set(row.noteId, row) }
  const positions = manifest.multiNoteIds.map((noteId, index) => {
    const row = byNote.get(noteId), userId = manifest.userIds[index]
    assert.ok(id(noteId) && id(userId) && row?.userId === userId && integer(row.version) && row.version <= 2147483647
      && Number.isInteger(row.active) && row.active === row.version % 2, 'complete manifest-order vector required')
    return { noteId, userId, present: true, version: row.version, active: row.active }
  })
  const canonical = goJSON(positions)
  return { canonical, sha256: sha(canonical) }
}

export function controlsIdentity(controls) {
  // Actual phase validation below uses the exported original strict validator.
  // This serializer also exposes original-Go Unicode/HTML golden behavior.
  assert.ok(exact(controls, ['projections', 'search']) && Array.isArray(controls.projections) && controls.projections.length === 2)
  const nullable = value => { assert.ok(value === null || typeof value === 'string'); return { String: value === null ? '' : value, Valid: value !== null } }
  const canonical = ['follow-v1', 'feed-v1'].map(projection => {
    const rows = controls.projections.filter(row => row.projection === projection); assert.equal(rows.length, 1)
    return goJSON({ Projection: projection, RebuiltAt: nullable(rows[0].rebuiltAt) })
  })
  const search = controls.search
  assert.ok(search?.name === 'search-v1' && typeof search.indexAlias === 'string' && typeof search.activeGeneration === 'string')
  canonical.push(goJSON({ Name: search.name, IndexAlias: search.indexAlias, ActiveGeneration: search.activeGeneration, RebuiltAt: nullable(search.rebuiltAt) }))
  const inner = canonical.map(sha), outer = goJSON(inner)
  return { canonical, inner, outer, sha256: sha(outer) }
}

export function phaseSQL(manifest) {
  assert.ok(manifest.multiNoteIds.length >= 1 && manifest.multiNoteIds.length <= 4096 && manifest.multiNoteIds.every(id)
    && new Set(manifest.multiNoteIds).size === manifest.multiNoteIds.length)
  const ids = manifest.multiNoteIds.map(value => `'${value}'`).join(',')
  return `SET TRANSACTION ISOLATION LEVEL REPEATABLE READ; START TRANSACTION WITH CONSISTENT SNAPSHOT, READ ONLY;
SELECT JSON_OBJECT('_kind','fact','noteId',CAST(l.blog_id AS CHAR),'userId',CAST(l.user_id AS CHAR),'active',l.active,'version',l.version)
 FROM tb_blog_like l WHERE l.blog_id IN (${ids}) ORDER BY l.blog_id,l.user_id;
SELECT JSON_OBJECT('_kind','note','noteId',CAST(b.id AS CHAR),'liked',b.liked,
 'activeRelations',(SELECT COUNT(*) FROM tb_blog_like l WHERE l.blog_id=b.id AND l.active=1)) FROM tb_blog b WHERE b.id IN (${ids}) ORDER BY b.id;
SELECT JSON_OBJECT('_kind','control-projection','projection',projection,'rebuiltAt',DATE_FORMAT(rebuilt_at,'%Y-%m-%d %H:%i:%s.%f'))
 FROM tb_community_projection_control WHERE projection IN ('follow-v1','feed-v1') ORDER BY projection;
SELECT JSON_OBJECT('_kind','control-search','name',name,'indexAlias',index_alias,'activeGeneration',active_generation,
 'rebuiltAt',DATE_FORMAT(rebuilt_at,'%Y-%m-%d %H:%i:%s.%f')) FROM tb_note_search_control WHERE name='search-v1';
COMMIT;`
}

export function phaseErrors(manifest, state, fixtureSHA, expectedControls) {
  const errors = []
  if (!milliseconds(state.startedAt) || !milliseconds(state.endedAt) || state.startedAt > state.endedAt
      || typeof fixtureSHA !== 'string' || !/^[a-f0-9]{64}$/.test(fixtureSHA)) errors.push('phase capture time or fixture identity invalid')
  if (state?.captureKind !== PHASE_FACTS || state.fixtureManifestSHA256 !== fixtureSHA || state.fullAuditPerformed !== false
      || Object.hasOwn(state, 'eventCount') || Object.hasOwn(state, 'events') || Object.hasOwn(state, 'receipts') || Object.hasOwn(state, 'delivery')) errors.push('phase facts cannot impersonate a source-history audit')
  if (!controlRowsValid(state.controlRows)) errors.push('phase control rows invalid or changed')
  try {
    if (controlsIdentity(state.controlRows).sha256 !== controlsIdentity(expectedControls).sha256) errors.push('phase control rows invalid or changed')
    if (vectorIdentity(manifest, state.facts).sha256 !== state.factVectorSHA256 || controlsIdentity(state.controlRows).sha256 !== state.controlsSHA256
        || state.digestContract !== DIGEST || state.transitions !== state.facts.reduce((sum, row) => sum + row.version, 0)) errors.push('phase exact vector or digest differs')
  } catch { errors.push('phase complete relation vector invalid') }
  const expected = new Map(manifest.multiNoteIds.map((noteId, i) => [noteId, manifest.userIds[i]]))
  if (!Array.isArray(state.notes) || state.notes.length !== expected.size || new Set(state.notes.map(row => row.noteId)).size !== expected.size
      || state.notes.some(row => !expected.has(row.noteId) || !integer(row.liked) || !integer(row.activeRelations) || row.liked !== row.activeRelations
        || row.activeRelations !== state.facts.filter(fact => fact.noteId === row.noteId && fact.active === 1).length)) errors.push('phase note counts differ from all selected facts')
  return [...new Set(errors)]
}

export function makePhaseFacts(manifest, rawRows, { label, startedAt, endedAt, fixtureSHA, expectedControls }) {
  assert.ok(rawRows.every(row => ['fact', 'note', 'control-projection', 'control-search'].includes(row._kind)))
  const take = kind => rawRows.filter(row => row._kind === kind).map(({ _kind, ...row }) => row)
  const projections = take('control-projection'), search = take('control-search'); assert.equal(search.length, 1)
  const state = { captureKind: PHASE_FACTS, label, startedAt, endedAt, fixtureManifestSHA256: fixtureSHA,
    fullAuditPerformed: false, facts: take('fact'), notes: take('note'),
    controlRows: { projections: ['follow-v1', 'feed-v1'].map(name => projections.find(row => row.projection === name)), search: search[0] },
    scope: 'Only fresh same-RR facts, note counters and controls; no event/delivery/receipt history read or inferred.' }
  assert.equal(projections.length, 2)
  const vector = vectorIdentity(manifest, state.facts), controls = controlsIdentity(state.controlRows)
  Object.assign(state, { transitions: state.facts.reduce((sum, row) => sum + row.version, 0), digestContract: DIGEST,
    factVectorSHA256: vector.sha256, controlsSHA256: controls.sha256 })
  assert.deepEqual(phaseErrors(manifest, state, fixtureSHA, expectedControls), [])
  return state
}

export function acceptedSample(timelineRaw, capture, { baselineEvents, notBeforeMs, clean = true }) {
  assert.ok(Buffer.isBuffer(timelineRaw) && timelineRaw.length <= 16 << 20 && integer(baselineEvents) && milliseconds(notBeforeMs))
  const notBefore = millisecondsNS(notBeforeMs)
  const expected = capture.transitions - baselineEvents; assert.ok(integer(expected))
  let offset = 0, selected = null
  for (let index = 0; index < timelineRaw.length; index++) {
    if (timelineRaw[index] !== 10) continue
    const line = timelineRaw.subarray(offset, index + 1), row = JSON.parse(line.toString()), begin = offset; offset = index + 1
    if (!Array.isArray(row.errors) || !Array.isArray(row.semanticViolations) || row.errors.length || row.semanticViolations.length) continue
    const sql = row.sql, kafka = row.kafka, stamp = timestampNS(sql?.startedAt), finished = timestampNS(sql?.finishedAt), completed = timestampNS(kafka?.finishedAt)
    if (!sql || !kafka || stamp === null || stamp < notBefore || finished === null
        || timestampNS(row.at) === null || finished < stamp || completed === null || completed < finished || completed > stamp + 4000000000n
        || row.vectorSql?.SQLCommitted !== true || !Number.isFinite(row.durationMs) || row.durationMs < 0 || row.durationMs > 4000
        || sql.digestContract !== DIGEST || sql.factVectorSHA256 !== capture.factVectorSHA256 || sql.controlsSHA256 !== capture.controlsSHA256
        || !integer(sql.counts?.events) || sql.counts.events !== expected) continue
    if (clean && (!['pending', 'processing', 'needsReview', 'other'].every(key => sql.counts[key] === 0) || sql.counts.done !== expected || sql.parked !== 0
        || !Array.isArray(sql.receipts) || sql.receipts.length !== 3 || !groups.every(group => sql.receipts.filter(receipt => receipt.projection === group && receipt.ignored === expected && receipt.missing === 0 && receipt.applied === 0).length === 1)
        || !Array.isArray(kafka.groups) || kafka.groups.length !== 3 || !groups.every(group => kafka.groups.filter(value => value.group === 'community.' + group && value.initialized === true && value.lag === 0).length === 1))) continue
    selected = { offsetBytes: begin, lengthBytes: line.length, lineSHA256: sha(line), at: row.at, sqlStartedAt: sql.startedAt,
      factVectorSHA256: sql.factVectorSHA256, controlsSHA256: sql.controlsSHA256, stageEvents: expected,
      scope: 'One complete accepted original observer NDJSON line; rolling whole-file SHA is not an identity.' }
  }
  return selected // An incomplete trailing line cannot establish acceptance.
}

export function recoveryEvidence(summary, timelineRaw, nativeEndMs, baselineRef, baselineEvents) {
  assert.ok(summary && summary.baselineAuditEventCount === baselineEvents && summary.baselineCountSource === 'bound-node-full-reconciliation'
    && exact(summary.baselineAudit, ['path', 'sha256']) && summary.baselineAudit.path === baselineRef.path
    && summary.baselineAudit.sha256 === baselineRef.sha256, 'complete same-window observer recovery collection required')
  const completeCollection = summary.collectionValid === true && summary.allSamplesComplete === true && summary.failedSamples === 0 && summary.semanticViolationCount === 0
  assert.ok(Buffer.isBuffer(timelineRaw) && timelineRaw.length <= 16 << 20 && (nativeEndMs === null || milliseconds(nativeEndMs)))
  const nativeEnd = nativeEndMs === null ? null : millisecondsNS(nativeEndMs)
  const lines = timelineRaw.toString().split('\n'); assert.equal(lines.pop(), '', 'final observer timeline must be complete')
  let earliest = null
  for (const line of lines) {
    const row = JSON.parse(line), at = timestampNS(row.sql?.startedAt), complete = timestampNS(row.kafka?.finishedAt)
    if (!(nativeEnd !== null && at !== null && complete !== null && at >= nativeEnd && complete >= at && complete <= nativeEnd + 60000000000n && row.errors?.length === 0 && row.semanticViolations?.length === 0
        && row.sql?.digestContract === DIGEST && /^[a-f0-9]{64}$/.test(row.sql.factVectorSHA256) && /^[a-f0-9]{64}$/.test(row.sql.controlsSHA256))) continue
    const state = { transitions: baselineEvents + row.sql.counts.events, factVectorSHA256: row.sql.factVectorSHA256, controlsSHA256: row.sql.controlsSHA256 }
    const proof = acceptedSample(Buffer.from(line + '\n'), state, { baselineEvents, notBeforeMs: nativeEndMs })
    if (proof && (!earliest || Number(complete) / 1000000 < earliest.completedAt)) earliest = { ...proof, completedAt: Number(complete) / 1000000, recoveryMsFromNativeEnd: Number(complete - nativeEnd) / 1000000 }
  }
  return { nativeRecoveryVerified: completeCollection && Boolean(earliest), recovered: earliest, completeCollection, nativeEndUnavailable: nativeEndMs === null,
    summaryPassed: summary.valid === true, finalTimelineSHA256: sha(timelineRaw),
    scope: 'Separate original60s native recovery evidence; a later full audit cannot turn a failed recovery into a pass.' }
}
