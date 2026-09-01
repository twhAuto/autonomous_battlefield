#!/usr/bin/env bash

set -Eeuo pipefail

# Verifies the workloads and observability endpoints deployed to local kind.

cluster_name="${KIND_CLUSTER_NAME:-battlefield}"
namespace="${KUBERNETES_NAMESPACE:-battlefield}"
kube_context="${KUBERNETES_CONTEXT:-kind-${cluster_name}}"
api_port="${SMOKE_TEST_API_PORT:-18081}"
prometheus_port="${SMOKE_TEST_PROMETHEUS_PORT:-18082}"
elasticsearch_port="${SMOKE_TEST_ELASTICSEARCH_PORT:-18083}"
gateway_port="${SMOKE_TEST_GATEWAY_PORT:-8888}"
frontend_host="${SMOKE_TEST_FRONTEND_HOST:-battlefield.localhost}"
grafana_host="${SMOKE_TEST_GRAFANA_HOST:-grafana.localhost}"
kibana_host="${SMOKE_TEST_KIBANA_HOST:-kibana.localhost}"
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

ports=("${api_port}" "${prometheus_port}" "${elasticsearch_port}" "${gateway_port}")
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
  service/battlefield-prometheus \
  "${prometheus_port}:9090" \
  >"${runtime_dir}/prometheus-port-forward.log" 2>&1 &
pids+=("$!")

kube port-forward \
  --namespace "${namespace}" \
  service/battlefield-api \
  "${api_port}:8080" \
  >"${runtime_dir}/api-port-forward.log" 2>&1 &
pids+=("$!")

kube port-forward \
  --namespace "${namespace}" \
  service/battlefield-elasticsearch \
  "${elasticsearch_port}:9200" \
  >"${runtime_dir}/elasticsearch-port-forward.log" 2>&1 &
pids+=("$!")

wait_for_url() {
  local name="$1"
  local url="$2"
  local resolve="${3:-}"
  local curl_args=(--fail --silent --max-time 2 --noproxy '*')

  if [[ -n "${resolve}" ]]; then
    curl_args+=(--resolve "${resolve}")
  fi

  for _ in {1..30}; do
    if curl "${curl_args[@]}" "${url}" >/dev/null; then
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

wait_for_url \
  "Frontend liveness through Gateway API" \
  "http://${frontend_host}:${gateway_port}/livez" \
  "${frontend_host}:${gateway_port}:127.0.0.1"
wait_for_url "API readiness" "http://127.0.0.1:${api_port}/readyz"
wait_for_url \
  "Gateway API application route" \
  "http://${frontend_host}:${gateway_port}/v1/scenarios" \
  "${frontend_host}:${gateway_port}:127.0.0.1"
wait_for_url \
  "API request for local metrics sample" \
  "http://127.0.0.1:${api_port}/v1/scenarios"
wait_for_url "API metrics" "http://127.0.0.1:${api_port}/metrics"
wait_for_url "Prometheus readiness" "http://127.0.0.1:${prometheus_port}/-/ready"
wait_for_url \
  "Grafana health through Gateway API" \
  "http://${grafana_host}:${gateway_port}/api/health" \
  "${grafana_host}:${gateway_port}:127.0.0.1"
wait_for_url \
  "Kibana health through Gateway API" \
  "http://${kibana_host}:${gateway_port}/api/status" \
  "${kibana_host}:${gateway_port}:127.0.0.1"
wait_for_url \
  "Elasticsearch readiness" \
  "http://127.0.0.1:${elasticsearch_port}/_cluster/health"

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
wait_for_prometheus_query \
  "Prometheus kube-state-metrics discovery" \
  'sum(up{job="battlefield-pods",scrape_pod=~"battlefield-kube-state-metrics-.+"})' \
  1
wait_for_prometheus_query \
  "Kubernetes workload state metrics" \
  'sum(kube_pod_info{namespace="battlefield"})' \
  1
wait_for_prometheus_query \
  "Kubernetes container readiness metrics" \
  'sum(kube_pod_container_status_ready{namespace="battlefield"})' \
  1
wait_for_prometheus_query \
  "Prometheus kubelet cAdvisor discovery" \
  'sum(up{job="kubernetes-cadvisor"})' \
  1
wait_for_prometheus_query \
  "Kubernetes pod resource metrics" \
  'count(container_memory_working_set_bytes{namespace="battlefield",pod!=""})' \
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
  --noproxy '*' \
  --resolve "${grafana_host}:${gateway_port}:127.0.0.1" \
  --get \
  --data-urlencode 'query=Autonomous Battlefield Overview' \
  --output "${runtime_dir}/grafana-search.json" \
  "http://${grafana_host}:${gateway_port}/api/search"
if ! grep -q '"uid":"battlefield-overview"' \
  "${runtime_dir}/grafana-search.json"; then
  echo "Grafana did not provision the Autonomous Battlefield dashboard." >&2
  exit 1
fi

curl --fail --silent --max-time 2 \
  --noproxy '*' \
  --resolve "${grafana_host}:${gateway_port}:127.0.0.1" \
  --get \
  --data-urlencode 'query=Kubernetes Workloads' \
  --output "${runtime_dir}/grafana-kubernetes-search.json" \
  "http://${grafana_host}:${gateway_port}/api/search"
if ! grep -q '"uid":"battlefield-kubernetes"' \
  "${runtime_dir}/grafana-kubernetes-search.json"; then
  echo "Grafana did not provision the Kubernetes Workloads dashboard." >&2
  exit 1
fi

curl --fail --silent --max-time 2 \
  --noproxy '*' \
  --resolve "${kibana_host}:${gateway_port}:127.0.0.1" \
  --output "${runtime_dir}/kibana-data-views.json" \
  "http://${kibana_host}:${gateway_port}/api/data_views"
if ! grep -Eq '"id"[[:space:]]*:[[:space:]]*"battlefield-logs"' \
  "${runtime_dir}/kibana-data-views.json"; then
  echo "Kibana did not provision the Autonomous Battlefield Logs data view." >&2
  exit 1
fi

curl --fail --silent --max-time 2 \
  --noproxy '*' \
  --resolve "${kibana_host}:${gateway_port}:127.0.0.1" \
  --output "${runtime_dir}/kibana-ai-decisions-search.json" \
  "http://${kibana_host}:${gateway_port}/api/saved_objects/search/battlefield-ai-decisions"
if ! grep -Eq '"title"[[:space:]]*:[[:space:]]*"Battlefield AI Decisions"' \
  "${runtime_dir}/kibana-ai-decisions-search.json"; then
  echo "Kibana did not provision the Battlefield AI Decisions saved search." >&2
  exit 1
fi

wait_for_elasticsearch_logs() {
  local output_file="${runtime_dir}/elasticsearch-application-log-count.json"

  for _ in {1..30}; do
    if curl --fail --silent --max-time 2 \
      --output "${output_file}" \
      "http://127.0.0.1:${elasticsearch_port}/battlefield-app-*/_count?q=service:api" \
      && grep -Eq '"count"[[:space:]]*:[[:space:]]*[1-9][0-9]*' "${output_file}"; then
      echo "Elasticsearch structured application logs: OK"
      return 0
    fi
    sleep 1
  done

  echo "Elasticsearch did not receive a structured API log." >&2
  sed -n '1,120p' "${output_file}" >&2
  return 1
}

wait_for_elasticsearch_logs

echo "Local kind application and observability smoke test completed successfully."
