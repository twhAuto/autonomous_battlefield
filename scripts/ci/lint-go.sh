#!/usr/bin/env bash
# Checks Go formatting and runs static analysis for the backend without rewriting source
# files.

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
backend_dir="${project_dir}/backend"
# Pinned here rather than in tool-versions.env, whose changes redeploy every component.
gocognit_version="v1.3.0"
# SonarQube's go:S3776 fails the quality gate above this cognitive complexity; gocognit
# scores functions, test files included, the same way.
maximum_cognitive_complexity=15

for command in go gofmt; do
  if ! command -v "${command}" >/dev/null 2>&1; then
    echo "Required command not found: ${command}" >&2
    exit 1
  fi
done

go_version="$(go env GOVERSION)"
lint_cache="${GOCACHE:-${TMPDIR:-/tmp}/autonomous-battlefield-go-cache-${go_version}}"

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
  GOCACHE="${lint_cache}" go vet ./...
)
echo "Go vet: OK"

complexity_status=0
complex_functions="$(
  cd "${backend_dir}"
  GOCACHE="${lint_cache}" go run "github.com/uudashr/gocognit/cmd/gocognit@${gocognit_version}" \
    -over "${maximum_cognitive_complexity}" .
)" || complexity_status=$?
if [[ -n "${complex_functions}" ]]; then
  echo "These functions exceed cognitive complexity ${maximum_cognitive_complexity} (SonarQube go:S3776); split them:" >&2
  echo "${complex_functions}" >&2
  exit 1
fi
if ((complexity_status != 0)); then
  echo "gocognit ${gocognit_version} failed with exit status ${complexity_status}." >&2
  exit "${complexity_status}"
fi
echo "Go cognitive complexity: OK"
