#!/usr/bin/env bash
# Only sanitized VM reports enter Jenkins artifacts. State, plans and keys stay private.
set -Eeuo pipefail
set +x
umask 077
: "${PLATFORM_PRIVATE:?}" "${PLATFORM_ACTION:?}" "${PLATFORM_REVISION:?}"
: "${PLATFORM_INVENTORY:?}" "${PLATFORM_VAULT_PASSWORD:?}" "${PLATFORM_SUDO_PASSWORD:?}"
case "$PLATFORM_ACTION" in plan|apply|verify) ;; *) exit 2 ;; esac
extra=()
case "${VM_REPLACE_OS:-false}" in true) extra+=(--replace-os) ;; false) ;; *) exit 2 ;; esac
chmod 600 "$PLATFORM_INVENTORY" "$PLATFORM_VAULT_PASSWORD" "$PLATFORM_SUDO_PASSWORD"
python3 scripts/infra/vm.py "$PLATFORM_ACTION" \
  --revision "$PLATFORM_REVISION" --inventory "$PLATFORM_INVENTORY" \
  --vault-password-file "$PLATFORM_VAULT_PASSWORD" --become-password-file "$PLATFORM_SUDO_PASSWORD" \
  --receipt "$PLATFORM_PRIVATE/receipt.json" --report "$PLATFORM_PRIVATE/$PLATFORM_ACTION.json" "${extra[@]}"
mkdir -p artifacts
cp -- "$PLATFORM_PRIVATE/$PLATFORM_ACTION.json" "artifacts/vm-$PLATFORM_ACTION.json"
