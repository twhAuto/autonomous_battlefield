#!/usr/bin/env bash
# Prepare private launch files for the registered production cluster; never expose it.
set -Eeuo pipefail
umask 077
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${project_dir}/scripts/lib/deployment-environment.sh"
[[ "${DEPLOY_ENV:-production}" == production ]] || { echo 'This command only prepares production.' >&2; exit 1; }
export DEPLOY_ENV=production
battlefield_env_preflight
python3 "${project_dir}/scripts/ops/prepare-limited-production.py" "$@"
