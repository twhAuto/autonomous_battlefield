#!/usr/bin/env bash
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
const mounts = args.flatMap((arg, i) => arg === '--volume' ? [args[i + 1]] : []);
if (args.includes('config') || args.includes('test')) {
  assert.ok(mounts.includes(`${process.env.SECURITY_ARTIFACT_DIR}:/reports:ro`));
  assert.ok(args.includes('/reports/rendered-manifests.yaml'));
  assert.ok(fs.statSync(`${process.env.SECURITY_ARTIFACT_DIR}/rendered-manifests.yaml`).size > 0);
  if (args.includes('test')) assert.ok(mounts.some((m) => m.endsWith('/policy/kubernetes:/policy:ro')));
}
if (process.env.EXPECTED_TRIVY_IMAGE) assert.ok(args.includes(process.env.EXPECTED_TRIVY_IMAGE));
process.stdout.write('{}\n');
process.exit(args.includes(process.env.FAIL_SCANNER_COMMAND) ? 23 : 0);
STUB
cat >"${test_dir}/bin/go" <<'STUB'
#!/usr/bin/env bash
printf '{}\n'
STUB
chmod +x "${test_dir}/bin/"*
export PATH="${test_dir}/bin:${PATH}"
export SECURITY_ARTIFACT_DIR="${test_dir}/reports with spaces"
unset EXPECTED_TRIVY_IMAGE FAIL_SCANNER_COMMAND

# Render real manifests but stub networked scanners. Custom artifact locations
# must reach both Kubernetes scanners and scanner failures must fail the gate.
for FAIL_SCANNER_COMMAND in '' config test; do
  export FAIL_SCANNER_COMMAND
  status=0
  "${project_dir}/scripts/ci/security-and-policy.sh" >"${test_dir}/security.log" 2>&1 || status=$?
  if [[ -z "${FAIL_SCANNER_COMMAND}" ]]; then
    [[ "${status}" == 0 ]] || { cat "${test_dir}/security.log" >&2; exit 1; }
  else
    [[ "${status}" == 1 ]] || { cat "${test_dir}/security.log" >&2; exit 1; }
  fi
done
unset FAIL_SCANNER_COMMAND

# The local scan shares the central pin while retaining its existing override.
source "${project_dir}/scripts/ci/tool-versions.env"
export EXPECTED_TRIVY_IMAGE="${TRIVY_IMAGE}"
unset TRIVY_IMAGE
SCAN_COMPONENTS=backend "${project_dir}/scripts/ci/scan-images.sh" >"${test_dir}/scan.log"
TRIVY_IMAGE=example.invalid/trivy:test EXPECTED_TRIVY_IMAGE=example.invalid/trivy:test \
  SCAN_COMPONENTS=frontend "${project_dir}/scripts/ci/scan-images.sh" >"${test_dir}/scan.log"
echo 'Security launcher artifact paths, failure propagation, and tool pin checks passed.'
