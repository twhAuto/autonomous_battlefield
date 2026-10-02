#!/usr/bin/env bash
# Explicit dev bootstrap; never recreates or deletes an existing cluster.
set -Eeuo pipefail
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${project_dir}/scripts/lib/deployment-environment.sh"
[[ "$#" == 0 && "${DEPLOY_ENV:-dev}" == dev ]] || {
  echo 'Usage: create-kind-cluster.sh (dev only; recreation is not supported)' >&2; exit 1;
}
export DEPLOY_ENV=dev
profile="${BATTLEFIELD_ENV_FILE:-${HOME}/.config/autonomous-battlefield/dev/deployment.env}"
if [[ -f "${profile}" ]]; then
  battlefield_env_load
else
  export KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-battlefield}"
  export DOCKER_HOST="${DOCKER_HOST:-$(docker context inspect "${DOCKER_CONTEXT:-$(docker context show)}" --format '{{.Endpoints.docker.Host}}')}"
  [[ "${DOCKER_HOST}" == unix:///* ]] || { echo 'Dev requires a local Docker socket.' >&2; exit 1; }
  unset DOCKER_CONTEXT
fi
docker info >/dev/null 2>&1 || { echo 'Start Docker/WSL Docker integration before starting dev.' >&2; exit 1; }
nodes="$(docker ps -a --filter "label=io.x-k8s.kind.cluster=${KIND_CLUSTER_NAME}" --format '{{.Names}}')"
if [[ -n "${nodes}" ]]; then
  while IFS= read -r node; do
    if [[ "$(docker inspect --format '{{.State.Running}}' "${node}")" != true ]]; then
      docker start "${node}" >/dev/null
    fi
  done <<<"${nodes}"
else
  [[ ! -f "${profile}" ]] || { echo 'Registered cluster is missing; refusing to replace its identity or data.' >&2; exit 1; }
  kind create cluster --name "${KIND_CLUSTER_NAME}" --config "${project_dir}/infra/deploy/kind.yaml"
fi
if [[ ! -f "${profile}" ]]; then
  # Setup must use the kubeconfig created/exported by Kind, not the current context.
  ready=0
  for _ in {1..30}; do
    if kubectl --context "kind-${KIND_CLUSTER_NAME}" --request-timeout=2s get namespace kube-system >/dev/null 2>&1; then ready=1; break; fi
    sleep 1
  done
  [[ "${ready}" == 1 ]] || { echo 'Dev API did not start; inspect the existing nodes.' >&2; exit 1; }
  BATTLEFIELD_CONFIG_DIR="$(dirname -- "${profile}")" BATTLEFIELD_ENV_FILE="${profile}" \
    "${project_dir}/scripts/cd/setup-environment.sh" dev "kind-${KIND_CLUSTER_NAME}"
fi
battlefield_env_load
# A stopped API server may need a short readiness window after Docker starts it.
ready=0
for _ in {1..30}; do
  if battlefield_env_verify 2>/dev/null; then ready=1; break; fi
  sleep 1
done
[[ "${ready}" == 1 ]] || { echo 'Dev API is unavailable or its identity changed.' >&2; exit 1; }
battlefield_env_verify_docker
version="${ENVOY_GATEWAY_VERSION:-v1.9.0}"
[[ "${version}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || exit 1
kubectl --context "${KUBERNETES_CONTEXT}" apply --server-side -f "https://github.com/envoyproxy/gateway/releases/download/${version}/install.yaml"
kubectl --context "${KUBERNETES_CONTEXT}" wait --namespace envoy-gateway-system --for=condition=Available deployment/envoy-gateway --timeout=300s
echo "Dev cluster ready. Configure dev secrets, then run ./scripts/cd/deploy-local.sh (${TARGET_URL})."
