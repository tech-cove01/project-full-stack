import http from 'k6/http'
import { check } from 'k6'
import { Counter, Trend } from 'k6/metrics'
import { SharedArray } from 'k6/data'

const data = JSON.parse(open('./.p2-seckill.json'))
const tokens = new SharedArray('tokens', () => data.tokens)
const activityId = __ENV.ACTIVITY_ID || data.duplicateActivityId
const baseUrl = (__ENV.BASE_URL || data.baseUrl || 'http://127.0.0.1:8081').replace(/\/$/, '')
const vus = Number(__ENV.VUS || 20)

export const options = {
  scenarios: {
    duplicate: {
      executor: 'per-vu-iterations',
      vus,
      iterations: 1,
      maxDuration: '1m',
    },
  },
  thresholds: {
    checks: ['rate>0.99'],
  },
  summaryTrendStats: ['avg', 'med', 'p(95)', 'p(99)', 'max'],
}

const accepted = new Counter('duplicate_accepted')
const duplicate = new Counter('duplicate_rejected')
const rateLimited = new Counter('duplicate_rate_limited')
const systemError = new Counter('duplicate_system_error')
const admission = new Trend('duplicate_admission_ms', true)

export default function () {
  const token = tokens[0]
  const response = http.post(`${baseUrl}/voucher-order/seckill/${activityId}`, null, {
    headers: { authorization: token },
    tags: { name: 'duplicate-seckill' },
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
  } else if (String(body.errorMsg || '').includes('重复')) {
    duplicate.add(1)
  } else {
    systemError.add(1)
  }
  check(response, {
    'status is 200 or 429': r => r.status === 200 || r.status === 429,
    'business response parsed': () => body !== null,
  })
}
