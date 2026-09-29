#!/usr/bin/env bash
# Checks that run-e2e.sh passes a fresh port each attempt, retries only a port-bind
# failure, and preserves other failures and arguments.

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
test_dir="$(mktemp -d "${TMPDIR:-/tmp}/battlefield-e2e-runner-test.XXXXXX")"
cleanup() { rm -rf -- "${test_dir}"; }
trap cleanup EXIT

mkdir -p "${test_dir}/bin"
# Stub npm: records each call's port and arguments, then fails as scripted.
cat >"${test_dir}/bin/npm" <<'STUB'
#!/usr/bin/env bash
set -Eeuo pipefail
printf '%s %s\n' "${E2E_PORT}" "$*" >>"${NPM_CALLS}"
calls="$(wc -l <"${NPM_CALLS}")"
case "${NPM_SCRIPT:-}" in
  bind-once) ((calls > 1)) || { echo "Error: http://127.0.0.1:${E2E_PORT} is already used"; exit 1; } ;;
  bind-always) echo "Error: http://127.0.0.1:${E2E_PORT} is already used"; exit 1 ;;
  test-failure) echo "1 failed"; exit 7 ;;
esac
STUB
chmod +x "${test_dir}/bin/npm"

run() {
  : >"${test_dir}/calls"
  (cd "${test_dir}" && PATH="${test_dir}/bin:${PATH}" NPM_CALLS="${test_dir}/calls" NPM_SCRIPT="$1" \
    "${project_dir}/scripts/test/run-e2e.sh" --grep 'two words' >/dev/null 2>&1)
}
calls() { wc -l <"${test_dir}/calls"; }

run success
[[ "$(calls)" == 1 ]] || { echo "success should run once" >&2; exit 1; }
grep -Eq '^[0-9]+ run test:e2e -- --grep two words$' "${test_dir}/calls" || { echo "port or arguments not passed: $(cat "${test_dir}/calls")" >&2; exit 1; }

run bind-once
[[ "$(calls)" == 2 ]] || { echo "a bind failure should retry once" >&2; exit 1; }

status=0; run bind-always || status=$?
[[ "${status}" == 1 && "$(calls)" == 3 ]] || { echo "bind retries must stop at three (status ${status}, calls $(calls))" >&2; exit 1; }

status=0; run test-failure || status=$?
[[ "${status}" == 7 && "$(calls)" == 1 ]] || { echo "a test failure must not retry (status ${status}, calls $(calls))" >&2; exit 1; }

echo "e2e runner tests passed"
