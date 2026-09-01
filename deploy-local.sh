#!/usr/bin/env bash

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cluster_name="${KIND_CLUSTER_NAME:-battlefield}"
backend_image="${BACKEND_IMAGE:-autonomous-battlefield-backend}"
frontend_image="${FRONTEND_IMAGE:-autonomous-battlefield-frontend}"
image_tag="$(git -C "${project_dir}" rev-parse HEAD)"
dirty_worktree=0

if [[ -n "$(git -C "${project_dir}" status --porcelain --untracked-files=normal)" ]]; then
  dirty_worktree=1
fi

echo "Building the backend and frontend from the current working tree..."
ALLOW_DIRTY=1 \
  DEPLOY_COMPONENTS=backend,frontend \
  "${project_dir}/scripts/ci/build-images.sh"

if [[ "${dirty_worktree}" == "1" ]]; then
  echo
  echo "Removing stale commit-tagged images from kind before loading dirty-worktree builds..."
  while IFS= read -r node; do
    docker exec "${node}" crictl rmi "${backend_image}:${image_tag}" >/dev/null 2>&1 || true
    docker exec "${node}" crictl rmi "${frontend_image}:${image_tag}" >/dev/null 2>&1 || true
  done < <(kind get nodes --name "${cluster_name}")
fi

echo
echo "Deploying local configuration, backend, and frontend to kind cluster ${cluster_name}..."
FORCE_RESTART="${dirty_worktree}" \
  DEPLOY_COMPONENTS=infrastructure,backend,frontend \
  "${project_dir}/scripts/cd/deploy-kind.sh" "${image_tag}"

echo
echo "Local application deployment is ready: http://battlefield.localhost:8888"
