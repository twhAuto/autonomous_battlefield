#!/usr/bin/env bash
set -Eeuo pipefail

project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=../ci/tool-versions.env
source "${project_dir}/scripts/ci/tool-versions.env"
test_file="${1:-scripts/perf/k6-smoke.js}"
report_dir="${project_dir}/artifacts/performance"
mkdir -p "${report_dir}"

if [[ ! -f "${project_dir}/${test_file}" ]]; then
  echo "k6 test not found: ${test_file}" >&2
  exit 1
fi

docker run --rm --add-host battlefield.localhost:host-gateway \
  -e TARGET_URL="${TARGET_URL:-http://battlefield.localhost:8888}" \
  -e K6_VUS="${K6_VUS:-3}" -e K6_DURATION="${K6_DURATION:-30s}" \
  -e ADMIN_USERNAME="${ADMIN_USERNAME:-}" -e ADMIN_PASSWORD="${ADMIN_PASSWORD:-}" \
  -v "${project_dir}:/work:ro" -v "${report_dir}:/reports" \
  "${K6_IMAGE}" run --summary-export=/reports/k6-summary.json "/work/${test_file}"
