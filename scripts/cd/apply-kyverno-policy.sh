#!/usr/bin/env bash

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck disable=SC1091
source "${project_dir}/scripts/ci/tool-versions.env"
kube_context="${KUBERNETES_CONTEXT:-kind-${KIND_CLUSTER_NAME:-battlefield}}"
public_key="${COSIGN_PUBLIC_KEY_FILE:?COSIGN_PUBLIC_KEY_FILE is required}"
template="${project_dir}/deploy/overlays/jenkins/kyverno/verify-images.yaml.template"
runtime_dir="$(mktemp -d "${TMPDIR:-/tmp}/battlefield-kyverno.XXXXXX")"
policy="${runtime_dir}/verify-images.yaml"
trap 'rm -rf -- "${runtime_dir}"' EXIT INT TERM

if [[ ! -s "${public_key}" ]]; then
  echo "Cosign public key is missing or empty." >&2
  exit 1
fi

kubectl --context "${kube_context}" apply -f \
  "https://github.com/kyverno/kyverno/releases/download/${KYVERNO_VERSION}/install.yaml"
kubectl --context "${kube_context}" rollout status deployment/kyverno-admission-controller \
  --namespace kyverno --timeout=180s

awk -v key_file="${public_key}" '
  /__COSIGN_PUBLIC_KEY__/ {
    prefix = substr($0, 1, index($0, "__COSIGN_PUBLIC_KEY__") - 1)
    while ((getline line < key_file) > 0) print prefix line
    close(key_file)
    next
  }
  { print }
' "${template}" >"${policy}"
kubectl --context "${kube_context}" apply -f "${policy}"
kubectl --context "${kube_context}" wait --for=condition=Ready \
  clusterpolicy/battlefield-verified-images --timeout=120s
