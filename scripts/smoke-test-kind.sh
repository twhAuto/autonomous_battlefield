#!/usr/bin/env bash

set -Eeuo pipefail

cluster_name="${KIND_CLUSTER_NAME:-battlefield}"
namespace="${KUBERNETES_NAMESPACE:-battlefield}"
kube_context="${KUBERNETES_CONTEXT:-kind-${cluster_name}}"
frontend_port="${SMOKE_TEST_FRONTEND_PORT:-18080}"
api_port="${SMOKE_TEST_API_PORT:-18081}"
prometheus_port="${SMOKE_TEST_PROMETHEUS_PORT:-18082}"
grafana_port="${SMOKE_TEST_GRAFANA_PORT:-18083}"
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

for command in curl kubectl node; do
  if ! command -v "${command}" >/dev/null 2>&1; then
    echo "Required command not found: ${command}" >&2
    exit 1
  fi
done

ports=("${frontend_port}" "${api_port}" "${prometheus_port}" "${grafana_port}")
for port in "${ports[@]}"; do
  if [[ ! "${port}" =~ ^[0-9]+$ ]] || ((port < 1024 || port > 65535)); then
    echo "Smoke-test ports must be integers between 1024 and 65535." >&2
    exit 1
  fi
done

for ((left = 0; left < ${#ports[@]}; left++)); do
  for ((right = left + 1; right < ${#ports[@]}; right++)); do
    if [[ "${ports[${left}]}" == "${ports[${right}]}" ]]; then
      echo "All smoke-test ports must be different." >&2
      exit 1
    fi
  done
done

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
  service/battlefield-prometheus \
  "${prometheus_port}:9090" \
  >"${runtime_dir}/prometheus-port-forward.log" 2>&1 &
pids+=("$!")

kube port-forward \
  --namespace "${namespace}" \
  service/battlefield-grafana \
  "${grafana_port}:3000" \
  >"${runtime_dir}/grafana-port-forward.log" 2>&1 &
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

    for pid in "${pids[@]}"; do
      if ! kill -0 "${pid}" 2>/dev/null; then
        echo "A kubectl port-forward process exited unexpectedly." >&2
        for log in "${runtime_dir}"/*-port-forward.log; do
          sed -n '1,120p' "${log}" >&2
        done
        return 1
      fi
    done

    sleep 1
  done

  echo "${name} did not become healthy: ${url}" >&2
  return 1
}

wait_for_url "Frontend liveness" "http://127.0.0.1:${frontend_port}/livez"
wait_for_url "API readiness" "http://127.0.0.1:${api_port}/readyz"
wait_for_url "Frontend-to-API proxy" "http://127.0.0.1:${frontend_port}/v1/scenarios"
wait_for_url "API metrics" "http://127.0.0.1:${api_port}/metrics"
wait_for_url "Prometheus readiness" "http://127.0.0.1:${prometheus_port}/-/ready"
wait_for_url "Grafana health" "http://127.0.0.1:${grafana_port}/api/health"

wait_for_prometheus_query() {
  local name="$1"
  local query="$2"
  local minimum="$3"
  local output_file="${runtime_dir}/prometheus-query.json"

  for _ in {1..30}; do
    if curl --fail --silent --max-time 2 \
      --get \
      --data-urlencode "query=${query}" \
      --output "${output_file}" \
      "http://127.0.0.1:${prometheus_port}/api/v1/query" \
      && node -e '
        const fs = require("fs");
        const response = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
        const value = Number(response.data?.result?.[0]?.value?.[1]);
        process.exit(Number.isFinite(value) && value >= Number(process.argv[2]) ? 0 : 1);
      ' "${output_file}" "${minimum}"; then
      echo "${name}: OK"
      return 0
    fi
    sleep 1
  done

  echo "${name} did not reach ${minimum} healthy target(s): ${query}" >&2
  sed -n '1,120p' "${output_file}" >&2
  return 1
}

wait_for_prometheus_query \
  "Prometheus API discovery" \
  'sum(up{job="battlefield-pods",component="api"})' \
  1
wait_for_prometheus_query \
  "Prometheus engine discovery" \
  'sum(up{job="battlefield-pods",component="engine"})' \
  1

curl --fail --silent --max-time 2 \
  --output "${runtime_dir}/api-metrics.txt" \
  "http://127.0.0.1:${api_port}/metrics"
if ! grep -q '^battlefield_http_server_requests_total' \
  "${runtime_dir}/api-metrics.txt"; then
  echo "API metrics did not contain battlefield_http_server_requests_total." >&2
  exit 1
fi

curl --fail --silent --max-time 2 \
  --get \
  --data-urlencode 'query=Autonomous Battlefield Overview' \
  --output "${runtime_dir}/grafana-search.json" \
  "http://127.0.0.1:${grafana_port}/api/search"
if ! grep -q '"uid":"battlefield-overview"' \
  "${runtime_dir}/grafana-search.json"; then
  echo "Grafana did not provision the Autonomous Battlefield dashboard." >&2
  exit 1
fi

echo "Local kind application and observability smoke test completed successfully."
