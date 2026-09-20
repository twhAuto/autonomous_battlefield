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

for command in git tar; do
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
  rm -rf -- "${baseline_dir}"
}
trap cleanup EXIT

git -C "${project_dir}" archive "${base_revision}" | tar -x -C "${baseline_dir}"

export SONAR_PROJECT_KEY="autonomous-battlefield-pr-${pr_number}"
export SONAR_PROJECT_NAME="Autonomous Battlefield PR ${pr_number}"

SONAR_PROJECT_BASE_DIR="${baseline_dir}" \
SONAR_QUALITY_GATE_WAIT=false \
  "${project_dir}/scripts/ci/run-sonarqube-gate.sh" "base-${base_revision}"

"${project_dir}/scripts/ci/run-sonarqube-gate.sh" "pr-${pr_number}"
