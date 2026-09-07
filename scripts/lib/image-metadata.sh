#!/usr/bin/env bash

# Shared by image publication and the admission policy renderer.
image_repository_valid() {
  [[ "$1" =~ ^[a-z0-9][a-z0-9.-]*(:[0-9]+)?(/[a-z0-9]+([._-]+[a-z0-9]+)*)+$ ]]
}

published_image_repositories() {
  local registry_namespace="${REGISTRY_NAMESPACE:-ghcr.io/twh09}"
  published_backend_image="${PUBLISHED_BACKEND_IMAGE:-${registry_namespace}/autonomous-battlefield-backend}"
  published_frontend_image="${PUBLISHED_FRONTEND_IMAGE:-${registry_namespace}/autonomous-battlefield-frontend}"
  if ! image_repository_valid "${published_backend_image}" || ! image_repository_valid "${published_frontend_image}"; then
    echo 'Published images must be repository names without tags, digests, or wildcards.' >&2
    return 1
  fi
}

# Partial rollouts must still verify the repository of each preserved image.
policy_image_repository() {
  local reference="$1" fallback="$2"
  if [[ -z "${reference}" ]]; then
    printf '%s\n' "${fallback}"
  elif [[ "${reference}" =~ @sha256:[0-9a-f]{64}$ ]] && image_repository_valid "${reference%@*}"; then
    printf '%s\n' "${reference%@*}"
  else
    echo 'Policy image references must be immutable repository@sha256 references.' >&2
    return 1
  fi
}

# Normalize common Git clone URLs to a credential-free repository URL.
image_source_url() {
  local url="${SOURCE_URL:-$(git -C "$1" config --get remote.origin.url || true)}"
  [[ -n "${url}" ]] || return 0
  case "${url}" in
    git@*:*) url="${url#git@}"; url="https://${url/:/\/}" ;;
    ssh://git@*/*) url="https://${url#ssh://git@}" ;;
  esac
  if [[ ! "${url}" =~ ^https?://[a-zA-Z0-9.-]+(:[0-9]+)?/[a-zA-Z0-9._~/%+-]+$ ]]; then
    echo 'Set SOURCE_URL to a credential-free HTTP(S) repository URL.' >&2
    return 1
  fi
  printf '%s\n' "${url%.git}"
}
