import http from 'k6/http'
import { check } from 'k6'
import { Counter, Trend } from 'k6/metrics'
import { SharedArray } from 'k6/data'
import exec from 'k6/execution'

const data = JSON.parse(open('./.p2-seckill.json'))
const tokens = new SharedArray('tokens', () => data.tokens)
const activityId = __ENV.ACTIVITY_ID || data.multiActivityId
const total = Number(__ENV.TOTAL || tokens.length)
const vus = Number(__ENV.VUS || 20)
const iterations = Math.max(1, Math.ceil(total / vus))
const baseUrl = (__ENV.BASE_URL || data.baseUrl || 'http://127.0.0.1:8081').replace(/\/$/, '')

export const options = {
  scenarios: {
    seckill: {
      executor: 'per-vu-iterations',
      vus,
      iterations,
      maxDuration: __ENV.MAX_DURATION || '3m',
    },
  },
  thresholds: {
    checks: ['rate>0.99'],
    http_req_failed: ['rate<0.01'],
  },
  summaryTrendStats: ['avg', 'med', 'p(95)', 'p(99)', 'max'],
}

const accepted = new Counter('seckill_accepted')
const soldOut = new Counter('seckill_sold_out')
const duplicate = new Counter('seckill_duplicate')
const rateLimited = new Counter('seckill_rate_limited')
const otherBusiness = new Counter('seckill_other_business')
const systemError = new Counter('seckill_system_error')
const admission = new Trend('seckill_admission_ms', true)

export default function () {
  const index = exec.scenario.iterationInTest % tokens.length
  const token = tokens[index]
  const response = http.post(`${baseUrl}/voucher-order/seckill/${activityId}`, null, {
    headers: { authorization: token },
    tags: { name: 'seckill' },
  })
  admission.add(response.timings.duration)
  let body = null
  try { body = response.json() } catch { /* non-JSON */ }
  if (response.status === 429) {
    rateLimited.add(1)
  } else if (response.status >= 500 || !body) {
    systemError.add(1)
  } else if (body.success === true) {
    accepted.add(1)
  } else if (String(body.errorMsg || '').includes('库存不足')) {
    soldOut.add(1)
  } else if (String(body.errorMsg || '').includes('重复')) {
    duplicate.add(1)
  } else {
    otherBusiness.add(1)
  }
  check(response, {
    'status is 200 or 429': r => r.status === 200 || r.status === 429,
    'business response parsed': () => body !== null,
  })
}
