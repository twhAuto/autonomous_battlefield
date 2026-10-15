#!/usr/bin/env bash
# Only the secret-free CLI worker report enters Jenkins artifacts; receipts stay private.
set -Eeuo pipefail
set +x
umask 077
: "${PLATFORM_PRIVATE:?}" "${PLATFORM_ACTION:?}"
case "$PLATFORM_ACTION" in
  verify) args=() ;;
  plan) args=(--revision "${PLATFORM_REVISION:?}" --receipt "$PLATFORM_PRIVATE/receipt.json") ;;
  apply)
    : "${PLATFORM_SUDO_PASSWORD:?}"
    chmod 600 "$PLATFORM_SUDO_PASSWORD"
    args=(--revision "${PLATFORM_REVISION:?}" --receipt "$PLATFORM_PRIVATE/receipt.json" --become-password-file "$PLATFORM_SUDO_PASSWORD") ;;
  *) exit 2 ;;
esac
mkdir -p artifacts
status=0
python3 scripts/infra/cli-worker.py "$PLATFORM_ACTION" --report "$PLATFORM_PRIVATE/$PLATFORM_ACTION.json" "${args[@]}" || status=$?
if [[ -f "$PLATFORM_PRIVATE/$PLATFORM_ACTION.json" ]]; then
  cp -- "$PLATFORM_PRIVATE/$PLATFORM_ACTION.json" "artifacts/cli-$PLATFORM_ACTION.json"
fi
exit "$status"
