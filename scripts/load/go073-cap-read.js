import http from 'k6/http'
import exec from 'k6/execution'
import { SharedArray } from 'k6/data'
import { Counter, Rate, Trend } from 'k6/metrics'

// Credentials live in an OS-temporary input, never in output or metric tags.
const fixture = JSON.parse(open(__ENV.CAP_READ_INPUT))
const readers = new SharedArray('capacity-readers', () => fixture.readers)
const rate = Number(__ENV.CAP_READ_RATE)
const seconds = Number(__ENV.CAP_READ_SECONDS)
const mode = __ENV.CAP_READ_MODE
const phase = __ENV.CAP_READ_PHASE || 'stable'
const abort = __ENV.CAP_READ_ABORT !== '0' && phase === 'stable'
if (!Number.isInteger(rate) || rate < 1 || rate > 20000 || !Number.isInteger(seconds) || seconds < 1 || seconds > 600 || !['hot', 'multi'].includes(mode)) throw new Error('invalid read load settings')

const attempted = new Counter('cap_read_attempted')
const completed = new Counter('cap_read_completed')
const success = new Counter('cap_read_success')
const systemErrors = new Counter('cap_read_system_errors')
const httpErrors = new Counter('cap_read_http_errors')
const contentErrors = new Counter('cap_read_content_errors')
const systemErrorRatio = new Rate('cap_read_system_error_ratio')
const failureRatio = new Rate('cap_read_failure_ratio')
const allLatency = new Trend('cap_read_all_ms', true)
const successfulLatency = new Trend('cap_read_success_ms', true)
let errorLogged = false

export const options = {
  scenarios: { read: { executor: 'constant-arrival-rate', rate, timeUnit: '1s', duration: `${seconds}s`, gracefulStop: '10s', preAllocatedVUs: Number(__ENV.CAP_READ_PRE_VUS || 64), maxVUs: Number(__ENV.CAP_READ_MAX_VUS || 512) } },
  thresholds: {
    // These force zero counters to appear, so absent data cannot be called zero.
    cap_read_attempted: ['count>=0'], cap_read_completed: ['count>=0'], cap_read_success: ['count>=0'],
    cap_read_system_errors: ['count>=0'], cap_read_http_errors: ['count>=0'],
    dropped_iterations: [{ threshold: 'count==0', abortOnFail: abort, delayAbortEval: '10s' }],
    cap_read_system_error_ratio: [{ threshold: 'rate<0.01', abortOnFail: abort, delayAbortEval: '10s' }],
    cap_read_failure_ratio: [{ threshold: 'rate<0.01', abortOnFail: abort, delayAbortEval: '10s' }],
    cap_read_content_errors: [{ threshold: 'count==0', abortOnFail: abort, delayAbortEval: '2s' }],
    cap_read_all_ms: [{ threshold: 'p(95)<=200', abortOnFail: abort, delayAbortEval: '10s' }, { threshold: 'p(99)<=500', abortOnFail: abort, delayAbortEval: '10s' }],
  },
  summaryTrendStats: ['avg', 'med', 'p(95)', 'p(99)', 'max'],
  // IDs, paths and users are intentionally excluded: metrics stay bounded.
  systemTags: ['status', 'method', 'name', 'scenario', 'error_code', 'expected_response'],
}

export default function () {
  const readerIndex = mode === 'hot' ? 0 : exec.scenario.iterationInTest % readers.length
  const reader = readers[readerIndex]
  attempted.add(1)
  // Each VU has at most one bounded diagnostic; success requests do not log.
  const response = http.get(`${fixture.baseUrl}/feed/notes?limit=${fixture.pageSize}`, {
    headers: { authorization: reader.token }, tags: { name: 'feed-20', phase, mode }, timeout: '5s',
  })
  allLatency.add(response.timings.duration)
  let parsed = null
  try { parsed = response.json() } catch (_) { /* counted below */ }
  const isSystemError = response.status === 0 || response.status >= 500 || parsed === null
  const isHTTPError = response.status !== 200
  const items = parsed && parsed.success === true && parsed.data ? parsed.data.items : null
  const correct = Array.isArray(items) && items.length === fixture.pageSize && parsed.data.source === 'database' && items.every((item, i) => item.id === reader.expectedIds[i] && item.userId === reader.authorId && item.liked === 0 && item.comments === 0 && item.likedByMe === false && item.shopId === '1')
  systemErrors.add(isSystemError ? 1 : 0)
  systemErrorRatio.add(isSystemError)
  httpErrors.add(isHTTPError ? 1 : 0)
  contentErrors.add(!correct && !isSystemError && !isHTTPError ? 1 : 0)
  failureRatio.add(isSystemError || isHTTPError || !correct)
  success.add(!isSystemError && !isHTTPError && correct ? 1 : 0)
  if (!isSystemError && !isHTTPError && correct) {
    successfulLatency.add(response.timings.duration)
  } else if (!errorLogged) {
    errorLogged = true
    console.warn(JSON.stringify({ phase, mode, readerIndex, status: response.status, systemError: isSystemError, httpError: isHTTPError, contentError: !correct, bodyRetained: false }))
  }
  // A threshold/resource abort may interrupt HTTP calls. Started iterations
  // without this terminal classification remain explicitly unknown.
  completed.add(1)
}
