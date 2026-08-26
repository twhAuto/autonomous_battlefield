#!/usr/bin/env bash

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cluster_name="${KIND_CLUSTER_NAME:-battlefield}"
namespace="${KUBERNETES_NAMESPACE:-battlefield}"
kube_context="${KUBERNETES_CONTEXT:-kind-${cluster_name}}"
image_tag="$(git -C "${project_dir}" rev-parse HEAD)"
dirty_worktree=0

if [[ -n "$(git -C "${project_dir}" status --porcelain --untracked-files=normal)" ]]; then
  dirty_worktree=1
fi

echo "Building the backend and frontend from the current working tree..."
ALLOW_DIRTY=1 \
  DEPLOY_COMPONENTS=backend,frontend \
  "${project_dir}/scripts/ci/build-images.sh"

echo
echo "Deploying the backend and frontend to kind cluster ${cluster_name}..."
DEPLOY_COMPONENTS=backend,frontend \
  "${project_dir}/scripts/cd/deploy-kind.sh" "${image_tag}"

if [[ "${dirty_worktree}" == "1" ]]; then
  echo
  echo "Restarting application pods so the dirty-worktree images are used..."
  kubectl --context "${kube_context}" rollout restart \
    deployment/battlefield-api \
    deployment/battlefield-engine \
    deployment/battlefield-frontend \
    --namespace "${namespace}"

  for deployment in battlefield-api battlefield-engine battlefield-frontend; do
    kubectl --context "${kube_context}" rollout status \
      "deployment/${deployment}" \
      --namespace "${namespace}" \
      --timeout="${ROLLOUT_TIMEOUT:-300s}"
  done
fi

echo
echo "Local application deployment is ready: http://battlefield.localhost:8888"
