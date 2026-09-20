#!/usr/bin/env bash
# Run the shared Community Build analysis serially and fail on a red quality gate.
set -Eeuo pipefail

revision="${1:-}"
if [[ -z "${revision}" ]]; then
  echo "Usage: $0 REVISION" >&2
  exit 2
fi

: "${SONAR_SCANNER_HOME:?SONAR_SCANNER_HOME must name the configured scanner installation}"
scanner="${SONAR_SCANNER_HOME}/bin/sonar-scanner"
if [[ ! -x "${scanner}" ]]; then
  echo "SonarScanner is not executable: ${scanner}" >&2
  exit 1
fi
if ! command -v flock >/dev/null 2>&1; then
  echo "flock is required to serialize Community Build analysis" >&2
  exit 1
fi

lock_timeout="${SONAR_LOCK_TIMEOUT_SECONDS:-600}"
gate_timeout="${SONAR_QUALITY_GATE_TIMEOUT_SECONDS:-300}"
if [[ ! "${lock_timeout}" =~ ^[0-9]+$ || ! "${gate_timeout}" =~ ^[1-9][0-9]*$ ]]; then
  echo "SonarQube lock and quality-gate timeouts must be whole seconds" >&2
  exit 2
fi

lock_file="${SONAR_ANALYSIS_LOCK_FILE:-${HOME}/.cache/autonomous-battlefield/sonarqube-analysis.lock}"
umask 077
mkdir -p -- "$(dirname -- "${lock_file}")"
exec 9>"${lock_file}"
if ! flock --exclusive --timeout "${lock_timeout}" 9; then
  echo "Timed out waiting for the shared SonarQube analysis lock: ${lock_file}" >&2
  exit 1
fi

# Analyze the checked-out files as the one Community Build project branch. Jenkins
# multibranch metadata would otherwise make the scanner request branch/PR analysis.
env \
  -u BRANCH_NAME \
  -u CHANGE_BRANCH \
  -u CHANGE_ID \
  -u CHANGE_TARGET \
  -u GIT_BRANCH \
  "${scanner}" \
  -Dsonar.projectVersion="${revision}" \
  -Dsonar.qualitygate.wait=true \
  -Dsonar.qualitygate.timeout="${gate_timeout}"
