#!/usr/bin/env bash
# Deployment entry point for a registered Kind environment. Resolves selected components
# and delegates rollout, validation, and cleanup to cd/lib/kind-deployment.sh.

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${project_dir}/scripts/lib/deployment-environment.sh"
battlefield_env_preflight
if [[ "${DEPLOY_ENV}" == production && -z "${BATTLEFIELD_OPERATION_LOCK_FD:-}" ]]; then
  exec python3 "${project_dir}/scripts/infra/operation-lock.py" -- "$0" "$@"
fi
if [[ "${DEPLOY_ENV}" == production ]]; then
  python3 "${project_dir}/scripts/infra/operation-lock.py" --check
fi
[[ "${DEPLOY_ENV}" != dev ]] || battlefield_env_verify_docker
node "${project_dir}/scripts/lib/application-env.cjs" validate "${BATTLEFIELD_SECRET_FILE}" "${DEPLOY_ENV}"

deploy_command="$0"

# shellcheck source=scripts/lib/deployment-components.sh
source "${project_dir}/scripts/lib/deployment-components.sh"
# shellcheck source=scripts/cd/lib/kind-deployment.sh
source "${project_dir}/scripts/cd/lib/kind-deployment.sh"

backend_image="${BACKEND_IMAGE:-autonomous-battlefield-backend}"
frontend_image="${FRONTEND_IMAGE:-autonomous-battlefield-frontend}"
image_source="${IMAGE_SOURCE:-local}"
enforce_supply_chain_policy="${ENFORCE_SUPPLY_CHAIN_POLICY:-0}"
backend_image_ref="${BACKEND_IMAGE_REF:-}"
frontend_image_ref="${FRONTEND_IMAGE_REF:-}"
cluster_name="${KIND_CLUSTER_NAME:-battlefield}"
namespace="${KUBERNETES_NAMESPACE:-battlefield}"
logging_namespace="${LOGGING_NAMESPACE:-battlefield-logging}"
kube_context="${KUBERNETES_CONTEXT:-kind-${cluster_name}}"
rollback_on_failure="${ROLLBACK_ON_FAILURE:-0}"
force_restart="${FORCE_RESTART:-0}"
rollout_timeout="${ROLLOUT_TIMEOUT:-300s}"
observability_rollout_timeout="${OBSERVABILITY_ROLLOUT_TIMEOUT:-900s}"
image_tag="${1:-${IMAGE_TAG:-$(git -C "${project_dir}" rev-parse HEAD)}}"
backend_image_tag="${BACKEND_IMAGE_TAG:-${image_tag}}"
frontend_image_tag="${FRONTEND_IMAGE_TAG:-${image_tag}}"
deploy_components="${DEPLOY_COMPONENTS:-all}"
secret_file="${BATTLEFIELD_SECRET_FILE}"
ai_secret_file="${BATTLEFIELD_AI_SECRET_FILE}"

runtime_dir=""
rollback_overlay=""
rollback_overlay_ready=0
application_update_started=0
current_backend_image=""
current_engine_image=""
current_agent_image=""
current_mcp_image=""
current_frontend_image=""
effective_backend_image=""
effective_backend_tag=""
effective_frontend_image=""
effective_frontend_tag=""
effective_backend_ref=""
effective_frontend_ref=""
declare -A previous_revisions=()
rollout_pids=()

deployment_components_select "${deploy_components}" deployment
if deployment_component_selected application || deployment_component_selected infrastructure; then
  # Apply the authoritative .env and refresh its consumers together.
  deployment_components_add infrastructure
  deployment_components_add application
fi
if ! deployment_components_any_selected; then
  echo "No deployable changes detected; skipping Kubernetes deployment."
  exit 0
fi

trap kind_deploy_cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

kind_deploy_validate_inputs "$@"
kind_deploy_validate_cluster
kind_deploy_capture_current_images
kind_deploy_resolve_images
if [[ "${image_source}" == "registry" ]] && deployment_component_selected application; then
  "${project_dir}/scripts/cd/verify-registry-images.sh" "${effective_backend_ref}" "${effective_frontend_ref}"
elif [[ "${image_source}" == "registry" ]] && deployment_component_selected migration; then
  "${project_dir}/scripts/cd/verify-registry-images.sh" "${effective_backend_ref}"
fi
kind_deploy_validate_artifacts
kind_deploy_prepare_overlays
kind_deploy_load_images

deployment_component_selected infrastructure && kind_deploy_infrastructure
if [[ "${DEPLOY_ENV}" == production ]]; then
  # Keep security policy in sync on every rollout, independently of tunnel activation.
  "${project_dir}/scripts/cd/reconcile-network-policies.sh"
fi
[[ "${image_source}" == "registry" ]] && kind_deploy_registry_credentials
if [[ "${image_source}" == "registry" && "${enforce_supply_chain_policy}" == "1" ]]; then
  POLICY_BACKEND_IMAGE_REF="${effective_backend_ref:-${current_backend_image}}" \
    POLICY_FRONTEND_IMAGE_REF="${effective_frontend_ref:-${current_frontend_image}}" \
    "${project_dir}/scripts/cd/apply-kyverno-policy.sh"
fi
deployment_component_selected migration && kind_deploy_migration
deployment_component_selected application && kind_deploy_application
deployment_component_selected observability && kind_deploy_observability
deployment_component_selected gateway && kind_deploy_gateway

kind_deploy_print_summary
