#!/usr/bin/env bash
# Runs the Playwright suite on a free loopback port so concurrent CI builds on one
# agent do not collide. Choosing a port and binding it are separate steps, so
# another process can take the port in between; only that bind failure is retried,
# with a new port, up to E2E_PORT_ATTEMPTS times.
#
# Run from frontend/: ../scripts/test/run-e2e.sh [playwright arguments]

set -Eeuo pipefail

attempts="${E2E_PORT_ATTEMPTS:-3}"
log="$(mktemp "${TMPDIR:-/tmp}/battlefield-e2e.XXXXXX")"
trap 'rm -f -- "${log}"' EXIT

free_port() {
  node -e "const s=require('net').createServer();s.listen(0,'127.0.0.1',()=>{console.log(s.address().port);s.close()})"
}

for ((attempt = 1; attempt <= attempts; attempt++)); do
  E2E_PORT="$(free_port)"
  export E2E_PORT
  status=0
  npm run test:e2e -- "$@" 2>&1 | tee "${log}" || status=$?
  if ((status == 0)); then
    exit 0
  fi
  if ! grep -q 'is already used' "${log}" || ((attempt == attempts)); then
    exit "${status}"
  fi
  echo "E2E port ${E2E_PORT} was taken before the dev server bound it; retrying on a new port (${attempt}/${attempts})." >&2
done
