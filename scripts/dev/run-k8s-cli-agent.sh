#!/usr/bin/env bash
# Runs a host-side model CLI agent against the registered dev cluster through temporary
# AI gateway forwarding and a generated agent configuration.

set -Eeuo pipefail
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${project_dir}/scripts/lib/deployment-environment.sh"
[[ "${DEPLOY_ENV:-dev}" == dev ]] || { echo "Local CLI agent requires dev." >&2; exit 1; }
battlefield_env_preflight


project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
backend_dir="${project_dir}/backend"
requested_provider="${1:-auto}"
export PATH="${HOME}/.local/bin:${PATH}"
cluster_name="${KIND_CLUSTER_NAME:-battlefield}"
kube_context="${KUBERNETES_CONTEXT:-kind-${cluster_name}}"
namespace="${KUBERNETES_NAMESPACE:-battlefield}"
gateway_port="${HOST_AI_GATEWAY_PORT:-18084}"
health_port="${HOST_AGENT_HEALTH_PORT:-18083}"
# Concurrent decision slots across all enabled CLIs. Eight lets both factions of four
# concurrent model-vs-model battles decide at once; the worker accepts 1-8, and the
# database runtime_policy.cli_concurrency still caps executions across all workers.
agent_concurrency="${HOST_AGENT_CONCURRENCY:-8}"
validate_agent_concurrency() {
  if [[ ! "${agent_concurrency}" =~ ^[1-8]$ ]]; then
    echo "HOST_AGENT_CONCURRENCY must be a whole number from 1 to 8." >&2
    exit 2
  fi
}
validate_agent_concurrency
cli_minimum_version() {
  local value="${1#codex-cli }" major minor patch
  IFS=. read -r major minor patch <<<"${value% }"
  printf '%s.%s.%s' "${major}" "${minor:-0}" "${patch:-0}"
}

# Local CLIs allow the selected version or newer.
resolve_version_prefixes() {
  codex_version_prefix='codex-cli 0.155.'
  claude_version_prefix='2.1.267 '
  antigravity_version_prefix='1.2.'
  codex_minimum_version="$(cli_minimum_version "${codex_version_prefix}")"
  claude_minimum_version="$(cli_minimum_version "${claude_version_prefix}")"
  antigravity_minimum_version="$(cli_minimum_version "${antigravity_version_prefix}")"
}
resolve_version_prefixes
runtime_dir="$(mktemp -d "${TMPDIR:-/tmp}/battlefield-host-agent.XXXXXX")"
port_forward_pid=""
agent_pid=""
otel_forward_pid=""
profiles_enabled=0
use_codex=0
use_gemini=0
use_claude=0

kube() {
  kubectl --context "${kube_context}" --namespace "${namespace}" "$@"
}

# Background kubectl itself rather than the kube function, so $! names the forward
# that cleanup stops and not a wrapper subshell that would leave it running.
start_port_forward() {
  local log="$1"
  shift
  kubectl --context "${kube_context}" --namespace "${namespace}" port-forward --address 127.0.0.1 "$@" >"${log}" 2>&1 &
}

set_profile_flags() {
  local codex_value="$1"
  local gemini_value="$2"
  local claude_value="$3"
  local patch
  patch="$(printf '{"stringData":{"CODEX_CLI_PROFILE_AVAILABLE":"%s","GEMINI_CLI_PROFILE_AVAILABLE":"%s","CLAUDE_CLI_PROFILE_AVAILABLE":"%s"}}' "${codex_value}" "${gemini_value}" "${claude_value}")"
  kube patch secret battlefield-ai --type merge --patch "${patch}" >/dev/null
}

cleanup() {
  local exit_code=$?
  trap - EXIT INT TERM

  if [[ "${profiles_enabled}" == "1" ]]; then
    echo "Disabling host CLI profiles in the Kubernetes battle form..."
    set_profile_flags false false false || true
    kube rollout restart deployment/battlefield-api >/dev/null 2>&1 || true
  fi
  for process_id in "${agent_pid}" "${port_forward_pid}" "${otel_forward_pid}"; do
    if [[ -n "${process_id}" ]]; then
      kill "${process_id}" 2>/dev/null || true
    fi
  done
  for process_id in "${agent_pid}" "${port_forward_pid}" "${otel_forward_pid}"; do
    if [[ -n "${process_id}" ]]; then
      wait "${process_id}" 2>/dev/null || true
    fi
  done
  rm -rf -- "${runtime_dir}"
  unset BATTLEFIELD_AGENT_SECRET
  exit "${exit_code}"
}

trap cleanup EXIT INT TERM

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Required command not found: $1" >&2
    exit 1
  fi
}

accept_cli_version() {
  local binary="$1" prefix_name="$2"
  local version prefix="${!prefix_name}" minimum numeric
  version="$("${binary}" --version 2>/dev/null)" || return 1
  numeric="${version}"
  case "${binary}" in
    codex) [[ "${numeric}" == 'codex-cli '* ]] || return 1; numeric="${numeric#codex-cli }" ;;
    claude) numeric="${numeric% (Claude Code)}" ;;
  esac
  # Stable three-part releases only; reject prereleases and unexpected banners.
  # Bound components before Bash arithmetic and interpret leading zeros as decimal.
  [[ "${numeric}" =~ ^([0-9]{1,9})\.([0-9]{1,9})\.([0-9]{1,9})$ ]] || return 1
  local installed=("${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}" "${BASH_REMATCH[3]}")
  minimum="$(cli_minimum_version "${prefix}")"
  local major minor patch index
  IFS=. read -r major minor patch <<<"${minimum}"
  local baseline=("${major}" "${minor:-0}" "${patch:-0}")
  for index in 0 1 2; do
    (( 10#${installed[index]} >= 10#${baseline[index]} )) || return 1
    if (( 10#${installed[index]} > 10#${baseline[index]} )); then
      break
    fi
  done
  # Pass the accepted version to the worker's existing profile check.
  printf -v "${prefix_name}" '%s' "${version}"
}

codex_available() {
  command -v codex >/dev/null 2>&1 \
    && accept_cli_version codex codex_version_prefix \
    && [[ -s "${HOME}/.codex/auth.json" ]]
}

antigravity_available() {
  command -v agy >/dev/null 2>&1 \
    && accept_cli_version agy antigravity_version_prefix \
    && "${runtime_dir}/agent" -check-antigravity-auth >/dev/null 2>&1
}

claude_available() {
  command -v claude >/dev/null 2>&1 \
    && accept_cli_version claude claude_version_prefix \
    && "${runtime_dir}/agent" -check-claude-auth >/dev/null 2>&1
}

select_profiles() {
  case "${requested_provider}" in
    auto)
      codex_available && use_codex=1
      antigravity_available && use_gemini=1
      claude_available && use_claude=1
      ;;
    codex)
      if ! codex_available; then
        echo "Codex CLI ${codex_minimum_version} or newer with a saved login is required. Run codex login." >&2
        exit 1
      fi
      use_codex=1
      ;;
    antigravity|gemini)
      if ! antigravity_available; then
        echo "Antigravity CLI ${antigravity_minimum_version} or newer with a saved login is required. Run agy to sign in." >&2
        exit 1
      fi
      use_gemini=1
      ;;
    claude)
      if ! claude_available; then
        echo "Claude Code ${claude_minimum_version} or newer with a saved subscription login is required. Run claude auth login." >&2
        exit 1
      fi
      use_claude=1
      ;;
    all)
      if ! codex_available || ! antigravity_available || ! claude_available; then
        echo "All three supported and authenticated CLIs are required." >&2
        exit 1
      fi
      use_codex=1
      use_gemini=1
      use_claude=1
      ;;
    both)
      if ! codex_available || ! antigravity_available; then
        echo "Both supported and authenticated Codex and Antigravity CLIs are required." >&2
        exit 1
      fi
      use_codex=1
      use_gemini=1
      ;;
    *)
      echo "Usage: $0 [auto|codex|antigravity|claude|both|all]" >&2
      exit 2
      ;;
  esac
  if [[ "${use_codex}" != "1" && "${use_gemini}" != "1" && "${use_claude}" != "1" ]]; then
    echo "No supported authenticated subscription CLI was found." >&2
    echo "Run 'codex login', 'claude auth login', or start 'agy' and complete Google sign-in." >&2
    exit 1
  fi
}

write_agent_config() {
  local prefetch_context="${HOST_CLI_PREFETCH_CONTEXT:-true}"
  if [[ "${prefetch_context}" != true && "${prefetch_context}" != false ]]; then
    echo "HOST_CLI_PREFETCH_CONTEXT must be true or false." >&2
    return 2
  fi
  {
    printf 'ai:\n'
    printf '  environment: local\n'
    printf '  agentHealthAddress: "127.0.0.1:%s"\n' "${health_port}"
    printf '  mcpUrl: "http://127.0.0.1:%s/mcp"\n' "${gateway_port}"
    printf '  decisionTimeout: 3m\n'
    printf '  agentConcurrency: %s\n' "${agent_concurrency}"
    printf '  profiles:\n'
    if [[ "${use_codex}" == "1" ]]; then
      printf '    - id: codex-subscription-local\n'
      printf '      displayName: Codex / ChatGPT subscription\n'
      printf '      provider: codex-cli\n'
      printf '      prefetchContext: %s\n' "${prefetch_context}"
      printf '      modelOptions:\n        - id: gpt-6-luna\n          reasoningEfforts: [low, medium, high]\n        - id: gpt-5.6-luna\n          reasoningEfforts: [low, medium, high]\n        - id: gpt-6.1-sol\n          reasoningEfforts: [low, medium, high]\n'
      printf '      runtime: cli\n'
      printf '      model: gpt-5.6-luna\n'
      printf '      scope: local\n'
      printf '      supportedVersionPrefix: "%s"\n' "${codex_version_prefix}"
      printf '      maxSteps: 3\n'
      printf '      enabled: true\n'
    fi
    if [[ "${use_claude}" == "1" ]]; then
      printf '    - id: claude-subscription-local\n'
      printf '      displayName: Claude subscription\n'
      printf '      provider: claude-cli\n'
      printf '      prefetchContext: %s\n' "${prefetch_context}"
      printf '      modelOptions:\n        - id: haiku\n        - id: sonnet\n          reasoningEfforts: [low, medium, high]\n        - id: opus\n          reasoningEfforts: [low, medium, high]\n'
      printf '      runtime: cli\n'
      printf '      model: sonnet\n'
      printf '      scope: local\n'
      printf '      supportedVersionPrefix: "%s"\n' "${claude_version_prefix}"
      printf '      maxSteps: 3\n'
      printf '      enabled: true\n'
    fi
    if [[ "${use_gemini}" == "1" ]]; then
      printf '    - id: gemini-subscription-local\n'
      printf '      displayName: Antigravity / Google subscription\n'
      printf '      provider: antigravity-cli\n'
      printf '      runtime: cli\n'
      printf '      model: gemini-3.8-flash-medium\n'
      printf '      modelOptions:\n        - id: gemini-3.8-flash-low\n        - id: gemini-3.8-flash-medium\n        - id: gemini-3.8-flash-high\n'
      printf '      scope: local\n'
      printf '      supportedVersionPrefix: "%s"\n' "${antigravity_version_prefix}"
      printf '      maxSteps: 3\n'
      printf '      enabled: true\n'
    fi
  } >"${runtime_dir}/config.yaml"
  chmod 600 "${runtime_dir}/config.yaml"
}

show_forward_error() {
  local log_file="$1"
  if [[ -n "${log_file}" && -s "${log_file}" ]]; then
    tail -n 30 -- "${log_file}" >&2
    if grep -Fq 'address already in use' "${log_file}"; then
      echo "A local port is already occupied. Stop the existing bridge or set HOST_AI_GATEWAY_PORT (and HOST_AGENT_HEALTH_PORT for another agent). Collector ports use HOST_OTEL_PORT and HOST_OTEL_HEALTH_PORT." >&2
    fi
  fi
}

wait_for_endpoint() {
  local url="$1"
  local process_id="$2"
  local label="$3"
  local log_file="${4:-}"
  local attempts="${5:-150}"
  local attempt
  # Host startup may run two serial 15-second subscription auth checks.
  for ((attempt=0; attempt<attempts; attempt++)); do
    # A different listener must not make a failed port-forward appear healthy.
    if ! kill -0 "${process_id}" 2>/dev/null; then
      echo "${label} exited before becoming ready." >&2
      show_forward_error "${log_file}"
      return 1
    fi
    if curl --noproxy '*' --connect-timeout 1 --max-time 1 --fail --silent --show-error --output /dev/null "${url}" 2>/dev/null; then
      if kill -0 "${process_id}" 2>/dev/null; then
        return
      fi
    fi
    sleep 0.2
  done
  echo "${label} did not become ready." >&2
  show_forward_error "${log_file}"
  return 1
}

require_command base64
require_command curl
require_command go
require_command kubectl
echo "Building the host decision agent..."
(cd "${backend_dir}" && go build -o "${runtime_dir}/agent" ./cmd/agent)
select_profiles

if [[ ! "${gateway_port}" =~ ^[0-9]+$ || ! "${health_port}" =~ ^[0-9]+$ ]]; then
  echo "HOST_AI_GATEWAY_PORT and HOST_AGENT_HEALTH_PORT must be numeric." >&2
  exit 1
fi

kube get deployment battlefield-api >/dev/null
echo "Waiting for the Kubernetes AI gateway deployment to become ready..."
kube rollout status deployment/battlefield-ai-gateway --timeout=120s
encoded_secret="$(kube get secret battlefield-ai -o 'jsonpath={.data.BATTLEFIELD_AGENT_SECRET}')"
if [[ -z "${encoded_secret}" ]]; then
  echo "The battlefield-ai capability secret is missing." >&2
  exit 1
fi
export BATTLEFIELD_AGENT_SECRET="$(printf '%s' "${encoded_secret}" | base64 --decode)"
unset encoded_secret

write_agent_config


echo "Forwarding the Kubernetes AI gateway to http://127.0.0.1:${gateway_port}..."
start_port_forward "${runtime_dir}/port-forward.log" service/battlefield-ai-gateway "${gateway_port}:8084"
port_forward_pid=$!
wait_for_endpoint "http://127.0.0.1:${gateway_port}/healthz" "${port_forward_pid}" "AI gateway port-forward" "${runtime_dir}/port-forward.log"

LANGFUSE_ENABLED="$(battlefield_env_langfuse_enabled)"
if [[ "${LANGFUSE_ENABLED}" == 1 ]]; then
  otel_port="${HOST_OTEL_PORT:-14318}"
  otel_health_port="${HOST_OTEL_HEALTH_PORT:-14333}"
  start_port_forward "${runtime_dir}/otel-forward.log" service/battlefield-otel-collector "${otel_port}:4318" "${otel_health_port}:13133"
  otel_forward_pid=$!
  wait_for_endpoint "http://127.0.0.1:${otel_health_port}/" "${otel_forward_pid}" "Collector port-forward" "${runtime_dir}/otel-forward.log"
  export OTEL_EXPORTER_OTLP_ENDPOINT="http://127.0.0.1:${otel_port}"
fi

echo "Starting the authenticated host decision agent..."
(cd "${backend_dir}" && exec "${runtime_dir}/agent" -config "${runtime_dir}/config.yaml") &
agent_pid=$!
wait_for_endpoint "http://127.0.0.1:${health_port}/healthz" "${agent_pid}" "Host agent" "" 300

codex_flag=false
gemini_flag=false
claude_flag=false
[[ "${use_codex}" == "1" ]] && codex_flag=true
[[ "${use_gemini}" == "1" ]] && gemini_flag=true
[[ "${use_claude}" == "1" ]] && claude_flag=true
set_profile_flags "${codex_flag}" "${gemini_flag}" "${claude_flag}"
profiles_enabled=1
kube rollout restart deployment/battlefield-api >/dev/null
kube rollout status deployment/battlefield-api --timeout=120s

echo
echo "Host subscription agent is ready with ${agent_concurrency} concurrent decision slot(s). Keep this terminal open during the battle."
echo "Open http://battlefield.localhost:8888 and select an enabled subscription controller."
echo "Decisions occur at ticks 10, 20, 30, ...; use at least 20 maximum ticks for a useful smoke test."
echo "Press Ctrl+C after the battle to disconnect the host agent and hide its profiles."

set +e
bridge_processes=("${agent_pid}" "${port_forward_pid}")
[[ -z "${otel_forward_pid}" ]] || bridge_processes+=("${otel_forward_pid}")
wait -n "${bridge_processes[@]}"
exit_code=$?
set -e
echo "A host-agent process exited; shutting down the bridge." >&2
exit "${exit_code}"
