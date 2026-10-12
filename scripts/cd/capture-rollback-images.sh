#!/usr/bin/env bash
# Records the running backend and frontend image references before a rollout so a
# failed verification can restore them. A missing Deployment records an empty value.

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"

if [[ "$#" -ne 1 ]]; then
  echo "Usage: $0 <output-env-file>" >&2
  exit 1
fi
output_file="$1"

source "${project_dir}/scripts/lib/deployment-environment.sh"
battlefield_env_preflight

running_image() {
  local deployment="$1" container="$2"
  kubectl --context "${KUBERNETES_CONTEXT}" \
    get "deployment/${deployment}" \
    --namespace "${KUBERNETES_NAMESPACE}" \
    -o jsonpath="{.spec.template.spec.containers[?(@.name==\"${container}\")].image}" \
    2>/dev/null || true
}

backend_image="$(running_image battlefield-api api)"
frontend_image="$(running_image battlefield-frontend frontend)"

mkdir -p -- "$(dirname -- "${output_file}")"
printf 'PREVIOUS_BACKEND_IMAGE_REF=%s\nPREVIOUS_FRONTEND_IMAGE_REF=%s\n' \
  "${backend_image}" "${frontend_image}" >"${output_file}"
