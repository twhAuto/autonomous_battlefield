#!/usr/bin/env bash

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
test_dir="$(mktemp -d "${TMPDIR:-/tmp}/autonomous-battlefield-kind-deployment-test.XXXXXX")"

cleanup() {
  chmod -R u+w "${test_dir}" 2>/dev/null || true
  rm -rf -- "${test_dir}"
}
trap cleanup EXIT INT TERM

# shellcheck source=scripts/cd/lib/kind-deployment.sh
source "${project_dir}/scripts/cd/lib/kind-deployment.sh"

secret_file="${test_dir}/jenkins-credential"
target_file="${test_dir}/secret.env"
database_url='postgres://battlefield:password@postgres:5432/battlefield?sslmode=disable'

printf 'DATABASE_URL=%s\n' "${database_url}" >"${secret_file}"
chmod 400 "${secret_file}"

kind_deploy_prepare_database_secret "${target_file}" >/dev/null

if [[ "$(stat -c '%a' "${target_file}")" != "600" ]]; then
  echo "Staged database secret must have mode 600." >&2
  exit 1
fi

if ! grep -Fxq "MCP_DATABASE_URL=${database_url}" "${target_file}"; then
  echo "Staged database secret did not inherit DATABASE_URL for MCP_DATABASE_URL." >&2
  exit 1
fi

if [[ "$(grep -c '^MCP_DATABASE_URL=' "${target_file}")" != "1" ]]; then
  echo "Staged database secret must contain exactly one MCP_DATABASE_URL." >&2
  exit 1
fi

echo "Kind deployment secret staging tests passed."
