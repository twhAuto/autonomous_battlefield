#!/usr/bin/env bash
# Checks scanner launcher arguments, artifact locations, failure propagation, and bounded
# vulnerability-database download retries.

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
test_dir="$(mktemp -d "${TMPDIR:-/tmp}/battlefield-security-test.XXXXXX")"
trap 'rm -rf -- "${test_dir}"' EXIT
mkdir -p "${test_dir}/bin"

cat >"${test_dir}/bin/docker" <<'STUB'
#!/usr/bin/env node
const assert = require('node:assert/strict');
const fs = require('node:fs');
const args = process.argv.slice(2);
if (args[0] === 'image' && args[1] === 'inspect') process.exit(0);
if (process.env.SCANNER_CALL_LOG) fs.appendFileSync(process.env.SCANNER_CALL_LOG, JSON.stringify(args) + '\n');
const mounts = args.flatMap((arg, i) => arg === '--volume' ? [args[i + 1]] : []);
if (args.includes('--download-db-only') || args.includes('fs')) {
  assert.ok(mounts.includes(`${process.env.TRIVY_CACHE_VOLUME}:/root/.cache/trivy`));
  assert.ok(args.includes('--no-progress'));
  if (args.includes('--download-db-only')) {
    assert.ok(args.includes('--timeout'));
    if (process.env.DB_FAILURE === 'all' || (process.env.DB_FAILURE === 'mirror' && args.includes('mirror.gcr.io/aquasec/trivy-db:2'))) process.exit(23);
  } else {
    assert.ok(args.includes('--skip-db-update'));
    // Vitest removes temporary coverage files while this scan runs in parallel.
    // Exclude only generated reports; frontend source and lockfiles stay in scope.
    const skippedDirs = args.flatMap((arg, i) => arg === '--skip-dirs' ? [args[i + 1]] : []);
    assert.deepEqual(skippedDirs, ['artifacts', 'frontend/coverage', 'frontend/playwright-report', 'frontend/test-results']);
    assert.equal(args.at(-1), '.');
    assert.ok(args.includes('--include-dev-deps'));
  }
}
if (args.includes('config') || args.includes('test')) {
  assert.ok(mounts.includes(`${process.env.SECURITY_ARTIFACT_DIR}:/reports:ro`));
  assert.ok(args.includes('/reports/rendered-manifests.yaml'));
  assert.ok(fs.statSync(`${process.env.SECURITY_ARTIFACT_DIR}/rendered-manifests.yaml`).size > 0);
  if (args.includes('test')) assert.ok(mounts.some((m) => m.endsWith('/infra/deploy/policy/kubernetes:/policy:ro')));
}
if (process.env.EXPECTED_TRIVY_IMAGE) assert.ok(args.includes(process.env.EXPECTED_TRIVY_IMAGE));
process.stdout.write('{}\n');
process.exit(args.includes(process.env.FAIL_SCANNER_COMMAND) ? 23 : 0);
STUB
cat >"${test_dir}/bin/go" <<'STUB'
#!/usr/bin/env node
const assert = require('node:assert/strict');
const fs = require('node:fs');
const args = process.argv.slice(2);
assert.equal(args[0], 'run');
assert.match(args[1], /^golang\.org\/x\/vuln\/cmd\/govulncheck@v/);
fs.appendFileSync(process.env.GO_CALL_LOG, JSON.stringify(args) + '\n');
const report = JSON.stringify({fixture: process.env.GO_SCAN_CASE || 'clean'}) + '\n';
if (args.includes('-json')) {
  assert.equal(args.at(-1), './...');
  process.stdout.write(report);
  // Like real govulncheck JSON mode, findings still exit successfully.
  process.exit(process.env.GO_SCAN_CASE === 'scan-error' ? 23 : 0);
}
assert.equal(args.at(-1), '-mode=convert');
assert.equal(fs.readFileSync(0, 'utf8'), report);
process.stdout.write('Go vulnerability summary\n');
process.exit(process.env.GO_SCAN_CASE === 'finding' ? 3 : process.env.GO_SCAN_CASE === 'convert-error' ? 23 : 0);
STUB
chmod +x "${test_dir}/bin/"*
export PATH="${test_dir}/bin:${PATH}"
export SECURITY_ARTIFACT_DIR="${test_dir}/reports with spaces"
unset EXPECTED_TRIVY_IMAGE FAIL_SCANNER_COMMAND DB_FAILURE GO_SCAN_CASE
export TRIVY_CACHE_VOLUME=battlefield-security-test-cache
export SCANNER_CALL_LOG="${test_dir}/scanner-calls.jsonl"
export GO_CALL_LOG="${test_dir}/go-calls.jsonl"

# JSON findings must fail the gate despite a successful scanner exit. Preserve
# both reports, and fail closed if either scanning or report conversion fails.
for GO_SCAN_CASE in clean finding scan-error convert-error; do
  export GO_SCAN_CASE
  : >"${GO_CALL_LOG}"
  rm -rf -- "${SECURITY_ARTIFACT_DIR}"
  status=0
  "${project_dir}/scripts/ci/security-and-policy.sh" >"${test_dir}/security.log" 2>&1 || status=$?
  expected_status=1
  [[ "${GO_SCAN_CASE}" != clean ]] || expected_status=0
  [[ "${status}" == "${expected_status}" ]] || { cat "${test_dir}/security.log" >&2; exit 1; }
  node <<'CHECK'
const assert = require('node:assert/strict');
const fs = require('node:fs');
const calls = fs.readFileSync(process.env.GO_CALL_LOG, 'utf8').trim().split('\n').map(JSON.parse);
const scanError = process.env.GO_SCAN_CASE === 'scan-error';
assert.equal(calls.length, scanError ? 1 : 2);
assert.ok(calls[0].includes('-json'));
assert.equal(JSON.parse(fs.readFileSync(`${process.env.SECURITY_ARTIFACT_DIR}/govulncheck.json`)).fixture, process.env.GO_SCAN_CASE);
const summary = `${process.env.SECURITY_ARTIFACT_DIR}/govulncheck.txt`;
if (scanError) assert.ok(!fs.existsSync(summary));
else assert.equal(fs.readFileSync(summary, 'utf8'), 'Go vulnerability summary\n');
CHECK
done
unset GO_SCAN_CASE

# Render real manifests but stub networked scanners. Custom artifact locations
# must reach both Kubernetes scanners and scanner failures must fail the gate.
for FAIL_SCANNER_COMMAND in '' fs config test; do
  export FAIL_SCANNER_COMMAND
  status=0
  "${project_dir}/scripts/ci/security-and-policy.sh" >"${test_dir}/security.log" 2>&1 || status=$?
  if [[ -z "${FAIL_SCANNER_COMMAND}" ]]; then
    [[ "${status}" == 0 ]] || { cat "${test_dir}/security.log" >&2; exit 1; }
  else
    [[ "${status}" == 1 ]] || { cat "${test_dir}/security.log" >&2; exit 1; }
    case "${FAIL_SCANNER_COMMAND}" in
      fs) expected_report=trivy-filesystem.json ;;
      config) expected_report=trivy-kubernetes.json ;;
      test) expected_report=conftest.json ;;
    esac
    grep -Fq "failed; see ${SECURITY_ARTIFACT_DIR}/${expected_report}." "${test_dir}/security.log"

  fi
done
unset FAIL_SCANNER_COMMAND

# A transport failure must switch repositories; exhausting both must fail closed.
for DB_FAILURE in mirror all; do
  export DB_FAILURE
  : >"${SCANNER_CALL_LOG}"
  status=0
  "${project_dir}/scripts/ci/security-and-policy.sh" >"${test_dir}/security.log" 2>&1 || status=$?
  if [[ "${DB_FAILURE}" == mirror ]]; then
    [[ "${status}" == 0 ]] || { cat "${test_dir}/security.log" >&2; exit 1; }
  else
    [[ "${status}" == 1 ]] || { cat "${test_dir}/security.log" >&2; exit 1; }
  fi
  node <<'CHECK'
const assert = require('node:assert/strict');
const fs = require('node:fs');
const calls = fs.readFileSync(process.env.SCANNER_CALL_LOG, 'utf8').trim().split('\n').map(JSON.parse);
const downloads = calls.filter(args => args.includes('--download-db-only'));
assert.equal(downloads.length, 2);
assert.ok(downloads[0].includes('mirror.gcr.io/aquasec/trivy-db:2'));
assert.ok(downloads[1].includes('ghcr.io/aquasecurity/trivy-db:2'));
assert.equal(calls.filter(args => args.includes('fs')).length, process.env.DB_FAILURE === 'all' ? 0 : 1);
CHECK
done
unset DB_FAILURE

# The local scan shares the central pin while retaining its existing override.
source "${project_dir}/scripts/ci/tool-versions.env"
export EXPECTED_TRIVY_IMAGE="${TRIVY_IMAGE}"
unset TRIVY_IMAGE
SCAN_COMPONENTS=backend "${project_dir}/scripts/ci/scan-images.sh" >"${test_dir}/scan.log"
TRIVY_IMAGE=example.invalid/trivy:test EXPECTED_TRIVY_IMAGE=example.invalid/trivy:test \
  SCAN_COMPONENTS=frontend "${project_dir}/scripts/ci/scan-images.sh" >"${test_dir}/scan.log"
echo 'Security launcher artifact paths, failure propagation, and tool pin checks passed.'
