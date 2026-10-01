#!/usr/bin/env bash
# Builds and deploys the current working tree to the registered dev cluster, requires
# existing secret files, and runs smoke checks after rollout.

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${project_dir}/scripts/lib/deployment-environment.sh"
[[ "${DEPLOY_ENV:-dev}" == dev ]] || { echo 'deploy-local.sh only deploys dev.' >&2; exit 1; }
export DEPLOY_ENV=dev
battlefield_env_preflight
battlefield_env_verify_docker
[[ -f "${BATTLEFIELD_SECRET_FILE}" && -f "${BATTLEFIELD_AUTH_SECRET_FILE}" ]] || {
  echo 'Project .env is missing; run ./scripts/cd/prepare-dev-secrets.sh first.' >&2
  exit 1
}
cluster_name="${KIND_CLUSTER_NAME:-battlefield}"
backend_image="${BACKEND_IMAGE:-autonomous-battlefield-backend}"
frontend_image="${FRONTEND_IMAGE:-autonomous-battlefield-frontend}"
image_tag="$(git -C "${project_dir}" rev-parse HEAD)"
dirty_worktree=0

if [[ -n "$(git -C "${project_dir}" status --porcelain --untracked-files=normal)" ]]; then
  dirty_worktree=1
fi
node "${project_dir}/scripts/lib/application-env.cjs" validate "${BATTLEFIELD_SECRET_FILE}"

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
# .env is ignored by Git; restart application pods to consume secret-only edits too.
FORCE_RESTART=1 \
  DEPLOY_COMPONENTS="${DEPLOY_COMPONENTS:-infrastructure,backend,frontend,gateway}" \
  "${project_dir}/scripts/cd/deploy.sh" "${image_tag}"

echo
"${project_dir}/scripts/cd/smoke-test.sh"
echo "Dev application deployment is ready: ${TARGET_URL}"
