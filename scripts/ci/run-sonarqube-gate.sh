#!/usr/bin/env bash
# Run a Community Build analysis and fail on a red quality gate.
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
gate_timeout="${SONAR_QUALITY_GATE_TIMEOUT_SECONDS:-300}"
if [[ ! "${gate_timeout}" =~ ^[1-9][0-9]*$ ]]; then
  echo "The SonarQube quality-gate timeout must be a positive whole number of seconds" >&2
  exit 2
fi

scanner_arguments=(
  -Dsonar.projectVersion="${revision}"
  -Dsonar.qualitygate.wait=true
  -Dsonar.qualitygate.timeout="${gate_timeout}"
)
if [[ -n "${SONAR_PROJECT_KEY:-}" ]]; then
  if [[ ! "${SONAR_PROJECT_KEY}" =~ ^[A-Za-z0-9_.:-]*[A-Za-z_.:-][A-Za-z0-9_.:-]*$ ]]; then
    echo "SONAR_PROJECT_KEY contains unsupported characters or only digits" >&2
    exit 2
  fi
  scanner_arguments+=(
    -Dsonar.projectKey="${SONAR_PROJECT_KEY}"
    -Dsonar.projectName="${SONAR_PROJECT_NAME:-${SONAR_PROJECT_KEY}}"
  )
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
  "${scanner_arguments[@]}"
