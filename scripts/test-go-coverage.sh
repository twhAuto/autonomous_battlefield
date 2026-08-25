#!/usr/bin/env bash

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
backend_dir="${project_dir}/backend"
coverage_file="${GO_COVERAGE_FILE:-${backend_dir}/coverage.out}"
minimum_coverage="${GO_COVERAGE_MIN:-}"

if ! command -v go >/dev/null 2>&1; then
  echo "Required command not found: go" >&2
  exit 1
fi

go_version="$(go env GOVERSION)"
coverage_cache="${GOCACHE:-${TMPDIR:-/tmp}/autonomous-battlefield-go-cache-${go_version}}"

if [[ -n "${minimum_coverage}" && ! "${minimum_coverage}" =~ ^([0-9]+)(\.[0-9]+)?$ ]]; then
  echo "GO_COVERAGE_MIN must be a percentage such as 70 or 82.5." >&2
  exit 1
fi

mkdir -p "$(dirname -- "${coverage_file}")"
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
if [[ -n "${minimum_coverage}" ]] && ! awk -v actual="${coverage_percent}" -v minimum="${minimum_coverage}" 'BEGIN { exit !(actual + 0 >= minimum + 0) }'; then
  echo "Go coverage ${coverage_percent}% is below the required ${minimum_coverage}%." >&2
  exit 1
fi
