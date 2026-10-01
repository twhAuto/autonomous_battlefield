#!/usr/bin/env bash
# Render runtime manifests and synthetic production tunnel/TLS fixtures without secrets.
set -Eeuo pipefail
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
scratch="$(mktemp -d)"
trap 'rm -rf -- "${scratch}"' EXIT
for overlay in deploy/base/infrastructure deploy/overlays/local/migration deploy/overlays/local/application \
  deploy/overlays/local/observability deploy/overlays/local/logging-collector deploy/overlays/local/gateway \
  deploy/overlays/k3s/application deploy/overlays/k3s/migration deploy/overlays/k3s/gateway \
  deploy/overlays/k3s/logging-collector; do
  printf '%s\n' '---'
  kubectl kustomize "${project_dir}/${overlay}"
done
mkdir -p "${scratch}/deploy/overlays/local/infrastructure" "${scratch}/deploy/base"
cp -R "${project_dir}/deploy/overlays/local/observability" "${scratch}/deploy/overlays/local/"
cp -R "${project_dir}/deploy/overlays/k3s" "${scratch}/deploy/overlays/"
cp -R "${project_dir}/deploy/base/infrastructure" "${scratch}/deploy/base/"
cp "${project_dir}/deploy/overlays/local/infrastructure/"*.yaml "${scratch}/deploy/overlays/local/infrastructure/"
for name in secret ai-secret runtime cache; do
  # Only synthetic scan data, never an operator's private application inputs.
  printf 'SCAN_FIXTURE=synthetic\n' >"${scratch}/deploy/overlays/local/infrastructure/${name}.env"
done
# Public system roots are a syntax fixture, never a production kubelet trust choice.
fixture_ca="$(python3 -c 'import ssl; print(ssl.get_default_verify_paths().cafile)')"
python3 "${project_dir}/scripts/ops/configure-production-prometheus.py" "${scratch}/deploy/overlays/local/observability/prometheus" "${fixture_ca}"
for overlay in local/observability k3s/observability k3s/infrastructure; do
  printf '%s\n' '---'
  kubectl kustomize "${scratch}/deploy/overlays/${overlay}"
done
python3 "${project_dir}/scripts/ops/render-limited-launch.py" --output "${scratch}" \
  --hostname play.example.com --team ci-fixture --audience "$(printf 'a%.0s' {1..64})" \
  --tunnel-id 12345678-1234-1234-1234-123456789012 \
  --cloudflared-image "cloudflare/cloudflared@sha256:$(printf 'b%.0s' {1..64})" --admin owner@example.com
python3 - "${scratch}" <<'PY'
import json, sys
from pathlib import Path
for name in ('tunnel.json', 'network-policies.json'):
    for item in json.loads((Path(sys.argv[1])/name).read_text())['items']:
        item['metadata']['namespace'] = 'battlefield'
        print('---')
        print(json.dumps(item))
PY
