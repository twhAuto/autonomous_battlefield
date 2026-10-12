#!/usr/bin/env bash
# Decides whether a failed post-deployment verification can roll application images
# back automatically. Rollback needs an application change and both previous digests;
# otherwise the deployment is left for operator recovery.

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
digest_ref='^[a-z0-9][a-z0-9._:/-]+@sha256:[0-9a-f]{64}$'

application_changed=0
IFS=',' read -r -a components <<<"${DEPLOY_COMPONENTS:-all}"
for component in "${components[@]}"; do
  case "${component}" in
    all|backend|frontend|application) application_changed=1 ;;
  esac
done

if [[ "${application_changed}" != 1 ]]; then
  echo 'The targeted deployment did not change application images; skipping image rollback.'
  exit 0
fi

backend_ref="${PREVIOUS_BACKEND_IMAGE_REF:-}"
frontend_ref="${PREVIOUS_FRONTEND_IMAGE_REF:-}"
if [[ ! "${backend_ref}" =~ ${digest_ref} || ! "${frontend_ref}" =~ ${digest_ref} ]]; then
  echo 'Previous backend and frontend digest references are not both available for automatic rollback.'
  exit 0
fi

exec "${project_dir}/scripts/cd/rollback.sh" "${backend_ref}" "${frontend_ref}"
