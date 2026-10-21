// k6 representative-load scenario: queues the admitted number of scripted battles and
// replays the battle page's one-second polling (status, events, decisions) per viewer.

import http from 'k6/http';
import { check, sleep, fail } from 'k6';
import { Trend, Counter, Rate } from 'k6/metrics';

const baseURL = __ENV.TARGET_URL || 'http://battlefield.localhost:8888';
// Admission allows two unfinished battles per person, so one identity drives two battles.
const battleCount = Number(__ENV.K6_BATTLES || 2);
// Battles run one after another, so the default lets both finish inside a 10-minute run.
const battleTicks = Number(__ENV.K6_BATTLE_TICKS || 270);
// Stable IDs let reruns replay faction creation instead of accumulating profiles.
const factionIds = ['6b1f0c52-3c55-4f4e-9d59-8a0c0e8f0a01', '6b1f0c52-3c55-4f4e-9d59-8a0c0e8f0a02'];

// Recorded at teardown from server timestamps. One engine worker runs one battle at a
// time, so later admitted battles wait in the queue.
const queueWait = new Trend('battle_queue_wait_seconds');
// Wall-clock run time divided by requested ticks; 1.0 means exactly one tick per second.
const paceRatio = new Trend('battle_pace_ratio');
const eventsRead = new Counter('battle_events_read');
// Its own threshold: one failed battle would barely move the shared checks rate.
const battlesCompleted = new Rate('battles_completed');

export const options = {
  vus: Number(__ENV.K6_VUS || 20),
  duration: __ENV.K6_DURATION || '10m',
  setupTimeout: '1m',
  teardownTimeout: '1m',
  thresholds: {
    http_req_failed: ['rate<0.01'],
    // Matches the API latency SLI threshold in docs/operations/observability.md.
    'http_req_duration{expected_response:true}': ['p(95)<500'],
    checks: ['rate>0.99'],
    battle_pace_ratio: ['max<1.1'],
    battles_completed: ['rate==1'],
  },
};

function jsonParams(cookies, name) {
  return { cookies, headers: { 'Content-Type': 'application/json' }, tags: { name } };
}

function login() {
  if (!__ENV.ADMIN_USERNAME || !__ENV.ADMIN_PASSWORD) fail('ADMIN_USERNAME and ADMIN_PASSWORD are required');
  const response = http.post(`${baseURL}/v1/auth/login`, JSON.stringify({
    username: __ENV.ADMIN_USERNAME,
    password: __ENV.ADMIN_PASSWORD,
  }), { headers: { 'Content-Type': 'application/json' } });
  if (response.status !== 204) fail(`Login failed with HTTP ${response.status}`);
  const cookies = {};
  for (const [name, values] of Object.entries(response.cookies)) {
    if (values.length) cookies[name] = values[0].value;
  }
  return cookies;
}

export function setup() {
  const cookies = login();
  factionIds.forEach((id, index) => {
    const response = http.post(`${baseURL}/v1/factions`, JSON.stringify({
      id,
      name: `Load Test ${index + 1}`,
      doctrine: 'Scripted load-test faction; balance economy and defense.',
    }), jsonParams(cookies, 'create faction'));
    if (response.status !== 201) fail(`Faction setup failed with HTTP ${response.status}`);
  });
  const battles = [];
  for (let index = 0; index < battleCount; index += 1) {
    const id = crypto.randomUUID();
    const response = http.post(`${baseURL}/v1/battles`, JSON.stringify({
      id,
      scenarioId: 'frontier-siege',
      participants: factionIds.map((factionId) => ({ factionId, controller: { type: 'scripted' } })),
      tickCount: battleTicks,
    }), jsonParams(cookies, 'queue battle'));
    if (response.status !== 202) {
      fail(`Battle ${index + 1} was not admitted (HTTP ${response.status}); finish or stop unfinished battles first`);
    }
    battles.push(id);
  }
  return { cookies, battles };
}

// Per-VU viewer state, mirroring one open battle page.
let cursor = 0;
let attempt = 0;

export default function (data) {
  const battleId = data.battles[(__VU - 1) % data.battles.length];
  const params = (name) => ({ cookies: data.cookies, tags: { name } });

  const status = http.get(`${baseURL}/v1/battles/${battleId}`, params('battle status'));
  check(status, { 'battle status is available': (r) => r.status === 200 });
  if (status.status !== 200) {
    sleep(1);
    return;
  }
  const battle = status.json();
  // A reclaimed battle restarts its event sequence under the new attempt.
  if (battle.attempt !== attempt) cursor = 0;
  attempt = battle.attempt;
  // The battle page stops polling a settled battle; its viewer browses history instead.
  if (!['queued', 'running'].includes(battle.status)) {
    const history = http.get(`${baseURL}/v1/battles?limit=25`, params('battle history'));
    check(history, { 'battle history is available': (r) => r.status === 200 });
    sleep(1);
    return;
  }
  // The battle page reads per-attempt data only after the engine claims the battle.
  if (attempt < 1) {
    sleep(1);
    return;
  }

  const events = http.get(
    `${baseURL}/v1/battles/${battleId}/events?attempt=${attempt}&after=${cursor}&limit=50`,
    params('battle events'),
  );
  check(events, { 'battle events are available': (r) => r.status === 200 });
  if (events.status === 200) {
    const page = events.json();
    eventsRead.add(page.events.length);
    if (page.nextCursor !== null) cursor = page.nextCursor;
  }

  const decisions = http.get(`${baseURL}/v1/battles/${battleId}/decisions?attempt=${attempt}`, params('battle decisions'));
  check(decisions, { 'decision history is available': (r) => r.status === 200 });

  // Occasionally browse, as a viewer returning to the history page would.
  if (__ITER % 30 === 0) {
    const history = http.get(`${baseURL}/v1/battles?limit=25`, params('battle history'));
    check(history, { 'battle history is available': (r) => r.status === 200 });
  }
  sleep(1);
}

export function teardown(data) {
  // Battle detail omits timestamps; the history summaries carry them.
  const history = http.get(`${baseURL}/v1/battles?limit=100`, { cookies: data.cookies, tags: { name: 'battle history' } });
  const summaries = history.status === 200 ? history.json().battles : [];
  for (const battleId of data.battles) {
    const battle = summaries.find((summary) => summary.id === battleId) || {};
    console.log(`battle ${battleId}: status=${battle.status} tick=${battle.currentTick}/${battleTicks} created=${battle.createdAt} started=${battle.startedAt} completed=${battle.completedAt}`);
    battlesCompleted.add(battle.status === 'completed');
    if (battle.startedAt) queueWait.add((Date.parse(battle.startedAt) - Date.parse(battle.createdAt)) / 1000);
    if (battle.status === 'completed' && battle.completedAt) {
      paceRatio.add((Date.parse(battle.completedAt) - Date.parse(battle.startedAt)) / 1000 / battle.currentTick);
    }
    // Release admission slots for battles the run did not finish.
    if (battle.status === 'queued' || battle.status === 'running') {
      http.post(`${baseURL}/v1/battles/${battleId}/stop`, null, { cookies: data.cookies, tags: { name: 'stop battle' } });
    }
  }
}
