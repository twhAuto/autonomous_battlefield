#!/usr/bin/env bash
set -Eeuo pipefail

cluster_name="${KIND_CLUSTER_NAME:-battlefield}"
context="${KUBERNETES_CONTEXT:-kind-${cluster_name}}"
namespace="${KUBERNETES_NAMESPACE:-battlefield}"
report_dir="${REPORT_DIR:-artifacts/failure-drills}"

if [[ "${RUN_DESTRUCTIVE_KIND_DRILLS:-}" != "I_UNDERSTAND_THIS_RESTARTS_DISPOSABLE_KIND_WORKLOADS" ]]; then
  echo "Set RUN_DESTRUCTIVE_KIND_DRILLS=I_UNDERSTAND_THIS_RESTARTS_DISPOSABLE_KIND_WORKLOADS." >&2
  exit 2
fi
if [[ "${context}" != kind-* ]]; then
  echo "Failure drills are restricted to a disposable Kind context." >&2
  exit 2
fi

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
