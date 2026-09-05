import http from 'k6/http';
import { check, sleep } from 'k6';

const baseURL = __ENV.TARGET_URL || 'http://battlefield.localhost:8888';

export const options = {
  vus: Number(__ENV.K6_VUS || 3),
  duration: __ENV.K6_DURATION || '30s',
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<750'],
    checks: ['rate>0.99'],
  },
};

export function setup() {
  if (!__ENV.ADMIN_USERNAME || !__ENV.ADMIN_PASSWORD) return null;
  const response = http.post(`${baseURL}/v1/auth/login`, JSON.stringify({
    username: __ENV.ADMIN_USERNAME,
    password: __ENV.ADMIN_PASSWORD,
  }), { headers: { 'Content-Type': 'application/json' } });
  check(response, { 'login accepted': (r) => r.status === 204 });
  return response.cookies;
}

export default function (cookies) {
  const parameters = cookies ? { cookies } : {};
  const liveness = http.get(`${baseURL}/livez`, parameters);
  check(liveness, { 'frontend is live': (r) => r.status === 200 });
  const catalog = http.get(`${baseURL}/v1/scenarios`, parameters);
  check(catalog, { 'scenario catalog is available': (r) => r.status === 200 });
  sleep(0.25);
}
