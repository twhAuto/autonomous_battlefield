// k6 smoke-load scenario for application HTTP requests, with response checks and
// latency/error thresholds.

import http from 'k6/http';
import { check, sleep, fail } from 'k6';

const service = __ENV.BATTLEFIELD_K6_SERVICE_FILE ? JSON.parse(open(__ENV.BATTLEFIELD_K6_SERVICE_FILE)) : null;
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
  if (service) {
    if (!baseURL.startsWith('https://') || baseURL.replace(/\/$/, '') !== service.origin.replace(/\/$/, '')) fail('Service credential destination mismatch');
    return null;
  }
  if (!__ENV.ADMIN_USERNAME || !__ENV.ADMIN_PASSWORD) return null;
  const response = http.post(`${baseURL}/v1/auth/login`, JSON.stringify({
    username: __ENV.ADMIN_USERNAME,
    password: __ENV.ADMIN_PASSWORD,
  }), { headers: { 'Content-Type': 'application/json' } });
  if (!check(response, { 'login accepted': (r) => r.status === 204 })) {
    fail(`Login failed with HTTP ${response.status}`);
  }
  // Response cookies are arrays of objects; request cookies require string values.
  const cookies = {};
  for (const [name, values] of Object.entries(response.cookies)) {
    if (values.length) cookies[name] = values[0].value;
  }
  if (!Object.keys(cookies).length) fail('Login returned no session cookies');
  return cookies;
}

export default function (cookies) {
  const parameters = service
    ? { redirects: 0, headers: { 'CF-Access-Client-Id': service.client_id, 'CF-Access-Client-Secret': service.client_secret } }
    : (cookies ? { cookies } : {});
  const liveness = http.get(`${baseURL}/livez`, parameters);
  check(liveness, { 'frontend is live': (r) => r.status === 200 });
  const catalog = http.get(`${baseURL}/v1/scenarios`, parameters);
  check(catalog, { 'scenario catalog is available': (r) => r.status === 200 });
  sleep(0.25);
}
