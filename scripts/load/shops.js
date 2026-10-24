import http from 'k6/http';
import { check, sleep } from 'k6';

export const options = {
  vus: Number(__ENV.VUS || 20),
  duration: __ENV.DURATION || '30s',
  thresholds: {
    http_req_failed: ['rate<0.01'],
    checks: ['rate>0.99'],
    http_req_duration: [`p(95)<${Number(__ENV.SLO_P95_MS || 500)}`],
  },
  summaryTrendStats: ['avg', 'med', 'p(95)', 'p(99)', 'max'],
};

const baseUrl = (__ENV.BASE_URL || 'http://127.0.0.1:8081').replace(/\/$/, '');

export default function () {
  const response = http.get(`${baseUrl}/shop/1`);
  check(response, {
    'HTTP 200': (r) => r.status === 200,
    'shop found': (r) => {
      try { return r.json('success') === true && r.json('data.id') === '1'; }
      catch { return false; }
    },
  });
  sleep(0.1);
}
