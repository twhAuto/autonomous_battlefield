#!/usr/bin/env bash
# Runs promtool unit tests for the Prometheus SLI rules with the deployed Prometheus image.
set -Eeuo pipefail
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
deployment="${project_dir}/deploy/overlays/local/observability/prometheus/deployment.yaml"
image="$(sed -n 's/^ *image: \(prom\/prometheus:.*\)$/\1/p' "${deployment}")"
[[ -n "${image}" ]] || { echo "Prometheus image not found in ${deployment}" >&2; exit 1; }

docker run --rm --user "$(id -u):$(id -g)" --entrypoint promtool \
  -v "${project_dir}:/work:ro" --workdir /work/scripts/test \
  "${image}" test rules prometheus-rules.test.yml
