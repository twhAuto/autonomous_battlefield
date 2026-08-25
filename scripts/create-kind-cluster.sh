#!/usr/bin/env bash

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cluster_name="${KIND_CLUSTER_NAME:-battlefield}"
kube_context="${KUBERNETES_CONTEXT:-kind-${cluster_name}}"
envoy_gateway_version="${ENVOY_GATEWAY_VERSION:-v1.9.0}"
gateway_node_port=30080
gateway_host_port=8888
recreate=0

if [[ "${1:-}" == "--recreate" ]]; then
  recreate=1
  shift
fi

if [[ "$#" -ne 0 ]]; then
  echo "Usage: $0 [--recreate]" >&2
  exit 1
fi

for command in docker grep kind kubectl; do
  if ! command -v "${command}" >/dev/null 2>&1; then
    echo "Required command not found: ${command}" >&2
    exit 1
  fi
done

if ! [[ "${envoy_gateway_version}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "ENVOY_GATEWAY_VERSION must be a release such as v1.9.0." >&2
  exit 1
fi

cluster_exists=0
if kind get clusters | grep -Fxq "${cluster_name}"; then
  cluster_exists=1
fi

if [[ "${cluster_exists}" == "1" && "${recreate}" == "1" ]]; then
  echo "Deleting kind cluster ${cluster_name} as requested..."
  kind delete cluster --name "${cluster_name}"
  cluster_exists=0
fi

if [[ "${cluster_exists}" == "1" ]]; then
  control_plane_node="$(kind get nodes --name "${cluster_name}" | grep -- '-control-plane$' | head -n 1)"
  published_port="$(docker port "${control_plane_node}" "${gateway_node_port}/tcp" 2>/dev/null || true)"

  if ! grep -Fxq "127.0.0.1:${gateway_host_port}" <<<"${published_port}"; then
    echo "kind cluster ${cluster_name} does not publish node port ${gateway_node_port} on localhost:${gateway_host_port}." >&2
    echo "Recreate this disposable local cluster with: $0 --recreate" >&2
    exit 1
  fi

  echo "Using existing kind cluster ${cluster_name}."
else
  echo "Creating kind cluster ${cluster_name} with localhost gateway access..."
  kind create cluster \
    --name "${cluster_name}" \
    --config "${project_dir}/deploy/kind.yaml"
fi

echo "Installing Envoy Gateway ${envoy_gateway_version}..."
kubectl --context "${kube_context}" apply --server-side \
  -f "https://github.com/envoyproxy/gateway/releases/download/${envoy_gateway_version}/install.yaml"
kubectl --context "${kube_context}" wait \
  --namespace envoy-gateway-system \
  --for=condition=Available \
  deployment/envoy-gateway \
  --timeout=300s

echo
echo "kind cluster ${cluster_name} is ready for application deployment."
echo "After ./scripts/deploy-kind.sh, open:"
echo "  http://battlefield.localhost:${gateway_host_port}"
echo "  http://grafana.localhost:${gateway_host_port}"
