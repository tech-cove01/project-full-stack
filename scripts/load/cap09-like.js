import http from 'k6/http'
import exec from 'k6/execution'
import { Counter, Rate, Trend } from 'k6/metrics'
import { stateFromBody, classifyWrite, pairForVU } from './cap09-like-contract.mjs'

// Private input contains only this cohort's tokens and its pre-phase DB facts.
const fixture = JSON.parse(open(__ENV.CAP_LIKE_INPUT))
const mode = __ENV.CAP_LIKE_MODE
const rate = Number(__ENV.CAP_LIKE_RATE), seconds = Number(__ENV.CAP_LIKE_SECONDS)
const phase = __ENV.CAP_LIKE_PHASE || 'stable'
if (!['multi', 'hot'].includes(mode) || !Number.isInteger(rate) || rate < 1 || rate > 5000 ||
    !Number.isInteger(seconds) || seconds < 1 || seconds > 180) throw new Error('invalid bounded like workload')
const abort = phase === 'stable'
const attempted = new Counter('cap_like_attempted')
const completed = new Counter('cap_like_completed')
const success = new Counter('cap_like_success')
const mutation = new Counter('cap_like_mutation')
const noop = new Counter('cap_like_noop')
const rejected = new Counter('cap_like_rejected')
const uncertain = new Counter('cap_like_effect_unknown')
const contentErrors = new Counter('cap_like_content_errors')
const systemErrors = new Counter('cap_like_system_errors')
const httpErrors = new Counter('cap_like_http_errors')
const recovery = new Counter('cap_like_recovery_reads')
const recoveryFailed = new Counter('cap_like_recovery_failed')
const retiredRelations = new Counter('cap_like_retired_relations')
const failureRatio = new Rate('cap_like_failure_ratio')
const systemRatio = new Rate('cap_like_system_error_ratio')
const allLatency = new Trend('cap_like_all_ms', true)
const mutationLatency = new Trend('cap_like_mutation_ms', true)
const recoveryLatency = new Trend('cap_like_recovery_ms', true)
const scenarioStart = new Trend('cap_native_scenario_start_unix_ms')
const scenarioDuration = new Trend('cap_native_scenario_duration_ms')
let relation = null, current = null, retired = false, errorLogged = false

export const options = {
  scenarios: { write: { executor: 'constant-arrival-rate', rate, timeUnit: '1s', duration: `${seconds}s`,
    gracefulStop: '10s', preAllocatedVUs: Number(__ENV.CAP_LIKE_PRE_VUS || 256), maxVUs: Number(__ENV.CAP_LIKE_MAX_VUS || 512) } },
  thresholds: {
    cap_like_attempted: ['count>=0'], cap_like_completed: ['count>=0'], cap_like_success: ['count>=0'],
    cap_like_mutation: ['count>=0'], cap_like_noop: [{ threshold: 'count==0', abortOnFail: abort, delayAbortEval: '2s' }], cap_like_rejected: ['count>=0'],
    cap_like_effect_unknown: [{ threshold: 'count==0', abortOnFail: abort, delayAbortEval: '2s' }], cap_like_system_errors: ['count>=0'], cap_like_http_errors: ['count>=0'],
    cap_like_recovery_reads: ['count>=0'], cap_like_recovery_failed: ['count>=0'],
    cap_like_retired_relations: ['count>=0'],
    'http_reqs{name:like-write}': ['count>=0'], 'http_reqs{name:like-recover}': ['count>=0'],
    dropped_iterations: [{ threshold: 'count==0', abortOnFail: abort, delayAbortEval: '10s' }],
    cap_like_failure_ratio: [{ threshold: 'rate<0.01', abortOnFail: abort, delayAbortEval: '10s' }],
    cap_like_system_error_ratio: [{ threshold: 'rate<0.01', abortOnFail: abort, delayAbortEval: '10s' }],
    cap_like_content_errors: [{ threshold: 'count==0', abortOnFail: abort, delayAbortEval: '2s' }],
    cap_like_all_ms: [{ threshold: 'p(95)<=200', abortOnFail: abort, delayAbortEval: '10s' },
      { threshold: 'p(99)<=500', abortOnFail: abort, delayAbortEval: '10s' }],
  },
  summaryTrendStats: ['avg', 'med', 'min', 'p(95)', 'p(99)', 'max'],
  systemTags: ['status', 'method', 'name', 'scenario', 'error_code', 'expected_response'],
}

function parsed(response) { try { return response.json() } catch (_) { return null } }
function params(name) { return { headers: { authorization: relation.user.token, 'Content-Type': 'application/json' },
  tags: { name, mode, phase }, timeout: '5s' } }

function recoverAndRetire() {
  // A fact read can race an earlier unknown commit. Read for reconciliation,
  // but never issue another write on this relation during the same phase.
  retired = true; retiredRelations.add(1); recovery.add(1)
  const read = http.get(`${fixture.baseUrl}/notes/${relation.noteId}/like`, params('like-recover'))
  recoveryLatency.add(read.timings.duration)
  const state = read.status === 200 ? stateFromBody(parsed(read), relation.noteId) : null
  if (!state) recoveryFailed.add(1)
  else current = state
}

export default function () {
  if (retired) return
  if (!relation) { relation = pairForVU(fixture, mode, exec.vu.idInTest); current = relation.initial; recovery.add(0); recoveryFailed.add(0); retiredRelations.add(0); scenarioStart.add(exec.scenario.startTime); scenarioDuration.add(seconds * 1000) }
  const wanted = !current.liked
  attempted.add(1)
  const response = http.put(`${fixture.baseUrl}/notes/${relation.noteId}/like`, JSON.stringify({ liked: wanted }), params('like-write'))
  allLatency.add(response.timings.duration)
  const outcome = classifyWrite(response.status, parsed(response), relation.noteId, current, wanted)
  const isMutation = outcome.kind === 'mutation'
  const isSystem = response.status === 0 || response.status >= 500
  const invalid = outcome.kind === 'invalid'
  success.add(isMutation || outcome.kind === 'noop' ? 1 : 0)
  mutation.add(isMutation ? 1 : 0); noop.add(outcome.kind === 'noop' ? 1 : 0)
  rejected.add(outcome.kind === 'rejected' ? 1 : 0)
  // Outcome uncertainty is retained even if a later authoritative GET recovers.
  uncertain.add(outcome.kind === 'uncertain' || invalid ? 1 : 0)
  contentErrors.add(invalid ? 1 : 0); systemErrors.add(isSystem ? 1 : 0)
  httpErrors.add(response.status !== 200 ? 1 : 0)
  failureRatio.add(!isMutation); systemRatio.add(isSystem)
  if (isMutation) { mutationLatency.add(response.timings.duration); current = outcome.state }
  else {
    if (!errorLogged) { errorLogged = true; console.warn(JSON.stringify({ phase, mode, vu: exec.vu.idInTest,
      status: response.status, kind: outcome.kind, bodyRetained: false, tokenRetained: false })) }
  }
  // A full-test abort may interrupt before this sample: attempted-completed is
  // kept separately from a completed response with an unknown commit outcome.
  completed.add(1)
  if (!isMutation) recoverAndRetire()
}
