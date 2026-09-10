#!/usr/bin/env bash
# Runs source/dependency security checks and validates rendered Kubernetes manifests
# against schemas and repository policy.

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck disable=SC1091
source "${project_dir}/scripts/ci/tool-versions.env"
artifact_dir="${SECURITY_ARTIFACT_DIR:-${project_dir}/artifacts/security}"
mkdir -p "${artifact_dir}"
failure=0
trivy_cache_volume="${TRIVY_CACHE_VOLUME:-autonomous-battlefield-trivy-cache}"
if [[ ! "${trivy_cache_volume}" =~ ^[a-zA-Z0-9][a-zA-Z0-9_.-]*$ ]]; then
  echo "Invalid Trivy cache volume name: ${trivy_cache_volume}" >&2
  exit 1
fi

for command in docker git go kubectl; do
  if ! command -v "${command}" >/dev/null 2>&1; then
    echo "Required security command not found: ${command}" >&2
    exit 1
  fi
done

echo "Running reachable Go vulnerability analysis..."
if ! (cd "${project_dir}/backend" && go run "golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}" -json ./... >"${artifact_dir}/govulncheck.json"); then
  echo "Go vulnerability analysis failed; see ${artifact_dir}/govulncheck.json." >&2
  failure=1
fi

echo "Auditing frontend runtime and CI dependencies..."
npm_audit_report="${artifact_dir}/npm-audit.json"
npm_audit_log_dir="${artifact_dir}/npm-logs"
mkdir -p "${npm_audit_log_dir}"
npm_audit_passed=0
npm_audit_definitive=0
trivy_ignore_unfixed=(--ignore-unfixed)

run_npm_audit() {
  local report="$1"
  shift
  docker run --rm \
    --network bridge \
    --volume "${project_dir}/frontend:/workspace:ro" \
    --volume "${npm_audit_log_dir}:/npm-logs" \
    --workdir /workspace \
    --env NPM_CONFIG_LOGS_DIR=/npm-logs \
    "${NPM_AUDIT_IMAGE:?NPM_AUDIT_IMAGE must be configured}" \
    npm audit --audit-level=high --json \
      --fetch-retries=2 \
      --fetch-retry-mintimeout=5000 \
      --fetch-retry-maxtimeout=15000 \
      --fetch-timeout=60000 "$@" >"${report}"
}

for attempt in 1 2; do
  : >"${npm_audit_report}"
  if run_npm_audit "${npm_audit_report}"; then
    npm_audit_passed=1
    break
  fi

  # A completed audit with findings is authoritative and must fail immediately.
  # Retry only when npm could not obtain an audit report from the registry.
  if grep -Eq '"auditReportVersion"[[:space:]]*:|"vulnerabilities"[[:space:]]*:' "${npm_audit_report}"; then
    npm_audit_definitive=1
    break
  fi
  if [[ "${attempt}" -lt 2 ]]; then
    echo "npm audit endpoint unavailable; retrying once..." >&2
    sleep 5
  fi
done

if [[ "${npm_audit_passed}" == "1" ]]; then
  printf '{"mode":"full"}\n' >"${artifact_dir}/npm-audit-mode.json"
elif [[ "${npm_audit_definitive}" == "1" ]]; then
  echo "npm audit failed; see ${npm_audit_report}." >&2
  failure=1
else
  echo "Full npm audit endpoint unavailable; requiring Trivy coverage for all frontend dependencies, including unfixed vulnerabilities." >&2
  printf '{"mode":"trivy-all-dependencies","reason":"full-npm-audit-endpoint-unavailable"}\n' \
    >"${artifact_dir}/npm-audit-mode.json"
  trivy_ignore_unfixed=()
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
  echo "Git secret scan failed; see ${artifact_dir}/gitleaks.json." >&2
  failure=1
fi

# Retry only database preparation, never a completed scan with findings.
# Explicitly switch registries after a transport failure such as a reset mid-download.
trivy_db_ready=0
for repository in mirror.gcr.io/aquasec/trivy-db:2 ghcr.io/aquasecurity/trivy-db:2; do
  echo "Preparing Trivy vulnerability database from ${repository}..."
  if docker run --rm \
    --volume "${trivy_cache_volume}:/root/.cache/trivy" \
    "${TRIVY_IMAGE}" image --download-db-only --no-progress --timeout 3m \
    --db-repository "${repository}"; then
    trivy_db_ready=1
    break
  fi
done

echo "Scanning source and infrastructure configuration with Trivy..."
if [[ "${trivy_db_ready}" != "1" ]]; then
  echo "Trivy database preparation failed for both registries; vulnerability scan cannot pass." >&2
  failure=1
else
  if ! docker run --rm \
    --volume "${trivy_cache_volume}:/root/.cache/trivy" \
    --volume "${project_dir}:/project:ro" \
    --workdir /project \
    "${TRIVY_IMAGE}" \
    fs --skip-db-update --no-progress --timeout 10m --scanners vuln,secret,misconfig --severity HIGH,CRITICAL \
    --include-dev-deps "${trivy_ignore_unfixed[@]}" --skip-dirs artifacts --skip-dirs frontend/coverage \
    --skip-dirs frontend/playwright-report --skip-dirs frontend/test-results --exit-code 1 \
    --format json --output /dev/stdout . \
    >"${artifact_dir}/trivy-filesystem.json"; then
    echo "Trivy source scan failed; see ${artifact_dir}/trivy-filesystem.json." >&2
    failure=1
  fi

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
  --volume "${trivy_cache_volume}:/root/.cache/trivy" \
  --volume "${project_dir}:/project:ro" \
  --volume "${artifact_dir}:/reports:ro" \
  --workdir /project \
  "${TRIVY_IMAGE}" \
  config --severity HIGH,CRITICAL --exit-code 1 \
  --format json --output /dev/stdout /reports/rendered-manifests.yaml \
  >"${artifact_dir}/trivy-kubernetes.json"; then
  echo "Trivy Kubernetes scan failed; see ${artifact_dir}/trivy-kubernetes.json." >&2
  failure=1
fi

echo "Validating rendered Kubernetes schemas..."
if ! docker run --rm -i "${KUBECONFORM_IMAGE}" \
  -strict -summary -ignore-missing-schemas <"${manifest_file}" \
  >"${artifact_dir}/kubeconform.txt" 2>&1; then
  echo "Kubernetes schema validation failed; see ${artifact_dir}/kubeconform.txt." >&2
  failure=1
fi

echo "Evaluating application workload policy..."
if ! docker run --rm \
  --volume "${project_dir}/policy/kubernetes:/policy:ro" \
  --volume "${artifact_dir}:/reports:ro" \
  "${CONFTEST_IMAGE}" test /reports/rendered-manifests.yaml \
  --policy /policy --all-namespaces --output json \
  >"${artifact_dir}/conftest.json"; then
  echo "Kubernetes workload policy failed; see ${artifact_dir}/conftest.json." >&2
  failure=1
fi

if [[ "${failure}" != "0" ]]; then
  echo "One or more source-security or Kubernetes-policy checks failed." >&2
  exit 1
fi
echo "Source-security and Kubernetes-policy checks passed."
