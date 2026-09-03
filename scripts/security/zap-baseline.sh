#!/usr/bin/env bash
set -Eeuo pipefail

project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=../ci/tool-versions.env
source "${project_dir}/scripts/ci/tool-versions.env"
report_dir="${project_dir}/artifacts/security/zap"
mkdir -p "${report_dir}"

docker run --rm --add-host battlefield.localhost:host-gateway \
  -v "${report_dir}:/zap/wrk:rw" \
  "${ZAP_IMAGE}" zap-baseline.py \
  -t "${ZAP_TARGET_URL:-http://battlefield.localhost:8888}" \
  -J zap-report.json -w zap-report.md -r zap-report.html \
  -m "${ZAP_SPIDER_MINUTES:-2}" -I
