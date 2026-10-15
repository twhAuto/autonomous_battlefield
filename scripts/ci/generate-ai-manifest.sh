#!/usr/bin/env bash
# Writes the runtime's embedded AI provenance as a build artifact for image metadata and
# attestations.

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
output="${AI_MANIFEST_FILE:-${project_dir}/artifacts/supply-chain/ai-manifest.json}"
mkdir -p "$(dirname -- "${output}")"

# Read embedded provenance from the same package used by the runtime.
metadata="$(cd "${project_dir}/backend" && go run ./cmd/aieval -mode metadata)"
IFS=$'\t' read -r prompt_version prompt_hash tool_schema_version game_data_hash <<<"${metadata}"
if [[ ! "${game_data_hash}" =~ ^[0-9a-f]{64}$ ]]; then
  echo "aieval metadata did not report a game content digest" >&2
  exit 1
fi
# Hash relative names as well as content, independently of the CI checkout path.
evaluation_hash="$(cd "${project_dir}/backend/evals" && find . -type f -print0 | LC_ALL=C sort -z | xargs -0 sha256sum | sha256sum | awk '{print $1}')"
revision="$(git -C "${project_dir}" rev-parse HEAD)"

printf '{\n  "schemaVersion": "battlefield.ai-build-manifest.v2",\n  "revision": "%s",\n  "promptVersion": "%s",\n  "promptSourceSha256": "%s",\n  "toolSchemaVersion": "%s",\n  "gameDataBundleSha256": "%s",\n  "evaluationCorpusVersion": "2026-09-03.v1",\n  "evaluationBundleSha256": "%s",\n  "supportedProfileTypes": ["openai-api", "gemini-api", "codex-cli", "antigravity-cli", "claude-cli", "scripted"]\n}\n' \
  "${revision}" "${prompt_version}" "${prompt_hash}" "${tool_schema_version}" "${game_data_hash}" "${evaluation_hash}" >"${output}"
