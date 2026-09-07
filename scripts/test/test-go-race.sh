#!/usr/bin/env bash
# Runs backend tests with Go's race detector and checks the compiler prerequisites needed
# for instrumentation.

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
backend_dir="${project_dir}/backend"

for command in go gcc; do
  if ! command -v "${command}" >/dev/null 2>&1; then
    echo "Required race-detector command not found: ${command}" >&2
    exit 1
  fi
done
if [[ -z "${TEST_DATABASE_URL:-}" ]]; then
  echo "TEST_DATABASE_URL must be set; race tests may not skip PostgreSQL integration coverage." >&2
  exit 1
fi

mkdir -p "${project_dir}/artifacts/tests"
(
  cd "${backend_dir}"
  CGO_ENABLED=1 go test -race -json ./... | tee "${project_dir}/artifacts/tests/go-race.json"
)
