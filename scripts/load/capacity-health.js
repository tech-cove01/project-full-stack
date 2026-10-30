// Generator/exporter calibration only: these are NOT business throughput results.
// Match trade's per-completion unique record tag and buffered JSON output path.
import http from 'k6/http'
import exec from 'k6/execution'
import { Counter, Trend } from 'k6/metrics'

const rate = Number(__ENV.CAP_RATE)
if (![100, 400].includes(rate)) throw new Error('fixed calibration rate required')
export const options = {
  systemTags: ['status', 'method', 'name', 'scenario'],
  scenarios: { stable: { executor: 'constant-arrival-rate', rate, timeUnit: '1s', duration: '15s',
    preAllocatedVUs: 200, maxVUs: 800, gracefulStop: '10s' } },
  thresholds: { dropped_iterations: ['count>=0'], cap_requests: ['count>=0'] },
  summaryTrendStats: ['avg', 'med', 'p(95)', 'p(99)', 'max'],
}
const records = new Counter('cap_request_record')
const requests = new Counter('cap_requests')
const errors = new Counter('cap_system_errors')
const allLatency = new Trend('cap_admission_ms', true)
export default function () {
  const started = Date.now()
  const response = http.get('http://127.0.0.1:8250/health/live', {
    tags: { name: 'capacity-generator-calibration', phase: 'stable' }, timeout: '10s', redirects: 0,
  })
  let body
  try { body = response.json() } catch (_) { /* classified below */ }
  const valid = response.status === 200 && body?.status === 'UP' && body?.version === __ENV.CAP_API_VERSION
  requests.add(1)
  errors.add(valid ? 0 : 1)
  allLatency.add(response.timings.duration, { phase: 'stable' })
  records.add(1, { record: JSON.stringify({ phase: 'stable', index: exec.scenario.iterationInTest,
    started, ended: Date.now(), durationMs: response.timings.duration, blockedMs: response.timings.blocked,
    status: response.status, category: valid ? 'health_only' : 'system_error', orderId: '' }) })
}
