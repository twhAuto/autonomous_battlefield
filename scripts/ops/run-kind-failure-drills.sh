#!/usr/bin/env bash
# Restarts engine, database, and MCP workloads in an explicitly registered disposable dev
# cluster, verifies each was replaced and recovered, then runs smoke checks and writes a
# recovery report.

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${project_dir}/scripts/lib/deployment-environment.sh"
battlefield_env_preflight

cluster_name="${KIND_CLUSTER_NAME:-battlefield}"
context="${KUBERNETES_CONTEXT:-kind-${cluster_name}}"
namespace="${KUBERNETES_NAMESPACE:-battlefield}"
report_dir="${REPORT_DIR:-artifacts/failure-drills}"
recovery_timeout="${DRILL_RECOVERY_TIMEOUT_SECONDS:-180}"
mcp_outage_seconds="${MCP_OUTAGE_SECONDS:-30}"

if [[ "${RUN_DESTRUCTIVE_KIND_DRILLS:-}" != "I_UNDERSTAND_THIS_RESTARTS_DISPOSABLE_KIND_WORKLOADS" ]]; then
  echo "Set RUN_DESTRUCTIVE_KIND_DRILLS=I_UNDERSTAND_THIS_RESTARTS_DISPOSABLE_KIND_WORKLOADS." >&2
  exit 2
fi
if [[ "${DEPLOY_ENV}" != dev || "${BF_DISPOSABLE:-0}" != 1 ]]; then
  echo 'Failure drills require an explicitly registered disposable dev cluster; production is forbidden.' >&2
  exit 2
fi
battlefield_env_verify_docker

k() { kubectl --context "${context}" --namespace "${namespace}" "$@"; }
pod_uids() { k get pods -l "$1" -o 'jsonpath={.items[*].metadata.uid}'; }

# Waits until every pod matching the selector is new (none of the given UIDs) and Ready,
# then prints the seconds elapsed since the given start time.
await_replacement() {
  local selector="$1" old_uids="$2" since="$3" deadline=$((SECONDS + recovery_timeout)) uids uid replaced
  while ((SECONDS < deadline)); do
    uids="$(pod_uids "${selector}")"
    replaced=1
    for uid in ${old_uids}; do [[ " ${uids} " == *" ${uid} "* ]] && replaced=0; done
    if [[ -n "${uids}" && "${replaced}" == 1 ]] \
      && k wait pod -l "${selector}" --for=condition=Ready --timeout=5s >/dev/null 2>&1; then
      echo $((SECONDS - since))
      return 0
    fi
    sleep 1
  done
  echo "Pods for ${selector} were not replaced and Ready within ${recovery_timeout}s." >&2
  return 1
}

# Deletes the selected pods and reports how long the replacement took to become Ready.
# An empty selector would otherwise "pass" without disrupting anything.
restart_drill() {
  local selector="$1" before since
  before="$(pod_uids "${selector}")"
  [[ -n "${before}" ]] || { echo "No pods match ${selector}; the drill would not disrupt anything." >&2; return 1; }
  since=${SECONDS}
  k delete pod -l "${selector}" --wait=false >&2
  await_replacement "${selector}" "${before}" "${since}"
}

mkdir -p "${report_dir}"
started="$(date -u +%FT%TZ)"

engine_seconds="$(restart_drill app.kubernetes.io/component=engine)"
database_seconds="$(restart_drill app.kubernetes.io/component=database)"

mcp_selector=app.kubernetes.io/component=mcp
mcp_before="$(pod_uids "${mcp_selector}")"
[[ -n "${mcp_before}" ]] || { echo "No pods match ${mcp_selector}; the drill would not disrupt anything." >&2; exit 1; }
k scale deployment/battlefield-mcp --replicas=0 >&2
sleep "${mcp_outage_seconds}"
mcp_since=${SECONDS}
k scale deployment/battlefield-mcp --replicas=1 >&2
mcp_seconds="$(await_replacement "${mcp_selector}" "${mcp_before}" "${mcp_since}")"

"${project_dir}/scripts/cd/smoke-test-kind.sh"
cat >"${report_dir}/summary.json" <<EOF
{"schemaVersion":"battlefield.failure-drill-report.v2","startedAt":"${started}","completedAt":"$(date -u +%FT%TZ)","context":"${context}","drills":[{"name":"engine-restart","recoverySeconds":${engine_seconds}},{"name":"postgresql-restart","recoverySeconds":${database_seconds}},{"name":"mcp-outage","outageSeconds":${mcp_outage_seconds},"recoverySeconds":${mcp_seconds}}],"outcome":"passed"}
EOF
