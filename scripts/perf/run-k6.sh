#!/usr/bin/env bash
# Runs a containerized k6 scenario against the registered environment's gateway and writes
# performance artifacts.

set -Eeuo pipefail

project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${project_dir}/scripts/lib/deployment-environment.sh"
battlefield_env_preflight
# shellcheck source=../ci/tool-versions.env
source "${project_dir}/scripts/ci/tool-versions.env"
test_file="${1:-tests/performance/k6-smoke.js}"
report_dir="${project_dir}/artifacts/performance"
mkdir -p "${report_dir}"

if [[ ! -f "${project_dir}/${test_file}" ]]; then
  echo "k6 test not found: ${test_file}" >&2
  exit 1
fi

# Containers do not inherit the agent's trusted private certificate authorities.
# Use the Ubuntu agent bundle by default; permit another PEM bundle explicitly.
ca_bundle="${K6_CA_BUNDLE:-/etc/ssl/certs/ca-certificates.crt}"
if [[ ! -f "${ca_bundle}" || ! -r "${ca_bundle}" || ! -s "${ca_bundle}" ]]; then
  echo "k6 CA bundle must be a readable, nonempty file: ${ca_bundle}" >&2
  exit 1
fi
ca_bundle="$(realpath -- "${ca_bundle}")"

# The kind gateway is published on host loopback, not the Docker bridge.
docker run --rm --network host --add-host battlefield.localhost:127.0.0.1 \
  --user "$(id -u):$(id -g)" \
  -v "${ca_bundle}:/certs/ca-bundle.crt:ro" \
  -e SSL_CERT_FILE=/certs/ca-bundle.crt \
  -e TARGET_URL="${TARGET_URL:-http://battlefield.localhost:8888}" \
  -e K6_VUS="${K6_VUS:-3}" -e K6_DURATION="${K6_DURATION:-30s}" \
  -e ADMIN_USERNAME="${ADMIN_USERNAME:-}" -e ADMIN_PASSWORD="${ADMIN_PASSWORD:-}" \
  -v "${project_dir}:/work:ro" -v "${report_dir}:/reports" \
  "${K6_IMAGE}" run --summary-export=/reports/k6-summary.json "/work/${test_file}"
