#!/usr/bin/env bash
# Explicit public tunnel deployment; normal application deploys never activate it.
set -Eeuo pipefail
set +x
umask 077
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${project_dir}/scripts/lib/deployment-environment.sh"
[[ "${DEPLOY_ENV:-production}" == production ]] || { echo 'Tunnel deployment requires production.' >&2; exit 1; }
export DEPLOY_ENV=production
battlefield_env_preflight
runtime_dir="$(mktemp -d)"
trap 'rm -rf -- "${runtime_dir}"' EXIT
python3 "${project_dir}/scripts/ops/prepare-limited-production.py" --output "${runtime_dir}"
kubectl --context "${KUBERNETES_CONTEXT}" -n "${KUBERNETES_NAMESPACE}" create secret generic battlefield-tunnel \
  --from-file="credentials.json=${BF_TUNNEL_CREDENTIALS_FILE}" --dry-run=client -o yaml | \
  kubectl --context "${KUBERNETES_CONTEXT}" -n "${KUBERNETES_NAMESPACE}" apply -f -
kubectl --context "${KUBERNETES_CONTEXT}" -n "${KUBERNETES_NAMESPACE}" apply -f "${runtime_dir}/tunnel.json"
kubectl --context "${KUBERNETES_CONTEXT}" -n "${KUBERNETES_NAMESPACE}" rollout restart deployment/battlefield-tunnel
kubectl --context "${KUBERNETES_CONTEXT}" -n "${KUBERNETES_NAMESPACE}" rollout status deployment/battlefield-tunnel --timeout=180s
