#!/usr/bin/env bash
# Bounded rollout settling and target diagnostics for the Prometheus smoke checks.

wait_for_prometheus_api_targets() {
  local timeout="${SMOKE_TEST_PROMETHEUS_TIMEOUT_SECONDS:-120}"
  if [[ ! "${timeout}" =~ ^[1-9][0-9]{0,2}$ ]] || (( timeout > 600 )); then
    echo 'SMOKE_TEST_PROMETHEUS_TIMEOUT_SECONDS must be an integer from 1 to 600.' >&2
    return 1
  fi
  local deadline=$((SECONDS + timeout))
  while (( SECONDS < deadline )); do
    if kube -n "${namespace}" --request-timeout=5s get deployment battlefield-api -o json >"${runtime_dir}/api-deployment.json" \
      && kube -n "${namespace}" --request-timeout=5s get pods -l app.kubernetes.io/name=autonomous-battlefield,app.kubernetes.io/component=api -o json >"${runtime_dir}/api-pods.json" \
      && curl --fail --silent --noproxy '*' --max-time 5 --output "${runtime_dir}/api-targets.json" \
        "http://127.0.0.1:${prometheus_port}/api/v1/targets?state=active" \
      && node "${project_dir}/scripts/lib/prometheus-targets.cjs" "${runtime_dir}/api-deployment.json" \
        "${runtime_dir}/api-pods.json" "${runtime_dir}/api-targets.json"; then
      echo 'Prometheus scrapes all current ready API replicas: OK'
      return 0
    fi
    if (( SECONDS < deadline )); then sleep 1; fi
  done
  echo "Prometheus did not freshly scrape every current API replica within ${timeout}s." >&2
  prometheus_target_diagnostics
  return 1
}

prometheus_target_diagnostics() {
  local targets_file="${runtime_dir}/prometheus-targets.json"
  if ! curl --fail --silent --max-time 5 \
    --output "${targets_file}" \
    "http://127.0.0.1:${prometheus_port}/api/v1/targets?state=active"; then
    echo "Prometheus target diagnostics unavailable (request failed)." >&2
    return 0
  fi
  node - "${targets_file}" <<'JS' >&2 || true
const fs = require('fs');
try {
  const response = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
  if (response.status !== 'success' || !Array.isArray(response.data?.activeTargets)) {
    throw new Error('unexpected response');
  }
  const targets = response.data.activeTargets.filter(t =>
    ['battlefield-pods', 'kubernetes-cadvisor'].includes(t.labels?.job));
  console.error(`Prometheus active application/node targets: ${targets.length}`);
  for (const t of targets.slice(0, 50)) {
    console.error(JSON.stringify({
      job: t.labels.job, pod: t.labels.scrape_pod, component: t.labels.component,
      health: t.health, lastScrape: t.lastScrape,
      lastError: String(t.lastError || '').slice(0, 500),
    }));
  }
} catch {
  console.error('Prometheus target diagnostics unavailable (invalid response).');
}
JS
}

wait_for_prometheus_query() {
  local name="$1" query="$2" minimum="$3"
  local timeout="${SMOKE_TEST_PROMETHEUS_TIMEOUT_SECONDS:-120}"
  if [[ ! "${timeout}" =~ ^[1-9][0-9]{0,2}$ ]] || (( timeout > 600 )); then
    echo "SMOKE_TEST_PROMETHEUS_TIMEOUT_SECONDS must be an integer from 1 to 600." >&2
    return 1
  fi
  local deadline=$((SECONDS + timeout))
  local remaining request_timeout
  local output_file="${runtime_dir}/prometheus-query.json"
  while (( SECONDS < deadline )); do
    remaining=$((deadline - SECONDS))
    if (( remaining <= 0 )); then break; fi
    request_timeout=2
    if (( remaining < request_timeout )); then request_timeout="${remaining}"; fi
    if curl --fail --silent --max-time "${request_timeout}" \
      --get --data-urlencode "query=${query}" --output "${output_file}" \
      "http://127.0.0.1:${prometheus_port}/api/v1/query" \
      && node -e '
        const fs = require("fs");
        try {
          const response = JSON.parse(fs.readFileSync(process.argv[1], "utf8"));
          const value = Number(response.data?.result?.[0]?.value?.[1]);
          process.exit(response.status === "success" && Number.isFinite(value) && value >= Number(process.argv[2]) ? 0 : 1);
        } catch { process.exit(1); }
      ' "${output_file}" "${minimum}"; then
      echo "${name}: OK"
      return 0
    fi
    if (( SECONDS < deadline )); then sleep 1; fi
  done
  echo "${name} did not reach ${minimum} healthy target(s) within ${timeout}s: ${query}" >&2
  prometheus_target_diagnostics
  return 1
}
