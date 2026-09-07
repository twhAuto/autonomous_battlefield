#!/usr/bin/env bash
# Runs backend tests with package-wide coverage and fails when aggregate statement
# coverage is below the configured minimum.

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
backend_dir="${project_dir}/backend"
coverage_file="${GO_COVERAGE_FILE:-${backend_dir}/coverage.out}"
coverage_summary_file="${GO_COVERAGE_SUMMARY_FILE:-${backend_dir}/coverage-total.txt}"
minimum_coverage="${GO_COVERAGE_MIN:-80}"

if ! command -v go >/dev/null 2>&1; then
  echo "Required command not found: go" >&2
  exit 1
fi

go_version="$(go env GOVERSION)"
coverage_cache="${GOCACHE:-${TMPDIR:-/tmp}/autonomous-battlefield-go-cache-${go_version}}"

if [[ ! "${minimum_coverage}" =~ ^([0-9]+)(\.[0-9]+)?$ ]] ||
  ! awk -v minimum="${minimum_coverage}" 'BEGIN { exit !(minimum + 0 <= 100) }'; then
  echo "GO_COVERAGE_MIN must be a percentage from 0 to 100." >&2
  exit 1
fi

mkdir -p \
  "$(dirname -- "${coverage_file}")" \
  "$(dirname -- "${coverage_summary_file}")"
(
  cd "${backend_dir}"
  GOCACHE="${coverage_cache}" go test -covermode=atomic -coverprofile="${coverage_file}" ./...
)

coverage_percent="$(cd "${backend_dir}" && GOCACHE="${coverage_cache}" go tool cover -func="${coverage_file}" | awk '/^total:/ {gsub(/%/, "", $3); print $3}')"
if [[ -z "${coverage_percent}" ]]; then
  echo "Could not read total Go coverage from ${coverage_file}." >&2
  exit 1
fi

echo "Go total coverage: ${coverage_percent}%"
printf '%s\n' "${coverage_percent}" >"${coverage_summary_file}"
if ! awk -v actual="${coverage_percent}" -v minimum="${minimum_coverage}" 'BEGIN { exit !(actual + 0 >= minimum + 0) }'; then
  echo "Go coverage ${coverage_percent}% is below the required ${minimum_coverage}%." >&2
  exit 1
fi
