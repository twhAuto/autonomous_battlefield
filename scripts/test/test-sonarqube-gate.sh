#!/usr/bin/env bash
set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
runner="${project_dir}/scripts/ci/run-sonarqube-gate.sh"
temp_dir="$(mktemp -d)"
cleanup() {
  rm -rf -- "${temp_dir}"
}
trap cleanup EXIT

mkdir -p "${temp_dir}/scanner/bin" "${temp_dir}/state"
cat >"${temp_dir}/scanner/bin/sonar-scanner" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
if ! mkdir "${SONAR_TEST_STATE}/active" 2>/dev/null; then
  touch "${SONAR_TEST_STATE}/overlap"
  exit 99
fi
for name in BRANCH_NAME CHANGE_BRANCH CHANGE_ID CHANGE_TARGET GIT_BRANCH; do
  if [[ -n "${!name+x}" ]]; then
    touch "${SONAR_TEST_STATE}/multibranch-metadata"
  fi
done
printf '%s\n' "$*" >>"${SONAR_TEST_STATE}/calls"
sleep 1
rmdir "${SONAR_TEST_STATE}/active"
EOF
chmod 0755 "${temp_dir}/scanner/bin/sonar-scanner"

export SONAR_SCANNER_HOME="${temp_dir}/scanner"
export SONAR_ANALYSIS_LOCK_FILE="${temp_dir}/sonarqube.lock"
export SONAR_TEST_STATE="${temp_dir}/state"
export SONAR_LOCK_TIMEOUT_SECONDS=5
export SONAR_QUALITY_GATE_TIMEOUT_SECONDS=17
export BRANCH_NAME=PR-123
export CHANGE_BRANCH=fix/example
export CHANGE_ID=123
export CHANGE_TARGET=dev
export GIT_BRANCH=origin/PR-123

"${runner}" first-revision &
first_pid=$!
for _ in {1..50}; do
  [[ -d "${SONAR_TEST_STATE}/active" ]] && break
  sleep 0.02
done
[[ -d "${SONAR_TEST_STATE}/active" ]] || {
  echo "First fake scanner did not start" >&2
  exit 1
}
"${runner}" second-revision &
second_pid=$!
wait "${first_pid}"
wait "${second_pid}"

[[ ! -e "${SONAR_TEST_STATE}/overlap" ]] || {
  echo "Concurrent analyses were not serialized" >&2
  exit 1
}
[[ ! -e "${SONAR_TEST_STATE}/multibranch-metadata" ]] || {
  echo "Jenkins multibranch metadata reached the Community Build scanner" >&2
  exit 1
}
[[ "$(wc -l <"${SONAR_TEST_STATE}/calls")" -eq 2 ]]
grep -F -- '-Dsonar.projectVersion=first-revision -Dsonar.qualitygate.wait=true -Dsonar.qualitygate.timeout=17' "${SONAR_TEST_STATE}/calls" >/dev/null
grep -F -- '-Dsonar.projectVersion=second-revision -Dsonar.qualitygate.wait=true -Dsonar.qualitygate.timeout=17' "${SONAR_TEST_STATE}/calls" >/dev/null

echo "SonarQube gate wrapper checks passed."
