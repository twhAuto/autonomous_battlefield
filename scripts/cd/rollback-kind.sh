#!/usr/bin/env bash

set -Eeuo pipefail

# Restores application images after a failed continuous-delivery verification.

cluster_name="${KIND_CLUSTER_NAME:-battlefield}"
namespace="${KUBERNETES_NAMESPACE:-battlefield}"
kube_context="${KUBERNETES_CONTEXT:-kind-${cluster_name}}"
rollout_timeout="${ROLLOUT_TIMEOUT:-120s}"
backend_image="${BACKEND_IMAGE:-autonomous-battlefield-backend}"
frontend_image="${FRONTEND_IMAGE:-autonomous-battlefield-frontend}"
deployments=(battlefield-api battlefield-engine battlefield-agent battlefield-mcp battlefield-frontend)

if [[ "$#" -gt 2 ]]; then
  echo "Usage: $0 [previous-backend-git-sha [previous-frontend-git-sha]]" >&2
  exit 1
fi

if [[ ! "${rollout_timeout}" =~ ^[1-9][0-9]*s$ ]]; then
  echo "ROLLOUT_TIMEOUT must be a positive number of seconds such as 120s." >&2
  exit 1
fi

for image_name in "${backend_image}" "${frontend_image}"; do
  if [[ ! "${image_name}" =~ ^[a-z0-9][a-z0-9._:/-]*$ ]]; then
    echo "Invalid container image name: ${image_name}" >&2
    exit 1
  fi
done

if ! command -v kubectl >/dev/null 2>&1; then
  echo "Required command not found: kubectl" >&2
  exit 1
fi

kube() {
  kubectl --context "${kube_context}" "$@"
}

if [[ "$#" -ge 1 ]]; then
  previous_backend_tag="$1"
  previous_frontend_tag="${2:-$1}"

  for image_tag in "${previous_backend_tag}" "${previous_frontend_tag}"; do
    if [[ ! "${image_tag}" =~ ^[0-9a-f]{40}$ ]]; then
      echo "Previous image tags must be full 40-character lowercase Git SHAs." >&2
      exit 1
    fi
  done

  echo "Restoring backend ${previous_backend_tag} and frontend ${previous_frontend_tag}..."
  kube set image deployment/battlefield-api \
    "api=${backend_image}:${previous_backend_tag}" \
    --namespace "${namespace}"
  kube set image deployment/battlefield-engine \
    "engine=${backend_image}:${previous_backend_tag}" \
    --namespace "${namespace}"
  kube set image deployment/battlefield-agent \
    "agent=${backend_image}:${previous_backend_tag}" \
    --namespace "${namespace}"
  kube set image deployment/battlefield-mcp \
    "mcp=${backend_image}:${previous_backend_tag}" \
    --namespace "${namespace}"
  kube set image deployment/battlefield-frontend \
    "frontend=${frontend_image}:${previous_frontend_tag}" \
    --namespace "${namespace}"

  for deployment in "${deployments[@]}"; do
    kube rollout status "deployment/${deployment}" \
      --namespace "${namespace}" \
      --timeout="${rollout_timeout}"
  done

  echo "Application images restored successfully."
  exit 0
fi

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
