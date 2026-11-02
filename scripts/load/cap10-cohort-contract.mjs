// Pure cohort cardinality contract. Historical artifacts remain 512-slot data.
import assert from 'node:assert/strict'

export const DEFAULT_POOL_SIZE = 512
export const MAX_POOL_SIZE = 4096
export const MAX_ENGINES = 16

export function poolSize(value = DEFAULT_POOL_SIZE) {
  assert.ok(Number.isInteger(value) && value >= 1 && value <= MAX_POOL_SIZE, 'bounded integer relationship pool size required')
  return value
}

export function engineCount(value = 2, pool = DEFAULT_POOL_SIZE) {
  poolSize(pool)
  assert.ok(Number.isInteger(value) && value >= 1 && value <= Math.min(MAX_ENGINES, pool), 'bounded integer engine count required')
  return value
}

export function ptsConcurrency(pool = DEFAULT_POOL_SIZE, engines = 2) {
  poolSize(pool); engineCount(engines, pool)
  return pool === 512 && engines === 2 ? 513 : pool + engines
}

export function fixturePoolSize(manifest) {
  assert.ok(manifest && typeof manifest === 'object' && !Array.isArray(manifest), 'fixture object required')
  const count = poolSize(Object.hasOwn(manifest, 'poolSize') ? manifest.poolSize : DEFAULT_POOL_SIZE)
  for (const field of ['users', 'multiNotes', 'multiRelations']) {
    if (Object.hasOwn(manifest, field)) assert.equal(manifest[field], count, 'fixture declared counts differ from its relationship pool')
  }
  for (const field of ['userIds', 'multiNoteIds']) {
    if (Object.hasOwn(manifest, field)) assert.ok(Array.isArray(manifest[field]) && manifest[field].length === count, 'fixture identity arrays differ from its relationship pool')
  }
  return count
}

export function commandPoolSize(command, fixture) {
  assert.ok(command && typeof command === 'object' && !Array.isArray(command), 'reserved command object required')
  const count = poolSize(Object.hasOwn(command, 'poolSize') ? command.poolSize : DEFAULT_POOL_SIZE)
  if (fixture !== undefined) assert.equal(fixturePoolSize(fixture), count, 'reserved command differs from its original fixture pool')
  return count
}
