#!/usr/bin/env bash
set -Eeuo pipefail

project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=../ci/tool-versions.env
source "${project_dir}/scripts/ci/tool-versions.env"
images_file="${BUILD_IMAGES_FILE:-${project_dir}/artifacts/build/images.env}"
public_key="${COSIGN_PUBLIC_KEY_FILE:?COSIGN_PUBLIC_KEY_FILE is required}"
docker_config_dir="${DOCKER_CONFIG:-${HOME}/.docker}"
container_user="$(id -u):$(id -g)"
report_dir="${project_dir}/artifacts/aws-promotion"
mkdir -p "${report_dir}"

for command in aws docker; do
  command -v "${command}" >/dev/null || { echo "Missing command: ${command}" >&2; exit 1; }
done
[[ -f "${images_file}" ]] || { echo "Missing ${images_file}" >&2; exit 1; }
# shellcheck disable=SC1090
source "${images_file}"

aws_account_id="${AWS_ACCOUNT_ID:?AWS_ACCOUNT_ID is required}"
aws_region="${AWS_REGION:?AWS_REGION is required}"
registry="${aws_account_id}.dkr.ecr.${aws_region}.amazonaws.com"
aws ecr get-login-password --region "${aws_region}" | \
  docker login --username AWS --password-stdin "${registry}"

container_run() {
  local image="$1"
  shift
  docker run --rm -v "${docker_config_dir}:/root/.docker:ro" "$image" "$@"
}

cosign_run() {
  docker run --rm \
    --user "${container_user}" \
    --env HOME=/tmp \
    --env DOCKER_CONFIG=/docker-config \
    --volume "${docker_config_dir}:/docker-config:ro" \
    --volume "$(dirname -- "${public_key}"):/verification-key:ro" \
    "${COSIGN_IMAGE}" "$@"
}

promote() {
  local component="$1"
  local source_ref="$2"
  local target_repo="$3"
  local source_digest="${source_ref##*@}"
  local target_ref="${target_repo}:sha-${source_digest#sha256:}"

  [[ "${source_ref}" == *@sha256:* ]] || { echo "${component} source is not a digest reference" >&2; return 1; }
  docker run --rm -v "${docker_config_dir}:/root/.docker:ro" \
    "${ORAS_IMAGE}" copy --recursive "${source_ref}" "${target_ref}"

  local copied_digest
  copied_digest="$(container_run "${CRANE_IMAGE}" digest "${target_ref}")"
  if [[ "${copied_digest}" != "${source_digest}" ]]; then
    echo "Digest changed during ${component} promotion: ${source_digest} -> ${copied_digest}" >&2
    return 1
  fi

  cosign_run verify --key "/verification-key/$(basename -- "${public_key}")" \
    "${target_ref}" >"${report_dir}/${component}-signature.json"
  for type in cyclonedx slsaprovenance https://autonomous-battlefield.dev/attestations/ai-manifest/v1; do
    safe_type="$(printf '%s' "${type}" | tr '/:' '--')"
    cosign_run verify-attestation --key "/verification-key/$(basename -- "${public_key}")" \
      --type "${type}" "${target_ref}" >"${report_dir}/${component}-${safe_type}.json"
  done
  docker run --rm -v "${docker_config_dir}:/root/.docker:ro" \
    "${ORAS_IMAGE}" discover --format json "${target_ref}" >"${report_dir}/${component}-referrers.json"
  printf '%s=%s@%s\n' "${component^^}_ECR_IMAGE_REF" "${target_repo}" "${copied_digest}" >>"${report_dir}/images.env"
}

: >"${report_dir}/images.env"
promote backend "${BACKEND_IMAGE_REF:?BACKEND_IMAGE_REF missing}" "${ECR_BACKEND_REPOSITORY:?ECR_BACKEND_REPOSITORY required}"
promote frontend "${FRONTEND_IMAGE_REF:?FRONTEND_IMAGE_REF missing}" "${ECR_FRONTEND_REPOSITORY:?ECR_FRONTEND_REPOSITORY required}"
