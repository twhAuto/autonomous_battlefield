#!/usr/bin/env bash
# Checks deployed application flows through temporary port forwards and optionally
# verifies metrics, logs, and tracing endpoints.

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${project_dir}/scripts/lib/deployment-environment.sh"
battlefield_env_preflight

# Verifies the registered application and optionally its observability endpoints.

cluster_name="${KIND_CLUSTER_NAME:-battlefield}"
namespace="${KUBERNETES_NAMESPACE:-battlefield}"
kube_context="${KUBERNETES_CONTEXT:-kind-${cluster_name}}"
api_port="${SMOKE_TEST_API_PORT:-18081}"
prometheus_port="${SMOKE_TEST_PROMETHEUS_PORT:-18082}"
elasticsearch_port="${SMOKE_TEST_ELASTICSEARCH_PORT:-18083}"
tempo_port="${SMOKE_TEST_TEMPO_PORT:-18084}"
gateway_port="${SMOKE_TEST_GATEWAY_PORT:-8888}"
grafana_host="${SMOKE_TEST_GRAFANA_HOST:-grafana.localhost}"
kibana_host="${SMOKE_TEST_KIBANA_HOST:-kibana.localhost}"
auth_secret_name="${SMOKE_TEST_AUTH_SECRET_NAME:-battlefield-auth}"
runtime_dir="$(mktemp -d "${TMPDIR:-/tmp}/autonomous-battlefield-smoke.XXXXXX")"
application_cookie_file=""
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

for command in base64 curl kubectl node; do
  if ! command -v "${command}" >/dev/null 2>&1; then
    echo "Required command not found: ${command}" >&2
    exit 1
  fi
done

ports=("${api_port}" "${prometheus_port}" "${elasticsearch_port}" "${tempo_port}" "${gateway_port}")
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

if [[ "${SMOKE_TEST_OBSERVABILITY}" == 1 ]]; then
  kube port-forward \
    --namespace "${namespace}" \
    service/battlefield-prometheus \
    "${prometheus_port}:9090" \
    >"${runtime_dir}/prometheus-port-forward.log" 2>&1 &
  pids+=("$!")
fi

if [[ "${SMOKE_TEST_OBSERVABILITY}" == 1 ]]; then
  kube port-forward \
    --namespace "${namespace}" \
    service/battlefield-tempo \
    "${tempo_port}:3200" \
    >"${runtime_dir}/tempo-port-forward.log" 2>&1 &
  pids+=("$!")
fi

kube port-forward \
  --namespace "${namespace}" \
  service/battlefield-api \
  "${api_port}:8080" \
  >"${runtime_dir}/api-port-forward.log" 2>&1 &
pids+=("$!")

if [[ "${SMOKE_TEST_OBSERVABILITY}" == 1 ]]; then
  kube port-forward \
    --namespace "${namespace}" \
    service/battlefield-elasticsearch \
    "${elasticsearch_port}:9200" \
    >"${runtime_dir}/elasticsearch-port-forward.log" 2>&1 &
  pids+=("$!")
fi

wait_for_url() {
  local name="$1"
  local url="$2"
  local resolve="${3:-}"
  local cookie_file="${4:-}"
  local curl_args=(--fail --silent --max-time 2 --noproxy '*')

  if [[ -n "${resolve}" ]]; then
    curl_args+=(--resolve "${resolve}")
  fi
  if [[ -n "${cookie_file}" ]]; then
    curl_args+=(--cookie "${cookie_file}")
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

read_auth_secret_value() {
  local key="$1"
  local encoded_value=""

  encoded_value="$(
    kube get secret "${auth_secret_name}" \
      --namespace "${namespace}" \
      -o "jsonpath={.data.${key}}" \
      2>/dev/null || true
  )"
  if [[ -z "${encoded_value}" ]]; then
    return 1
  fi
  printf '%s' "${encoded_value}" | base64 --decode
}

prepare_application_session() {
  local auth_enabled=""
  local cookie_secure=""
  local username=""
  local password=""
  local login_payload="${runtime_dir}/login.json"
  local login_status=""

  local auth_mode
  auth_mode="$(read_auth_secret_value BATTLEFIELD_AUTH_MODE || true)"
  if [[ "${auth_mode}" == cloudflare ]]; then
    : "${BATTLEFIELD_ACCESS_SERVICE_TOKEN_FILE:?Production smoke requires a private CI service credential file}"
    BATTLEFIELD_SMOKE_ORIGIN="$(read_auth_secret_value BATTLEFIELD_PUBLIC_ORIGIN)" \
    BATTLEFIELD_SMOKE_CI_CLIENT_ID="$(read_auth_secret_value BATTLEFIELD_ACCESS_CI_CLIENT_ID)" \
    BATTLEFIELD_SMOKE_ISSUER="$(read_auth_secret_value BATTLEFIELD_ACCESS_ISSUER)" \
      python3 "${project_dir}/scripts/ops/verify-access-service.py" "http://127.0.0.1:${api_port}"
    return
  fi
  if [[ "${DEPLOY_ENV}" == production ]]; then
    echo 'Production smoke refuses a non-Cloudflare authentication mode.' >&2
    return 1
  fi
  auth_enabled="$(read_auth_secret_value BATTLEFIELD_AUTH_ENABLED || true)"
  case "${auth_enabled,,}" in
    "" | false)
      return
      ;;
    true)
      ;;
    *)
      echo "${auth_secret_name} has an invalid BATTLEFIELD_AUTH_ENABLED value." >&2
      return 1
      ;;
  esac

  cookie_secure="$(read_auth_secret_value BATTLEFIELD_AUTH_COOKIE_SECURE || true)"
  case "${cookie_secure,,}" in
    false)
      ;;
    true)
      [[ "${TARGET_URL}" == https://* ]] || { echo "Secure auth cookies require an HTTPS TARGET_URL." >&2; return 1; }
      ;;
    *)
      echo "${auth_secret_name} has an invalid BATTLEFIELD_AUTH_COOKIE_SECURE value." >&2
      return 1
      ;;
  esac

  username="$(read_auth_secret_value BATTLEFIELD_ADMIN_USERNAME || true)"
  password="$(read_auth_secret_value BATTLEFIELD_ADMIN_PASSWORD || true)"
  if [[ -z "${username}" || -z "${password}" ]]; then
    echo "${auth_secret_name} does not contain smoke-test login credentials." >&2
    return 1
  fi

  printf '%s\0%s' "${username}" "${password}" | node -e '
    const fs = require("fs");
    const input = fs.readFileSync(0);
    const separator = input.indexOf(0);
    if (separator < 0) process.exit(1);
    process.stdout.write(JSON.stringify({
      username: input.subarray(0, separator).toString("utf8"),
      password: input.subarray(separator + 1).toString("utf8"),
    }));
  ' >"${login_payload}"
  chmod 600 "${login_payload}"

  application_cookie_file="${runtime_dir}/application-cookies.txt"
  touch "${application_cookie_file}"
  chmod 600 "${application_cookie_file}"
  login_status="$(
    curl --silent --show-error --max-time 5 \
      --noproxy '*' \
      --header 'Content-Type: application/json' \
      --data-binary "@${login_payload}" \
      --cookie-jar "${application_cookie_file}" \
      --output /dev/null \
      --write-out '%{http_code}' \
      "${TARGET_URL}/v1/auth/login"
  )"
  if [[ "${login_status}" != "204" ]]; then
    echo "Smoke-test administrator login failed with HTTP ${login_status}." >&2
    return 1
  fi
  echo "Administrator login through Gateway API: OK"
}

if [[ "${DEPLOY_ENV}" == production ]]; then
  frontend_port="${SMOKE_TEST_FRONTEND_PORT:-18085}"
  kube port-forward --namespace "${namespace}" service/battlefield-frontend "${frontend_port}:8080" >"${runtime_dir}/frontend-port-forward.log" 2>&1 &
  pids+=("$!")
  wait_for_url "Frontend liveness through private port forward" "http://127.0.0.1:${frontend_port}/livez"
else
  wait_for_url "Frontend liveness through Gateway API" "${TARGET_URL}/livez"
fi
wait_for_url "API readiness" "http://127.0.0.1:${api_port}/readyz"
prepare_application_session
if [[ "${DEPLOY_ENV}" != production ]]; then
  wait_for_url "Gateway API application route" "${TARGET_URL}/v1/scenarios" "" "${application_cookie_file}"
fi
wait_for_url "API metrics" "http://127.0.0.1:${api_port}/metrics"
if [[ "${SMOKE_TEST_OBSERVABILITY}" != 1 ]]; then
  echo "${DEPLOY_ENV} application smoke test completed successfully."
  exit 0
fi
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
wait_for_url "Tempo readiness" "http://127.0.0.1:${tempo_port}/ready"

source "${project_dir}/scripts/lib/prometheus-smoke.sh"

wait_for_prometheus_api_targets
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

# Gateway traffic may reach a different replica. Probes and /metrics bypass
# request instrumentation, so exercise an application route on this forward.
# An anonymous session returns 401 when authentication is enabled, or 200 otherwise.
session_status="$(curl --silent --show-error --max-time 2 --noproxy '*' \
  --output /dev/null --write-out '%{http_code}' \
  "http://127.0.0.1:${api_port}/v1/auth/session")"
if [[ "${session_status}" != 200 && "${session_status}" != 401 ]]; then
  echo "API metrics session probe failed with HTTP ${session_status}." >&2
  exit 1
fi

# Instrumentation records the request after the response is written.
for _ in {1..30}; do
  if curl --fail --silent --max-time 2 --noproxy '*' \
    --output "${runtime_dir}/api-metrics.txt" \
    "http://127.0.0.1:${api_port}/metrics" \
    && grep -q '^battlefield_http_server_requests_total' "${runtime_dir}/api-metrics.txt"; then
    break
  fi
  sleep 1
done
if ! grep -q '^battlefield_http_server_requests_total' "${runtime_dir}/api-metrics.txt"; then
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
  --resolve "${grafana_host}:${gateway_port}:127.0.0.1" \
  --get \
  --data-urlencode 'query=Battle Traces' \
  --output "${runtime_dir}/grafana-traces-search.json" \
  "http://${grafana_host}:${gateway_port}/api/search"
if ! grep -q '"uid":"battle-traces"' \
  "${runtime_dir}/grafana-traces-search.json"; then
  echo "Grafana did not provision the Battle Traces dashboard." >&2
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

echo "${DEPLOY_ENV} application and observability smoke test completed successfully."
