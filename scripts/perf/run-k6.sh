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
# Disable tracing before reading credentials; Docker inherits them by name so they
# are not exposed in its command-line arguments.
set +x
if [[ "${DEPLOY_ENV:-dev}" == production && ( -z "${ADMIN_USERNAME:-}" || -z "${ADMIN_PASSWORD:-}" ) ]]; then
  auth_json="$(kubectl --context "${KUBERNETES_CONTEXT}" --namespace "${KUBERNETES_NAMESPACE}" \
    --request-timeout=10s get secret "${SMOKE_TEST_AUTH_SECRET_NAME:-battlefield-auth}" -o json)"
  read_auth_value() {
    printf '%s' "${auth_json}" | python3 -c 'import sys,json,base64; print(base64.b64decode(json.load(sys.stdin).get("data", {}).get(sys.argv[1], "")).decode(), end="")' "$1"
  }
  auth_enabled="$(read_auth_value BATTLEFIELD_AUTH_ENABLED)"
  case "${auth_enabled,,}" in
    true)
      ADMIN_USERNAME="${ADMIN_USERNAME:-$(read_auth_value BATTLEFIELD_ADMIN_USERNAME)}"
      ADMIN_PASSWORD="${ADMIN_PASSWORD:-$(read_auth_value BATTLEFIELD_ADMIN_PASSWORD)}"
      [[ -n "${ADMIN_USERNAME}" && -n "${ADMIN_PASSWORD}" ]] || {
        echo 'Production k6 requires deployed admin credentials when authentication is enabled.' >&2
        exit 1
      }
      ;;
    false) ;;
    *) echo 'Production auth Secret has invalid BATTLEFIELD_AUTH_ENABLED.' >&2; exit 1 ;;
  esac
  unset auth_json
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

# The kind gateway is published on host loopback, not the Docker bridge.
docker run --rm --network host --add-host battlefield.localhost:127.0.0.1 \
  --user "$(id -u):$(id -g)" \
  -v "${ca_bundle}:/certs/ca-bundle.crt:ro" \
  -e SSL_CERT_FILE=/certs/ca-bundle.crt \
  -e TARGET_URL="${TARGET_URL:-http://battlefield.localhost:8888}" \
  -e K6_VUS="${K6_VUS:-3}" -e K6_DURATION="${K6_DURATION:-30s}" \
  -e ADMIN_USERNAME -e ADMIN_PASSWORD \
  -v "${project_dir}:/work:ro" -v "${report_dir}:/reports" \
  "${K6_IMAGE}" run --summary-export=/reports/k6-summary.json "/work/${test_file}"
