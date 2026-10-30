// GO-073-CAP. k6 still schedules open-loop arrivals. Its buffered JSON exporter
// carries one redacted record per completed business request; no per-request
// console or credential-bearing metric tags are used.
import http from 'k6/http'
import exec from 'k6/execution'
import { SharedArray } from 'k6/data'
import { Counter, Trend } from 'k6/metrics'

const tokens = new SharedArray('capacity-trade-sessions', () => JSON.parse(open(__ENV.CAP_INPUT)).tokens)
const rate = Number(__ENV.CAP_RATE)
const warm = Number(__ENV.CAP_WARM_SECONDS)
const seconds = Number(__ENV.CAP_SECONDS)
const warmSlots = rate * (warm + 1) + 10
const pre = Number(__ENV.CAP_PRE_VUS)
const max = Number(__ENV.CAP_MAX_VUS)
const base = __ENV.CAP_API_BASE
const voucher = __ENV.CAP_ACTIVITY_ID
if (!/^http:\/\/127\.0\.0\.1:8250$/.test(base) || !/^[1-9]\d*$/.test(voucher)) throw new Error('dedicated capacity trade environment required')

export const options = {
  systemTags: ['status', 'method', 'name', 'scenario'],
  thresholds: { dropped_iterations: ['count>=0'], cap_requests: ['count>=0'] },
  scenarios: {
    warm: { executor: 'constant-arrival-rate', exec: 'warmPhase', rate, timeUnit: '1s', duration: `${warm}s`, preAllocatedVUs: pre, maxVUs: max, gracefulStop: '10s' },
    stable: { executor: 'constant-arrival-rate', exec: 'stablePhase', startTime: `${warm + 10}s`, rate, timeUnit: '1s', duration: `${seconds}s`, preAllocatedVUs: pre, maxVUs: max, gracefulStop: '10s' },
  },
  summaryTrendStats: ['avg', 'med', 'p(95)', 'p(99)', 'max'],
}
const records = new Counter('cap_request_record')
const requests = new Counter('cap_requests')
const errors = new Counter('cap_system_errors')
const accepted = new Counter('cap_accepted')
const allLatency = new Trend('cap_admission_ms', true)
const acceptedLatency = new Trend('cap_accepted_ms', true)

function run(phase, offset) {
  const index = offset + exec.scenario.iterationInTest
  if ((phase === 'warm' && index >= warmSlots) || index >= tokens.length) throw new Error('unique user partition exhausted')
  const started = Date.now()
  const response = http.post(`${base}/voucher-order/seckill/${voucher}`, null, {
    headers: { authorization: tokens[index] }, tags: { name: 'capacity-trade', phase }, timeout: '10s', redirects: 0,
  })
  const ended = Date.now()
  let body = null
  try { body = response.json() } catch (_) { /* classified below */ }
  let category = 'system_error'
  if (response.status === 200 && body?.success === true && typeof body.data === 'string' && /^[1-9]\d*$/.test(body.data)) category = 'accepted'
  else if (response.status === 429) category = 'rate_limited'
  else if (response.status === 200 && body?.success === false) {
    const text = String(body.errorMsg || '')
    if (text.includes('库存不足')) category = 'sold_out'
    else if (text.includes('已有有效订单') || text.includes('重复')) category = 'duplicate'
    else category = 'other_business'
  }
  requests.add(1)
  errors.add(category === 'system_error' ? 1 : 0)
  accepted.add(category === 'accepted' ? 1 : 0)
  allLatency.add(response.timings.duration, { phase })
  if (category === 'accepted') acceptedLatency.add(response.timings.duration, { phase })
  const record = { phase, index, started, ended, durationMs: response.timings.duration,
    blockedMs: response.timings.blocked, status: response.status, category,
    orderId: category === 'accepted' ? body.data : '' }
  records.add(1, { record: JSON.stringify(record) })
}
export function warmPhase() { run('warm', 0) }
export function stablePhase() { run('stable', warmSlots) }
