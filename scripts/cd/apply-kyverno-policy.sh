#!/usr/bin/env bash
# Renders and installs the image-verification admission policy for the registered cluster
# using the configured Cosign public key.

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${project_dir}/scripts/lib/deployment-environment.sh"
battlefield_env_preflight

# shellcheck disable=SC1091
source "${project_dir}/scripts/ci/tool-versions.env"
source "${project_dir}/scripts/lib/image-metadata.sh"
published_image_repositories
policy_backend="$(policy_image_repository "${POLICY_BACKEND_IMAGE_REF:-}" "${published_backend_image}")"
policy_frontend="$(policy_image_repository "${POLICY_FRONTEND_IMAGE_REF:-}" "${published_frontend_image}")"
kube_context="${KUBERNETES_CONTEXT:-kind-${KIND_CLUSTER_NAME:-battlefield}}"
public_key="${COSIGN_PUBLIC_KEY_FILE:?COSIGN_PUBLIC_KEY_FILE is required}"
docker_config_file="${DOCKER_CONFIG:-${HOME}/.docker}/config.json"
template="${project_dir}/infra/deploy/overlays/jenkins/kyverno/verify-images.yaml.template"
runtime_dir="$(mktemp -d "${TMPDIR:-/tmp}/battlefield-kyverno.XXXXXX")"
policy="${runtime_dir}/verify-images.yaml"
trap 'rm -rf -- "${runtime_dir}"' EXIT INT TERM

if [[ ! -s "${public_key}" ]]; then
  echo "Cosign public key is missing or empty." >&2
  exit 1
fi
if [[ ! -s "${docker_config_file}" ]]; then
  echo "An authenticated Docker config is required for Kyverno registry access." >&2
  exit 1
fi

awk -v key_file="${public_key}" -v backend="${policy_backend}" -v frontend="${policy_frontend}" '
  /__COSIGN_PUBLIC_KEY__/ {
    prefix = substr($0, 1, index($0, "__COSIGN_PUBLIC_KEY__") - 1)
    while ((getline line < key_file) > 0) print prefix line
    close(key_file)
    next
  }
  { gsub(/__BACKEND_IMAGE_REPOSITORY__/, backend); gsub(/__FRONTEND_IMAGE_REPOSITORY__/, frontend); print }
' "${template}" >"${policy}"

if [[ "${CLUSTER_DRIVER:-kind}" == kind ]]; then
  kubectl --context "${kube_context}" apply \
    --server-side \
    --force-conflicts \
    --field-manager=battlefield-jenkins \
    -f \
    "https://github.com/kyverno/kyverno/releases/download/${KYVERNO_VERSION}/install.yaml"
fi
if ! kubectl --context "${kube_context}" wait \
  --namespace kyverno \
  --for=condition=Available \
  deployment/kyverno-admission-controller \
  --timeout=180s; then
  echo "Kyverno admission controller did not become available." >&2
  kubectl --context "${kube_context}" get pods \
    --namespace kyverno -o wide >&2 || true
  kubectl --context "${kube_context}" describe deployment/kyverno-admission-controller \
    --namespace kyverno >&2 || true
  kubectl --context "${kube_context}" logs \
    --namespace kyverno \
    --selector app.kubernetes.io/component=admission-controller \
    --all-containers=true \
    --prefix=true \
    --tail=100 >&2 || true
  exit 1
fi

kubectl --context "${kube_context}" create secret generic battlefield-ghcr \
  --namespace kyverno \
  --type=kubernetes.io/dockerconfigjson \
  --from-file=.dockerconfigjson="${docker_config_file}" \
  --dry-run=client -o yaml | \
  kubectl --context "${kube_context}" apply \
    --server-side \
    --field-manager=battlefield-jenkins \
    -f -

kubectl --context "${kube_context}" apply -f "${policy}"
kubectl --context "${kube_context}" wait --for=condition=Ready \
  clusterpolicy/battlefield-verified-images --timeout=120s
