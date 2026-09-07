#!/usr/bin/env bash
# Publishes image signatures and attestations from build/security artifacts, then verifies
# the resulting supply-chain evidence.

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck disable=SC1091
source "${project_dir}/scripts/ci/tool-versions.env"
images_file="${BUILD_IMAGES_FILE:-${project_dir}/artifacts/build/images.env}"
artifact_dir="${project_dir}/artifacts/supply-chain"
cosign_key="${COSIGN_KEY_FILE:?COSIGN_KEY_FILE must point to the Jenkins signing-key credential}"
cosign_public_key="${COSIGN_PUBLIC_KEY_FILE:?COSIGN_PUBLIC_KEY_FILE must point to the reviewed public key}"
docker_config_dir="${DOCKER_CONFIG:-${HOME}/.docker}"
container_user="$(id -u):$(id -g)"
mkdir -p "${artifact_dir}"

if [[ ! -f "${images_file}" || ! -f "${cosign_key}" || ! -f "${cosign_public_key}" ]]; then
  echo "Image metadata and both Cosign key files are required." >&2
  exit 1
fi
# shellcheck disable=SC1090
source "${images_file}"
"${project_dir}/scripts/ci/generate-ai-manifest.sh"

cosign_run() {
  docker run --rm \
    --user "${container_user}" \
    --env HOME=/tmp \
    --env DOCKER_CONFIG=/docker-config \
    --env COSIGN_PASSWORD \
    --volume "${docker_config_dir}:/docker-config:ro" \
    --volume "$(dirname -- "${cosign_key}"):/signing-key:ro" \
    --volume "$(dirname -- "${cosign_public_key}"):/verification-key:ro" \
    --volume "${artifact_dir}:/evidence" \
    "${COSIGN_IMAGE}" "$@"
}

publish_cosign_evidence() {
  local reference="$1"
  local sbom="$2"
  local provenance="$3"
  shift 3
  local format_args=("$@")

  cosign_run attest "${format_args[@]}" --yes --key "/signing-key/$(basename -- "${cosign_key}")" \
    --type cyclonedx --predicate "/evidence/$(basename -- "${sbom}")" "${reference}"
  cosign_run attest "${format_args[@]}" --yes --key "/signing-key/$(basename -- "${cosign_key}")" \
    --type slsaprovenance --predicate "/evidence/$(basename -- "${provenance}")" "${reference}"
  cosign_run attest "${format_args[@]}" --yes --key "/signing-key/$(basename -- "${cosign_key}")" \
    --type https://autonomous-battlefield.dev/attestations/ai-manifest/v1 \
    --predicate /evidence/ai-manifest.json "${reference}"
  cosign_run sign "${format_args[@]}" --yes --key "/signing-key/$(basename -- "${cosign_key}")" \
    --annotations "revision=${IMAGE_TAG:?IMAGE_TAG is required}" "${reference}"
}

attest() {
  local component="$1"
  local reference="$2"
  [[ -z "${reference}" ]] && return 0
  local sbom="${artifact_dir}/${component}-sbom.cdx.json"
  local provenance="${artifact_dir}/${component}-provenance.json"

  docker run --rm \
    --env SYFT_REGISTRY_AUTH_USERNAME="${GHCR_USER:-}" \
    --env SYFT_REGISTRY_AUTH_PASSWORD="${GHCR_TOKEN:-}" \
    "${SYFT_IMAGE}" "${reference}" -o cyclonedx-json >"${sbom}"
  docker buildx imagetools inspect "${reference}" \
    --format '{{ json .Provenance.SLSA }}' >"${provenance}"
  if [[ ! -s "${provenance}" ]] || grep -Eq '^null$|^\{\}$' "${provenance}"; then
    echo "BuildKit provenance is missing for ${reference}." >&2
    return 1
  fi
  if ! grep -Eq '"builder"[[:space:]]*:[[:space:]]*\{' "${provenance}"; then
    echo "BuildKit provenance for ${reference} is not an SLSA v0.2 predicate." >&2
    echo "Cosign's slsaprovenance attestation requires the top-level builder field." >&2
    return 1
  fi

  # Cosign v3 evidence is the portable source of truth copied to ECR as OCI 1.1
  # referrers. Kyverno 1.15 uses Cosign's legacy digest-tag discovery, so publish
  # a second compatibility set covering the same digest and predicates.
  publish_cosign_evidence "${reference}" "${sbom}" "${provenance}"
  publish_cosign_evidence "${reference}" "${sbom}" "${provenance}" \
    --new-bundle-format=false --use-signing-config=false

  cosign_run verify --key "/verification-key/$(basename -- "${cosign_public_key}")" \
    --annotations "revision=${IMAGE_TAG}" "${reference}" >"${artifact_dir}/${component}-signature-verification.json"
  for type in cyclonedx slsaprovenance https://autonomous-battlefield.dev/attestations/ai-manifest/v1; do
    safe_type="$(printf '%s' "${type}" | tr '/:' '--')"
    cosign_run verify-attestation --key "/verification-key/$(basename -- "${cosign_public_key}")" \
      --type "${type}" "${reference}" >"${artifact_dir}/${component}-${safe_type}-verification.json"
  done
}

attest backend "${BACKEND_IMAGE_REF:-}"
attest frontend "${FRONTEND_IMAGE_REF:-}"
