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

go_tool_dir="$(go env GOTOOLDIR)"
if [[ ! -x "${go_tool_dir}/covdata" ]]; then
  echo "The Go installation is incomplete: ${go_tool_dir}/covdata is missing." >&2
  echo "Reinstall the Go version used by this agent before generating coverage." >&2
  exit 1
fi

if [[ -n "${minimum_coverage}" && ! "${minimum_coverage}" =~ ^([0-9]+)(\.[0-9]+)?$ ]]; then
  echo "GO_COVERAGE_MIN must be a percentage such as 70 or 82.5." >&2
  exit 1
fi

mkdir -p "$(dirname -- "${coverage_file}")"
(
  cd "${backend_dir}"
  go test -covermode=atomic -coverprofile="${coverage_file}" ./...
)

coverage_percent="$(go tool cover -func="${coverage_file}" | awk '/^total:/ {gsub(/%/, "", $3); print $3}')"
if [[ -z "${coverage_percent}" ]]; then
  echo "Could not read total Go coverage from ${coverage_file}." >&2
  exit 1
fi

echo "Go total coverage: ${coverage_percent}%"
if [[ -n "${minimum_coverage}" ]] && ! awk -v actual="${coverage_percent}" -v minimum="${minimum_coverage}" 'BEGIN { exit !(actual + 0 >= minimum + 0) }'; then
  echo "Go coverage ${coverage_percent}% is below the required ${minimum_coverage}%." >&2
  exit 1
fi
