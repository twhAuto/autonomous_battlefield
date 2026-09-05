#!/usr/bin/env bash

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"

if [[ -z "${TEST_DATABASE_URL:-}" ]]; then
  echo "TEST_DATABASE_URL must be set by with-test-postgres.sh." >&2
  exit 1
fi

"${project_dir}/scripts/test/test-go-coverage.sh"
"${project_dir}/scripts/test/test-go-race.sh"
