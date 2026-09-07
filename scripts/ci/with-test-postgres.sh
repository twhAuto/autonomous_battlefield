#!/usr/bin/env bash
# Compatibility entry point for scripts/test/with-test-postgres.sh; forwards arguments and
# preserves the invoked command's exit status.

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
exec "${project_dir}/scripts/test/with-test-postgres.sh" "$@"
