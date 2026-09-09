#!/usr/bin/env bash
# Runs a host-side model CLI agent against the registered dev cluster through temporary
# MCP forwarding and a generated agent configuration.

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
mcp_port="${HOST_MCP_PORT:-18084}"
health_port="${HOST_AGENT_HEALTH_PORT:-18083}"
runtime_dir="$(mktemp -d "${TMPDIR:-/tmp}/battlefield-host-agent.XXXXXX")"
port_forward_pid=""
agent_pid=""
otel_forward_pid=""
profiles_enabled=0
use_codex=0
use_gemini=0

kube() {
  kubectl --context "${kube_context}" --namespace "${namespace}" "$@"
}

set_profile_flags() {
  local codex_value="$1"
  local gemini_value="$2"
  local patch
  patch="$(printf '{"stringData":{"CODEX_CLI_PROFILE_AVAILABLE":"%s","GEMINI_CLI_PROFILE_AVAILABLE":"%s"}}' "${codex_value}" "${gemini_value}")"
  kube patch secret battlefield-ai --type merge --patch "${patch}" >/dev/null
}

cleanup() {
  local exit_code=$?
  trap - EXIT INT TERM

  if [[ "${profiles_enabled}" == "1" ]]; then
    echo "Disabling host CLI profiles in the Kubernetes battle form..."
    set_profile_flags false false || true
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

codex_available() {
  command -v codex >/dev/null 2>&1 \
    && [[ "$(codex --version 2>/dev/null || true)" == codex-cli\ 0.152.* ]] \
    && [[ -s "${HOME}/.codex/auth.json" ]]
}

antigravity_available() {
  command -v agy >/dev/null 2>&1 \
    && [[ "$(agy --version 2>/dev/null || true)" == 1.1.* ]] \
    && "${runtime_dir}/agent" -check-antigravity-auth >/dev/null 2>&1
}

select_profiles() {
  case "${requested_provider}" in
    auto)
      codex_available && use_codex=1
      antigravity_available && use_gemini=1
      ;;
    codex)
      if ! codex_available; then
        echo "Codex CLI 0.152.x with an existing 'codex login' is required." >&2
        exit 1
      fi
      use_codex=1
      ;;
    antigravity|gemini)
      if ! antigravity_available; then
        echo "Antigravity CLI 1.1.x with a working saved login is required. Run agy to sign in." >&2
        exit 1
      fi
      use_gemini=1
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
      echo "Usage: $0 [auto|codex|antigravity|both]" >&2
      exit 2
      ;;
  esac
  if [[ "${use_codex}" != "1" && "${use_gemini}" != "1" ]]; then
    echo "No supported authenticated subscription CLI was found." >&2
    echo "Run 'codex login' or start 'agy' and complete Google sign-in." >&2
    exit 1
  fi
}

write_agent_config() {
  {
    printf 'ai:\n'
    printf '  environment: local\n'
    printf '  agentHealthAddress: "127.0.0.1:%s"\n' "${health_port}"
    printf '  mcpUrl: "http://127.0.0.1:%s/mcp"\n' "${mcp_port}"
    printf '  decisionTimeout: 2m\n'
    printf '  profiles:\n'
    if [[ "${use_codex}" == "1" ]]; then
      printf '    - id: codex-subscription-local\n'
      printf '      displayName: Codex / ChatGPT subscription\n'
      printf '      provider: codex-cli\n'
      printf '      runtime: cli\n'
      printf '      model: gpt-5.6-luna\n'
      printf '      scope: local\n'
      printf '      supportedVersionPrefix: codex-cli 0.152.\n'
      printf '      maxSteps: 3\n'
      printf '      enabled: true\n'
    fi
    if [[ "${use_gemini}" == "1" ]]; then
      printf '    - id: gemini-subscription-local\n'
      printf '      displayName: Antigravity / Google subscription\n'
      printf '      provider: antigravity-cli\n'
      printf '      runtime: cli\n'
      printf '      model: gemini-3.7-flash-medium\n'
      printf '      scope: local\n'
      printf '      supportedVersionPrefix: 1.1.\n'
      printf '      maxSteps: 3\n'
      printf '      enabled: true\n'
    fi
  } >"${runtime_dir}/config.yaml"
  chmod 600 "${runtime_dir}/config.yaml"
}

wait_for_endpoint() {
  local url="$1"
  local process_id="$2"
  local label="$3"
  # Allow the bounded subscription authentication probe to finish at startup.
  for _ in {1..150}; do
    if curl --fail --silent --show-error --output /dev/null "${url}" 2>/dev/null; then
      return
    fi
    if ! kill -0 "${process_id}" 2>/dev/null; then
      echo "${label} exited before becoming ready." >&2
      return 1
    fi
    sleep 0.2
  done
  echo "${label} did not become ready." >&2
  return 1
}

require_command base64
require_command curl
require_command go
require_command kubectl
echo "Building the host decision agent..."
(cd "${backend_dir}" && go build -o "${runtime_dir}/agent" ./cmd/agent)
select_profiles

if [[ ! "${mcp_port}" =~ ^[0-9]+$ || ! "${health_port}" =~ ^[0-9]+$ ]]; then
  echo "HOST_MCP_PORT and HOST_AGENT_HEALTH_PORT must be numeric." >&2
  exit 1
fi

kube get deployment battlefield-api >/dev/null
kube get deployment battlefield-mcp >/dev/null
encoded_secret="$(kube get secret battlefield-ai -o 'jsonpath={.data.BATTLEFIELD_AGENT_SECRET}')"
if [[ -z "${encoded_secret}" ]]; then
  echo "The battlefield-ai capability secret is missing." >&2
  exit 1
fi
export BATTLEFIELD_AGENT_SECRET="$(printf '%s' "${encoded_secret}" | base64 --decode)"
unset encoded_secret

write_agent_config


echo "Forwarding Kubernetes MCP to http://127.0.0.1:${mcp_port}/mcp..."
kube port-forward --address 127.0.0.1 service/battlefield-mcp "${mcp_port}:8084" >"${runtime_dir}/port-forward.log" 2>&1 &
port_forward_pid=$!
wait_for_endpoint "http://127.0.0.1:${mcp_port}/healthz" "${port_forward_pid}" "MCP port-forward"

LANGFUSE_ENABLED="$(battlefield_env_langfuse_enabled)"
if [[ "${LANGFUSE_ENABLED}" == 1 ]]; then
  otel_port="${HOST_OTEL_PORT:-14318}"
  otel_health_port="${HOST_OTEL_HEALTH_PORT:-14333}"
  kube port-forward --address 127.0.0.1 service/battlefield-otel-collector "${otel_port}:4318" "${otel_health_port}:13133" >"${runtime_dir}/otel-forward.log" 2>&1 &
  otel_forward_pid=$!
  wait_for_endpoint "http://127.0.0.1:${otel_health_port}/" "${otel_forward_pid}" "Collector port-forward"
  export OTEL_EXPORTER_OTLP_ENDPOINT="http://127.0.0.1:${otel_port}"
fi

echo "Starting the authenticated host decision agent..."
(cd "${backend_dir}" && exec "${runtime_dir}/agent" -config "${runtime_dir}/config.yaml") &
agent_pid=$!
wait_for_endpoint "http://127.0.0.1:${health_port}/healthz" "${agent_pid}" "Host agent"

codex_flag=false
gemini_flag=false
[[ "${use_codex}" == "1" ]] && codex_flag=true
[[ "${use_gemini}" == "1" ]] && gemini_flag=true
set_profile_flags "${codex_flag}" "${gemini_flag}"
profiles_enabled=1
kube rollout restart deployment/battlefield-api >/dev/null
kube rollout status deployment/battlefield-api --timeout=120s

echo
echo "Host subscription agent is ready. Keep this terminal open during the battle."
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
