#!/usr/bin/env bash

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
backend_dir="${project_dir}/backend"

for command in go gofmt; do
  if ! command -v "${command}" >/dev/null 2>&1; then
    echo "Required command not found: ${command}" >&2
    exit 1
  fi
done

mapfile -d '' go_files < <(find "${backend_dir}" -type f -name '*.go' -print0)
if ((${#go_files[@]} == 0)); then
  echo "No Go files found under ${backend_dir}." >&2
  exit 1
fi

unformatted="$(gofmt -l "${go_files[@]}")"
if [[ -n "${unformatted}" ]]; then
  echo "The following Go files need gofmt:" >&2
  echo "${unformatted}" >&2
  exit 1
fi

echo "Go formatting: OK"
(
  cd "${backend_dir}"
  go vet ./...
)
echo "Go vet: OK"
