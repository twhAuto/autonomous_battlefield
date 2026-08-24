#!/usr/bin/env bash

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
backend_image="${BACKEND_IMAGE:-autonomous-battlefield-backend}"
frontend_image="${FRONTEND_IMAGE:-autonomous-battlefield-frontend}"
cluster_name="${KIND_CLUSTER_NAME:-battlefield}"
namespace="${KUBERNETES_NAMESPACE:-battlefield}"
kube_context="${KUBERNETES_CONTEXT:-kind-${cluster_name}}"
rollback_on_failure="${ROLLBACK_ON_FAILURE:-0}"
rollout_timeout="${ROLLOUT_TIMEOUT:-120s}"
image_tag="${1:-${IMAGE_TAG:-$(git -C "${project_dir}" rev-parse HEAD)}}"
runtime_dir=""
application_update_started=0
rollback_overlay_ready=0
rollback_overlay=""
declare -A previous_revisions=()

kube() {
  kubectl --context "${kube_context}" "$@"
}

rollback_application() {
  local rollback_failed=0
  local rollback_started=0

  echo "Deployment failed; restoring the previous application revisions..." >&2

  if [[ "${rollback_overlay_ready}" == "1" ]]; then
    if ! kube apply -k "${rollback_overlay}"; then
      return 1
    fi

    for deployment in battlefield-api battlefield-engine battlefield-frontend; do
      if ! kube rollout status "deployment/${deployment}" \
        --namespace "${namespace}" \
        --timeout="${rollout_timeout}"; then
        rollback_failed=1
      fi
    done

    return "${rollback_failed}"
  fi

  for deployment in battlefield-api battlefield-engine battlefield-frontend; do
    local previous_revision="${previous_revisions[${deployment}]:-}"
    local current_revision
    current_revision="$(
      kube get "deployment/${deployment}" \
        --namespace "${namespace}" \
        -o jsonpath='{.metadata.annotations.deployment\.kubernetes\.io/revision}' \
        2>/dev/null || true
    )"

    if [[ -n "${previous_revision}" && "${current_revision}" != "${previous_revision}" ]]; then
      rollback_started=1
      if ! kube rollout undo "deployment/${deployment}" \
        --namespace "${namespace}" \
        --to-revision "${previous_revision}"; then
        rollback_failed=1
      fi
    fi
  done

  if [[ "${rollback_started}" == "1" ]]; then
    for deployment in battlefield-api battlefield-engine battlefield-frontend; do
      if ! kube rollout status "deployment/${deployment}" \
        --namespace "${namespace}" \
        --timeout="${rollout_timeout}"; then
        rollback_failed=1
      fi
    done
  fi

  return "${rollback_failed}"
}

cleanup() {
  local exit_code=$?
  trap - EXIT INT TERM

  if [[ "${exit_code}" != "0" && "${rollback_on_failure}" == "1" && "${application_update_started}" == "1" ]]; then
    if ! rollback_application; then
      echo "Automatic application rollback did not complete successfully." >&2
    fi
  fi

  if [[ -n "${runtime_dir}" && -d "${runtime_dir}" ]]; then
    rm -rf -- "${runtime_dir}"
  fi

  exit "${exit_code}"
}

trap cleanup EXIT INT TERM

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Required command not found: $1" >&2
    exit 1
  fi
}

require_command docker
require_command git
require_command grep
require_command kind
require_command kubectl

if [[ "${rollback_on_failure}" != "0" && "${rollback_on_failure}" != "1" ]]; then
  echo "ROLLBACK_ON_FAILURE must be either 0 or 1." >&2
  exit 1
fi

if [[ ! "${rollout_timeout}" =~ ^[1-9][0-9]*s$ ]]; then
  echo "ROLLOUT_TIMEOUT must be a positive number of seconds such as 120s." >&2
  exit 1
fi

for image_name in "${backend_image}" "${frontend_image}"; do
  if [[ ! "${image_name}" =~ ^[a-z0-9][a-z0-9._:/-]*$ ]]; then
    echo "Invalid container image name: ${image_name}" >&2
    exit 1
  fi
done

if [[ "$#" -gt 1 ]]; then
  echo "Usage: $0 [full-git-sha]" >&2
  exit 1
fi

if [[ ! "${image_tag}" =~ ^[0-9a-f]{40}$ ]]; then
  echo "The image tag must be a full 40-character lowercase Git commit SHA." >&2
  exit 1
fi

if ! git -C "${project_dir}" cat-file -e "${image_tag}^{commit}" 2>/dev/null; then
  echo "The commit ${image_tag} is not available in this Git repository." >&2
  exit 1
fi

if ! kind get clusters | grep -Fxq "${cluster_name}"; then
  echo "kind cluster not found: ${cluster_name}" >&2
  exit 1
fi

for image in \
  "${backend_image}:${image_tag}" \
  "${frontend_image}:${image_tag}"; do
  if ! docker image inspect "${image}" >/dev/null 2>&1; then
    echo "Local image not found: ${image}" >&2
    echo "Build the revision first with ./scripts/build-images.sh" >&2
    exit 1
  fi
done

secret_file="${BATTLEFIELD_SECRET_FILE:-${project_dir}/deploy/overlays/local/infrastructure/secret.env}"
if [[ ! -f "${secret_file}" ]]; then
  echo "Local Kubernetes secret file not found: ${secret_file}" >&2
  echo "Copy secret.env.example to secret.env before deploying." >&2
  exit 1
fi

runtime_dir="$(mktemp -d "${TMPDIR:-/tmp}/autonomous-battlefield-kustomize.XXXXXX")"
mkdir -p \
  "${runtime_dir}/deploy/base" \
  "${runtime_dir}/deploy/overlays/local"

cp -R \
  "${project_dir}/deploy/base/application" \
  "${project_dir}/deploy/base/infrastructure" \
  "${project_dir}/deploy/base/migration" \
  "${runtime_dir}/deploy/base/"
cp -R \
  "${project_dir}/deploy/overlays/local/application" \
  "${project_dir}/deploy/overlays/local/infrastructure" \
  "${project_dir}/deploy/overlays/local/migration" \
  "${runtime_dir}/deploy/overlays/local/"
cp -R \
  "${project_dir}/deploy/overlays/local/application" \
  "${runtime_dir}/deploy/overlays/local/rollback-application"
cp "${secret_file}" "${runtime_dir}/deploy/overlays/local/infrastructure/secret.env"
chmod 600 "${runtime_dir}/deploy/overlays/local/infrastructure/secret.env"
rollback_overlay="${runtime_dir}/deploy/overlays/local/rollback-application"

cat >>"${runtime_dir}/deploy/overlays/local/application/kustomization.yaml" <<EOF

images:
  - name: autonomous-battlefield-backend
    newName: "${backend_image}"
    newTag: "${image_tag}"
  - name: autonomous-battlefield-frontend
    newName: "${frontend_image}"
    newTag: "${image_tag}"
EOF

cat >>"${runtime_dir}/deploy/overlays/local/migration/kustomization.yaml" <<EOF

images:
  - name: autonomous-battlefield-backend
    newName: "${backend_image}"
    newTag: "${image_tag}"
EOF

echo "Loading commit-addressed images into kind cluster ${cluster_name}..."
kind load docker-image "${backend_image}:${image_tag}" --name "${cluster_name}"
kind load docker-image "${frontend_image}:${image_tag}" --name "${cluster_name}"

echo "Applying local infrastructure..."
kube apply -k "${runtime_dir}/deploy/overlays/local/infrastructure"
kube rollout status statefulset/battlefield-postgres \
  --namespace "${namespace}" \
  --timeout="${rollout_timeout}"

echo "Running the database migration gate..."
kube delete job battlefield-migrate \
  --namespace "${namespace}" \
  --ignore-not-found
kube apply -k "${runtime_dir}/deploy/overlays/local/migration"

if ! kube wait --for=condition=complete \
  job/battlefield-migrate \
  --namespace "${namespace}" \
  --timeout="${rollout_timeout}"; then
  kube logs job/battlefield-migrate --namespace "${namespace}" || true
  exit 1
fi

kube logs job/battlefield-migrate --namespace "${namespace}"

echo "Deploying application revision ${image_tag}..."
previous_backend_image="$(
  kube get deployment/battlefield-api \
    --namespace "${namespace}" \
    -o jsonpath='{.spec.template.spec.containers[?(@.name=="api")].image}' \
    2>/dev/null || true
)"
previous_engine_image="$(
  kube get deployment/battlefield-engine \
    --namespace "${namespace}" \
    -o jsonpath='{.spec.template.spec.containers[?(@.name=="engine")].image}' \
    2>/dev/null || true
)"
previous_frontend_image="$(
  kube get deployment/battlefield-frontend \
    --namespace "${namespace}" \
    -o jsonpath='{.spec.template.spec.containers[?(@.name=="frontend")].image}' \
    2>/dev/null || true
)"

if [[ -n "${previous_backend_image}" \
  && "${previous_backend_image}" == "${previous_engine_image}" \
  && -n "${previous_frontend_image}" \
  && "${previous_backend_image}" != *@sha256:* \
  && "${previous_frontend_image}" != *@sha256:* \
  && "${previous_backend_image}" == *:* \
  && "${previous_frontend_image}" == *:* ]]; then
  previous_backend_name="${previous_backend_image%:*}"
  previous_backend_tag="${previous_backend_image##*:}"
  previous_frontend_name="${previous_frontend_image%:*}"
  previous_frontend_tag="${previous_frontend_image##*:}"

  cat >>"${rollback_overlay}/kustomization.yaml" <<EOF

images:
  - name: autonomous-battlefield-backend
    newName: "${previous_backend_name}"
    newTag: "${previous_backend_tag}"
  - name: autonomous-battlefield-frontend
    newName: "${previous_frontend_name}"
    newTag: "${previous_frontend_tag}"
EOF
  rollback_overlay_ready=1
fi

for deployment in battlefield-api battlefield-engine battlefield-frontend; do
  previous_revisions["${deployment}"]="$(
    kube get "deployment/${deployment}" \
      --namespace "${namespace}" \
      -o jsonpath='{.metadata.annotations.deployment\.kubernetes\.io/revision}' \
      2>/dev/null || true
  )"
done

application_update_started=1
kube apply -k "${runtime_dir}/deploy/overlays/local/application"

for deployment in battlefield-api battlefield-engine battlefield-frontend; do
  kube rollout status "deployment/${deployment}" \
    --namespace "${namespace}" \
    --timeout="${rollout_timeout}"
done
application_update_started=0

echo
echo "Deployed ${image_tag} to kind cluster ${cluster_name}."
kube get deployments \
  --namespace "${namespace}" \
  -o custom-columns='NAME:.metadata.name,READY:.status.readyReplicas,DESIRED:.spec.replicas,IMAGE:.spec.template.spec.containers[0].image'
kube get job battlefield-migrate \
  --namespace "${namespace}" \
  -o custom-columns='NAME:.metadata.name,SUCCEEDED:.status.succeeded,IMAGE:.spec.template.spec.containers[0].image'
