#!/usr/bin/env bash
# Compatibility launcher for smoke-test.sh.
set -Eeuo pipefail
exec "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/smoke-test.sh" "$@"
