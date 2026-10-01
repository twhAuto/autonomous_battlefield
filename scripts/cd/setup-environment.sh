#!/usr/bin/env bash
# Register an existing cluster without altering its workloads or original kubeconfig.
set -Eeuo pipefail
umask 077
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
environment="${1:-}"
driver="${3:-${CLUSTER_DRIVER:-kind}}"
[[ "${driver}" == kind || "${driver}" == k3s ]] || { echo 'Driver must be kind or k3s.' >&2; exit 1; }
source_context="${2:-kind-battlefield}"
[[ "$#" -le 3 && "${environment}" =~ ^(dev|production)$ ]] || {
  echo 'Usage: setup-environment.sh dev|production [existing-context] [kind|k3s]' >&2; exit 1;
}
config_dir="${BATTLEFIELD_CONFIG_DIR:-${HOME}/.config/autonomous-battlefield/${environment}}"
profile="${BATTLEFIELD_ENV_FILE:-${config_dir}/deployment.env}"
[[ ! -e "${profile}" && ! -L "${profile}" && ! -e "${config_dir}/kubeconfig" && ! -L "${config_dir}/kubeconfig" ]] || {
  echo "Registration already exists in ${config_dir}; refusing to overwrite cluster identity or credentials." >&2; exit 1;
}
check_config="$(mktemp)"
trap 'rm -f -- "${check_config}"' EXIT
uid="$(kubectl --context "${source_context}" --request-timeout=10s get namespace kube-system -o jsonpath='{.metadata.uid}')"
[[ -n "${uid}" ]] || exit 1
if [[ "${driver}" == kind ]]; then
  cluster="${KIND_CLUSTER_NAME:-battlefield}"
  endpoint="${DOCKER_HOST:-$(docker context inspect "${DOCKER_CONTEXT:-$(docker context show)}" --format '{{.Endpoints.docker.Host}}')}"
  [[ "${environment}" != dev || "${endpoint}" == unix:///* ]] || { echo 'Dev requires a local Docker socket.' >&2; exit 1; }
  DOCKER_CONTEXT= DOCKER_HOST="${endpoint}" kind get kubeconfig --name "${cluster}" >"${check_config}"
  docker_uid="$(KUBECONFIG="${check_config}" kubectl --context "kind-${cluster}" --request-timeout=10s get namespace kube-system -o jsonpath='{.metadata.uid}')"
  [[ "${uid}" == "${docker_uid}" ]] || { echo 'Source context and Docker cluster differ.' >&2; exit 1; }
else
  [[ "${environment}" == production && -z "${KIND_CLUSTER_NAME:-}" ]] || { echo 'K3s requires production and no Kind override.' >&2; exit 1; }
  : "${TARGET_URL:?Set TARGET_URL explicitly for the new K3s registration}"
  cluster="$(kubectl --context "${source_context}" get nodes -o jsonpath='{.items[0].metadata.name}')"
  python3 "${project_dir}/scripts/lib/k3s-delivery.py" verify "${source_context}" "${cluster}"
  ca_file="${KUBELET_CA_FILE:-${HOME}/.local/state/battlefield/k3s/kubelet-ca.crt}"
  python3 - "${ca_file}" <<'CA'
from pathlib import Path
import ssl, sys
pem = Path(sys.argv[1]).read_text()
if 'PRIVATE KEY' in pem: raise SystemExit('Expected public kubelet CA certificates only')
ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT).load_verify_locations(cadata=pem)
CA
  endpoint=""
fi
mkdir -p "${config_dir}"
chmod 700 "${config_dir}"
stage="$(mktemp -d "${config_dir}/.register.XXXXXX")"
trap 'rm -f -- "${check_config}"; rm -rf -- "${stage}"' EXIT
python3 - "${project_dir}" "${config_dir}" "${profile}" <<'PATHS'
from pathlib import Path
import sys
root, directory, profile = map(lambda s: Path(s).resolve(), sys.argv[1:])
if directory.is_relative_to(root) or profile.parent != directory:
    raise SystemExit('Registration must be outside the checkout, with its profile in the configuration directory')
PATHS
kubectl --context "${source_context}" config view --minify --flatten --raw >"${stage}/kubeconfig"
KUBECONFIG="${stage}/kubeconfig" kubectl config rename-context "${source_context}" "battlefield-${environment}" >/dev/null
registered_uid="$(KUBECONFIG="${stage}/kubeconfig" kubectl --context "battlefield-${environment}" --request-timeout=10s get namespace kube-system -o jsonpath='{.metadata.uid}')"
[[ "${registered_uid}" == "${uid}" ]] || { echo 'Cluster identity changed while registering.' >&2; exit 1; }
{
  printf 'BF_ENV=%s\nBF_CONTEXT=%s\nBF_CLUSTER=%s\nBF_UID=%s\n' "${environment}" "battlefield-${environment}" "${cluster}" "${uid}"
  printf 'BF_DRIVER=%s\n' "${driver}"
  [[ "${driver}" != k3s ]] || printf 'BF_KUBELET_CA_FILE=kubelet-ca.crt\n'
  printf 'BF_DOCKER_HOST=%s\nBF_URL=%s\nBF_KUBECONFIG=kubeconfig\nBF_DISPOSABLE=0\n' "${endpoint}" "${TARGET_URL:-http://battlefield.localhost:8888}"
} >"${stage}/deployment.env"
[[ "${driver}" != k3s ]] || install -m 600 "${ca_file}" "${stage}/kubelet-ca.crt"
node "${project_dir}/scripts/lib/application-env.cjs" profile "${stage}/deployment.env" >/dev/null
# Exclusive publication keeps existing identities/credentials intact even if two
# registrations race. The profile is published last; roll back only our inodes.
python3 - "${stage}" "${config_dir}" "${profile}" <<'PUBLISH'
import os, sys
from pathlib import Path
stage, directory, profile = map(Path, sys.argv[1:])
pairs = [(stage/'kubeconfig',directory/'kubeconfig')]
if (stage/'kubelet-ca.crt').exists(): pairs.append((stage/'kubelet-ca.crt',directory/'kubelet-ca.crt'))
pairs.append((stage/'deployment.env',profile))
created = []
try:
    for source, target in pairs:
        os.link(source,target)
        created.append((source,target))
except OSError:
    for source,target in reversed(created):
        if target.exists() and os.path.samefile(source,target): target.unlink()
    raise SystemExit('Registration publication failed; existing files were preserved') from None
PUBLISH
echo "Registered ${environment}: context battlefield-${environment}, cluster UID ${uid}."
echo "Configuration: ${profile}"
echo 'Workloads, databases, and your original kubeconfig are unchanged.'
