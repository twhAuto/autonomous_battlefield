#!/usr/bin/env bash
# Starts an isolated PostgreSQL container, exports its connection URL to the requested
# test command, and removes it on exit.

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck disable=SC1091
source "${project_dir}/scripts/ci/tool-versions.env"

postgres_image="${POSTGRES_TEST_IMAGE_OVERRIDE:-${POSTGRES_TEST_IMAGE}}"
build_identity="${BUILD_TAG:-${BUILD_NUMBER:-local-$$}}"
container_name="battlefield-ci-postgres-$(printf '%s' "${build_identity}" | tr -cs '[:alnum:].-' '-')"
database_name="battlefield_test"
database_user="battlefield_test"
# run-query-plans.sh validates this 24-byte / 48-hex credential shape; update both together.
database_password="$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')"
container_id=""

cleanup() {
  if [[ -n "${container_id}" ]]; then
    docker rm --force "${container_id}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT INT TERM

if [[ "$#" -eq 0 ]]; then
  echo "Usage: $0 command [argument ...]" >&2
  exit 2
fi
for command in docker od tr; do
  if ! command -v "${command}" >/dev/null 2>&1; then
    echo "Required command not found: ${command}" >&2
    exit 1
  fi
done

container_id="$(docker run --detach --rm \
  --name "${container_name}" \
  --publish 127.0.0.1::5432 \
  --env "POSTGRES_DB=${database_name}" \
  --env "POSTGRES_USER=${database_user}" \
  --env "POSTGRES_PASSWORD=${database_password}" \
  --health-cmd "pg_isready -U ${database_user} -d ${database_name}" \
  --health-interval 1s \
  --health-timeout 3s \
  --health-retries 30 \
  "${postgres_image}")"

for _ in $(seq 1 45); do
  health="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}starting{{end}}' "${container_id}")"
  if [[ "${health}" == "healthy" ]]; then
    break
  fi
  if [[ "${health}" == "unhealthy" ]]; then
    docker logs "${container_id}" >&2 || true
    echo "PostgreSQL test container became unhealthy." >&2
    exit 1
  fi
  sleep 1
done

if [[ "$(docker inspect --format '{{.State.Health.Status}}' "${container_id}")" != "healthy" ]]; then
  docker logs "${container_id}" >&2 || true
  echo "PostgreSQL test container did not become ready." >&2
  exit 1
fi

host_port="$(docker port "${container_id}" 5432/tcp | awk -F: 'NR == 1 {print $NF}')"
if [[ ! "${host_port}" =~ ^[0-9]+$ ]]; then
  echo "Could not resolve the PostgreSQL test port." >&2
  exit 1
fi

export TEST_DATABASE_URL="postgres://${database_user}:${database_password}@127.0.0.1:${host_port}/${database_name}?sslmode=disable"
export TEST_POSTGRES_CONTAINER_ID="${container_id}"
"$@"
