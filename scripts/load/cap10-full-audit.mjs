// Explicit like-vector-v1 complete audit; legacy SQL stays in cap10-like.mjs.
import assert from 'node:assert/strict'
import { observerPolicy } from './cap10-observer-policy.mjs'

export const AUDIT_CONTRACT = Object.freeze({ contract: 'like-cohort-audit-v1',
  sourceScope: 'selected-aggregate-id-with-blog-or-like-attribution',
  hashContract: 'communityprojection.Decode/LIKE/canonical-ascii-v1' })
const projections = ['follow-v1', 'feed-v1', 'search-v1']
const note = "JSON_UNQUOTE(JSON_EXTRACT(o.payload,'$.blogId'))"
const user = "JSON_UNQUOTE(JSON_EXTRACT(o.payload,'$.userId'))"
const liked = "JSON_UNQUOTE(JSON_EXTRACT(o.payload,'$.liked'))"
const version = 'CAST(o.aggregate_version AS CHAR)'

function canonicalIDSQL(value) {
  // HEX has no newline/Unicode bytes. No saturating unsigned cast or original
  // string '$' anchor can accidentally accept a noncanonical numeric identity.
  return `(REGEXP_LIKE(CONVERT(HEX(${value}) USING ascii),'^3[1-9](3[0-9]){0,19}$','c') AND
    (OCTET_LENGTH(${value})<20 OR BINARY ${value}<=BINARY '18446744073709551615'))`
}

export const SOURCE_VALID_SQL = `COALESCE(
  BINARY o.event_type=BINARY 'LIKE_CHANGED' AND BINARY o.aggregate_type=BINARY 'BLOG'
  AND JSON_TYPE(o.payload)='OBJECT' AND JSON_LENGTH(o.payload)=4
  AND JSON_TYPE(JSON_EXTRACT(o.payload,'$.blogId'))='STRING'
  AND JSON_TYPE(JSON_EXTRACT(o.payload,'$.userId'))='STRING'
  AND JSON_TYPE(JSON_EXTRACT(o.payload,'$.liked'))='BOOLEAN'
  AND JSON_TYPE(JSON_EXTRACT(o.payload,'$.version'))='INTEGER'
  AND ${canonicalIDSQL('o.aggregate_id')} AND ${canonicalIDSQL(note)} AND ${canonicalIDSQL(user)}
  AND o.aggregate_version BETWEEN 1 AND 2147483647
  AND BINARY ${note}=BINARY o.aggregate_id
  AND BINARY JSON_UNQUOTE(JSON_EXTRACT(o.payload,'$.version'))=BINARY ${version}
  AND BINARY ${liked}=BINARY IF(MOD(o.aggregate_version,2)=1,'true','false')
  AND BINARY o.event_id=BINARY CONCAT('LIKE:',o.aggregate_id,':',${user},':',${version}),0)`

export const CANONICAL_JSON_SQL = `CONCAT(
  '{"eventId":"',o.event_id,'","eventType":"LIKE_CHANGED","aggregateType":"BLOG","aggregateId":"',o.aggregate_id,
  '","version":',${version},',"payload":{"blogId":"',${note},'","userId":"',${user},
  '","liked":',${liked},',"version":',${version},'}}')`

export const EXPECTED_HASH_SQL = `CASE WHEN ${SOURCE_VALID_SQL}=1
  THEN UNHEX(SHA2(${CANONICAL_JSON_SQL},256)) ELSE NULL END`

const canonicalID = id => typeof id === 'string' && /^[1-9][0-9]{0,19}$/.test(id)
  && BigInt(id) <= 18446744073709551615n && String(BigInt(id)) === id

function quotedIDs(ids) {
  assert.ok(Array.isArray(ids) && ids.length >= 1 && ids.length <= 4096, 'bounded owned note set required')
  assert.equal(new Set(ids).size, ids.length, 'unique owned note IDs required')
  assert.ok(ids.every(canonicalID), 'canonical owned note IDs required')
  return ids.map(id => `'${id}'`).join(',')
}

export function auditSQL(noteIDs) {
  const ids = quotedIDs(noteIDs)
  // Attribution is a collation-level superset. Preserve aliases for exact
  // validation; do not claim unrelated other kinds sharing IDs were audited.
  const scope = `o.aggregate_id IN (${ids}) AND
    (o.aggregate_type='BLOG' OR o.event_type='LIKE_CHANGED' OR o.event_id LIKE 'LIKE:%')`
  const selected = `selected AS (SELECT o.event_id,o.event_type,o.aggregate_type,o.aggregate_id,o.aggregate_version,o.payload
    FROM tb_community_outbox o WHERE ${scope})`
  // Receipt hash_values intentionally has only event_id+digest, with a maximal
  // LIMIT as a non-sampling merge barrier. Confirm actual materialization and
  // temporary memory/disk cost in the native plan; this is not budget proof.
  return `SET TRANSACTION ISOLATION LEVEL REPEATABLE READ;
START TRANSACTION WITH CONSISTENT SNAPSHOT, READ ONLY;
SELECT JSON_OBJECT('_kind','fact','noteId',CAST(l.blog_id AS CHAR),'userId',CAST(l.user_id AS CHAR),'active',l.active,'version',l.version)
 FROM tb_blog_like l WHERE l.blog_id IN (${ids}) ORDER BY l.blog_id,l.user_id;
SELECT JSON_OBJECT('_kind','note','noteId',CAST(b.id AS CHAR),'liked',b.liked,
 'activeRelations',(SELECT COUNT(*) FROM tb_blog_like l WHERE l.blog_id=b.id AND l.active=1))
 FROM tb_blog b WHERE b.id IN (${ids}) ORDER BY b.id;
WITH ${selected},checked AS (SELECT o.aggregate_id,o.aggregate_version,o.payload,${SOURCE_VALID_SQL} AS codec_valid FROM selected o)
SELECT JSON_OBJECT('_kind','event','noteId',g.note_id,'userId',g.user_id,'count',g.event_count,
 'distinctVersions',g.distinct_versions,'minVersion',g.min_version,
 'maxVersion',g.max_version,'invalid',g.invalid_count)
 FROM (SELECT o.aggregate_id AS note_id,${user} AS user_id,COUNT(*) AS event_count,
 COUNT(DISTINCT o.aggregate_version) AS distinct_versions,MIN(o.aggregate_version) AS min_version,
 MAX(o.aggregate_version) AS max_version,SUM(CASE WHEN o.codec_valid=1 THEN 0 ELSE 1 END) AS invalid_count
 FROM checked o GROUP BY o.aggregate_id,user_id,HEX(o.aggregate_id),HEX(${user})) g ORDER BY g.note_id,g.user_id;
WITH selected AS (SELECT o.delivery_status FROM tb_community_outbox o WHERE ${scope})
SELECT JSON_OBJECT('_kind','delivery','status',o.delivery_status,'count',COUNT(*))
 FROM selected o GROUP BY o.delivery_status,HEX(o.delivery_status);
WITH ${selected},hash_values AS (SELECT o.event_id,${EXPECTED_HASH_SQL} AS expected_hash FROM selected o
 LIMIT 18446744073709551615),
hashed AS (SELECT event_id,IF(expected_hash IS NULL,0,1) AS codec_valid,expected_hash FROM hash_values),
projections AS (SELECT DISTINCT projection,HEX(projection) AS projection_hex FROM tb_community_projection_event)
SELECT JSON_OBJECT('_kind','receipt','projection',r.projection,'state',r.state,'count',COUNT(*),
 'invalidHash',SUM(CASE WHEN o.codec_valid<>1 OR o.expected_hash IS NULL OR r.event_hash IS NULL
    OR OCTET_LENGTH(r.event_hash)<>32 OR BINARY r.event_hash<>BINARY o.expected_hash THEN 1 ELSE 0 END),
 'invalidProjection',SUM(CASE WHEN BINARY r.projection IN (BINARY 'follow-v1',BINARY 'feed-v1',BINARY 'search-v1') THEN 0 ELSE 1 END),
 'invalidState',SUM(CASE WHEN BINARY r.state=BINARY 'IGNORED' THEN 0 ELSE 1 END))
 FROM hashed o STRAIGHT_JOIN projections p ON 1=1
 STRAIGHT_JOIN tb_community_projection_event r FORCE INDEX (PRIMARY)
 ON r.projection=p.projection AND BINARY r.projection=BINARY p.projection
 AND r.event_id=CONVERT(o.event_id USING ascii) COLLATE ascii_bin AND BINARY r.event_id=BINARY o.event_id
 GROUP BY r.projection,r.state,HEX(r.projection),HEX(r.state);
SELECT JSON_OBJECT('_kind','parked','projection',projection,'state',state,'count',COUNT(*))
 FROM tb_community_consumer_failure GROUP BY projection,state,HEX(projection),HEX(state);
SELECT JSON_OBJECT('_kind','control-projection','projection',projection,'rebuiltAt',DATE_FORMAT(rebuilt_at,'%Y-%m-%d %H:%i:%s.%f'))
 FROM tb_community_projection_control WHERE projection IN ('follow-v1','feed-v1') ORDER BY projection;
SELECT JSON_OBJECT('_kind','control-search','name',name,'indexAlias',index_alias,'activeGeneration',active_generation,
 'rebuiltAt',DATE_FORMAT(rebuilt_at,'%Y-%m-%d %H:%i:%s.%f')) FROM tb_note_search_control WHERE name='search-v1';
SELECT JSON_OBJECT('_kind','watermark','maxOutboxId',CAST(COALESCE(MAX(id),0) AS CHAR)) FROM tb_community_outbox;
SELECT JSON_OBJECT('_kind','audit','contract','${AUDIT_CONTRACT.contract}',
 'sourceScope','${AUDIT_CONTRACT.sourceScope}','hashContract','${AUDIT_CONTRACT.hashContract}');
COMMIT;`
}

export function auditContractMatches(value) {
  return Boolean(value && typeof value === 'object' && !Array.isArray(value)
    && Object.keys(value).sort().join(',') === Object.keys(AUDIT_CONTRACT).sort().join(',')
    && Object.entries(AUDIT_CONTRACT).every(([key, expected]) => value[key] === expected))
}

const count = value => Number.isSafeInteger(value) && value >= 0
const exactFields = (row, fields) => row && typeof row === 'object' && !Array.isArray(row)
  && Object.keys(row).sort().join(',') === [...fields].sort().join(',')
function controlDate(value) {
  if (value === null) return true
  if (typeof value !== 'string' || value.length !== 26 || !/^[1-9][0-9]{3}-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01]) ([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]\.[0-9]{6}$/.test(value)) return false
  const year = Number(value.slice(0, 4)), month = Number(value.slice(5, 7)), day = Number(value.slice(8, 10))
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0)
  return day <= [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31][month - 1]
}
export function controlRowsValid(value) {
  if (!exactFields(value, ['projections', 'search']) || !Array.isArray(value.projections) || value.projections.length !== 2) return false
  const names = new Set()
  for (const row of value.projections) {
    if (!exactFields(row, ['projection', 'rebuiltAt']) || !['follow-v1', 'feed-v1'].includes(row.projection) || names.has(row.projection) || !controlDate(row.rebuiltAt)) return false
    names.add(row.projection)
  }
  const row = value.search
  return exactFields(row, ['name', 'indexAlias', 'activeGeneration', 'rebuiltAt']) && row.name === 'search-v1'
    && typeof row.indexAlias === 'string' && row.indexAlias.length <= 128 && /^[\x00-\x7f]*$/.test(row.indexAlias)
    && typeof row.activeGeneration === 'string' && row.activeGeneration.length <= 255 && /^[\x00-\x7f]*$/.test(row.activeGeneration)
    && controlDate(row.rebuiltAt)
}

// Missing receipts are handled by the existing convergence/recovery gate.
// Invalid source/receipt identities or hashes are semantic failures immediately.
export function auditErrors(manifest, state, expectedManifestSHA256) {
  const errors = []
  if (!auditContractMatches(state?.auditContract)) errors.push('full audit contract marker missing or invalid')
  if (typeof expectedManifestSHA256 !== 'string' || expectedManifestSHA256.length !== 64 || !/^[a-f0-9]{64}$/.test(expectedManifestSHA256)
      || state.fixtureManifestSHA256 !== expectedManifestSHA256) errors.push('full audit original fixture bytes differ')
  if (!controlRowsValid(state.controlRows)) errors.push('full audit complete nullable control rows invalid')
  if (!exactFields(state.sourceWatermark, ['maxOutboxId', 'diagnosticOnly']) || state.sourceWatermark.diagnosticOnly !== true
      || !(state.sourceWatermark.maxOutboxId === '0' || canonicalID(state.sourceWatermark.maxOutboxId))) errors.push('full audit diagnostic source watermark invalid')
  const notes = manifest.multiNoteIds, users = manifest.userIds
  assert.ok(Array.isArray(notes) && Array.isArray(users) && notes.length === users.length, 'complete audit manifest pairs required')
  quotedIDs(notes); quotedIDs(users)
  const expected = new Map(notes.map((noteId, index) => [noteId, users[index]]))
  const facts = new Set(), actualNotes = new Set(), events = new Set(), receiptGroups = new Set()
  for (const row of state.facts) {
    if (expected.get(row.noteId) !== row.userId || facts.has(row.noteId)
        || !count(row.version) || row.version > 2147483647 || ![0, 1].includes(row.active) || row.active !== row.version % 2) errors.push('full audit fact identity or version invalid')
    facts.add(row.noteId)
  }
  if (facts.size !== expected.size || state.facts.length !== expected.size) errors.push('full audit requires every manifest pair including version zero')
  for (const row of state.notes) {
    if (!expected.has(row.noteId) || actualNotes.has(row.noteId) || !count(row.liked) || !count(row.activeRelations)) errors.push('full audit note identity or count invalid')
    actualNotes.add(row.noteId)
  }
  if (actualNotes.size !== expected.size || state.notes.length !== expected.size) errors.push('full audit requires every manifest note')
  for (const row of state.events) {
    if (expected.get(row.noteId) !== row.userId || events.has(row.noteId)
        || !['count', 'distinctVersions', 'minVersion', 'maxVersion', 'invalid'].every(key => count(row[key]))
        || row.count < 1 || row.invalid !== 0) errors.push('full audit source identity or codec invalid')
    events.add(row.noteId)
  }
  for (const row of state.delivery) {
    if (!['PENDING', 'PROCESSING', 'DONE', 'NEEDS_REVIEW'].includes(row.status) || !count(row.count) || row.count < 1) errors.push('full audit delivery status or count invalid')
  }
  if (!count(state.transitions) || !count(state.eventCount)
      || state.facts.reduce((sum, row) => sum + row.version, 0) !== state.transitions
      || state.events.reduce((sum, row) => sum + row.count, 0) !== state.eventCount
      || state.delivery.reduce((sum, row) => sum + row.count, 0) !== state.eventCount) errors.push('full audit source fact or delivery totals differ')
  for (const row of state.receipts) {
    const key = JSON.stringify([row.projection, row.state])
    if (!projections.includes(row.projection) || row.state !== 'IGNORED' || receiptGroups.has(key)
        || !count(row.count) || row.count < 1 || row.count > state.eventCount
        || !['invalidHash', 'invalidProjection', 'invalidState'].every(field => count(row[field]) && row[field] <= row.count && row[field] === 0)) errors.push('full audit receipt identity state or hash invalid')
    receiptGroups.add(key)
  }
  for (const row of state.parked) {
    if (row.state !== 'RESOLVED' || !count(row.count) || row.count < 1) errors.push('full audit global consumer failure or unknown state')
  }
  return [...new Set(errors)]
}

export function baselineAuditReference(ready) {
  const ref = ready?.baselineAudit
  assert.equal(ready?.baselineCountSource, 'bound-node-full-reconciliation', 'explicit baseline count source required')
  assert.ok(count(ready?.baselineAuditEventCount), 'exact baseline audit count required')
  assert.ok(exactFields(ref, ['path', 'sha256']) && typeof ref.path === 'string' && ref.path.length <= 1024
    && !ref.path.startsWith('/') && !/[\\:\0]/.test(ref.path)
    && ref.path.split('/').every(part => /^[A-Za-z0-9._-]+$/.test(part) && part !== '.' && part !== '..')
    && ref.path.split('/').at(-1) === 'reconcile.json'
    && typeof ref.sha256 === 'string' && ref.sha256.length === 64 && /^[a-f0-9]{64}$/.test(ref.sha256), 'canonical baseline audit reference required')
  return { path: ref.path, sha256: ref.sha256 }
}

export function baselineAuditCount(ready, proof, proofSHA256, manifest, fixtureManifestSHA256) {
  const ref = baselineAuditReference(ready)
  assert.equal(ref.sha256, proofSHA256, 'baseline audit original bytes differ')
  assert.ok(proof?.valid === true && proof.factsValid === true && proof.downstreamConverged === true
    && Array.isArray(proof.errors) && proof.errors.length === 0, 'complete reconciled baseline required')
  const expected = observerPolicy({ CAP10_OBSERVER_MODE: 'like-vector-v1' })
  assert.ok(exactFields(proof.facts?.observerPolicy, Object.keys(expected))
    && Object.entries(expected).every(([key, value]) => proof.facts.observerPolicy[key] === value), 'baseline audit observer policy differs')
  assert.deepEqual(auditErrors(manifest, proof.facts, fixtureManifestSHA256), [])
  const total = ready.baselineAuditEventCount
  assert.equal(proof.facts.eventCount, total, 'baseline audit event count differs')
  assert.equal(proof.facts.transitions, total, 'baseline audit fact total differs')
  assert.ok(proof.facts.delivery.every(row => row.status === 'DONE'), 'baseline deliveries have not converged')
  for (const projection of projections) assert.equal(proof.facts.receipts.filter(row => row.projection === projection)
    .reduce((sum, row) => sum + row.count, 0), total, 'baseline receipts have not converged')
  assert.ok(proof.derived?.available === true && Array.isArray(proof.derived.errors) && proof.derived.errors.length === 0,
    'baseline derived facts unavailable')
  return total
}
