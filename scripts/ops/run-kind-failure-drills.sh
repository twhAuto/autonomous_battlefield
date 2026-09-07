#!/usr/bin/env bash
# Restarts engine, database, and MCP workloads in an explicitly registered disposable dev
# cluster, then runs smoke checks and writes a recovery report.

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${project_dir}/scripts/lib/deployment-environment.sh"
battlefield_env_preflight

cluster_name="${KIND_CLUSTER_NAME:-battlefield}"
context="${KUBERNETES_CONTEXT:-kind-${cluster_name}}"
namespace="${KUBERNETES_NAMESPACE:-battlefield}"
report_dir="${REPORT_DIR:-artifacts/failure-drills}"

if [[ "${RUN_DESTRUCTIVE_KIND_DRILLS:-}" != "I_UNDERSTAND_THIS_RESTARTS_DISPOSABLE_KIND_WORKLOADS" ]]; then
  echo "Set RUN_DESTRUCTIVE_KIND_DRILLS=I_UNDERSTAND_THIS_RESTARTS_DISPOSABLE_KIND_WORKLOADS." >&2
  exit 2
fi
if [[ "${DEPLOY_ENV}" != dev || "${BF_DISPOSABLE:-0}" != 1 ]]; then
  echo 'Failure drills require an explicitly registered disposable dev cluster; production is forbidden.' >&2
  exit 2
fi
battlefield_env_verify_docker

mkdir -p "${report_dir}"
started="$(date -u +%FT%TZ)"

kubectl --context "${context}" delete pod --namespace "${namespace}" \
  -l app.kubernetes.io/component=engine --wait=false
kubectl --context "${context}" rollout status deployment/battlefield-engine \
  --namespace "${namespace}" --timeout=180s

kubectl --context "${context}" delete pod --namespace "${namespace}" \
  -l app.kubernetes.io/name=postgres --wait=false
kubectl --context "${context}" rollout status statefulset/battlefield-postgres \
  --namespace "${namespace}" --timeout=180s

kubectl --context "${context}" scale deployment/battlefield-mcp --namespace "${namespace}" --replicas=0
kubectl --context "${context}" scale deployment/battlefield-mcp --namespace "${namespace}" --replicas=1
kubectl --context "${context}" rollout status deployment/battlefield-mcp \
  --namespace "${namespace}" --timeout=180s

./scripts/cd/smoke-test-kind.sh
cat >"${report_dir}/summary.json" <<EOF
{"schemaVersion":"battlefield.failure-drill-report.v1","startedAt":"${started}","completedAt":"$(date -u +%FT%TZ)","context":"${context}","drills":["engine-restart-and-lease-reclaim","postgresql-restart","mcp-outage-and-recovery"],"outcome":"passed"}
EOF
