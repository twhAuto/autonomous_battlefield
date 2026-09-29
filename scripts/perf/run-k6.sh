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

# Resolve production credentials from the same deployed Secret as smoke verification.
# Disable tracing before handling credentials; mount the private file read-only
# instead of exposing secret values in Docker arguments or environment variables.
set +x
service_mount=()
export BATTLEFIELD_K6_SERVICE_FILE=""
if [[ "${DEPLOY_ENV:-dev}" == production ]]; then
  auth_mode="$(kubectl --context "${KUBERNETES_CONTEXT}" --namespace "${KUBERNETES_NAMESPACE}" \
    --request-timeout=10s get secret "${SMOKE_TEST_AUTH_SECRET_NAME:-battlefield-auth}" \
    -o 'jsonpath={.data.BATTLEFIELD_AUTH_MODE}' | base64 --decode)"
  [[ "${auth_mode}" == cloudflare ]] || {
    echo 'Production k6 requires Cloudflare authentication.' >&2
    exit 1
  }
  : "${BATTLEFIELD_ACCESS_SERVICE_TOKEN_FILE:?Production k6 requires a CI service credential file}"
  ci_client_id="$(kubectl --context "${KUBERNETES_CONTEXT}" --namespace "${KUBERNETES_NAMESPACE}" \
    --request-timeout=10s get secret "${SMOKE_TEST_AUTH_SECRET_NAME:-battlefield-auth}" \
    -o 'jsonpath={.data.BATTLEFIELD_ACCESS_CI_CLIENT_ID}' | base64 --decode)"
  python3 "${project_dir}/scripts/lib/access_service.py" --origin "$TARGET_URL" --client-id "$ci_client_id"
  service_mount=(-v "$(realpath -- "$BATTLEFIELD_ACCESS_SERVICE_TOKEN_FILE"):/run/battlefield-access.json:ro")
  export BATTLEFIELD_K6_SERVICE_FILE=/run/battlefield-access.json
  unset ADMIN_USERNAME ADMIN_PASSWORD
fi
export ADMIN_USERNAME="${ADMIN_USERNAME:-}" ADMIN_PASSWORD="${ADMIN_PASSWORD:-}"

# Containers do not inherit the agent's trusted private certificate authorities.
# Use the Ubuntu agent bundle by default; permit another PEM bundle explicitly.
ca_bundle="${K6_CA_BUNDLE:-/etc/ssl/certs/ca-certificates.crt}"
if [[ ! -f "${ca_bundle}" || ! -r "${ca_bundle}" || ! -s "${ca_bundle}" ]]; then
  echo "k6 CA bundle must be a readable, nonempty file: ${ca_bundle}" >&2
  exit 1
fi
ca_bundle="$(realpath -- "${ca_bundle}")"

# k6 rejects duration combined with stages, so a ramp replaces the fixed VU count.
if [[ -n "${K6_STAGES:-}" ]]; then
  load_shape=(-e K6_STAGES)
else
  load_shape=(-e K6_VUS="${K6_VUS:-3}" -e K6_DURATION="${K6_DURATION:-30s}")
fi

# The kind gateway is published on host loopback, not the Docker bridge.
docker run --rm --network host --add-host battlefield.localhost:127.0.0.1 \
  --user "$(id -u):$(id -g)" \
  -v "${ca_bundle}:/certs/ca-bundle.crt:ro" \
  -e SSL_CERT_FILE=/certs/ca-bundle.crt \
  -e TARGET_URL="${TARGET_URL:-http://battlefield.localhost:8888}" \
  "${load_shape[@]}" -e K6_BATTLES -e K6_BATTLE_TICKS \
  -e ADMIN_USERNAME -e ADMIN_PASSWORD -e BATTLEFIELD_K6_SERVICE_FILE \
  "${service_mount[@]}" \
  -v "${project_dir}:/work:ro" -v "${report_dir}:/reports" \
  "${K6_IMAGE}" run --summary-export=/reports/k6-summary.json "/work/${test_file}"
