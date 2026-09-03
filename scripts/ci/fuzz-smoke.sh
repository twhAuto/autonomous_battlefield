#!/usr/bin/env bash

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
backend_dir="${project_dir}/backend"
fuzz_seconds="${FUZZ_SECONDS:-5}"

if [[ ! "${fuzz_seconds}" =~ ^[1-9][0-9]*$ ]]; then
  echo "FUZZ_SECONDS must be a positive integer." >&2
  exit 1
fi

mkdir -p "${project_dir}/artifacts/fuzz"
targets=(
  './internal/httpapi:FuzzReadJSON'
  './internal/ai:FuzzVerifyCapability'
  './internal/ai/mcpserver:FuzzMCPRequestBody'
  './internal/battle/server/api:FuzzEventCursorQuery'
  './internal/battle/server/catalog:FuzzScenarioYAML'
)

for target in "${targets[@]}"; do
  package="${target%%:*}"
  fuzz="${target##*:}"
  report="${project_dir}/artifacts/fuzz/${fuzz}.txt"
  (
    cd "${backend_dir}"
    go test "${package}" -run '^$' -fuzz "^${fuzz}$" -fuzztime "${fuzz_seconds}s"
  ) 2>&1 | tee "${report}"
done
