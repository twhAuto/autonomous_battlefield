#!/usr/bin/env bash
# Reconcile ingress restrictions without reading tunnel credentials or activating it.
set -Eeuo pipefail
umask 077
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${project_dir}/scripts/lib/deployment-environment.sh"
[[ "${DEPLOY_ENV:-production}" == production ]] || { echo 'Policy reconciliation requires production.' >&2; exit 1; }
export DEPLOY_ENV=production
battlefield_env_preflight
runtime_dir="$(mktemp -d)"
trap 'rm -rf -- "${runtime_dir}"' EXIT
python3 "${project_dir}/scripts/ops/render-limited-launch.py" --policies-only --output "${runtime_dir}"
kubectl --context "${KUBERNETES_CONTEXT}" -n "${KUBERNETES_NAMESPACE}" apply -f "${runtime_dir}/network-policies.json"
