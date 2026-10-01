#!/usr/bin/env bash
# Compatibility launcher for rollback.sh.
set -Eeuo pipefail
exec "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/rollback.sh" "$@"
