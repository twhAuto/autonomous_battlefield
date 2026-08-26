#!/usr/bin/env bash

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=scripts/lib/deployment-components.sh
source "${project_dir}/scripts/lib/deployment-components.sh"

backend_image="${BACKEND_IMAGE:-autonomous-battlefield-backend}"
frontend_image="${FRONTEND_IMAGE:-autonomous-battlefield-frontend}"
image_tag="${1:-${IMAGE_TAG:-$(git -C "${project_dir}" rev-parse HEAD)}}"
trivy_image="${TRIVY_IMAGE:-ghcr.io/aquasecurity/trivy:0.74.0@sha256:62b1e65e8869bc4b4c6aa4fa2b21595256c7c2f6018a9d9ad61caf87187c1969}"
trivy_cache_volume="${TRIVY_CACHE_VOLUME:-autonomous-battlefield-trivy-cache}"
scan_components="${SCAN_COMPONENTS:-${DEPLOY_COMPONENTS:-all}}"
deployment_components_select "${scan_components}" scan
scan_backend=0
scan_frontend=0
deployment_component_selected backend && scan_backend=1
deployment_component_selected frontend && scan_frontend=1

if [[ "${scan_backend}" == "0" && "${scan_frontend}" == "0" ]]; then
  echo "No application image changes detected; skipping image scan."
  exit 0
fi

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Required command not found: $1" >&2
    exit 1
  fi
}

require_command docker
require_command git

if [[ "$#" -gt 1 ]]; then
  echo "Usage: $0 [full-git-sha]" >&2
  exit 1
fi

if [[ ! "${image_tag}" =~ ^[0-9a-f]{40}$ ]]; then
  echo "The image tag must be a full 40-character lowercase Git commit SHA." >&2
  exit 1
fi

if [[ ! "${trivy_cache_volume}" =~ ^[a-zA-Z0-9][a-zA-Z0-9_.-]*$ ]]; then
  echo "Invalid Trivy cache volume name: ${trivy_cache_volume}" >&2
  exit 1
fi

scan_image() {
  local image="$1"

  if ! docker image inspect "${image}" >/dev/null 2>&1; then
    echo "Local image not found: ${image}" >&2
    exit 1
  fi

  echo "Scanning ${image} for fixable HIGH and CRITICAL vulnerabilities..."
  docker run --rm \
    --volume /var/run/docker.sock:/var/run/docker.sock \
    --volume "${trivy_cache_volume}:/root/.cache/trivy" \
    "${trivy_image}" \
    image \
    --exit-code 1 \
    --ignore-unfixed \
    --no-progress \
    --scanners vuln \
    --severity HIGH,CRITICAL \
    --timeout 10m \
    "${image}"
}

if [[ "${scan_backend}" == "1" ]]; then
  scan_image "${backend_image}:${image_tag}"
fi
if [[ "${scan_frontend}" == "1" ]]; then
  scan_image "${frontend_image}:${image_tag}"
fi
