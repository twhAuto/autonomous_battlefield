#!/usr/bin/env bash
# Scans published digest references and creates vulnerability/SBOM artifacts consumed by
# the attestation workflow.

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck disable=SC1091
source "${project_dir}/scripts/ci/tool-versions.env"
images_file="${BUILD_IMAGES_FILE:-${project_dir}/artifacts/build/images.env}"
artifact_dir="${project_dir}/artifacts/security"
mkdir -p "${artifact_dir}"

if [[ ! -f "${images_file}" ]]; then
  echo "Published image metadata not found: ${images_file}" >&2
  exit 1
fi
# shellcheck disable=SC1090
source "${images_file}"

scan() {
  local component="$1"
  local reference="$2"
  [[ -z "${reference}" ]] && return 0
  if [[ ! "${reference}" =~ ^[a-z0-9][a-z0-9._:/-]+@sha256:[0-9a-f]{64}$ ]]; then
    echo "Invalid immutable ${component} image reference." >&2
    return 1
  fi
  docker run --rm \
    --env TRIVY_USERNAME="${GHCR_USER:-}" \
    --env TRIVY_PASSWORD="${GHCR_TOKEN:-}" \
    "${TRIVY_IMAGE}" image --exit-code 1 --ignore-unfixed --no-progress \
    --scanners vuln --severity HIGH,CRITICAL --timeout 10m \
    --format json --output /dev/stdout "${reference}" \
    >"${artifact_dir}/${component}-image.json"
}

scan backend "${BACKEND_IMAGE_REF:-}"
scan frontend "${FRONTEND_IMAGE_REF:-}"

# Check the exact published frontend subject before signing or production rollout.
# with-ghcr-auth.sh already provides scoped Docker credentials for this pull.
verify_frontend() (
  local reference="$1" already_present=0 pulled=0
  if docker image inspect "${reference}" >/dev/null 2>&1; then
    already_present=1
  fi
  cleanup() {
    if [[ "${pulled}" == 1 && "${already_present}" == 0 ]]; then
      docker image rm "${reference}" >/dev/null 2>&1 ||
        echo "Could not remove the frontend verification image reference: ${reference}" >&2
    fi
  }
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  docker pull "${reference}"
  pulled=1
  python3 "${project_dir}/scripts/ci/verify-frontend-image.py" "${reference}"
)

if [[ -n "${FRONTEND_IMAGE_REF:-}" ]]; then
  verify_frontend "${FRONTEND_IMAGE_REF}"
fi
