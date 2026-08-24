#!/usr/bin/env bash

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cluster_name="${KIND_CLUSTER_NAME:-battlefield}"
namespace="${KUBERNETES_NAMESPACE:-battlefield}"
kube_context="${KUBERNETES_CONTEXT:-kind-${cluster_name}}"
rollout_timeout="${ROLLOUT_TIMEOUT:-120s}"
deployments=(battlefield-api battlefield-engine battlefield-frontend)

if [[ "$#" -gt 1 ]]; then
  echo "Usage: $0 [previous-full-git-sha]" >&2
  exit 1
fi

if [[ ! "${rollout_timeout}" =~ ^[1-9][0-9]*s$ ]]; then
  echo "ROLLOUT_TIMEOUT must be a positive number of seconds such as 120s." >&2
  exit 1
fi

if [[ "$#" == 1 ]]; then
  if [[ ! "$1" =~ ^[0-9a-f]{40}$ ]]; then
    echo "The previous image tag must be a full 40-character lowercase Git SHA." >&2
    exit 1
  fi

  echo "Redeploying previous immutable revision $1..."
  exec "${project_dir}/scripts/deploy-kind.sh" "$1"
fi

if ! command -v kubectl >/dev/null 2>&1; then
  echo "Required command not found: kubectl" >&2
  exit 1
fi

kube() {
  kubectl --context "${kube_context}" "$@"
}

rollback_failed=0

for deployment in "${deployments[@]}"; do
  if ! kube rollout undo "deployment/${deployment}" --namespace "${namespace}"; then
    rollback_failed=1
  fi
done

for deployment in "${deployments[@]}"; do
  if ! kube rollout status "deployment/${deployment}" \
    --namespace "${namespace}" \
    --timeout="${rollout_timeout}"; then
    rollback_failed=1
  fi
done

if [[ "${rollback_failed}" != "0" ]]; then
  echo "One or more application workloads could not be rolled back." >&2
  exit 1
fi

echo "Application Deployments rolled back successfully."
