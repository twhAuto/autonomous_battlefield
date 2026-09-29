#!/usr/bin/env bash
# Shared pre-merge and main deployment regression gate; external mutations use doubles.
set -Eeuo pipefail
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
for suite in deployment-components kind-deployment command-wrappers runtime-assurance deployment-environment security-launchers registry-auth image-metadata registry-verification sonarqube-gate e2e-runner; do
  bash "${project_dir}/scripts/test/test-${suite}.sh"
done
for suite in access-service limited-launch production-preparation production-hardening agent-vm-host cross-review cli-agent; do
  python3 "${project_dir}/scripts/test/test-${suite}.py"
done
node --test "${project_dir}/scripts/test/prometheus-targets.test.cjs"
