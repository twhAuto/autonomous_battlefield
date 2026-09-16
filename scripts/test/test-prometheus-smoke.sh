#!/usr/bin/env bash
# Exercise delayed convergence, bounded failure, and timeout diagnostics without a cluster.
set -euo pipefail
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${project_dir}/scripts/lib/prometheus-smoke.sh"
runtime_dir="$(mktemp -d)"
trap 'rm -rf "${runtime_dir}"' EXIT
prometheus_port=9090
SMOKE_TEST_PROMETHEUS_TIMEOUT_SECONDS=60
calls=0
scenario=delayed
curl() {
  local output="" url="" arg
  while (( $# )); do
    arg="$1"; shift
    if [[ "$arg" == --output ]]; then output="$1"; shift; fi
    url="$arg"
  done
  if [[ "$url" == *'/targets?'* ]]; then
    if [[ "$scenario" == unavailable ]]; then return 22; fi
    if [[ "$scenario" == malformed ]]; then echo 'invalid' > "$output"; return 0; fi
    cat > "$output" <<'JSON'
{"status":"success","data":{"activeTargets":[{"labels":{"job":"battlefield-pods","component":"api","scrape_pod":"api-test"},"health":"down","lastError":"connection refused","lastScrape":"2026-09-17T00:00:00Z"}]}}
JSON
    return 0
  fi
  calls=$((calls+1))
  local value=0
  if [[ "$scenario" == delayed ]] && (( calls >= 35 )); then value=1; fi
  printf '{"status":"success","data":{"result":[{"value":[0,"%s"]}]}}' "$value" > "$output"
}
# Advance Bash's elapsed-time counter instead of sleeping in deterministic tests.
sleep() { SECONDS=$((SECONDS+$1)); }
wait_for_prometheus_query "API" 'sum(up)' 1 > "$runtime_dir/result" 2>&1
[[ "$calls" == 35 ]]
grep -q 'API: OK' "$runtime_dir/result"
SMOKE_TEST_PROMETHEUS_TIMEOUT_SECONDS=4
for scenario in down unavailable malformed; do
  calls=0
  if wait_for_prometheus_query "API" 'sum(up)' 1 > "$runtime_dir/result" 2>&1; then
    echo "Unhealthy targets incorrectly passed" >&2; exit 1
  fi
  (( calls <= 4 ))
  grep -q 'within 4s' "$runtime_dir/result"
  if [[ "$scenario" == down ]]; then
    grep -q 'connection refused' "$runtime_dir/result"
    grep -q 'api-test' "$runtime_dir/result"
  else
    grep -q 'diagnostics unavailable' "$runtime_dir/result"
  fi
done
for timeout in 0 -1 nope 601; do
  SMOKE_TEST_PROMETHEUS_TIMEOUT_SECONDS="$timeout"
  if wait_for_prometheus_query "API" 'sum(up)' 1 > "$runtime_dir/result" 2>&1; then exit 1; fi
  grep -q 'integer from 1 to 600' "$runtime_dir/result"
done
echo 'Prometheus startup and diagnostic checks passed.'
