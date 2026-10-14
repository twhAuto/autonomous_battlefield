#!/usr/bin/env bash
# Shared pre-merge and main deployment regression gate; external mutations use doubles.
set -Eeuo pipefail
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
for suite in deployment-components kind-deployment runtime-assurance deployment-environment security-launchers registry-auth image-metadata registry-verification sonarqube-gate e2e-runner prometheus-rules failure-drills; do
  bash "${project_dir}/scripts/test/test-${suite}.sh"
done
for suite in assurance-job base-image-pins k3s-delivery k3s-delivery-rehearsal access-service limited-launch production-preparation production-hardening agent-vm-host cross-review cli-agent database-harness; do
  python3 "${project_dir}/scripts/test/test-${suite}.py"
done
node --test "${project_dir}/scripts/test/prometheus-targets.test.cjs"

bash "${project_dir}/scripts/test/test-server-bootstrap.sh"
