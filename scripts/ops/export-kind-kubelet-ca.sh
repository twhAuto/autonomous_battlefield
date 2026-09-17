#!/usr/bin/env bash
# Retrieve public serving certificates through the registered, trusted Docker daemon.
# Never learn trust from an unauthenticated TLS connection; never export private keys.
set -Eeuo pipefail
umask 077
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${project_dir}/scripts/lib/deployment-environment.sh"
export DEPLOY_ENV=production
battlefield_env_preflight
battlefield_env_verify_docker
destination="$(dirname -- "${BATTLEFIELD_ENV_FILE}")/kubelet-ca.crt"
scratch="$(mktemp -d)"
trap 'rm -rf -- "${scratch}"' EXIT
kind get nodes --name "${KIND_CLUSTER_NAME}" >"${scratch}/nodes"
test -s "${scratch}/nodes"
while IFS= read -r node; do
  [[ "${node}" =~ ^[a-z0-9][a-z0-9.-]*$ ]] || exit 1
  docker exec "${node}" cat /var/lib/kubelet/pki/kubelet.crt >"${scratch}/certificate"
  if grep -q 'PRIVATE KEY' "${scratch}/certificate"; then
    echo 'Unexpected private key in kubelet certificate file; refusing export.' >&2
    exit 1
  fi
  openssl x509 -in "${scratch}/certificate" -checkend 604800 -noout >/dev/null
  cat "${scratch}/certificate" >>"${scratch}/bundle"
done <"${scratch}/nodes"
# Parsing validates that the collected files form a usable trust bundle.
python3 - "${scratch}/bundle" <<'PY'
import ssl, sys
ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT).load_verify_locations(cafile=sys.argv[1])
PY
install -m 600 "${scratch}/bundle" "${destination}"
echo 'Exported trusted public kubelet serving certificates. Set BF_KUBELET_CA_FILE=kubelet-ca.crt in production deployment.env.'
echo 'Re-export after serving certificate rotation or adding nodes. Node IP SAN verification remains required.'
