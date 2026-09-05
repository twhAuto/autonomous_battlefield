#!/usr/bin/env bash

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
output="${AI_MANIFEST_FILE:-${project_dir}/artifacts/supply-chain/ai-manifest.json}"
mkdir -p "$(dirname -- "${output}")"

prompt_file="${project_dir}/backend/internal/ai/prompts/battlefield-agent-v2.txt"
prompt_hash="$(sha256sum "${prompt_file}" | awk '{print $1}')"
ruleset_hash="$(find "${project_dir}/backend/rulesets" -type f -name '*.yaml' -print0 | sort -z | xargs -0 sha256sum | sha256sum | awk '{print $1}')"
evaluation_hash="$(find "${project_dir}/backend/evals" -type f -print0 | sort -z | xargs -0 sha256sum | sha256sum | awk '{print $1}')"
revision="$(git -C "${project_dir}" rev-parse HEAD)"

printf '{\n  "schemaVersion": "battlefield.ai-build-manifest.v1",\n  "revision": "%s",\n  "promptVersion": "battlefield-agent-v2",\n  "promptSourceSha256": "%s",\n  "toolSchemaVersion": "battlefield.mcp-tools.v2",\n  "rulesetBundleSha256": "%s",\n  "evaluationCorpusVersion": "2026-09-03.v1",\n  "evaluationBundleSha256": "%s",\n  "supportedProfileTypes": ["openai-api", "gemini-api", "codex-cli", "antigravity-cli", "scripted"]\n}\n' \
  "${revision}" "${prompt_hash}" "${ruleset_hash}" "${evaluation_hash}" >"${output}"
