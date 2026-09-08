#!/usr/bin/env bash
# Checks runtime-assurance launchers in isolated workspaces, including paths with spaces
# and forwarded configuration.

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
test_dir="$(mktemp -d "${TMPDIR:-/tmp}/battlefield-assurance-test.XXXXXX")"
trap 'rm -rf -- "${test_dir}"' EXIT

# Exercise the real launchers in an isolated workspace, including paths with spaces.
fixture="${test_dir}/project with spaces"
mkdir -p "${fixture}/scripts/"{perf,security,ci,lib} "${fixture}/tests/performance" "${test_dir}/bin"
cp "${project_dir}/scripts/perf/run-k6.sh" "${fixture}/scripts/perf/"
cp "${project_dir}/scripts/security/zap-baseline.sh" "${fixture}/scripts/security/"
cp "${project_dir}/scripts/ci/tool-versions.env" "${fixture}/scripts/ci/"
cat >"${fixture}/scripts/lib/deployment-environment.sh" <<'STUB'
battlefield_env_preflight() { :; }
STUB
touch "${fixture}/tests/performance/k6-smoke.js" "${fixture}/tests/performance/custom scenario.js"

cat >"${test_dir}/bin/docker" <<'STUB'
#!/usr/bin/env node
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const args = process.argv.slice(2);
const value = (flag) => args[args.indexOf(flag) + 1];
const values = (flag) => args.flatMap((arg, i) => arg === flag ? [args[i + 1]] : []);
const expected = (name, fallback) => process.env[name] || fallback;
assert.deepEqual(args.slice(0, 2), ['run', '--rm']);
assert.equal(value('--network'), 'host');
assert.equal(value('--add-host'), 'battlefield.localhost:127.0.0.1');
assert.equal(value('--user'), `${process.getuid()}:${process.getgid()}`);
const env = Object.fromEntries(values('-e').map((item) => {
  const separator = item.indexOf('=');
  return separator < 0 ? [item, process.env[item] || ''] : [item.slice(0, separator), item.slice(separator + 1)];
}));
const mounts = values('-v');
let reportDir, reports;
if ('TARGET_URL' in env) {
  for (const [name, fallback] of Object.entries({
    TARGET_URL: 'http://battlefield.localhost:8888', K6_VUS: '3', K6_DURATION: '30s',
    ADMIN_USERNAME: '', ADMIN_PASSWORD: '',
  })) assert.equal(env[name], expected(name, fallback));
  assert.ok(args.includes('--summary-export=/reports/k6-summary.json'));
  assert.equal(args.at(-1), '/work/' + expected('EXPECTED_SCENARIO', 'tests/performance/k6-smoke.js'));
  reportDir = mounts.find((m) => m.endsWith(':/reports')).slice(0, -':/reports'.length);
  assert.equal(env.SSL_CERT_FILE, '/certs/ca-bundle.crt');
  assert.ok(mounts.includes(`${fs.realpathSync(expected('K6_CA_BUNDLE', '/etc/ssl/certs/ca-certificates.crt'))}:/certs/ca-bundle.crt:ro`));
  assert.ok(!args.includes('--insecure-skip-tls-verify'));
  reports = ['k6-summary.json'];
} else {
  assert.equal(value('-t'), expected('ZAP_TARGET_URL', 'http://battlefield.localhost:8888'));
  assert.equal(value('-m'), expected('ZAP_SPIDER_MINUTES', '2'));
  assert.equal(env.HOME, '/tmp');
  assert.equal(env.JAVA_TOOL_OPTIONS, '-Duser.home=/tmp');
  assert.equal(value('--workdir'), '/tmp');
  assert.ok(args.includes('-I'));
  reportDir = mounts.find((m) => m.endsWith(':/zap/wrk:rw')).slice(0, -':/zap/wrk:rw'.length);
  reports = ['-J', '-w', '-r'].map(value);
}
for (const report of reports) fs.writeFileSync(path.join(reportDir, report), 'test report\n');
process.exit(Number(expected('SCANNER_EXIT', '0')));
STUB
chmod +x "${test_dir}/bin/docker"
export PATH="${test_dir}/bin:${PATH}"
unset TARGET_URL ZAP_TARGET_URL K6_VUS K6_DURATION ADMIN_USERNAME ADMIN_PASSWORD ZAP_SPIDER_MINUTES SCANNER_EXIT EXPECTED_SCENARIO

unset K6_CA_BUNDLE
bash "${fixture}/scripts/perf/run-k6.sh"
printf 'test CA bundle\n' >"${test_dir}/custom ca.crt"
K6_CA_BUNDLE="${test_dir}/custom ca.crt" bash "${fixture}/scripts/perf/run-k6.sh"
for bundle in "${test_dir}/missing.crt" "${test_dir}/empty.crt"; do
  touch "${test_dir}/empty.crt"
  status=0
  K6_CA_BUNDLE="${bundle}" bash "${fixture}/scripts/perf/run-k6.sh" 2>"${test_dir}/error" || status=$?
  [[ "${status}" == 1 ]]
  grep -q 'k6 CA bundle must be a readable, nonempty file' "${test_dir}/error"
done
bash "${fixture}/scripts/security/zap-baseline.sh"
[[ -s "${fixture}/artifacts/performance/k6-summary.json" ]]
for report in json md html; do
  [[ -s "${fixture}/artifacts/security/zap/zap-report.${report}" ]]
done
TARGET_URL=http://custom.example:9000 K6_VUS=5 K6_DURATION=2s \
  ADMIN_USERNAME=test ADMIN_PASSWORD='dummy password' \
  EXPECTED_SCENARIO='tests/performance/custom scenario.js' \
  bash "${fixture}/scripts/perf/run-k6.sh" 'tests/performance/custom scenario.js'
ZAP_TARGET_URL=http://custom.example:9000 ZAP_SPIDER_MINUTES=1 \
  bash "${fixture}/scripts/security/zap-baseline.sh"

for script in perf/run-k6 security/zap-baseline; do
  status=0
  SCANNER_EXIT=23 bash "${fixture}/scripts/${script}.sh" || status=$?
  [[ "${status}" == 23 ]] || { echo "Scanner failure was lost: ${script}" >&2; exit 1; }
done
status=0
bash "${fixture}/scripts/perf/run-k6.sh" missing.js 2>"${test_dir}/error" || status=$?
[[ "${status}" == 1 ]]
grep -q 'k6 test not found: missing.js' "${test_dir}/error"
# Production resolves deployed credentials without putting them in Docker arguments.
cat >"${test_dir}/bin/kubectl" <<'STUB'
#!/usr/bin/env python3
import base64, json, os
values = {"BATTLEFIELD_AUTH_ENABLED": "true", "BATTLEFIELD_ADMIN_USERNAME": "test-admin",
          "BATTLEFIELD_ADMIN_PASSWORD": "test-password"}
if os.environ.get("MISSING_AUTH"):
    values.pop("BATTLEFIELD_ADMIN_PASSWORD")
print(json.dumps({"data": {k: base64.b64encode(v.encode()).decode() for k,v in values.items()}}))
STUB
chmod +x "${test_dir}/bin/kubectl"
DEPLOY_ENV=production KUBERNETES_CONTEXT=test KUBERNETES_NAMESPACE=battlefield \
  bash "${fixture}/scripts/perf/run-k6.sh"
status=0
DEPLOY_ENV=production KUBERNETES_CONTEXT=test KUBERNETES_NAMESPACE=battlefield MISSING_AUTH=1 \
  bash "${fixture}/scripts/perf/run-k6.sh" 2>"${test_dir}/error" || status=$?
[[ "${status}" == 1 ]]
grep -q 'requires deployed admin credentials' "${test_dir}/error"

# Exercise the real k6 scenario with HTTP doubles without requiring k6 locally.
node --input-type=module - "${project_dir}/tests/performance/k6-smoke.js" <<'JS'
import fs from 'node:fs';
import vm from 'node:vm';
import assert from 'node:assert/strict';
let status = 204;
let cookies = {session: [{value: 'test-session'}]};
const requests = [];
const source = fs.readFileSync(process.argv[2], 'utf8')
  .replace(/^import .*;$/gm, '')
  .replace('export default function', 'function iteration')
  .replaceAll('export ', '');
const context = vm.createContext({
  __ENV: {ADMIN_USERNAME:'test', ADMIN_PASSWORD:'test'},
  http: {post: () => ({status, cookies}), get: (url, params) => {
    requests.push(params.cookies.session); return {status:200};
  }},
  check: (r, checks) => Object.values(checks).every(fn => fn(r)),
  sleep: () => {}, fail: message => {throw new Error(message);},
});
vm.runInContext(source, context);
vm.runInContext('iteration(setup())', context);
assert.deepEqual(requests, ['test-session','test-session']);
status = 401;
assert.throws(() => vm.runInContext('setup()', context), /Login failed with HTTP 401/);
status = 204; cookies = {};
assert.throws(() => vm.runInContext('setup()', context), /no session cookies/);
JS
echo 'Runtime assurance launcher checks passed.'
