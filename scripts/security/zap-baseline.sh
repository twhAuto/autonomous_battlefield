#!/usr/bin/env bash
# Runs the pinned ZAP baseline scanner against the registered environment's frontend and
# writes security reports.

set -Eeuo pipefail
set +x
umask 077

project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${project_dir}/scripts/lib/deployment-environment.sh"
battlefield_env_preflight
# shellcheck source=../ci/tool-versions.env
source "${project_dir}/scripts/ci/tool-versions.env"
report_dir="${project_dir}/artifacts/security/zap"
mkdir -p "${report_dir}"

if [[ "${DEPLOY_ENV:-dev}" == production ]]; then
  # The scanner never receives credentials or network access. Capture only the
  # allowed CI reads and explicitly verify application content before scanning.
  runtime_dir="$(mktemp -d)"
  trap 'rm -rf -- "${runtime_dir}"' EXIT
  rm -f -- "${report_dir}/zap-report.json" "${report_dir}/zap-report.html" "${report_dir}/coverage.json"
  python3 "${project_dir}/scripts/security/capture-zap-traffic.py" "${runtime_dir}"
  docker run --rm --network none --hostname battlefield-zap --add-host battlefield-zap:127.0.0.1 --user "$(id -u):$(id -g)" \
    -e HOME=/tmp -e JAVA_TOOL_OPTIONS=-Duser.home=/tmp --workdir /tmp \
    -v "${runtime_dir}:/capture:ro" -v "${report_dir}:/zap/wrk:rw" \
    "${ZAP_IMAGE}" zap.sh -cmd -autorun /capture/plan.yaml
  test -s "${report_dir}/zap-report.json"
  python3 - "${report_dir}/zap-report.json" "${TARGET_URL}" <<'PY'
import json, sys
report = json.load(open(sys.argv[1]))
if not any(site.get('@name', '').rstrip('/') == sys.argv[2].rstrip('/') for site in report.get('site', [])):
    raise SystemExit('ZAP report does not contain the authenticated application origin')
PY
  printf '%s\n' '{"mode":"authenticated-passive-capture","paths":["/v1/auth/session","/v1/scenarios","/livez"],"credentialsExported":false,"fullApplicationScan":false}' >"${report_dir}/coverage.json"
  exit 0
fi

# The kind gateway is published on host loopback, not the Docker bridge.
docker run --rm --network host --add-host battlefield.localhost:127.0.0.1 \
  --user "$(id -u):$(id -g)" \
  -e HOME=/tmp -e JAVA_TOOL_OPTIONS=-Duser.home=/tmp --workdir /tmp \
  -v "${report_dir}:/zap/wrk:rw" \
  "${ZAP_IMAGE}" zap-baseline.py \
  -t "${ZAP_TARGET_URL:-http://battlefield.localhost:8888}" \
  -J zap-report.json -w zap-report.md -r zap-report.html \
  -m "${ZAP_SPIDER_MINUTES:-2}" -I
