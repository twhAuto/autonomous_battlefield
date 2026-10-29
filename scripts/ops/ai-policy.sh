#!/usr/bin/env bash
# Private operator control. Uses an existing kubectl context; does not expose a web route.
set -Eeuo pipefail
case "${1:-}" in
  stop) sql='UPDATE runtime_policy SET ai_enabled=false WHERE id;';;
  start) sql='UPDATE runtime_policy SET ai_enabled=true WHERE id;';;
  status) sql='SELECT * FROM runtime_policy; SELECT count(*) AS active_cli_leases FROM agent_execution_leases WHERE expires_at>now();';;
  cli-slots)
    # The league scheduler plans with this many CLI sides; match the agents' combined slots.
    [[ "${2:-}" =~ ^[1-8]$ ]] || { echo 'cli-slots needs a whole number from 1 to 8' >&2; exit 2; }
    sql="UPDATE runtime_policy SET cli_concurrency=${2} WHERE id; SELECT cli_concurrency FROM runtime_policy;";;
  usage) sql="SELECT identity,profile_id,count(*) AS records,sum(COALESCE((usage->>'requests')::bigint,0)) AS requests,sum(COALESCE((usage->>'inputTokens')::bigint,0)) AS input_tokens,sum(COALESCE((usage->>'outputTokens')::bigint,0)) AS output_tokens FROM friend_ai_usage WHERE created_at >= date_trunc('day',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' GROUP BY identity,profile_id;";;
  *) echo 'Usage: ai-policy.sh stop|start|status|usage|cli-slots N' >&2; exit 2;;
esac
# Select the PostgreSQL pod explicitly; this script never guesses another database.
: "${BATTLEFIELD_POSTGRES_POD:?Set BATTLEFIELD_POSTGRES_POD in the intended kubectl context}"
context=()
if [[ -n "${DEPLOY_ENV:-}" ]]; then
  # A named environment uses its registered kubeconfig and context, and refuses a
  # cluster whose identity does not match, instead of the shell's current context.
  # Like rollback, it skips platform readiness so the stop switch works during an
  # unrelated controller incident.
  source "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../lib" && pwd)/deployment-environment.sh"
  battlefield_env_preflight verify-recovery
  context=(--context "${KUBERNETES_CONTEXT}")
fi
printf '%s\n' "${sql}" | kubectl "${context[@]}" exec -i --namespace "${KUBERNETES_NAMESPACE:-battlefield}" "${BATTLEFIELD_POSTGRES_POD}" -- sh -c 'exec psql --no-psqlrc --set ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB"'
