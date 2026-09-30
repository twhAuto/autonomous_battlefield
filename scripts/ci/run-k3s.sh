#!/usr/bin/env bash
# Credentials, kubeconfig and raw plans never enter Jenkins artifacts.
set -Eeuo pipefail
set +x
umask 077
: "${PLATFORM_PRIVATE:?}" "${PLATFORM_ACTION:?}" "${PLATFORM_REVISION:?}"
: "${PLATFORM_INVENTORY:?}" "${PLATFORM_VAULT_PASSWORD:?}" "${PLATFORM_SUDO_PASSWORD:?}"
: "${GHCR_USER:?}" "${GHCR_TOKEN:?}"
case "$PLATFORM_ACTION" in plan|apply|verify) ;; *) exit 2 ;; esac
chmod 600 "$PLATFORM_INVENTORY" "$PLATFORM_VAULT_PASSWORD" "$PLATFORM_SUDO_PASSWORD"
registry_file="$PLATFORM_PRIVATE/registry.json"
trap 'rm -f -- "$registry_file"' EXIT
python3 - <<'PY'
import json, os, sys
from pathlib import Path
sys.path.insert(0, 'scripts/infra')
import k3s
record = k3s.bootstrap.private_file(Path.home() / '.config/autonomous-battlefield/production/recovery.json')
value = k3s.config.registry({'username': os.environ['GHCR_USER'], 'password': os.environ['GHCR_TOKEN'],
                           'probe_image': json.loads(record.read_text())['backend']})
k3s.tools.atomic(Path(os.environ['PLATFORM_PRIVATE']) / 'registry.json', k3s.vm.json_bytes(value))
PY
python3 scripts/infra/k3s.py "$PLATFORM_ACTION" \
  --revision "$PLATFORM_REVISION" --inventory "$PLATFORM_INVENTORY" \
  --vault-password-file "$PLATFORM_VAULT_PASSWORD" --become-password-file "$PLATFORM_SUDO_PASSWORD" \
  --registry-file "$registry_file" --receipt "$PLATFORM_PRIVATE/receipt.json" \
  --report "$PLATFORM_PRIVATE/$PLATFORM_ACTION.json"
mkdir -p artifacts
cp -- "$PLATFORM_PRIVATE/$PLATFORM_ACTION.json" "artifacts/k3s-$PLATFORM_ACTION.json"
