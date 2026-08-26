#!/usr/bin/env bash

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=scripts/lib/deployment-components.sh
source "${project_dir}/scripts/lib/deployment-components.sh"

backend_image="${BACKEND_IMAGE:-autonomous-battlefield-backend}"
frontend_image="${FRONTEND_IMAGE:-autonomous-battlefield-frontend}"
allow_dirty="${ALLOW_DIRTY:-0}"
version_file="${VERSION_FILE:-${project_dir}/VERSION}"
ci_build_number="${CI_BUILD_NUMBER:-${BUILD_NUMBER:-${GITHUB_RUN_NUMBER:-}}}"
ci_build_attempt="${CI_BUILD_ATTEMPT:-${GITHUB_RUN_ATTEMPT:-}}"
build_components="${BUILD_COMPONENTS:-${DEPLOY_COMPONENTS:-all}}"
deployment_components_select "${build_components}" build
build_backend=0
build_frontend=0
deployment_component_selected backend && build_backend=1
deployment_component_selected frontend && build_frontend=1

if [[ "${build_backend}" == "0" && "${build_frontend}" == "0" ]]; then
  echo "No application image changes detected; skipping image build."
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

for image_name in "${backend_image}" "${frontend_image}"; do
  if [[ ! "${image_name}" =~ ^[a-z0-9][a-z0-9._:/-]*$ ]]; then
    echo "Invalid container image name: ${image_name}" >&2
    exit 1
  fi
done

if [[ ! -f "${version_file}" ]]; then
  echo "Version file not found: ${version_file}" >&2
  exit 1
fi

base_version="$(<"${version_file}")"
if [[ ! "${base_version}" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "VERSION must contain one semantic version such as 0.1.0." >&2
  exit 1
fi

if [[ -n "${ci_build_number}" && ! "${ci_build_number}" =~ ^(0|[1-9][0-9]*)$ ]]; then
  echo "CI build number must be a non-negative integer." >&2
  exit 1
fi

if [[ -n "${ci_build_attempt}" && ! "${ci_build_attempt}" =~ ^[1-9][0-9]*$ ]]; then
  echo "CI build attempt must be a positive integer." >&2
  exit 1
fi

if [[ -n "${ci_build_attempt}" && -z "${ci_build_number}" ]]; then
  echo "A CI build attempt requires a CI build number." >&2
  exit 1
fi

build_version="${base_version}"
if [[ -n "${ci_build_number}" ]]; then
  build_version="${base_version}-build.${ci_build_number}"

  if [[ -n "${ci_build_attempt}" && "${ci_build_attempt}" != "1" ]]; then
    build_version="${build_version}.attempt.${ci_build_attempt}"
  fi
fi

image_tag="${IMAGE_TAG:-$(git -C "${project_dir}" rev-parse HEAD)}"
head_sha="$(git -C "${project_dir}" rev-parse HEAD)"

if [[ ! "${image_tag}" =~ ^[0-9a-f]{40}$ ]]; then
  echo "IMAGE_TAG must be a full 40-character lowercase Git commit SHA." >&2
  exit 1
fi

if [[ "${image_tag}" != "${head_sha}" ]]; then
  echo "IMAGE_TAG must identify the checked-out commit (${head_sha})." >&2
  exit 1
fi

if [[ -n "$(git -C "${project_dir}" status --porcelain --untracked-files=normal)" ]]; then
  if [[ "${allow_dirty}" != "1" ]]; then
    echo "The Git worktree is not clean." >&2
    echo "Commit or stash the changes before creating commit-addressed images." >&2
    echo "Set ALLOW_DIRTY=1 only for an intentional local test." >&2
    exit 1
  fi

  echo "Warning: building a dirty worktree with the explicit ALLOW_DIRTY override." >&2
fi

source_url="$(git -C "${project_dir}" config --get remote.origin.url || true)"

common_labels=(
  --label "org.opencontainers.image.revision=${image_tag}"
  --label "org.opencontainers.image.version=${build_version}"
)

if [[ -n "${source_url}" ]]; then
  common_labels+=(--label "org.opencontainers.image.source=${source_url}")
fi

backend_tags=(--tag "${backend_image}:${image_tag}")
frontend_tags=(--tag "${frontend_image}:${image_tag}")

if [[ -n "${ci_build_number}" ]]; then
  backend_tags+=(--tag "${backend_image}:${build_version}")
  frontend_tags+=(--tag "${frontend_image}:${build_version}")
fi

if [[ "${build_backend}" == "1" ]]; then
  echo "Building ${backend_image}:${image_tag} (version ${build_version})"
  docker build \
    "${common_labels[@]}" \
    "${backend_tags[@]}" \
    "${project_dir}/backend"
fi

if [[ "${build_frontend}" == "1" ]]; then
  echo "Building ${frontend_image}:${image_tag} (version ${build_version})"
  docker build \
    "${common_labels[@]}" \
    "${frontend_tags[@]}" \
    "${project_dir}/frontend"
fi

echo
echo "Built commit-addressed images:"
if [[ "${build_backend}" == "1" ]]; then
  echo "  ${backend_image}:${image_tag}"
fi
if [[ "${build_frontend}" == "1" ]]; then
  echo "  ${frontend_image}:${image_tag}"
fi

if [[ -n "${ci_build_number}" ]]; then
  echo
  echo "CI build aliases for the same images:"
  if [[ "${build_backend}" == "1" ]]; then
    echo "  ${backend_image}:${build_version}"
  fi
  if [[ "${build_frontend}" == "1" ]]; then
    echo "  ${frontend_image}:${build_version}"
  fi
fi

echo
echo "Deploy this revision to kind with:"
echo "  ./scripts/cd/deploy-kind.sh ${image_tag}"
