#!/usr/bin/env bash

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
backend_image="${BACKEND_IMAGE:-autonomous-battlefield-backend}"
frontend_image="${FRONTEND_IMAGE:-autonomous-battlefield-frontend}"
allow_dirty="${ALLOW_DIRTY:-0}"

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
  --label "org.opencontainers.image.version=${image_tag}"
)

if [[ -n "${source_url}" ]]; then
  common_labels+=(--label "org.opencontainers.image.source=${source_url}")
fi

echo "Building ${backend_image}:${image_tag}"
docker build \
  "${common_labels[@]}" \
  --tag "${backend_image}:${image_tag}" \
  "${project_dir}/backend"

echo "Building ${frontend_image}:${image_tag}"
docker build \
  "${common_labels[@]}" \
  --tag "${frontend_image}:${image_tag}" \
  "${project_dir}/frontend"

echo
echo "Built commit-addressed images:"
echo "  ${backend_image}:${image_tag}"
echo "  ${frontend_image}:${image_tag}"
echo
echo "Deploy this revision to kind with:"
echo "  ./scripts/deploy-kind.sh ${image_tag}"
