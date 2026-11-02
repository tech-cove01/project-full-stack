// Pure audit policy. These budgets do not change native-window sampling or recovery.
import assert from 'node:assert/strict'

export function observerPolicy(env = {}) {
  assert.ok(env && typeof env === 'object' && !Array.isArray(env), 'observer environment object required')
  const mode = Object.hasOwn(env, 'CAP10_OBSERVER_MODE') ? env.CAP10_OBSERVER_MODE : 'full-history-v1'
  assert.ok(mode === 'full-history-v1' || mode === 'like-vector-v1', 'explicit bounded observer mode required')
  return Object.freeze({ mode, fullAuditTimeoutMs: mode === 'full-history-v1' ? 30000 : 120000,
    scope: 'full-snapshot-audit-only' })
}
