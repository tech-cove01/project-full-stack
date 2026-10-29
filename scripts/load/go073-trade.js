import http from 'k6/http'
import exec from 'k6/execution'
import { SharedArray } from 'k6/data'

// The input file is created in the OS temporary directory by the orchestrator.
// Never put credentials, response bodies, or headers in a log or metric tag.
const input = JSON.parse(open(__ENV.GO073_INPUT))
const tokens = new SharedArray('go073 tokens', () => input.tokens)
const rate = Number(__ENV.GO073_RATE || 5)
const warmSeconds = Number(__ENV.GO073_WARM_SECONDS || 15)
const stableSeconds = Number(__ENV.GO073_STABLE_SECONDS || 180)
// k6 may start a boundary iteration at the nominal scenario endpoint.
const warmSlots = rate * (warmSeconds + 2) + 10

export const options = {
  // Force k6 to export the counter even when its actual value is zero.
  thresholds: { dropped_iterations: ['count>=0'] },
  scenarios: {
    warm: { executor: 'constant-arrival-rate', exec: 'warm', rate, timeUnit: '1s', duration: `${warmSeconds}s`, preAllocatedVUs: 20, maxVUs: 60 },
    stable: { executor: 'constant-arrival-rate', exec: 'stable', startTime: `${warmSeconds}s`, rate, timeUnit: '1s', duration: `${stableSeconds}s`, preAllocatedVUs: 20, maxVUs: 60 },
  },
  summaryTrendStats: ['avg', 'med', 'p(95)', 'p(99)', 'max'],
}

function run(phase, offset) {
  const index = offset + exec.scenario.iterationInTest
  if (phase === 'warm' && index >= warmSlots) throw new Error('warm user partition exhausted')
  if (index >= tokens.length) throw new Error('insufficient unique users')
  const started = Date.now()
  const response = http.post(`${input.baseUrl}/voucher-order/seckill/${input.activityId}`, null, {
    headers: { authorization: tokens[index] }, tags: { phase }, timeout: '10s',
  })
  const ended = Date.now()
  let body = null
  try { body = response.json() } catch (_) { /* captured as system error */ }
  let category = 'system_error'
  if (response.status === 200 && body && body.success === true && typeof body.data === 'string' && /^[1-9][0-9]*$/.test(body.data)) category = 'success'
  else if (response.status === 200 && body && body.success === false) {
    const message = String(body.errorMsg || '')
    if (message.includes('库存不足')) category = 'sold_out'
    else if (message.includes('已有有效订单') || message.includes('重复')) category = 'duplicate'
    else category = 'other_business'
  } else if (response.status === 429) category = 'rate_limited'
  console.log(JSON.stringify({ phase, index, started, ended, durationMs: response.timings.duration,
    status: response.status, category, orderId: category === 'success' ? body.data : null }))
}

export function warm() { run('warm', 0) }
export function stable() { run('stable', warmSlots) }
