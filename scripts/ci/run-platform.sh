#!/usr/bin/env bash
# Jenkins credentials remain outside the checkout; only callback summaries are archived.
set -Eeuo pipefail
set +x
umask 077
: "${PLATFORM_PRIVATE:?}" "${PLATFORM_ACTION:?}" "${PLATFORM_REVISION:?}"
: "${PLATFORM_INVENTORY:?}" "${PLATFORM_VAULT:?}" "${PLATFORM_VAULT_PASSWORD:?}" "${PLATFORM_SUDO_PASSWORD:?}"
case "$PLATFORM_ACTION" in plan|apply|verify) ;; *) exit 2 ;; esac
chmod 600 "$PLATFORM_INVENTORY" "$PLATFORM_VAULT" "$PLATFORM_VAULT_PASSWORD" "$PLATFORM_SUDO_PASSWORD"
python3 scripts/infra/platform.py "$PLATFORM_ACTION" \
  --revision "$PLATFORM_REVISION" \
  --inventory "$PLATFORM_INVENTORY" --vault "$PLATFORM_VAULT" \
  --vault-password-file "$PLATFORM_VAULT_PASSWORD" --become-password-file "$PLATFORM_SUDO_PASSWORD" \
  --receipt "$PLATFORM_PRIVATE/receipt.json" --report "$PLATFORM_PRIVATE/$PLATFORM_ACTION.json"
mkdir -p artifacts
cp -- "$PLATFORM_PRIVATE/$PLATFORM_ACTION.json" "artifacts/platform-$PLATFORM_ACTION.json"
