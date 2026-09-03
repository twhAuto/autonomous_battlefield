#!/usr/bin/env bash

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck disable=SC1091
source "${project_dir}/scripts/ci/tool-versions.env"
artifact_dir="${SECURITY_ARTIFACT_DIR:-${project_dir}/artifacts/security}"
mkdir -p "${artifact_dir}"
failure=0

for command in docker git go kubectl npm; do
  if ! command -v "${command}" >/dev/null 2>&1; then
    echo "Required security command not found: ${command}" >&2
    exit 1
  fi
done

echo "Running reachable Go vulnerability analysis..."
if ! (cd "${project_dir}/backend" && go run "golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}" -json ./... >"${artifact_dir}/govulncheck.json"); then
  failure=1
fi

echo "Auditing frontend runtime and CI dependencies..."
if ! (cd "${project_dir}/frontend" && npm audit --audit-level=high --json >"${artifact_dir}/npm-audit.json"); then
  failure=1
fi

echo "Scanning Git history for secrets with redacted findings..."
gitleaks_args=(git --redact=100 --config /repo/.gitleaks.toml --report-format json --report-path /reports/gitleaks.json)
if [[ -n "${CHANGE_TARGET:-}" ]] && git -C "${project_dir}" rev-parse "origin/${CHANGE_TARGET}" >/dev/null 2>&1; then
  merge_base="$(git -C "${project_dir}" merge-base "origin/${CHANGE_TARGET}" HEAD)"
  gitleaks_args+=(--log-opts "${merge_base}..HEAD")
fi
if ! docker run --rm \
  --volume "${project_dir}:/repo:ro" \
  --volume "${artifact_dir}:/reports" \
  --workdir /repo \
  "${GITLEAKS_IMAGE}" "${gitleaks_args[@]}"; then
  failure=1
fi

echo "Scanning source and infrastructure configuration with Trivy..."
if ! docker run --rm \
  --volume "${project_dir}:/project:ro" \
  --workdir /project \
  "${TRIVY_IMAGE:?TRIVY_IMAGE must be exported by Jenkins}" \
  fs --scanners vuln,secret,misconfig --severity HIGH,CRITICAL \
  --ignore-unfixed --skip-dirs artifacts --exit-code 1 \
  --format json --output /dev/stdout . \
  >"${artifact_dir}/trivy-filesystem.json"; then
  failure=1
fi

manifest_file="${artifact_dir}/rendered-manifests.yaml"
: >"${manifest_file}"
for overlay in \
  deploy/base/infrastructure \
  deploy/overlays/local/migration \
  deploy/overlays/local/application \
  deploy/overlays/local/observability \
  deploy/overlays/local/logging-collector \
  deploy/overlays/local/gateway; do
  printf '%s\n' '---' >>"${manifest_file}"
  kubectl kustomize "${project_dir}/${overlay}" >>"${manifest_file}"
done

echo "Scanning freshly rendered Kubernetes configuration with Trivy..."
if ! docker run --rm \
  --volume "${project_dir}:/project:ro" \
  --workdir /project \
  "${TRIVY_IMAGE:?TRIVY_IMAGE must be exported by Jenkins}" \
  config --severity HIGH,CRITICAL --exit-code 1 \
  --format json --output /dev/stdout /project/artifacts/security/rendered-manifests.yaml \
  >"${artifact_dir}/trivy-kubernetes.json"; then
  failure=1
fi

echo "Validating rendered Kubernetes schemas..."
if ! docker run --rm -i "${KUBECONFORM_IMAGE}" \
  -strict -summary -ignore-missing-schemas <"${manifest_file}" \
  >"${artifact_dir}/kubeconform.txt" 2>&1; then
  failure=1
fi

echo "Evaluating application workload policy..."
if ! docker run --rm \
  --volume "${project_dir}:/project:ro" \
  "${CONFTEST_IMAGE}" test /project/artifacts/security/rendered-manifests.yaml \
  --policy /project/policy/kubernetes --all-namespaces --output json \
  >"${artifact_dir}/conftest.json"; then
  failure=1
fi

if [[ "${failure}" != "0" ]]; then
  echo "One or more source-security or Kubernetes-policy checks failed." >&2
  exit 1
fi
echo "Source-security and Kubernetes-policy checks passed."
