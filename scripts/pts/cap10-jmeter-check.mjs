// Offline generated artifact checks only; no JMeter, HTTP, business or cloud.
import assert from 'node:assert/strict'
import { mkdtemp, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve, dirname, basename } from 'node:path'
import { createHash } from 'node:crypto'
import { bundle } from './cap10-jmeter.mjs'

const root = await mkdtemp(join(tmpdir(), 'cap10-cohort-bundle-'))
let checks = 0
try {
  for (const [poolSize, expectedEngines, concurrency] of [[512, 2, 513], [4096, 9, 4105], [17, 3, 20]]) {
    const folder = join(root, String(poolSize))
    const result = await bundle(folder, poolSize === 512 ? {} : { poolSize, expectedEngines })
    assert.equal(result.poolSize, poolSize); assert.equal(result.expectedEngines, expectedEngines)
    assert.equal(result.normalThreads, poolSize); assert.equal(result.ptsConcurrency, concurrency)
    assert.equal(result.teardownThreads, 1); assert.equal(result.csvSplitRequired, true)
    assert.equal(result.clockProtocol, 'dedicated-v1')
    const slots = (await readFile(join(folder, 'slots.csv'), 'utf8')).trim().split('\n')
    assert.equal(slots.shift(), 'slot'); assert.deepEqual(slots.map(Number), Array.from({ length: poolSize }, (_, n) => n))
    const shards = Array.from({ length: expectedEngines }, (_, e) => slots.map(Number).filter(slot => slot % expectedEngines === e))
    assert.equal(new Set(shards.flat()).size, poolSize)
    assert.ok(shards.every(shard => shard.length <= Math.floor(concurrency / expectedEngines)))
    const jmx = await readFile(join(folder, 'cap10-jmeter.jmx'), 'utf8')
    assert.ok(jmx.includes(`<stringProp name="ThreadGroup.num_threads">${poolSize}</stringProp>`))
    for (const [name, value] of [['poolSize', poolSize], ['expectedEngines', expectedEngines], ['clockProtocol', 'dedicated-v1']]) {
      assert.ok(jmx.includes(`<stringProp name="Argument.name">cap10.${name}</stringProp><stringProp name="Argument.value">${value}</stringProp>`))
    }
    assert.equal(result.jmxSHA256, createHash('sha256').update(jmx).digest('hex'))
    assert.equal(result.cohortSourceSHA256['scripts/load/cap10-cohort-contract.mjs'], createHash('sha256').update(await readFile(new URL('../load/cap10-cohort-contract.mjs', import.meta.url))).digest('hex'))
    await assert.rejects(() => bundle(folder, { poolSize, expectedEngines }))
    checks++
  }
  for (const [poolSize, expectedEngines] of [[0, 2], [4097, 9], [true, 2], ['4096', 9], [4096, 0], [4096, 17], [2, 3], [4096, true]]) {
    await assert.rejects(() => bundle(join(root, 'invalid'), { poolSize, expectedEngines }))
    checks++
  }
  console.log(JSON.stringify({ passed: true, checks, scope: 'generated legacy, 4096/9 and 17/3 artifacts only; no business or load' }))
} finally {
  assert.equal(dirname(resolve(root)), resolve(tmpdir()))
  assert.ok(basename(resolve(root)).startsWith('cap10-cohort-bundle-'))
  await rm(root, { recursive: true, force: true })
}
