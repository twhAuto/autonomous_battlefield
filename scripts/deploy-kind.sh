#!/usr/bin/env bash

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
backend_image="${BACKEND_IMAGE:-autonomous-battlefield-backend}"
frontend_image="${FRONTEND_IMAGE:-autonomous-battlefield-frontend}"
cluster_name="${KIND_CLUSTER_NAME:-battlefield}"
namespace="${KUBERNETES_NAMESPACE:-battlefield}"
image_tag="${1:-${IMAGE_TAG:-$(git -C "${project_dir}" rev-parse HEAD)}}"
runtime_dir=""

cleanup() {
  local exit_code=$?
  trap - EXIT INT TERM

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

secret_file="${project_dir}/deploy/overlays/local/infrastructure/secret.env"
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
  "${project_dir}/deploy/base/migration" \
  "${runtime_dir}/deploy/base/"
cp -R \
  "${project_dir}/deploy/overlays/local/application" \
  "${project_dir}/deploy/overlays/local/migration" \
  "${runtime_dir}/deploy/overlays/local/"

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
kubectl apply -k "${project_dir}/deploy/overlays/local/infrastructure"
kubectl rollout status statefulset/battlefield-postgres \
  --namespace "${namespace}" \
  --timeout=120s

echo "Running the database migration gate..."
kubectl delete job battlefield-migrate \
  --namespace "${namespace}" \
  --ignore-not-found
kubectl apply -k "${runtime_dir}/deploy/overlays/local/migration"

if ! kubectl wait --for=condition=complete \
  job/battlefield-migrate \
  --namespace "${namespace}" \
  --timeout=120s; then
  kubectl logs job/battlefield-migrate --namespace "${namespace}" || true
  exit 1
fi

kubectl logs job/battlefield-migrate --namespace "${namespace}"

echo "Deploying application revision ${image_tag}..."
kubectl apply -k "${runtime_dir}/deploy/overlays/local/application"

for deployment in battlefield-api battlefield-engine battlefield-frontend; do
  kubectl rollout status "deployment/${deployment}" \
    --namespace "${namespace}" \
    --timeout=120s
done

echo
echo "Deployed ${image_tag} to kind cluster ${cluster_name}."
kubectl get deployments \
  --namespace "${namespace}" \
  -o custom-columns='NAME:.metadata.name,READY:.status.readyReplicas,DESIRED:.spec.replicas,IMAGE:.spec.template.spec.containers[0].image'
kubectl get job battlefield-migrate \
  --namespace "${namespace}" \
  -o custom-columns='NAME:.metadata.name,SUCCEEDED:.status.succeeded,IMAGE:.spec.template.spec.containers[0].image'
