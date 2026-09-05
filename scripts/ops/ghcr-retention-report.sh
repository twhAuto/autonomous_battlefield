#!/usr/bin/env bash
set -Eeuo pipefail
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${project_dir}/scripts/lib/deployment-environment.sh"
battlefield_env_preflight


owner="${GHCR_OWNER:-twh09}"
report_dir="${REPORT_DIR:-artifacts/retention}"
mkdir -p "${report_dir}"

for command in gh kubectl; do
  command -v "${command}" >/dev/null || { echo "Missing command: ${command}" >&2; exit 1; }
done
: "${GH_TOKEN:?GH_TOKEN with read:packages is required}"

protected_file="${report_dir}/protected-digests.txt"
: >"${protected_file}"
kubectl --context "${KUBERNETES_CONTEXT:-kind-battlefield}" get deployments \
  --namespace "${KUBERNETES_NAMESPACE:-battlefield}" \
  -o jsonpath='{range .items[*].spec.template.spec.containers[*]}{.image}{"\n"}{end}' 2>/dev/null | \
  sed -n 's/.*@\(sha256:[0-9a-f]\{64\}\).*/\1/p' >>"${protected_file}" || true
if [[ -f artifacts/deployment/rollback-images.env ]]; then
  sed -n 's/.*@\(sha256:[0-9a-f]\{64\}\).*/\1/p' artifacts/deployment/rollback-images.env >>"${protected_file}"
fi
sort -u -o "${protected_file}" "${protected_file}"

for package in autonomous-battlefield-backend autonomous-battlefield-frontend; do
  gh api --paginate \
    -H 'Accept: application/vnd.github+json' \
    "/users/${owner}/packages/container/${package}/versions?per_page=100" \
    >"${report_dir}/${package}-versions.json"
done

cat >"${report_dir}/README.txt" <<EOF
REPORT ONLY — no package versions were deleted.
Generated: $(date -u +%FT%TZ)
Minimum retention: 90 days or last 20 successful main builds, whichever is greater.
Never delete a digest listed in protected-digests.txt, a release, an incident/legal hold,
or any subject whose signature/attestation referrers have not been inventoried.
EOF
