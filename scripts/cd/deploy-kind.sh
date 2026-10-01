#!/usr/bin/env bash
# Compatibility launcher for deploy.sh.
set -Eeuo pipefail
exec "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/deploy.sh" "$@"
