#!/usr/bin/env bash
# Reports GHCR package versions alongside running image references to support retention
# review; it does not delete registry versions.

set -Eeuo pipefail
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${project_dir}/scripts/lib/deployment-environment.sh"
battlefield_env_preflight

owner="${GHCR_OWNER:-twh09}"
report_dir="${REPORT_DIR:-${project_dir}/artifacts/retention}"

for command in gh kubectl; do
  command -v "${command}" >/dev/null || { echo "Missing command: ${command}" >&2; exit 1; }
done
: "${GH_TOKEN:?GH_TOKEN with read:packages is required}"

# Stage beside the report so publication is a rename on the same filesystem.
report_parent="$(dirname -- "${report_dir}")"
mkdir -p -- "${report_parent}"
staging="$(mktemp -d "${report_parent}/.retention-staging.XXXXXX")"
retired="${staging/.retention-staging./.retention-retired.}"
trap 'rm -rf -- "${staging}" "${retired}"' EXIT
protected_file="${staging}/protected-digests.txt"
# Collect all inputs before publishing; failed reads must preserve the last report.
kubectl --context "${KUBERNETES_CONTEXT:-kind-battlefield}" get deployments \
  --namespace "${KUBERNETES_NAMESPACE:-battlefield}" \
  -o jsonpath='{range .items[*].spec.template.spec.containers[*]}{.image}{"\n"}{end}' | \
  sed -n 's/.*@\(sha256:[0-9a-f]\{64\}\).*/\1/p' >"${protected_file}"
if [[ ! -s "${protected_file}" ]]; then
  echo "Retention inventory incomplete: no digest-pinned running images found; previous report preserved." >&2
  exit 1
fi
rollback_file="${project_dir}/artifacts/deployment/rollback-images.env"
if [[ -f "${rollback_file}" ]]; then
  sed -n 's/.*@\(sha256:[0-9a-f]\{64\}\).*/\1/p' "${rollback_file}" >>"${protected_file}"
fi
sort -u -o "${protected_file}" "${protected_file}"

for package in autonomous-battlefield-backend autonomous-battlefield-frontend; do
  gh api --paginate \
    -H 'Accept: application/vnd.github+json' \
    "/users/${owner}/packages/container/${package}/versions?per_page=100" \
    >"${staging}/${package}-versions.json"
done

cat >"${staging}/README.txt" <<EOF
REPORT ONLY — no package versions were deleted.
Generated: $(date -u +%FT%TZ)
Minimum retention: 90 days or last 20 successful main builds, whichever is greater.
Never delete a digest listed in protected-digests.txt, a release, an incident/legal hold,
or any subject whose signature/attestation referrers have not been inventoried.
EOF

# Swap the complete staged directory into place: readers never see a mix of old and
# new files, and a failed swap restores the previous report.
if [[ -e "${report_dir}" ]]; then
  mv -T -- "${report_dir}" "${retired}"
fi
if ! mv -T -- "${staging}" "${report_dir}"; then
  if [[ -e "${retired}" ]]; then
    mv -T -- "${retired}" "${report_dir}"
  fi
  echo "Could not publish the retention report; previous report preserved." >&2
  exit 1
fi
