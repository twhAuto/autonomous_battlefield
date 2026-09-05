#!/usr/bin/env bash

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
test_dir="$(mktemp -d "${TMPDIR:-/tmp}/autonomous-battlefield-kind-deployment-test.XXXXXX")"

if [[ ! -x "${project_dir}/scripts/cd/apply-kyverno-policy.sh" ]]; then
  echo "The Kyverno policy installer must be executable." >&2
  exit 1
fi
if ! grep -Fq -- '--server-side' "${project_dir}/scripts/cd/apply-kyverno-policy.sh"; then
  echo "The Kyverno installer must use server-side apply for its large CRDs." >&2
  exit 1
fi
if ! grep -Fq -- '--for=condition=Available' "${project_dir}/scripts/cd/apply-kyverno-policy.sh"; then
  echo "The Kyverno installer must wait for controller availability." >&2
  exit 1
fi
if ! grep -Fq 'imageRegistryCredentials:' \
  "${project_dir}/deploy/overlays/jenkins/kyverno/verify-images.yaml.template"; then
  echo "The Kyverno policy must declare credentials for private GHCR verification." >&2
  exit 1
fi
if ! grep -Fq -- '--new-bundle-format=false' \
  "${project_dir}/scripts/ci/attest-and-sign-images.sh"; then
  echo "Cosign evidence must include the legacy discovery mirror required by Kyverno." >&2
  exit 1
fi
if ! grep -Fq -- '--use-signing-config=false' \
  "${project_dir}/scripts/ci/attest-and-sign-images.sh"; then
  echo "Legacy Cosign publication must disable its new-format signing config." >&2
  exit 1
fi
if ! grep -Fq 'OBSERVABILITY_ROLLOUT_TIMEOUT:-900s' \
  "${project_dir}/scripts/cd/deploy-kind.sh"; then
  echo "Cold observability deployment must have a dedicated rollout budget." >&2
  exit 1
fi
if ! grep -Fq 'kind_deploy_report_workload_failure' \
  "${project_dir}/scripts/cd/lib/kind-deployment.sh"; then
  echo "Observability rollout failures must emit workload diagnostics." >&2
  exit 1
fi
elasticsearch_deployment="${project_dir}/deploy/overlays/local/observability/elasticsearch/deployment.yaml"
if ! grep -Fq 'name: prepare-elasticsearch-config' "${elasticsearch_deployment}"; then
  echo "Elasticsearch must prepare a writable configuration directory." >&2
  exit 1
fi
if grep -Fq 'subPath: elasticsearch.yml' "${elasticsearch_deployment}"; then
  echo "Elasticsearch configuration must not leave its keystore directory read-only." >&2
  exit 1
fi
prometheus_config="${project_dir}/deploy/overlays/local/observability/prometheus/prometheus.yml"
prometheus_rbac="${project_dir}/deploy/overlays/local/observability/prometheus/rbac.yaml"
if ! grep -Fq 'job_name: kubernetes-cadvisor' "${prometheus_config}" \
  || ! grep -Fq 'metrics_path: /metrics/cadvisor' "${prometheus_config}" \
  || ! grep -Fq 'role: node' "${prometheus_config}"; then
  echo "Prometheus must discover Kubernetes nodes and scrape kubelet cAdvisor metrics." >&2
  exit 1
fi
if ! grep -Fq 'nodes/metrics' "${prometheus_rbac}"; then
  echo "Prometheus must have narrowly scoped kubelet metrics permission." >&2
  exit 1
fi
if grep -Fq 'nodes/proxy' "${prometheus_rbac}" \
  || grep -Fq '/api/v1/nodes/' "${prometheus_config}"; then
  echo "Prometheus must scrape the kubelet directly instead of receiving broad node proxy access." >&2
  exit 1
fi

cleanup() {
  chmod -R u+w "${test_dir}" 2>/dev/null || true
  rm -rf -- "${test_dir}"
}
trap cleanup EXIT INT TERM

# shellcheck source=scripts/cd/lib/kind-deployment.sh
source "${project_dir}/scripts/cd/lib/kind-deployment.sh"
# shellcheck source=scripts/lib/deployment-components.sh
source "${project_dir}/scripts/lib/deployment-components.sh"

secret_file="${test_dir}/jenkins-credential"
target_file="${test_dir}/secret.env"
database_url='postgres://battlefield:password@postgres:5432/battlefield?sslmode=disable'

printf 'DATABASE_URL=%s\n' "${database_url}" >"${secret_file}"
chmod 400 "${secret_file}"

kind_deploy_prepare_database_secret "${target_file}" >/dev/null

if [[ "$(stat -c '%a' "${target_file}")" != "600" ]]; then
  echo "Staged database secret must have mode 600." >&2
  exit 1
fi

if ! grep -Fxq "MCP_DATABASE_URL=${database_url}" "${target_file}"; then
  echo "Staged database secret did not inherit DATABASE_URL for MCP_DATABASE_URL." >&2
  exit 1
fi

if [[ "$(grep -c '^MCP_DATABASE_URL=' "${target_file}")" != "1" ]]; then
  echo "Staged database secret must contain exactly one MCP_DATABASE_URL." >&2
  exit 1
fi

image_source=registry
deploy_components=application
secret_file="${secret_file}"
ai_secret_file="${test_dir}/missing-ai-secret"
effective_backend_ref='ghcr.io/twh09/autonomous-battlefield-backend@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
effective_frontend_ref='ghcr.io/twh09/autonomous-battlefield-frontend@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'
deployment_components_select "${deploy_components}" deployment
kind_deploy_prepare_overlays
rendered="${test_dir}/registry-render.yaml"
kubectl kustomize "${runtime_dir}/deploy/overlays/local/application" >"${rendered}"
if ! grep -Fq "ghcr.io/twh09/autonomous-battlefield-backend@sha256:aaaaaaaa" "${rendered}" ||
  ! grep -Fq "ghcr.io/twh09/autonomous-battlefield-frontend@sha256:bbbbbbbb" "${rendered}"; then
  echo "Registry deployment did not render immutable image digests." >&2
  exit 1
fi
rm -rf -- "${runtime_dir}"
runtime_dir=""

echo "Kind deployment secret staging tests passed."
