import assert from 'node:assert/strict'
import { poolSize, engineCount, ptsConcurrency, fixturePoolSize, commandPoolSize } from './cap10-cohort-contract.mjs'

assert.equal(fixturePoolSize({ users: 512, multiNotes: 512, multiRelations: 512 }), 512)
assert.equal(commandPoolSize({ tag: 'H1' }), 512)
assert.equal(ptsConcurrency(512, 2), 513)
assert.equal(ptsConcurrency(4096, 9), 4105)
assert.equal(ptsConcurrency(17, 3), 20)
assert.ok(Math.floor(4105 / 9) >= Math.ceil(4096 / 9))
for (const value of [0, -1, 4097, true, null, '4096', NaN, Infinity]) assert.throws(() => poolSize(value))
for (const value of [0, 17, true, null, 2.5]) assert.throws(() => engineCount(value, 4096))
assert.throws(() => engineCount(3, 2))
assert.throws(() => commandPoolSize({}, { poolSize: 4096 }))
assert.throws(() => commandPoolSize({ poolSize: 4096 }, {}))
for (const fixture of [{ poolSize: null }, { poolSize: 17, users: 512 }, { poolSize: 17, userIds: ['1'] }, { poolSize: 17, multiNotes: true }]) assert.throws(() => fixturePoolSize(fixture))
console.log(JSON.stringify({ passed: true, scope: 'pure legacy and bounded cohort cardinality; no business or load' }))
