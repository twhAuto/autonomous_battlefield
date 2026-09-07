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
