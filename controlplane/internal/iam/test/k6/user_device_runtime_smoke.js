import http from 'k6/http';
import { check, sleep } from 'k6';

const BASE = __ENV.BASE_URL || 'http://localhost:28000';
const USERNAME = __ENV.IAM_USERNAME || 'demo@example.com';
const PASSWORD = __ENV.IAM_PASSWORD || 'secret123';

export const options = {
  vus: 1,
  duration: '30s',
  thresholds: {
    http_req_failed: ['rate<0.05'],
    http_req_duration: ['p(95)<800'],
  },
};

function mustParseCookies(res) {
  const cookies = res.cookies || {};
  const access = cookies['access_token']?.[0]?.value || '';
  const refresh = cookies['refresh_token']?.[0]?.value || '';
  const deviceId = cookies['device_id']?.[0]?.value || '';
  const deviceSecret = cookies['device_secret']?.[0]?.value || '';
  return { access, refresh, deviceId, deviceSecret };
}

export default function () {
  const loginRes = http.post(`${BASE}/api/v1/auth/login`, JSON.stringify({
    username: USERNAME,
    password: PASSWORD,
  }), {
    headers: { 'Content-Type': 'application/json' },
  });

  check(loginRes, {
    'login 200': (r) => r.status === 200,
  });

  const c = mustParseCookies(loginRes);
  check(c, {
    'cookie access': (v) => !!v.access,
    'cookie refresh': (v) => !!v.refresh,
    'cookie device_id': (v) => !!v.deviceId,
    'cookie device_secret': (v) => !!v.deviceSecret,
  });

  const cookieHeader = `access_token=${c.access}; refresh_token=${c.refresh}; device_id=${c.deviceId}; device_secret=${c.deviceSecret}`;

  const devicesRes = http.get(`${BASE}/api/v1/me/devices?limit=10&offset=0`, {
    headers: { Cookie: cookieHeader },
  });

  check(devicesRes, {
    'devices 200': (r) => r.status === 200,
  });

  const refreshRes = http.post(`${BASE}/api/v1/auth/refresh`, null, {
    headers: { Cookie: cookieHeader },
  });

  check(refreshRes, {
    'refresh 204': (r) => r.status === 204,
  });

  const c2 = mustParseCookies(refreshRes);
  check(c2, {
    'refresh rotate device_id': (v) => !!v.deviceId && v.deviceId !== c.deviceId,
    'refresh rotate device_secret': (v) => !!v.deviceSecret && v.deviceSecret !== c.deviceSecret,
    'refresh rotate access': (v) => !!v.access && v.access !== c.access,
  });

  const cookieHeader2 = `access_token=${c2.access}; refresh_token=${c2.refresh}; device_id=${c2.deviceId}; device_secret=${c2.deviceSecret}`;

  const logoutRes = http.post(`${BASE}/api/v1/auth/logout`, null, {
    headers: { Cookie: cookieHeader2 },
  });

  check(logoutRes, {
    'logout 204': (r) => r.status === 204,
  });

  sleep(1);
}
