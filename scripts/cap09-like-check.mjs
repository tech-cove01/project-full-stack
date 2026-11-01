// Pure public-contract/cancellation boundary checks. No service, token or load.
import assert from 'node:assert/strict'
import { stateFromBody, classifyWrite, pairForVU, unknownBounds } from './load/cap09-like-contract.mjs'

let checks = 0
function check(name, fn) { fn(); checks++; console.log('PASS ' + name) }
const body = (liked, version, likes = 1, noteId = '101') => ({ success: true, data: { noteId, liked, version, likes } })
check('first true is a mutation, not an HTTP-only success', () => assert.equal(classifyWrite(200, body(true, 1), '101', { liked: false, version: 0 }, true).kind, 'mutation'))
check('unlike increments its own relation version', () => assert.equal(classifyWrite(200, body(false, 8, 0), '101', { liked: true, version: 7 }, false).kind, 'mutation'))
check('idempotent repeat is explicitly no-op', () => assert.equal(classifyWrite(200, body(true, 7), '101', { liked: true, version: 7 }, true).kind, 'noop'))
check('unexpected same version cannot be called mutation', () => assert.equal(classifyWrite(200, body(true, 7), '101', { liked: false, version: 7 }, true).kind, 'noop'))
check('version jump signals a second writer or invalid state', () => assert.equal(classifyWrite(200, body(true, 9), '101', { liked: false, version: 7 }, true).kind, 'invalid'))
check('wrong note cannot pass response classification', () => assert.equal(stateFromBody(body(true, 1, 1, '102'), '101'), null))
check('wrong wanted state is invalid even at version+1', () => assert.equal(classifyWrite(200, body(false, 8), '101', { liked: false, version: 7 }, true).kind, 'invalid'))
check('negative or fractional public counts are not valid', () => { assert.equal(stateFromBody(body(true, 1, -1), '101'), null); assert.equal(stateFromBody(body(true, 1, .5), '101'), null) })
check('versions cannot be string, negative or fractional', () => { for (const v of ['1', -1, .5]) assert.equal(stateFromBody(body(true, v), '101'), null) })
check('HTTP timeout/503 preserve effect uncertainty', () => { for (const status of [0, 503]) assert.equal(classifyWrite(status, null, '101', { liked: false, version: 4 }, true).kind, 'uncertain') })
check('business rejection is separate from effect uncertainty', () => assert.equal(classifyWrite(404, { success: false }, '101', { liked: false, version: 0 }, true).kind, 'rejected'))
check('a lost receipt recovers fact before choosing next target', () => {
  const before = { liked: false, version: 4 }, recovered = stateFromBody(body(true, 5), '101')
  assert.equal(classifyWrite(0, null, '101', before, true).kind, 'uncertain')
  assert.equal(!recovered.liked, false)
  assert.equal(classifyWrite(200, body(false, 6, 0), '101', recovered, !recovered.liked).kind, 'mutation')
})
const fixture = { users: [{ userId: '1' }, { userId: '2' }], multiNoteIds: ['101', '102'], hotNoteId: '103',
  initial: [{ userId: '1', noteId: '101', active: 0, version: 0 }, { userId: '2', noteId: '102', active: 1, version: 3 }] }
check('multi VUs own distinct exact relations', () => { const a = pairForVU(fixture, 'multi', 1), b = pairForVU(fixture, 'multi', 2); assert.notEqual(a.user.userId + ':' + a.noteId, b.user.userId + ':' + b.noteId); assert.equal(b.initial.liked, true) })
check('pool exhaustion fails closed instead of aliasing a relation', () => assert.throws(() => pairForVU(fixture, 'multi', 3)))
check('hot still has one distinct user per VU', () => { const hot = { ...fixture, initial: fixture.initial.map(x => ({ ...x, noteId: '103' })) }; assert.equal(pairForVU(hot, 'hot', 2).noteId, '103'); assert.notEqual(pairForVU(hot, 'hot', 1).user.userId, pairForVU(hot, 'hot', 2).user.userId) })
check('private initial state cannot refer to another relation', () => assert.throws(() => pairForVU(fixture, 'hot', 1)))
check('zero uncertainty is exact only with both channels zero', () => assert.deepEqual(unknownBounds(20, 20, 0), { classificationUnknown: 0, unknownLowerBound: 0, unknownUpperBound: 0, unknownIsExact: true, unknown: 0 }))
check('missing terminals alone retain the exact old-style unknown', () => assert.equal(unknownBounds(16133, 15933, 0).unknown, 200))
check('cancel race cannot double-count overlapping uncertainty as exact', () => { const b = unknownBounds(10, 8, 1); assert.equal(b.unknown, null); assert.equal(b.unknownLowerBound, 2); assert.equal(b.unknownUpperBound, 3) })
check('classified uncertain commit remains unknown after recovery', () => assert.equal(unknownBounds(10, 10, 1).unknown, 1))
check('invalid uncertainty counts cannot be clamped into a pass', () => assert.throws(() => unknownBounds(10, 11, 0)))
console.log(JSON.stringify({ checks, failed: 0, servicesStarted: false, loadRuns: 0 }))
