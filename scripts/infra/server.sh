#!/usr/bin/env bash
# One entry point for workstation bootstrap and host-side automation.
set -Eeuo pipefail
set +x
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
exec python3 "${project_dir}/scripts/infra/server.py" "$@"
