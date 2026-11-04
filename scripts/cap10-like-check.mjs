// Pure public-contract/cancellation boundary checks. No service, token or load.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import { posix } from 'node:path'
import { stateFromBody, classifyWrite, pairForVU, unknownBounds } from './load/cap10-like-contract.mjs'
import { nativeScenarioWindow } from './load/cap10-native-window.mjs'
import { fixturePoolSize } from './load/cap10-cohort-contract.mjs'
import { observerPolicy } from './load/cap10-observer-policy.mjs'
import { AUDIT_CONTRACT, auditSQL, auditContractMatches } from './load/cap10-full-audit.mjs'

let checks = 0
async function check(name, fn) { await fn(); checks++; console.log('PASS ' + name) }
await check('audit policy keeps the legacy budget and explicitly selects only the vector audit budget', () => {
  for (const env of [{}, { CAP10_OBSERVER_MODE: 'full-history-v1' }]) {
    assert.deepEqual(observerPolicy(env), { mode: 'full-history-v1', fullAuditTimeoutMs: 30000, scope: 'full-snapshot-audit-only' })
  }
  assert.deepEqual(observerPolicy({ CAP10_OBSERVER_MODE: 'like-vector-v1' }), { mode: 'like-vector-v1', fullAuditTimeoutMs: 120000, scope: 'full-snapshot-audit-only' })
  assert.equal(Object.isFrozen(observerPolicy()), true)
  for (const value of ['', null, undefined, 'full-history-v2', 'LIKE-VECTOR-V1', 120000, {}, true]) {
    assert.throws(() => observerPolicy({ CAP10_OBSERVER_MODE: value }))
  }
  for (const value of [null, [], 'like-vector-v1']) assert.throws(() => observerPolicy(value))
})
const body = (liked, version, likes = 1, noteId = '101') => ({ success: true, data: { noteId, liked, version, likes } })
await check('first true is a mutation, not an HTTP-only success', () => assert.equal(classifyWrite(200, body(true, 1), '101', { liked: false, version: 0 }, true).kind, 'mutation'))
await check('unlike increments its own relation version', () => assert.equal(classifyWrite(200, body(false, 8, 0), '101', { liked: true, version: 7 }, false).kind, 'mutation'))
await check('idempotent repeat is explicitly no-op', () => assert.equal(classifyWrite(200, body(true, 7), '101', { liked: true, version: 7 }, true).kind, 'noop'))
await check('unexpected same version cannot be called mutation', () => assert.equal(classifyWrite(200, body(true, 7), '101', { liked: false, version: 7 }, true).kind, 'noop'))
await check('version jump signals a second writer or invalid state', () => assert.equal(classifyWrite(200, body(true, 9), '101', { liked: false, version: 7 }, true).kind, 'invalid'))
await check('wrong note cannot pass response classification', () => assert.equal(stateFromBody(body(true, 1, 1, '102'), '101'), null))
await check('wrong wanted state is invalid even at version+1', () => assert.equal(classifyWrite(200, body(false, 8), '101', { liked: false, version: 7 }, true).kind, 'invalid'))
await check('negative or fractional public counts are not valid', () => { assert.equal(stateFromBody(body(true, 1, -1), '101'), null); assert.equal(stateFromBody(body(true, 1, .5), '101'), null) })
await check('versions cannot be string, negative or fractional', () => { for (const v of ['1', -1, .5]) assert.equal(stateFromBody(body(true, v), '101'), null) })
await check('HTTP timeout/503 preserve effect uncertainty', () => { for (const status of [0, 503]) assert.equal(classifyWrite(status, null, '101', { liked: false, version: 4 }, true).kind, 'uncertain') })
await check('business rejection is separate from effect uncertainty', () => assert.equal(classifyWrite(404, { success: false }, '101', { liked: false, version: 0 }, true).kind, 'rejected'))
await check('a lost receipt recovers fact before choosing next target', () => {
  const before = { liked: false, version: 4 }, recovered = stateFromBody(body(true, 5), '101')
  assert.equal(classifyWrite(0, null, '101', before, true).kind, 'uncertain')
  assert.equal(!recovered.liked, false)
  assert.equal(classifyWrite(200, body(false, 6, 0), '101', recovered, !recovered.liked).kind, 'mutation')
})
const fixture = { users: [{ userId: '1' }, { userId: '2' }], multiNoteIds: ['101', '102'], hotNoteId: '103',
  initial: [{ userId: '1', noteId: '101', active: 0, version: 0 }, { userId: '2', noteId: '102', active: 1, version: 3 }] }
await check('multi VUs own distinct exact relations', () => { const a = pairForVU(fixture, 'multi', 1), b = pairForVU(fixture, 'multi', 2); assert.notEqual(a.user.userId + ':' + a.noteId, b.user.userId + ':' + b.noteId); assert.equal(b.initial.liked, true) })
await check('pool exhaustion fails closed instead of aliasing a relation', () => assert.throws(() => pairForVU(fixture, 'multi', 3)))
await check('hot still has one distinct user per VU', () => { const hot = { ...fixture, initial: fixture.initial.map(x => ({ ...x, noteId: '103' })) }; assert.equal(pairForVU(hot, 'hot', 2).noteId, '103'); assert.notEqual(pairForVU(hot, 'hot', 1).user.userId, pairForVU(hot, 'hot', 2).user.userId) })
await check('private initial state cannot refer to another relation', () => assert.throws(() => pairForVU(fixture, 'hot', 1)))
await check('zero uncertainty is exact only with both channels zero', () => assert.deepEqual(unknownBounds(20, 20, 0), { classificationUnknown: 0, unknownLowerBound: 0, unknownUpperBound: 0, unknownIsExact: true, unknown: 0 }))
await check('missing terminals alone retain the exact old-style unknown', () => assert.equal(unknownBounds(16133, 15933, 0).unknown, 200))
await check('cancel race cannot double-count overlapping uncertainty as exact', () => { const b = unknownBounds(10, 8, 1); assert.equal(b.unknown, null); assert.equal(b.unknownLowerBound, 2); assert.equal(b.unknownUpperBound, 3) })
await check('classified uncertain commit remains unknown after recovery', () => assert.equal(unknownBounds(10, 10, 1).unknown, 1))
await check('invalid uncertainty counts cannot be clamped into a pass', () => assert.throws(() => unknownBounds(10, 11, 0)))
const nativeFixture = { users: Array.from({ length: 512 }, (_, i) => ({ userId: String(i + 1) })), multiNoteIds: Array.from({ length: 512 }, (_, i) => String(101 + i)), initial: Array.from({ length: 512 }, (_, i) => ({ userId: String(i + 1), noteId: String(101 + i), active: 0, version: 0 })) }
const timing = { metric: { min: 1200, max: 1200 }, durationMetric: { min: 60000, max: 60000 }, seconds: 60,
  wrapperStartedAt: 1000, wrapperEndedAt: 63000, exit: 0, externalStop: null }
await check('native window excludes process startup and trailing grace', () => { const result = nativeScenarioWindow(timing); assert.equal(result.startedAt, 1200); assert.equal(result.scheduledEndedAt, 61200); assert.equal(result.complete, true) })
await check('early abort never claims the scheduled native window completed', () => { assert.equal(nativeScenarioWindow({ ...timing, exit: 99 }).complete, false); assert.equal(nativeScenarioWindow({ ...timing, wrapperEndedAt: 3000 }).complete, false); assert.equal(nativeScenarioWindow({ ...timing, externalStop: { reason: 'stop' } }).complete, false) })
await check('missing or disagreeing native timing fails closed', () => { assert.throws(() => nativeScenarioWindow({ ...timing, metric: null })); assert.throws(() => nativeScenarioWindow({ ...timing, metric: { min: 1200, max: 1201 } })); assert.throws(() => nativeScenarioWindow({ ...timing, durationMetric: { min: 180000, max: 180000 } })) })
await check('native scenario start must fall in the actual wrapper interval', () => assert.throws(() => nativeScenarioWindow({ ...timing, metric: { min: 999, max: 999 } })))
await check('actual native script records its clock once per participating VU', () => {
  const values = new Map()
  class Metric { constructor(name) { this.name = name; values.set(name, []) } add(value) { values.get(this.name).push(value) } }
  const source = readFileSync('scripts/load/cap10-like.js', 'utf8').replace(/^import .*$/gm, '')
    .replace('export const options', 'const options').replace('export default function ()', 'function invoke()')
  let version = 0
  const context = { __ENV: { CAP_LIKE_INPUT: 'unused', CAP_LIKE_MODE: 'multi', CAP_LIKE_RATE: '1', CAP_LIKE_SECONDS: '60' },
    open: () => JSON.stringify(nativeFixture), Counter: Metric, Rate: Metric, Trend: Metric, stateFromBody, classifyWrite, pairForVU,
    exec: { vu: { idInTest: 1 }, scenario: { startTime: 1200 } }, console,
    http: { put: (_, input) => ({ status: 200, timings: { duration: 1 }, json: () => body(JSON.parse(input).liked, ++version) }) } }
  vm.runInNewContext(source + '\nglobalThis.invoke = invoke', context)
  context.invoke(); context.invoke()
  assert.deepEqual(values.get('cap_native_scenario_start_unix_ms'), [1200])
  assert.deepEqual(values.get('cap_native_scenario_duration_ms'), [60000])
  assert.equal(values.get('cap_like_mutation').reduce((a, b) => a + b), 2)
})
await check('warm records latency but only stable applies the original latency SLO', () => {
  const source = readFileSync('scripts/load/cap10-like.js', 'utf8').replace(/^import .*$/gm, '')
    .replace('export const options', 'const options').replace('export default function ()', 'function invoke()')
  const byPhase = {}
  for (const phase of ['warm', 'stable']) {
    const values = new Map()
    class Metric { constructor(name) { this.name = name; values.set(name, []) } add(value) { values.get(this.name).push(value) } }
    const context = { __ENV: { CAP_LIKE_INPUT: 'unused', CAP_LIKE_MODE: 'multi', CAP_LIKE_RATE: '1', CAP_LIKE_SECONDS: phase === 'warm' ? '10' : '60', CAP_LIKE_PHASE: phase },
      open: () => JSON.stringify(nativeFixture), Counter: Metric, Rate: Metric, Trend: Metric, stateFromBody, classifyWrite, pairForVU,
      exec: { vu: { idInTest: 1 }, scenario: { startTime: 1200 } }, console,
      http: { put: () => ({ status: 200, timings: { duration: 219.66 }, json: () => body(true, 1) }) } }
    vm.runInNewContext(source + '\nglobalThis.invoke = invoke; globalThis.testOptions = options', context)
    context.invoke()
    assert.deepEqual(values.get('cap_like_all_ms'), [219.66])
    assert.ok(context.testOptions.summaryTrendStats.includes('p(95)') && context.testOptions.summaryTrendStats.includes('p(99)'))
    byPhase[phase] = JSON.parse(JSON.stringify(context.testOptions.thresholds))
  }
  assert.deepEqual(byPhase.stable.cap_like_all_ms, [
    { threshold: 'p(95)<=200', abortOnFail: false },
    { threshold: 'p(99)<=500', abortOnFail: false }])
  assert.equal(Object.hasOwn(byPhase.warm, 'cap_like_all_ms'), false)
  delete byPhase.stable.cap_like_all_ms
  assert.deepEqual(byPhase.warm, byPhase.stable)
})
await check('terminal latency-only exit99 can establish a complete CAP10 window', () => {
  const result = nativeScenarioWindow({ ...timing, exit: 99, metrics: { cap_like_all_ms: { thresholds: { 'p(95)<=200': true } } } })
  assert.equal(result.complete, true); assert.equal(result.onlyTerminalLatencyFailure, true)
  assert.equal(nativeScenarioWindow({ ...timing, exit: 99, metrics: { dropped_iterations: { thresholds: { 'count==0': true } } } }).complete, false)
})
await check('actual native script retires and aborts on noop or unknown after retaining classification', () => {
  const source = readFileSync('scripts/load/cap10-like.js', 'utf8').replace(/^import .*$/gm, '')
    .replace('export const options', 'const options').replace('export default function ()', 'function invoke()')
  for (const kind of ['noop', 'unknown']) {
    const values = new Map(); let requests = 0, stopped = null
    class Metric { constructor(name) { this.name = name; values.set(name, []) } add(value) { values.get(this.name).push(value) } }
    const context = { __ENV: { CAP_LIKE_INPUT: 'unused', CAP_LIKE_MODE: 'multi', CAP_LIKE_RATE: '1', CAP_LIKE_SECONDS: '60' },
      open: () => JSON.stringify(nativeFixture), Counter: Metric, Rate: Metric, Trend: Metric, classifyWrite, pairForVU,
      exec: { vu: { idInTest: 1 }, scenario: { startTime: 1200 }, test: { abort: message => { stopped = message; throw new Error('native abort') } } },
      console: { warn() {} }, http: { put: () => { requests++; return { status: kind === 'noop' ? 200 : 0, timings: { duration: 1 }, json: () => body(true, 0) } } } }
    vm.runInNewContext(source + '\nglobalThis.invoke = invoke', context)
    assert.throws(() => context.invoke(), /native abort/)
    context.invoke()
    assert.equal(requests, 1); assert.match(stopped, /CAP10 correctness stop/)
    for (const key of ['cap_like_attempted', 'cap_like_completed', 'cap_like_retired_relations', kind === 'noop' ? 'cap_like_noop' : 'cap_like_effect_unknown']) assert.equal(values.get(key).reduce((a, b) => a + b), 1)
  }
})
const helperSource = readFileSync('scripts/cap10-like.mjs', 'utf8')
const auditCode = ['sql', 'snapshot'].map(name => helperSource.match(new RegExp('^async function ' + name + '[\\s\\S]*?^}', 'm'))[0]).join('\n')
function auditContext(policy, reply = { code: 0, stdout: '', stderr: '', timedOut: false }) {
  const calls = []
  const context = { assert, observationPolicy: policy, sqlBin: 'synthetic-mysql', owned: { mysql: '3481', database: 'synthetic-owned' },
    process: { env: { MYSQL_USER: 'synthetic-owner', MYSQL_PASSWORD: 'synthetic-only', CAP_READ_OBSERVER_USER: 'synthetic-reader', CAP_READ_OBSERVER_PASSWORD: 'synthetic-only' } }, auditSQL, auditContractMatches, fixtureManifestHashes: { get: () => 'f'.repeat(64) },
    command: async (binary, argv, options) => { calls.push({ binary, argv, options }); return reply },
    modeNotes: manifest => manifest.multiNoteIds,
    rows: raw => raw ? raw.split(/\r?\n/).filter(Boolean).map(line => JSON.parse(line)) : [] }
  vm.runInNewContext(auditCode + '\nglobalThis.sql = sql; globalThis.snapshot = snapshot', context)
  return { context, calls }
}
await check('actual snapshot alone receives the selected full-audit timeout and records its policy', async () => {
  const records = [{ _kind: 'fact', noteId: '101', userId: '1', active: 0, version: 2 }, { _kind: 'event', count: 2 }]
  for (const mode of ['full-history-v1', 'like-vector-v1']) {
    const policy = observerPolicy({ CAP10_OBSERVER_MODE: mode })
    const payload = mode === 'like-vector-v1' ? [...records, { _kind: 'audit', ...AUDIT_CONTRACT },
      { _kind: 'control-projection', projection: 'follow-v1', rebuiltAt: null },
      { _kind: 'control-projection', projection: 'feed-v1', rebuiltAt: null },
      { _kind: 'control-search', name: 'search-v1', indexAlias: '', activeGeneration: '', rebuiltAt: null },
      { _kind: 'watermark', maxOutboxId: '2' }] : records
    const { context, calls } = auditContext(policy, { code: 0, stdout: payload.map(row => JSON.stringify(row)).join('\n'), stderr: '', timedOut: false })
    await context.sql('SELECT 1')
    const state = await context.snapshot({ multiNoteIds: ['101'] }, 'multi', 'synthetic-audit')
    assert.equal(calls.length, 2)
    assert.equal(calls[0].options.timeoutMs, 30000)
    assert.equal(calls[1].options.timeoutMs, policy.fullAuditTimeoutMs)
    assert.ok(calls[1].argv.includes('--user=synthetic-reader'))
    assert.match(calls[1].options.input, /START TRANSACTION WITH CONSISTENT SNAPSHOT, READ ONLY;/)
    assert.match(calls[1].options.input, /COMMIT;$/)
    assert.deepEqual(state.observerPolicy, policy)
    assert.equal(state.transitions, 2); assert.equal(state.eventCount, 2)
    if (mode === 'like-vector-v1') {
      assert.equal(state.fixtureManifestSHA256, 'f'.repeat(64))
      assert.equal(state.controlRows.projections[0].projection, 'follow-v1')
      assert.equal(state.controlRows.search.rebuiltAt, null)
      assert.equal(state.sourceWatermark.maxOutboxId, '2'); assert.equal(state.sourceWatermark.diagnosticOnly, true)
    } else assert.equal(Object.hasOwn(state, 'controlRows'), false)
  }
})
await check('a timed-out complete audit rejects instead of fabricating empty facts or a passing report', async () => {
  const { context, calls } = auditContext(observerPolicy({ CAP10_OBSERVER_MODE: 'like-vector-v1' }), { code: null, stdout: '', stderr: '', timedOut: true })
  await assert.rejects(context.snapshot({ multiNoteIds: ['101'] }, 'multi', 'synthetic-audit'), /SQL timeout/)
  assert.equal(calls.length, 1); assert.equal(calls[0].options.timeoutMs, 120000)
})
await check('actual helper fixes Linux evidence to the same batch and requires explicit native-platform transition', async () => {
  const source = helperSource.slice(helperSource.indexOf('const executionPlatform ='), helperSource.indexOf('const privateRoot ='))
  const transition = { version: 1, batch: 'CAP-10', fromPlatform: 'windows', toPlatform: 'linux', switchCount: 1, authorized: true, hostLabel: 'synthetic-owned-host', userAuthorizationReference: 'synthetic-explicit-authorization' }
  const base = '/repo/docs/evidence/2026-11-01-cap10'
  const evaluate = (platform, changes = {}, evidence = transition, resources = { resourceProfile: 'baseline' }, file = {}) => {
    const root = platform === 'linux' ? base + '/linux' : base
    const context = { assert, join: posix.join, dirname: posix.dirname, basename: posix.basename, resolve: path => '/repo/' + path, hash: () => 'frozen-transition-hash',
      process: { platform, env: { CAP10_PLATFORM: platform === 'win32' ? 'windows' : platform, CAP10_EVIDENCE: root, CAP10_ENVIRONMENT_METADATA: root + '/environment.json', CAP10_TRANSITION_SHA256: 'frozen-transition-hash', ...changes } },
      lstat: async () => ({ isFile: () => file.regular !== false, isSymbolicLink: () => file.symlink === true }),
      realpath: async path => path.endsWith('.json') && file.outside ? '/foreign/environment.json' : path,
      readFile: async path => JSON.stringify(path.endsWith('environment-transition.json') ? evidence : resources) }
    return vm.runInNewContext('(async () => {' + source + '; return evidenceRoot })()', context)
  }
  assert.equal(await evaluate('linux'), base + '/linux')
  assert.equal(await evaluate('win32'), base)
  assert.equal(await evaluate('linux', { CAP10_ENVIRONMENT_METADATA: base + '/linux/environment-012345abcdef.json' }), base + '/linux')
  assert.equal(await evaluate('linux', { CAP10_ENVIRONMENT_METADATA: base + '/linux/environment-012345abcdef.json', CAP10_RESOURCE_PROFILE: 'mysql8-v1' }, transition, { resourceProfile: 'mysql8-v1' }), base + '/linux')
  assert.equal(await evaluate('linux', { CAP10_ENVIRONMENT_METADATA: base + '/linux/environment-012345abcdef.json', CAP10_RESOURCE_PROFILE: 'mysql8-kafka2-v1' }, transition, { resourceProfile: 'mysql8-kafka2-v1' }), base + '/linux')
  await assert.rejects(evaluate('linux', { CAP10_RESOURCE_PROFILE: 'mysql8-kafka2-v1' }, transition, { resourceProfile: 'mysql8-v1' }))
  await assert.rejects(evaluate('linux', { CAP10_RESOURCE_PROFILE: 'mysql8-v1' }, transition, { resourceProfile: 'mysql8-kafka2-v1' }))
  await assert.rejects(evaluate('linux', { CAP10_RESOURCE_PROFILE: 'mysql8-kafka4-v1' }, transition, { resourceProfile: 'mysql8-kafka4-v1' }))
  await assert.rejects(evaluate('linux', { CAP10_RESOURCE_PROFILE: 'mysql8-v1' }))
  await assert.rejects(evaluate('linux', { CAP10_ENVIRONMENT_METADATA: base + '/linux/input.json' }))
  for (const resources of [null, [], true, 1]) await assert.rejects(evaluate('linux', {}, transition, resources))
  for (const file of [{ regular: false }, { symlink: true }, { outside: true }]) await assert.rejects(evaluate('linux', {}, transition, {}, file))
  await assert.rejects(evaluate('linux', { CAP10_PLATFORM: 'windows' }))
  await assert.rejects(evaluate('linux', { CAP10_EVIDENCE: base }))
  await assert.rejects(evaluate('linux', { CAP10_TRANSITION_SHA256: 'different' }))
  await assert.rejects(evaluate('linux', {}, { ...transition, authorized: false }))
  await assert.rejects(evaluate('darwin'))
})
await check('actual phase registers cleanup before secret write and removes partial files on faults', async () => {
  const phaseSource = helperSource.match(/^    async function runPhase[\s\S]*?^    }\n    try/m)[0].replace(/\n    try$/, '')
  for (const fault of [null, 'registration', 'private-write']) {
    const steps = []; let exists = false
    const context = { Map, process: { env: {} }, manifest: nativeFixture, runtime: { users: nativeFixture.users }, mode: 'multi', dir: '/public',
      owned: { api: 'http://127.0.0.1:8281' }, count: 512, warmRate: 1, rate: 1, preVUs: 512, maxVUs: 512,
      join: (...parts) => parts.join('/'), tmpdir: () => '/owned/tmp', mkdtemp: async () => '/owned/tmp/exact-new',
      stopPresent: async () => false, assert, json: JSON.stringify,
      save: async path => { steps.push(path.includes('-private-input.json') ? 'registered' : 'removed-record'); if (fault === 'registration' && path.includes('-private-input.json')) throw new Error('registration fault') },
      writeFile: async () => { steps.push('secret-write'); exists = true; if (fault === 'private-write') throw new Error('private write fault') },
      unlink: async () => { steps.push('unlink'); if (!exists) { const error = new Error('absent'); error.code = 'ENOENT'; throw error } exists = false },
      rmdir: async () => steps.push('rmdir'), command: async () => { steps.push('native'); return { code: 0 } } }
    vm.runInNewContext(phaseSource + '\nglobalThis.runPhase = runPhase', context)
    if (fault) await assert.rejects(context.runPhase('warm', 10, { facts: [] }), /fault/)
    else await context.runPhase('warm', 10, { facts: [] })
    assert.equal(exists, false); assert.ok(steps.includes('rmdir')); assert.ok(steps.includes('removed-record'))
    if (fault === 'registration') assert.equal(steps.includes('secret-write'), false)
    else assert.ok(steps.indexOf('registered') < steps.indexOf('secret-write'))
  }
})
const factCode = helperSource.match(/^function factErrors[\s\S]*?^}/m)[0]
const derivedCode = helperSource.match(/^async function derived[\s\S]*?^}/m)[0]
const seedManifest = { userIds: nativeFixture.users.map(x => x.userId), multiNoteIds: nativeFixture.multiNoteIds, relationshipSeed: { version: 0 } }
function zeroState() { return { facts: nativeFixture.initial.map(x => ({ ...x })), notes: nativeFixture.multiNoteIds.map(noteId => ({ noteId, liked: 0, activeRelations: 0 })), events: [], transitions: 0, eventCount: 0 } }
function projectionContext(replies) {
  const context = { vectorMode: false, modeNotes: m => m.multiNoteIds, redisCommands: async () => replies, assert, observationPolicy: observerPolicy({}) }
  vm.runInNewContext(factCode + '\n' + derivedCode + '\nglobalThis.factErrors = factErrors; globalThis.derived = derived', context)
  return context
}
const emptyReplies = () => Array.from({ length: 512 }, () => [[], []]).flat()
await check('512 explicit inactive version-zero seed rows require no events or Redis versions', async () => {
  const c = projectionContext(emptyReplies()), state = zeroState()
  assert.equal(c.factErrors(seedManifest, 'multi', state).length, 0)
  assert.equal((await c.derived(seedManifest, 'multi', state)).errors.length, 0)
})
await check('mixed zero and inactive v2 facts reconcile with exact positive versions', async () => {
  const state = zeroState(), replies = emptyReplies()
  state.facts[0].version = 2; state.events = [{ noteId: '101', userId: '1', count: 2, distinctVersions: 2, minVersion: 1, maxVersion: 2, invalid: 0 }]
  state.transitions = state.eventCount = 2; replies[1] = ['1', '2']
  const c = projectionContext(replies)
  assert.equal(c.factErrors(seedManifest, 'multi', state).length, 0)
  assert.equal((await c.derived(seedManifest, 'multi', state)).errors.length, 0)
})
await check('v0 active, membership, explicit hash or foreign inactive-version field fails closed', async () => {
  const state = zeroState(); state.facts[0].active = 1
  assert.ok(projectionContext(emptyReplies()).factErrors(seedManifest, 'multi', state).length > 0)
  for (const [members, versions] of [[['1'], []], [[], ['1', '0']], [[], ['999999', '2']]]) {
    const replies = emptyReplies(); replies[0] = members; replies[1] = versions
    assert.ok((await projectionContext(replies).derived(seedManifest, 'multi', zeroState())).errors.length > 0)
  }
})
const fixtureCode = helperSource.match(/^async function loadFixture[\s\S]*?^}/m)[0]
const distributionCode = helperSource.match(/^function initialDistribution[\s\S]*?^}/m)[0]
function ownedFixture(count, explicit = true) {
  const manifest = { project: 'owned-project', baseUrl: 'http://127.0.0.1:8281', executionPlatform: 'windows',
    transitionSHA256: null, sourceSha: 'a'.repeat(40), expectedRevision: 'capacity-' + 'a'.repeat(40),
    status: 'READY', fixture: 'owned-fixture', credentialFile: '/owned/tmp/cohort/input.json',
    users: count, multiNotes: count, multiRelations: count,
    userIds: Array.from({ length: count }, (_, i) => String(i + 1)), multiNoteIds: Array.from({ length: count }, (_, i) => String(i + 10001)) }
  if (explicit) manifest.poolSize = count
  return manifest
}
function fixtureContext(manifest, action = 'inspect', runtimeCount = manifest.users) {
  const runtime = { fixture: manifest.fixture, users: manifest.userIds.slice(0, runtimeCount).map(userId => ({ userId, token: 'synthetic-only' })) }
  const context = { assert, fixturePoolSize, fixturePath: '/public/fixture.json', evidence() {},
    readFile: async path => JSON.stringify(path === '/public/fixture.json' ? manifest : runtime),
    hash: () => 'fixture-byte-hash', owned: { project: 'owned-project', api: 'http://127.0.0.1:8281' },
    executionPlatform: 'windows', process: { env: {} }, action, privateRoot: '/owned', join: posix.join, basename: posix.basename, fixtureManifestHashes: new WeakMap(),
    under: (path, root) => path.startsWith(root + '/') }
  vm.runInNewContext(fixtureCode + '\n' + distributionCode + '\nglobalThis.loadFixture = loadFixture; globalThis.initialDistribution = initialDistribution', context)
  return context
}
await check('actual fixture loader keeps legacy 512 and accepts complete explicit 4096', async () => {
  for (const manifest of [ownedFixture(512, false), ownedFixture(4096)]) {
    const context = fixtureContext(manifest), loaded = await context.loadFixture()
    assert.equal(loaded.runtime.users.length, manifest.users)
    const facts = manifest.userIds.map((userId, i) => ({ userId, noteId: manifest.multiNoteIds[i], active: i === manifest.users - 1 ? 1 : 0, version: i === manifest.users - 1 ? 1 : 0 }))
    const distribution = context.initialDistribution(manifest, { facts })
    assert.equal(distribution.configuredRelationships, manifest.users); assert.equal(distribution.existing, manifest.users)
    assert.equal(distribution.active, 1); assert.equal(distribution.inactive, manifest.users - 1)
  }
})
await check('actual fixture loader rejects aliases, cardinality mismatch and partial READY credentials', async () => {
  const foreignLegacy = ownedFixture(4096, false)
  await assert.rejects(fixtureContext(foreignLegacy).loadFixture())
  for (const field of ['userIds', 'multiNoteIds']) {
    const duplicate = ownedFixture(4096); duplicate[field][4095] = duplicate[field][0]
    await assert.rejects(fixtureContext(duplicate).loadFixture())
  }
  await assert.rejects(fixtureContext(ownedFixture(4096), 'finish', 5).loadFixture())
  const partial = ownedFixture(4096); partial.status = 'PREPARING'
  assert.equal((await fixtureContext(partial, 'finish', 5).loadFixture()).runtime.users.length, 5)
})
console.log(JSON.stringify({ checks, failed: 0, servicesStarted: false, loadRuns: 0 }))
