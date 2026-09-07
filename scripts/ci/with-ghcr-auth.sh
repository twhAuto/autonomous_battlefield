#!/usr/bin/env bash

set -Eeuo pipefail

if [[ "$#" == 0 ]]; then
  echo "Usage: $0 command [argument ...]" >&2
  exit 2
fi
: "${GHCR_USER:?GHCR_USER is required}"
: "${GHCR_TOKEN:?GHCR_TOKEN is required}"

# Keep credentials private to this command without losing the agent's builder.
registry_config="$(mktemp -d "${TMPDIR:-/tmp}/battlefield-docker-config.XXXXXX")"
command_pid=""
export DOCKER_CONFIG="${registry_config}"
cleanup() {
  if [[ -n "${command_pid}" ]]; then
    kill -TERM "${command_pid}" 2>/dev/null || true
    wait "${command_pid}" 2>/dev/null || true
  fi
  docker logout ghcr.io >/dev/null 2>&1 || true
  rm -rf -- "${registry_config}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
chmod 700 "${registry_config}"
export BUILDX_CONFIG="${BUILDX_CONFIG:-${HOME}/.docker/buildx}"
printf '%s' "${GHCR_TOKEN}" | docker login ghcr.io --username "${GHCR_USER}" --password-stdin

"$@" <&0 &
command_pid=$!
status=0
wait "${command_pid}" || status=$?
command_pid=""
exit "${status}"
