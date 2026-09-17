#!/usr/bin/env bash
# Jenkins records only after authentication, rollout, and runtime assurance have passed.
set -Eeuo pipefail
umask 077
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${project_dir}/scripts/lib/deployment-environment.sh"
[[ "${DEPLOY_ENV:-}" == production ]] || { echo 'Recovery records require production.' >&2; exit 1; }
battlefield_env_preflight
record="$(dirname -- "${BATTLEFIELD_ENV_FILE}")/recovery.json"
if [[ "${1:-}" == read && "$#" == 3 ]]; then
  python3 "${project_dir}/scripts/lib/production_recovery.py" read "${record}" "${BF_UID}" "$2" "$3"
elif [[ "${1:-}" == record && "$#" == 2 ]]; then
  runtime_dir="$(mktemp -d)"
  trap 'rm -rf -- "${runtime_dir}"' EXIT
  kubectl --context "${KUBERNETES_CONTEXT}" -n "${KUBERNETES_NAMESPACE}" get deployments \
    battlefield-api battlefield-engine battlefield-agent battlefield-mcp battlefield-frontend -o json >"${runtime_dir}/deployments.json"
  python3 "${project_dir}/scripts/lib/production_recovery.py" record "${record}" "${BF_UID}" "$2" "${runtime_dir}/deployments.json"
else
  echo 'Usage: production-recovery.sh read BACKEND FRONTEND | record VERIFIED_REVISION' >&2
  exit 1
fi
