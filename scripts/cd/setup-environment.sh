#!/usr/bin/env bash
# Register an existing cluster without altering its workloads or original kubeconfig.
set -Eeuo pipefail
umask 077
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
environment="${1:-}"
source_context="${2:-kind-battlefield}"
[[ "$#" -le 2 && "${environment}" =~ ^(dev|production)$ ]] || {
  echo 'Usage: setup-environment.sh dev|production [existing-context]' >&2; exit 1;
}
config_dir="${BATTLEFIELD_CONFIG_DIR:-${HOME}/.config/autonomous-battlefield/${environment}}"
profile="${BATTLEFIELD_ENV_FILE:-${config_dir}/environment.env}"
[[ ! -e "${profile}" && ! -e "${config_dir}/kubeconfig" ]] || {
  echo "Registration already exists in ${config_dir}; refusing to overwrite cluster identity or credentials." >&2; exit 1;
}
cluster="${KIND_CLUSTER_NAME:-battlefield}"
endpoint="${DOCKER_HOST:-$(docker context inspect "${DOCKER_CONTEXT:-$(docker context show)}" --format '{{.Endpoints.docker.Host}}')}"
[[ "${environment}" != dev || "${endpoint}" == unix:///* ]] || { echo 'Dev requires a local Docker socket.' >&2; exit 1; }
uid="$(kubectl --context "${source_context}" --request-timeout=10s get namespace kube-system -o jsonpath='{.metadata.uid}')"
[[ -n "${uid}" ]] || exit 1
# Check the selected Docker daemon contains this very cluster before registering it.
check_config="$(mktemp)"
trap 'rm -f -- "${check_config}"' EXIT
DOCKER_CONTEXT= DOCKER_HOST="${endpoint}" kind get kubeconfig --name "${cluster}" >"${check_config}"
docker_uid="$(KUBECONFIG="${check_config}" kubectl --context "kind-${cluster}" --request-timeout=10s get namespace kube-system -o jsonpath='{.metadata.uid}')"
[[ "${uid}" == "${docker_uid}" ]] || { echo 'Source context and Docker cluster differ.' >&2; exit 1; }
mkdir -p "${config_dir}"
chmod 700 "${config_dir}"
kubectl --context "${source_context}" config view --minify --flatten --raw >"${config_dir}/kubeconfig"
KUBECONFIG="${config_dir}/kubeconfig" kubectl config rename-context "${source_context}" "battlefield-${environment}" >/dev/null
{
  printf 'BF_ENV=%q\nBF_CONTEXT=%q\nBF_CLUSTER=%q\nBF_UID=%q\n' "${environment}" "battlefield-${environment}" "${cluster}" "${uid}"
  printf 'BF_DOCKER_HOST=%q\nBF_URL=%q\nBF_KUBECONFIG=%q\n' "${endpoint}" "${TARGET_URL:-http://battlefield.localhost:8888}" "${config_dir}/kubeconfig"
  printf 'BF_DATABASE_FILE=%q\nBF_AI_FILE=%q\nBF_AUTH_FILE=%q\nBF_DISPOSABLE=0\n' "${config_dir}/database.env" "${config_dir}/ai.env" "${config_dir}/auth.env"
} >"${profile}"
chmod 600 "${config_dir}/kubeconfig" "${profile}"
echo "Registered ${environment}: context battlefield-${environment}, cluster UID ${uid}."
echo "Configuration: ${profile}"
echo 'Workloads, databases, and your original kubeconfig are unchanged.'
