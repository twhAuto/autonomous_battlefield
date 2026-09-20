#!/usr/bin/env bash
# Verify actual Docker ignore behavior using synthetic files, without registry pulls.
set -Eeuo pipefail
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
test_dir="$(mktemp -d "${TMPDIR:-/tmp}/battlefield-docker-context.XXXXXX")"
trap 'rm -rf -- "${test_dir}"' EXIT

for component in backend frontend; do
  context="${test_dir}/${component}"
  output="${test_dir}/${component}-output"
  mkdir -p "${context}/nested"
  cp "${project_dir}/${component}/.dockerignore" "${context}/.dockerignore"
  for name in .env .env.local .env.production .env.backup .env.example; do
    printf 'SYNTHETIC_TEST_VALUE=example\n' >"${context}/${name}"
    printf 'SYNTHETIC_TEST_VALUE=example\n' >"${context}/nested/${name}"
  done
  printf 'application source\n' >"${context}/source.txt"
  printf 'FROM scratch\nCOPY . /\n' >"${context}/Dockerfile"
  docker build --output "type=local,dest=${output}" "${context}"
  for prefix in '' 'nested/'; do
    for name in .env .env.local .env.production .env.backup; do
      if [[ -e "${output}/${prefix}${name}" ]]; then
        echo "Local settings leaked into ${component} context: ${prefix}${name}" >&2
        exit 1
      fi
    done
    [[ -f "${output}/${prefix}.env.example" ]] || { echo "Missing example in ${component} context" >&2; exit 1; }
  done
  [[ -f "${output}/source.txt" ]] || { echo "Application source was excluded" >&2; exit 1; }
done
echo "Docker build contexts exclude local settings and retain source/examples."
