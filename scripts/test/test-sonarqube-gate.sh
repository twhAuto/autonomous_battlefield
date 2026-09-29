#!/usr/bin/env bash
set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
runner="${project_dir}/scripts/ci/run-sonarqube-gate.sh"
pr_runner="${project_dir}/scripts/ci/run-sonarqube-pr-gate.sh"
temp_dir="$(mktemp -d)"
cleanup() {
  rm -rf -- "${temp_dir}"
}
trap cleanup EXIT

mkdir -p "${temp_dir}/scanner/bin"
cat >"${temp_dir}/scanner/bin/sonar-scanner" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
for name in BRANCH_NAME CHANGE_BRANCH CHANGE_ID CHANGE_TARGET GIT_BRANCH; do
  if [[ -n "${!name+x}" ]]; then
    touch "${SONAR_TEST_STATE}/multibranch-metadata"
  fi
done
printf '%s\n' "$*" >>"${SONAR_TEST_STATE}/calls"
echo scan >>"${SONAR_TEST_STATE}/order"
EOF
chmod 0755 "${temp_dir}/scanner/bin/sonar-scanner"

# A curl double for the preview-project reset: records arguments and stdin, writes the
# response body to --output, and prints the configured HTTP status.
mkdir -p "${temp_dir}/bin"
cat >"${temp_dir}/bin/curl" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
arguments=("$@")
output=""
for ((index = 0; index < ${#arguments[@]}; index++)); do
  if [[ "${arguments[index]}" == --output ]]; then
    output="${arguments[index + 1]}"
  fi
done
printf '%s\n' "$*" >>"${SONAR_TEST_STATE}/curl-args"
cat >>"${SONAR_TEST_STATE}/curl-stdin"
echo curl >>"${SONAR_TEST_STATE}/order"
printf '%s' "${SONAR_TEST_DELETE_BODY:-}" >"${output}"
printf '%s' "${SONAR_TEST_DELETE_STATUS:-204}"
EOF
chmod 0755 "${temp_dir}/bin/curl"
export PATH="${temp_dir}/bin:${PATH}"
export SONAR_HOST_URL=https://sonar.test/
export SONAR_AUTH_TOKEN=test-token

export SONAR_SCANNER_HOME="${temp_dir}/scanner"
export SONAR_TEST_STATE="${temp_dir}"
export SONAR_QUALITY_GATE_TIMEOUT_SECONDS=17
export BRANCH_NAME=PR-123
export CHANGE_BRANCH=fix/example
export CHANGE_ID=123
export CHANGE_TARGET=dev
export GIT_BRANCH=origin/PR-123

"${runner}" main-revision
SONAR_PROJECT_KEY=autonomous-battlefield-pr-123 \
SONAR_PROJECT_NAME='Autonomous Battlefield PR 123' \
  "${runner}" pr-123
"${pr_runner}" 456 HEAD

[[ ! -e "${SONAR_TEST_STATE}/multibranch-metadata" ]] || {
  echo "Jenkins multibranch metadata reached the Community Build scanner" >&2
  exit 1
}
[[ "$(wc -l <"${SONAR_TEST_STATE}/calls")" -eq 4 ]]
grep -Fx -- '-Dsonar.projectVersion=main-revision -Dsonar.qualitygate.wait=true -Dsonar.qualitygate.timeout=17' "${SONAR_TEST_STATE}/calls" >/dev/null
grep -Fx -- '-Dsonar.projectVersion=pr-123 -Dsonar.qualitygate.wait=true -Dsonar.qualitygate.timeout=17 -Dsonar.projectKey=autonomous-battlefield-pr-123 -Dsonar.projectName=Autonomous Battlefield PR 123' "${SONAR_TEST_STATE}/calls" >/dev/null
grep -E -- '^-Dsonar.projectVersion=base-[0-9a-f]{40} -Dsonar.qualitygate.wait=false -Dsonar.qualitygate.timeout=17 -Dsonar.projectBaseDir=.+ -Dsonar.projectDate=[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}[+-][0-9]{4} -Dsonar.projectKey=autonomous-battlefield-pr-456 -Dsonar.projectName=Autonomous Battlefield PR 456$' "${SONAR_TEST_STATE}/calls" >/dev/null
# The baseline is dated at the merge-base commit (here HEAD itself), so issues that
# SonarQube backdates to commit dates in newly added files still count as new code.
grep -F -- "-Dsonar.projectDate=$(git -C "${project_dir}" log -1 --format=%cd --date=format:%Y-%m-%dT%H:%M:%S%z HEAD) " "${SONAR_TEST_STATE}/calls" >/dev/null
grep -Fx -- '-Dsonar.projectVersion=pr-456 -Dsonar.qualitygate.wait=true -Dsonar.qualitygate.timeout=17 -Dsonar.projectKey=autonomous-battlefield-pr-456 -Dsonar.projectName=Autonomous Battlefield PR 456' "${SONAR_TEST_STATE}/calls" >/dev/null

# The PR gate resets its preview project before seeding the baseline, because
# SonarQube rejects a merge-base-dated baseline older than the project's last analysis.
[[ "$(tr '\n' ' ' <"${SONAR_TEST_STATE}/order")" == "scan scan curl scan scan " ]] || {
  echo "The preview project was not reset before the baseline scan: $(tr '\n' ' ' <"${SONAR_TEST_STATE}/order")" >&2
  exit 1
}
grep -F -- '--request POST' "${SONAR_TEST_STATE}/curl-args" >/dev/null
grep -F -- 'project=autonomous-battlefield-pr-456' "${SONAR_TEST_STATE}/curl-args" >/dev/null
grep -F -- 'https://sonar.test/api/projects/delete' "${SONAR_TEST_STATE}/curl-args" >/dev/null
grep -Fx -- 'user = "test-token:"' "${SONAR_TEST_STATE}/curl-stdin" >/dev/null
if grep -F -- test-token "${SONAR_TEST_STATE}/curl-args" >/dev/null; then
  echo "The SonarQube token reached curl's process arguments" >&2
  exit 1
fi

# A first scan finds no project to delete, which is not an error.
SONAR_TEST_DELETE_STATUS=404 "${pr_runner}" 457 HEAD >/dev/null
[[ "$(wc -l <"${SONAR_TEST_STATE}/calls")" -eq 6 ]]

# A failed reset must stop before any scan rather than gate against stale history.
if SONAR_TEST_DELETE_STATUS=403 SONAR_TEST_DELETE_BODY='{"errors":[{"msg":"Insufficient privileges"}]}' \
  "${pr_runner}" 458 HEAD >"${temp_dir}/forbidden.log" 2>&1; then
  echo "A rejected preview-project reset was ignored" >&2
  exit 1
fi
grep -F -- 'HTTP 403' "${temp_dir}/forbidden.log" >/dev/null
grep -F -- 'Insufficient privileges' "${temp_dir}/forbidden.log" >/dev/null
[[ "$(wc -l <"${SONAR_TEST_STATE}/calls")" -eq 6 ]] || {
  echo "The scanner ran after a failed preview-project reset" >&2
  exit 1
}
if SONAR_AUTH_TOKEN='' SONAR_TOKEN='' "${pr_runner}" 459 HEAD >/dev/null 2>&1; then
  echo "The PR gate ran without a SonarQube token" >&2
  exit 1
fi

if SONAR_PROJECT_KEY=12345 "${runner}" invalid-key >/dev/null 2>&1; then
  echo "A numeric-only SonarQube project key was accepted" >&2
  exit 1
fi
if SONAR_PROJECT_DATE=2026-01-31 "${runner}" invalid-date >/dev/null 2>&1; then
  echo "A SonarQube analysis date without a time and offset was accepted" >&2
  exit 1
fi

echo "SonarQube gate wrapper checks passed."
