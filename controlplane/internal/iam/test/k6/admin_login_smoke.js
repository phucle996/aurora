import http from 'k6/http';
import { check, sleep } from 'k6';

export const options = {
  vus: 1,
  iterations: 1,
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<800'],
  },
};

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

export default function () {
  const payload = JSON.stringify({
    admin_api_key: __ENV.ADMIN_API_KEY || '',
    mfa_method: __ENV.MFA_METHOD || 'totp',
    mfa_code: __ENV.MFA_CODE || '',
    device_public_key: __ENV.DEVICE_PUBLIC_KEY || '',
  });

  const res = http.post(`${BASE_URL}/admin/auth/login`, payload, {
    headers: { 'Content-Type': 'application/json' },
  });

  check(res, {
    'login status is 200': (r) => r.status === 200,
  });

  sleep(1);
}
