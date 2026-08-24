#!/usr/bin/env bash

set -Eeuo pipefail

cluster_name="${KIND_CLUSTER_NAME:-battlefield}"
namespace="${KUBERNETES_NAMESPACE:-battlefield}"
kube_context="${KUBERNETES_CONTEXT:-kind-${cluster_name}}"
frontend_port="${SMOKE_TEST_FRONTEND_PORT:-18080}"
api_port="${SMOKE_TEST_API_PORT:-18081}"
runtime_dir="$(mktemp -d "${TMPDIR:-/tmp}/autonomous-battlefield-smoke.XXXXXX")"
pids=()

cleanup() {
  local exit_code=$?
  trap - EXIT INT TERM

  if ((${#pids[@]} > 0)); then
    kill "${pids[@]}" 2>/dev/null || true
    wait "${pids[@]}" 2>/dev/null || true
  fi

  rm -rf -- "${runtime_dir}"
  exit "${exit_code}"
}

trap cleanup EXIT INT TERM

for command in curl kubectl; do
  if ! command -v "${command}" >/dev/null 2>&1; then
    echo "Required command not found: ${command}" >&2
    exit 1
  fi
done

for port in "${frontend_port}" "${api_port}"; do
  if [[ ! "${port}" =~ ^[0-9]+$ ]] || ((port < 1024 || port > 65535)); then
    echo "Smoke-test ports must be integers between 1024 and 65535." >&2
    exit 1
  fi
done

if [[ "${frontend_port}" == "${api_port}" ]]; then
  echo "Frontend and API smoke-test ports must be different." >&2
  exit 1
fi

kube() {
  kubectl --context "${kube_context}" "$@"
}

kube port-forward \
  --namespace "${namespace}" \
  service/battlefield-frontend \
  "${frontend_port}:8080" \
  >"${runtime_dir}/frontend-port-forward.log" 2>&1 &
pids+=("$!")

kube port-forward \
  --namespace "${namespace}" \
  service/battlefield-api \
  "${api_port}:8080" \
  >"${runtime_dir}/api-port-forward.log" 2>&1 &
pids+=("$!")

wait_for_url() {
  local name="$1"
  local url="$2"

  for _ in {1..30}; do
    if curl --fail --silent --max-time 2 "${url}" >/dev/null; then
      echo "${name}: OK"
      return 0
    fi

    if ! kill -0 "${pids[0]}" 2>/dev/null || ! kill -0 "${pids[1]}" 2>/dev/null; then
      echo "A kubectl port-forward process exited unexpectedly." >&2
      cat "${runtime_dir}/frontend-port-forward.log" >&2
      cat "${runtime_dir}/api-port-forward.log" >&2
      return 1
    fi

    sleep 1
  done

  echo "${name} did not become healthy: ${url}" >&2
  return 1
}

wait_for_url "Frontend liveness" "http://127.0.0.1:${frontend_port}/healthz"
wait_for_url "API readiness" "http://127.0.0.1:${api_port}/readyz"
wait_for_url "Frontend-to-API proxy" "http://127.0.0.1:${frontend_port}/v1/scenarios"

echo "Local kind smoke test completed successfully."
