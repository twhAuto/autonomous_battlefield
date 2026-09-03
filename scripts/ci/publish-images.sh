#!/usr/bin/env bash

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=scripts/lib/deployment-components.sh
source "${project_dir}/scripts/lib/deployment-components.sh"

registry_namespace="${REGISTRY_NAMESPACE:-ghcr.io/twh09}"
backend_image="${PUBLISHED_BACKEND_IMAGE:-${registry_namespace}/autonomous-battlefield-backend}"
frontend_image="${PUBLISHED_FRONTEND_IMAGE:-${registry_namespace}/autonomous-battlefield-frontend}"
image_tag="${IMAGE_TAG:-$(git -C "${project_dir}" rev-parse HEAD)}"
platform="${IMAGE_PLATFORM:-linux/amd64}"
builder="${BUILDX_BUILDER:-battlefield-jenkins}"
build_components="${BUILD_COMPONENTS:-${DEPLOY_COMPONENTS:-all}}"
artifact_dir="${BUILD_ARTIFACT_DIR:-${project_dir}/artifacts/build}"
source_url="https://github.com/twh09/autonomous_battlefield"

deployment_components_select "${build_components}" build
mkdir -p "${artifact_dir}"
: >"${artifact_dir}/images.env"

for command in docker git sed; do
  if ! command -v "${command}" >/dev/null 2>&1; then
    echo "Required publish command not found: ${command}" >&2
    exit 1
  fi
done
if [[ ! "${image_tag}" =~ ^[0-9a-f]{40}$ ]] || [[ "${image_tag}" != "$(git -C "${project_dir}" rev-parse HEAD)" ]]; then
  echo "IMAGE_TAG must be the checked-out full Git commit SHA." >&2
  exit 1
fi
if [[ -n "$(git -C "${project_dir}" status --porcelain --untracked-files=normal)" && "${ALLOW_DIRTY:-0}" != "1" ]]; then
  echo "The Git worktree must be clean before publishing commit-addressed artifacts." >&2
  exit 1
fi
if ! docker buildx inspect "${builder}" >/dev/null 2>&1; then
  docker buildx create --name "${builder}" --driver docker-container >/dev/null
fi
docker buildx inspect --builder "${builder}" --bootstrap >/dev/null

publish() {
  local component="$1"
  local image="$2"
  local context="$3"
  local metadata="${artifact_dir}/${component}-build-metadata.json"
  local digest

  docker buildx build --builder "${builder}" \
    --platform "${platform}" \
    --push \
    --sbom=true \
    --provenance=mode=max \
    --metadata-file "${metadata}" \
    --label "org.opencontainers.image.source=${source_url}" \
    --label "org.opencontainers.image.revision=${image_tag}" \
    --label "org.opencontainers.image.version=${image_tag}" \
    --tag "${image}:${image_tag}" \
    "${context}"

  digest="$(sed -n 's/.*"containerimage.digest"[[:space:]]*:[[:space:]]*"\(sha256:[0-9a-f]\{64\}\)".*/\1/p' "${metadata}" | head -1)"
  if [[ ! "${digest}" =~ ^sha256:[0-9a-f]{64}$ ]]; then
    echo "Could not extract the published ${component} manifest digest." >&2
    exit 1
  fi
  printf '%s_IMAGE_REF=%s@%s\n' "${component^^}" "${image}" "${digest}" >>"${artifact_dir}/images.env"
  printf '%s_IMAGE_TAG=%s:%s\n' "${component^^}" "${image}" "${image_tag}" >>"${artifact_dir}/images.env"
  printf '%s_IMAGE_REVISION=%s\n' "${component^^}" "${image_tag}" >>"${artifact_dir}/images.env"
}

deployment_component_selected backend && publish backend "${backend_image}" "${project_dir}/backend"
deployment_component_selected frontend && publish frontend "${frontend_image}" "${project_dir}/frontend"

echo "Published image references:"
sed 's/^/  /' "${artifact_dir}/images.env"
