#!/usr/bin/env bash
# Verifies configured registry image digests and Cosign evidence before deployment uses
# them.

set -Eeuo pipefail

project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=../ci/tool-versions.env
source "${project_dir}/scripts/ci/tool-versions.env"
public_key="${COSIGN_PUBLIC_KEY_FILE:?COSIGN_PUBLIC_KEY_FILE is required}"
docker_config_dir="${DOCKER_CONFIG:-${HOME}/.docker}"
container_user="$(id -u):$(id -g)"
report_dir="${VERIFY_ARTIFACT_DIR:-${project_dir}/artifacts/deployment-verification}"
mkdir -p "${report_dir}"

cosign_run() {
  docker run --rm \
    --user "${container_user}" \
    --env HOME=/tmp \
    --env DOCKER_CONFIG=/docker-config \
    --volume "${docker_config_dir}:/docker-config:ro" \
    --volume "$(dirname -- "${public_key}"):/verification-key:ro" \
    "${COSIGN_IMAGE}" "$@"
}

verify_ref() {
  local component="$1"
  local reference="$2"
  local expected_revision="$3"
  local signature_report="${report_dir}/${component}-signature.json"
  [[ "${reference}" =~ ^[a-z0-9][a-z0-9._:/-]+@sha256:[0-9a-f]{64}$ ]] || {
    echo "Invalid ${component} digest reference." >&2
    return 1
  }
  local annotation_args=()
  if [[ -n "${expected_revision}" ]]; then
    if [[ ! "${expected_revision}" =~ ^[0-9a-f]{40}$ ]]; then
      echo "Invalid expected ${component} revision." >&2
      return 1
    fi
    annotation_args=(--annotations "revision=${expected_revision}")
  fi
  cosign_run verify --key "/verification-key/$(basename -- "${public_key}")" \
    "${annotation_args[@]}" "${reference}" >"${signature_report}"
  if [[ -z "${expected_revision}" ]] && ! grep -Eq '"revision"[[:space:]]*:[[:space:]]*"[0-9a-f]{40}"' "${signature_report}"; then
    echo "${component} signature has no full commit revision annotation." >&2
    return 1
  fi
  for type in cyclonedx slsaprovenance https://autonomous-battlefield.dev/attestations/ai-manifest/v1; do
    safe_type="$(printf '%s' "${type}" | tr '/:' '--')"
    cosign_run verify-attestation --key "/verification-key/$(basename -- "${public_key}")" \
      --type "${type}" "${reference}" >"${report_dir}/${component}-${safe_type}.json"
  done
}

verify_ref backend "${1:?backend digest reference required}" "${BACKEND_IMAGE_REVISION:-}"
if [[ -n "${2:-}" ]]; then
  verify_ref frontend "$2" "${FRONTEND_IMAGE_REVISION:-}"
fi
