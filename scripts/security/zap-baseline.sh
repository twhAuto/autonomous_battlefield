#!/usr/bin/env bash
set -Eeuo pipefail

project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=../ci/tool-versions.env
source "${project_dir}/scripts/ci/tool-versions.env"
report_dir="${project_dir}/artifacts/security/zap"
mkdir -p "${report_dir}"

# The kind gateway is published on host loopback, not the Docker bridge.
docker run --rm --network host --add-host battlefield.localhost:127.0.0.1 \
  --user "$(id -u):$(id -g)" \
  -e HOME=/tmp -e JAVA_TOOL_OPTIONS=-Duser.home=/tmp --workdir /tmp \
  -v "${report_dir}:/zap/wrk:rw" \
  "${ZAP_IMAGE}" zap-baseline.py \
  -t "${ZAP_TARGET_URL:-http://battlefield.localhost:8888}" \
  -J zap-report.json -w zap-report.md -r zap-report.html \
  -m "${ZAP_SPIDER_MINUTES:-2}" -I
