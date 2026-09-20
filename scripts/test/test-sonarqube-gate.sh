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
EOF
chmod 0755 "${temp_dir}/scanner/bin/sonar-scanner"

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
grep -E -- '^-Dsonar.projectVersion=base-[0-9a-f]{40} -Dsonar.qualitygate.wait=false -Dsonar.qualitygate.timeout=17 -Dsonar.projectBaseDir=.+ -Dsonar.projectKey=autonomous-battlefield-pr-456 -Dsonar.projectName=Autonomous Battlefield PR 456$' "${SONAR_TEST_STATE}/calls" >/dev/null
grep -Fx -- '-Dsonar.projectVersion=pr-456 -Dsonar.qualitygate.wait=true -Dsonar.qualitygate.timeout=17 -Dsonar.projectKey=autonomous-battlefield-pr-456 -Dsonar.projectName=Autonomous Battlefield PR 456' "${SONAR_TEST_STATE}/calls" >/dev/null

if SONAR_PROJECT_KEY=12345 "${runner}" invalid-key >/dev/null 2>&1; then
  echo "A numeric-only SonarQube project key was accepted" >&2
  exit 1
fi

echo "SonarQube gate wrapper checks passed."
