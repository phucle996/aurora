import http from 'k6/http';
import { check } from 'k6';

export const options = {
  scenarios: {
    burst: {
      executor: 'ramping-arrival-rate',
      startRate: 5,
      timeUnit: '1s',
      preAllocatedVUs: 20,
      maxVUs: 100,
      stages: [
        { target: 20, duration: '30s' },
        { target: 50, duration: '30s' },
        { target: 0, duration: '20s' },
      ],
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<800'],
  },
};

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const CRITICAL_PATH = __ENV.CRITICAL_PATH || '/admin/critical/ping';

export default function () {
  const res = http.post(`${BASE_URL}${CRITICAL_PATH}`, '{}', {
    headers: {
      'Content-Type': 'application/json',
      'X-Admin-Signature': __ENV.ADMIN_SIGNATURE || '',
      'X-Admin-Timestamp': __ENV.ADMIN_TIMESTAMP || '',
      'X-Admin-Nonce': __ENV.ADMIN_NONCE || '',
      'X-Admin-StepUp-Method': __ENV.STEPUP_METHOD || 'totp',
      'X-Admin-StepUp-Code': __ENV.STEPUP_CODE || '',
    },
  });

  check(res, {
    'critical status is 2xx': (r) => r.status >= 200 && r.status < 300,
  });
}
