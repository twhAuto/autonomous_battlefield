#!/usr/bin/env bash
# Seed a Community Build preview project from the target, then gate the PR checkout.
set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
pr_number="${1:-}"
target_ref="${2:-}"
if [[ ! "${pr_number}" =~ ^[1-9][0-9]*$ || -z "${target_ref}" ]]; then
  echo "Usage: $0 PR_NUMBER TARGET_REF" >&2
  exit 2
fi

for command in curl git tar; do
  if ! command -v "${command}" >/dev/null 2>&1; then
    echo "Required command not found: ${command}" >&2
    exit 1
  fi
done

base_revision="$(git -C "${project_dir}" merge-base HEAD "${target_ref}")"
temp_root="${TMPDIR:-/tmp}"
mkdir -p -- "${temp_root}"
baseline_dir="$(mktemp -d "${temp_root%/}/battlefield-sonar-base.XXXXXX")"
cleanup() {
  rm -rf -- "${baseline_dir}" "${baseline_dir}.delete-response"
}
trap cleanup EXIT

git -C "${project_dir}" archive "${base_revision}" | tar -x -C "${baseline_dir}"

export SONAR_PROJECT_KEY="autonomous-battlefield-pr-${pr_number}"
export SONAR_PROJECT_NAME="Autonomous Battlefield PR ${pr_number}"

# SonarQube backdates issues in newly added files to the line's commit date, so the
# New Code period must start at the merge-base commit, not at the moment the baseline
# is scanned; otherwise every issue in a new file looks old and escapes the gate. The
# baseline is therefore dated at the merge-base commit, and because SonarQube rejects
# an analysis dated before the project's latest one, each gated scan starts from an
# empty preview project.
base_date="$(git -C "${project_dir}" log -1 --format=%cd --date=format:%Y-%m-%dT%H:%M:%S%z "${base_revision}")"
: "${SONAR_HOST_URL:?SONAR_HOST_URL must name the SonarQube server}"
sonar_token="${SONAR_AUTH_TOKEN:-${SONAR_TOKEN:-}}"
if [[ -z "${sonar_token}" ]]; then
  echo "SONAR_AUTH_TOKEN or SONAR_TOKEN is required to reset the preview project" >&2
  exit 1
fi
delete_response="${baseline_dir}.delete-response"
# The token travels on stdin, never in process arguments.
delete_status="$(
  curl --silent --show-error --request POST \
    --output "${delete_response}" --write-out '%{http_code}' \
    --data-urlencode "project=${SONAR_PROJECT_KEY}" \
    --config - "${SONAR_HOST_URL%/}/api/projects/delete" <<<"user = \"${sonar_token}:\""
)" || delete_status="000"
case "${delete_status}" in
  200 | 204) echo "Reset SonarQube preview project ${SONAR_PROJECT_KEY}." ;;
  404) echo "SonarQube preview project ${SONAR_PROJECT_KEY} does not exist yet." ;;
  *)
    echo "Could not reset SonarQube preview project ${SONAR_PROJECT_KEY} (HTTP ${delete_status}):" >&2
    cat -- "${delete_response}" >&2 2>/dev/null || true
    exit 1
    ;;
esac

SONAR_PROJECT_BASE_DIR="${baseline_dir}" \
SONAR_PROJECT_DATE="${base_date}" \
SONAR_QUALITY_GATE_WAIT=false \
  "${project_dir}/scripts/ci/run-sonarqube-gate.sh" "base-${base_revision}"

"${project_dir}/scripts/ci/run-sonarqube-gate.sh" "pr-${pr_number}"
