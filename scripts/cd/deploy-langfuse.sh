#!/usr/bin/env bash
# Installs dedicated Langfuse stores and app on the registered cluster. Existing
# bootstrap credentials are reused; never regenerate credentials on upgrade.
set -Eeuo pipefail
set +x
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${project_dir}/scripts/lib/deployment-environment.sh"
battlefield_env_preflight
for tool in kubectl python3; do command -v "${tool}" >/dev/null || { echo "Required: ${tool}" >&2; exit 1; }; done
umask 077
runtime_dir="$(mktemp -d)"
trap 'rm -rf -- "${runtime_dir}"' EXIT
helm_binary="$(python3 "${project_dir}/scripts/lib/ensure-helm.py" "${runtime_dir}")"
namespace="${KUBERNETES_NAMESPACE}"
context="${KUBERNETES_CONTEXT}"
kube() { kubectl --context "${context}" --namespace "${namespace}" "$@"; }
helm_cluster() { "${helm_binary}" "$@" --kube-context "${context}"; }
# Do not take ownership of existing cluster-wide operator installations.
if ! kube get crd certificates.cert-manager.io >/dev/null 2>&1; then
  helm_cluster upgrade --install cert-manager oci://quay.io/jetstack/charts/cert-manager --version v1.20.2 \
    --namespace cert-manager --create-namespace --set crds.enabled=true --wait --timeout 5m
fi
kube wait --for=condition=Established crd/certificates.cert-manager.io crd/issuers.cert-manager.io --timeout=120s
if ! kube get crd clickhouseclusters.clickhouse.com >/dev/null 2>&1; then
  helm_cluster upgrade --install clickhouse-operator oci://ghcr.io/clickhouse/clickhouse-operator-helm --version 0.0.5 \
    --namespace clickhouse-operator --create-namespace --wait --timeout 5m
fi
kube wait --for=condition=Established crd/clickhouseclusters.clickhouse.com crd/keeperclusters.clickhouse.com --timeout=120s
# Bootstrap password is generated once and stored only in the Kubernetes Secret.
# Retrieve it explicitly using the command in docs/operations/langfuse.md.
if ! kube get secret battlefield-langfuse-bootstrap >/dev/null 2>&1; then
  python3 - "${runtime_dir}/bootstrap.env" <<'PY'
import secrets, sys
from pathlib import Path
values = {
 'LANGFUSE_INIT_ORG_ID': 'battlefield',
 'LANGFUSE_INIT_ORG_NAME': 'Autonomous Battlefield',
 'LANGFUSE_INIT_PROJECT_ID': 'battlefield-agents',
 'LANGFUSE_INIT_PROJECT_NAME': 'Battlefield Agents',
 'LANGFUSE_INIT_PROJECT_PUBLIC_KEY': 'pk-lf-' + secrets.token_hex(16),
 'LANGFUSE_INIT_PROJECT_SECRET_KEY': 'sk-lf-' + secrets.token_hex(32),
 'LANGFUSE_INIT_USER_EMAIL': 'admin@battlefield.local',
 'LANGFUSE_INIT_USER_NAME': 'Battlefield Admin',
 'LANGFUSE_INIT_USER_PASSWORD': secrets.token_urlsafe(32),
}
Path(sys.argv[1]).write_text(''.join(f'{k}={v}\n' for k,v in values.items()))
PY
  kube create secret generic battlefield-langfuse-bootstrap --from-env-file="${runtime_dir}/bootstrap.env" >/dev/null
fi
# Extract only the ingestion key pair into a separate least-privilege Secret.
kube get secret battlefield-langfuse-bootstrap -o json >"${runtime_dir}/bootstrap.json"
python3 - "${runtime_dir}" <<'PY'
import base64, json, sys
from pathlib import Path
root = Path(sys.argv[1]); data = json.loads((root/'bootstrap.json').read_text())['data']
public = base64.b64decode(data['LANGFUSE_INIT_PROJECT_PUBLIC_KEY'])
secret = base64.b64decode(data['LANGFUSE_INIT_PROJECT_SECRET_KEY'])
(root/'authorization').write_text('Basic ' + base64.b64encode(public + b':' + secret).decode())
PY
kube create secret generic battlefield-langfuse-export --from-file="authorization=${runtime_dir}/authorization" --dry-run=client -o yaml | kube apply -f - >/dev/null
helm_cluster upgrade --install langfuse langfuse --repo https://langfuse.github.io/langfuse-k8s --version 2.1.0 \
  --namespace "${namespace}" -f "${project_dir}/deploy/langfuse/values.yaml" --wait --timeout 15m
kube create configmap battlefield-langfuse-links \
  --from-literal=LANGFUSE_BASE_URL=http://langfuse.localhost:8888 \
  --from-literal=LANGFUSE_PROJECT_ID=battlefield-agents --dry-run=client -o yaml | kube apply -f - >/dev/null
kube create configmap battlefield-otel-collector-config --from-file="config.yaml=${project_dir}/deploy/langfuse/collector-config.yaml" --dry-run=client -o yaml | kube apply -f - >/dev/null
kube apply -f "${project_dir}/deploy/overlays/local/observability/otel-collector/deployment.yaml"
kube apply -f "${project_dir}/deploy/overlays/local/observability/otel-collector/service.yaml"
kube apply -f "${project_dir}/deploy/overlays/local/gateway/langfuse-route.yaml"
kube rollout restart deployment/battlefield-otel-collector deployment/battlefield-api
kube rollout status deployment/battlefield-otel-collector --timeout=120s
kube rollout status deployment/battlefield-api --timeout=120s
echo 'Langfuse is ready at http://langfuse.localhost:8888. See docs/operations/langfuse.md for login and validation.'
echo 'Future observability deployments and host CLI runs automatically detect this installation.'
