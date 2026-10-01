#!/usr/bin/env bash
# Jenkins records only after authentication, rollout, and runtime assurance have passed.
set -Eeuo pipefail
umask 077
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${project_dir}/scripts/lib/deployment-environment.sh"
[[ "${DEPLOY_ENV:-}" == production ]] || { echo 'Recovery records require production.' >&2; exit 1; }
battlefield_env_preflight
record="$(dirname -- "${BATTLEFIELD_ENV_FILE}")/recovery.json"
runtime_dir="$(mktemp -d)"
trap 'rm -rf -- "${runtime_dir}"' EXIT
if [[ "${1:-}" == read && ( ! -f "${record}" || "${2:-}" != *@sha256:* || "${3:-}" != *@sha256:* ) ]]; then
  exit 0
fi
# Fail closed if the database or its migration ledger cannot be inspected. This
# compares applied migrations, not a guess based on the current source tree.
kubectl --context "${KUBERNETES_CONTEXT}" -n "${KUBERNETES_NAMESPACE}" exec battlefield-postgres-0 -- \
  sh -ec 'exec psql -X -v ON_ERROR_STOP=1 -At --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" -c "$1"' _ \
  "SELECT COALESCE(json_agg(version_id ORDER BY version_id), '[]'::json) FROM (SELECT DISTINCT ON (version_id) version_id, is_applied FROM public.goose_db_version ORDER BY version_id, id DESC) v WHERE is_applied;" \
  >"${runtime_dir}/migrations.json"
if [[ "${1:-}" == read && "$#" == 3 ]]; then
  python3 "${project_dir}/scripts/lib/production_recovery.py" read "${record}" "${BF_UID}" "$2" "$3" "${runtime_dir}/migrations.json"
elif [[ "${1:-}" == record && "$#" == 2 ]]; then
  kubectl --context "${KUBERNETES_CONTEXT}" -n "${KUBERNETES_NAMESPACE}" get deployments \
    battlefield-api battlefield-engine battlefield-agent battlefield-mcp battlefield-frontend -o json >"${runtime_dir}/deployments.json"
  python3 "${project_dir}/scripts/lib/production_recovery.py" record "${record}" "${BF_UID}" "$2" "${runtime_dir}/deployments.json" "${runtime_dir}/migrations.json"
else
  echo 'Usage: production-recovery.sh read BACKEND FRONTEND | record VERIFIED_REVISION' >&2
  exit 1
fi
