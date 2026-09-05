#!/usr/bin/env bash
# Trusted machine configuration lives outside the checkout; never source secrets.

battlefield_env_error() { echo "Deployment environment: $*" >&2; return 1; }

battlefield_env_lock() {
  local key="$1" expected="$2"
  if [[ -n "${!key:-}" && "${!key}" != "${expected}" ]]; then
    battlefield_env_error "${key} conflicts with the registered ${DEPLOY_ENV} environment."; return 1
  fi
  printf -v "${key}" '%s' "${expected}"
  export "${key}"
}

battlefield_env_load() {
  local requested="${DEPLOY_ENV:-dev}"
  case "${requested}" in dev|production) ;; *) battlefield_env_error 'DEPLOY_ENV must be dev or production.'; return 1 ;; esac
  export DEPLOY_ENV="${requested}"
  local config="${BATTLEFIELD_ENV_FILE:-${HOME}/.config/autonomous-battlefield/${requested}/environment.env}"
  [[ -f "${config}" ]] || { battlefield_env_error "Missing ${config}; run scripts/cd/setup-environment.sh ${requested} on the target machine."; return 1; }
  unset BF_ENV BF_CONTEXT BF_CLUSTER BF_UID BF_DOCKER_HOST BF_URL BF_KUBECONFIG BF_DATABASE_FILE BF_AI_FILE BF_AUTH_FILE BF_DISPOSABLE
  # shellcheck disable=SC1090
  source "${config}" || return
  [[ "${BF_ENV:-}" == "${requested}" ]] || { battlefield_env_error 'Profile belongs to a different environment.'; return 1; }
  local field
  for field in BF_CONTEXT BF_CLUSTER BF_UID BF_DOCKER_HOST BF_URL BF_KUBECONFIG BF_DATABASE_FILE BF_AI_FILE BF_AUTH_FILE; do
    [[ -n "${!field:-}" ]] || { battlefield_env_error "Profile is missing ${field}."; return 1; }
  done
  battlefield_env_lock KUBERNETES_CONTEXT "${BF_CONTEXT}" || return
  battlefield_env_lock KIND_CLUSTER_NAME "${BF_CLUSTER}" || return
  battlefield_env_lock KUBERNETES_NAMESPACE battlefield || return
  battlefield_env_lock DOCKER_HOST "${BF_DOCKER_HOST}" || return
  if [[ -n "${DOCKER_CONTEXT:-}" ]]; then
    local endpoint
    endpoint="$(docker context inspect "${DOCKER_CONTEXT}" --format '{{.Endpoints.docker.Host}}')" || return
    [[ "${endpoint}" == "${BF_DOCKER_HOST}" ]] || { battlefield_env_error 'DOCKER_CONTEXT points to another daemon.'; return 1; }
  fi
  unset DOCKER_CONTEXT
  [[ "${DEPLOY_ENV}" != dev || "${DOCKER_HOST}" == unix:///* ]] || { battlefield_env_error 'Dev requires a local Unix Docker socket.'; return 1; }
  # Jenkins can bind a private copy of the dedicated kubeconfig; identity is checked below.
  export KUBECONFIG="${KUBECONFIG:-${BF_KUBECONFIG}}"
  [[ -f "${KUBECONFIG}" ]] || { battlefield_env_error 'Dedicated kubeconfig is missing.'; return 1; }
  export BATTLEFIELD_ENV_FILE="${config}"
  if [[ "${DEPLOY_ENV}" == dev ]]; then
    battlefield_env_lock BATTLEFIELD_SECRET_FILE "${BF_DATABASE_FILE}" || return
    battlefield_env_lock BATTLEFIELD_AI_SECRET_FILE "${BF_AI_FILE}" || return
    battlefield_env_lock BATTLEFIELD_AUTH_SECRET_FILE "${BF_AUTH_FILE}" || return
  fi
  export BATTLEFIELD_SECRET_FILE="${BATTLEFIELD_SECRET_FILE:-${BF_DATABASE_FILE}}"
  export BATTLEFIELD_AI_SECRET_FILE="${BATTLEFIELD_AI_SECRET_FILE:-${BF_AI_FILE}}"
  export BATTLEFIELD_AUTH_SECRET_FILE="${BATTLEFIELD_AUTH_SECRET_FILE:-${BF_AUTH_FILE}}"
  battlefield_env_lock TARGET_URL "${BF_URL}" || return
  battlefield_env_lock ZAP_TARGET_URL "${BF_URL}" || return
  if [[ "${DEPLOY_ENV}" == production ]]; then
    battlefield_env_lock IMAGE_SOURCE registry || return
    battlefield_env_lock ENFORCE_SUPPLY_CHAIN_POLICY 1 || return
  else
    battlefield_env_lock IMAGE_SOURCE local || return
    battlefield_env_lock ENFORCE_SUPPLY_CHAIN_POLICY 0 || return
  fi
  export SMOKE_TEST_OBSERVABILITY="${SMOKE_TEST_OBSERVABILITY:-$([[ "${DEPLOY_ENV}" == production ]] && echo 1 || echo 0)}"
}

battlefield_env_verify() {
  local uid
  uid="$(kubectl --context "${KUBERNETES_CONTEXT}" --request-timeout=10s get namespace kube-system -o jsonpath='{.metadata.uid}')" || return
  [[ -n "${uid}" && "${uid}" == "${BF_UID}" ]] || { battlefield_env_error 'Cluster identity mismatch; refusing to operate.'; return 1; }
}

battlefield_env_preflight() {
  battlefield_env_load || return
  battlefield_env_verify || return
}

battlefield_env_verify_docker() {
  docker info >/dev/null 2>&1 || { battlefield_env_error 'Docker is unavailable. Start the local Docker engine/WSL integration, then retry.'; return 1; }
  local config uid
  config="$(mktemp)" || return
  chmod 600 "${config}"
  if ! kind get kubeconfig --name "${KIND_CLUSTER_NAME}" >"${config}"; then rm -f "${config}"; return 1; fi
  uid="$(KUBECONFIG="${config}" kubectl --context "kind-${KIND_CLUSTER_NAME}" --request-timeout=10s get namespace kube-system -o jsonpath='{.metadata.uid}')" || { rm -f "${config}"; return 1; }
  rm -f "${config}"
  [[ "${uid}" == "${BF_UID}" ]] || { battlefield_env_error 'Docker Kind cluster does not match the registered Kubernetes cluster.'; return 1; }
}
