#!/usr/bin/env bash
# Checks the OpenAPI lint launcher with a docker double: the pinned image, a read-only
# spec mount, the strict config, telemetry opt-out, the archived report, and failure
# propagation.
set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck disable=SC1091
source "${project_dir}/scripts/ci/tool-versions.env"
test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT

mkdir -p "${test_dir}/bin"
cat >"${test_dir}/bin/docker" <<'DOCKER'
#!/usr/bin/env bash
printf '%s\n' "$@" >"${DOCKER_ARGS_FILE}"
echo "redocly output"
[[ "${DOCKER_FAIL:-0}" == "0" ]]
DOCKER
chmod +x "${test_dir}/bin/docker"
export PATH="${test_dir}/bin:${PATH}"
export DOCKER_ARGS_FILE="${test_dir}/docker-args"

fail() {
  echo "test-openapi-contract: $*" >&2
  exit 1
}

has_arg() {
  grep -qxF -- "$1" "${DOCKER_ARGS_FILE}" || fail "docker was not called with: $1"
}

# A clean lint passes and archives the report.
CONTRACT_ARTIFACT_DIR="${test_dir}/pass" "${project_dir}/scripts/ci/validate-openapi.sh" >"${test_dir}/pass.log" 2>&1 \
  || fail "a clean lint did not pass: $(cat "${test_dir}/pass.log")"
has_arg "${REDOCLY_IMAGE}"
[[ "${REDOCLY_IMAGE}" == *@sha256:* ]] || fail "REDOCLY_IMAGE is not pinned by digest: ${REDOCLY_IMAGE}"
has_arg "${project_dir}/docs/reference:/spec:ro"
has_arg "REDOCLY_TELEMETRY=off"
has_arg "redocly.yaml"
has_arg "openapi.yaml"
grep -q "redocly output" "${test_dir}/pass/openapi-lint.txt" || fail "the lint report was not archived"

# A failing lint must fail the launcher and still leave the report behind.
if DOCKER_FAIL=1 CONTRACT_ARTIFACT_DIR="${test_dir}/fail" \
  "${project_dir}/scripts/ci/validate-openapi.sh" >"${test_dir}/fail.log" 2>&1; then
  fail "a failing lint was reported as passing"
fi
grep -q "OpenAPI contract lint failed" "${test_dir}/fail.log" || fail "the failure was not reported"
[[ -s "${test_dir}/fail/openapi-lint.txt" ]] || fail "the failing report was not archived"

# The strict ruleset is what turns lint warnings into failures.
grep -qx -- "  - recommended-strict" "${project_dir}/docs/reference/redocly.yaml" \
  || fail "redocly.yaml no longer extends recommended-strict"

echo 'OpenAPI contract launcher, pinning, report archiving, and failure propagation checks passed.'
