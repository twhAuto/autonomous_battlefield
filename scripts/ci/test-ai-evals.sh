#!/usr/bin/env bash

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
mkdir -p "${project_dir}/artifacts/ai-eval"
(
  cd "${project_dir}/backend"
  go run ./cmd/aieval \
    --mode deterministic \
    --cases evals/cases \
    --pricing evals/pricing.yaml \
    --json ../artifacts/ai-eval/results.json \
    --junit ../artifacts/ai-eval/results.xml
)
