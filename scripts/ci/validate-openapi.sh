#!/usr/bin/env bash
# Lints docs/reference/openapi.yaml against the OpenAPI standard with the strict
# Redocly ruleset. Route parity with the served API is checked separately by the
# backend apicontract tests.

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck disable=SC1091
source "${project_dir}/scripts/ci/tool-versions.env"
artifact_dir="${CONTRACT_ARTIFACT_DIR:-${project_dir}/artifacts/contract}"
mkdir -p "${artifact_dir}"

if ! command -v docker >/dev/null 2>&1; then
  echo "Required command not found: docker" >&2
  exit 1
fi

echo "Linting the OpenAPI contract..."
# Telemetry and update checks are disabled so the lint needs no network beyond the pull.
if ! docker run --rm \
  --env REDOCLY_TELEMETRY=off \
  --env REDOCLY_SUPPRESS_UPDATE_NOTICE=true \
  --volume "${project_dir}/docs/reference:/spec:ro" \
  --workdir /spec \
  "${REDOCLY_IMAGE}" \
  lint --config redocly.yaml --format stylish openapi.yaml \
  >"${artifact_dir}/openapi-lint.txt" 2>&1; then
  cat "${artifact_dir}/openapi-lint.txt" >&2
  echo "OpenAPI contract lint failed; see ${artifact_dir}/openapi-lint.txt." >&2
  exit 1
fi
cat "${artifact_dir}/openapi-lint.txt"
echo "OpenAPI contract lint passed."
