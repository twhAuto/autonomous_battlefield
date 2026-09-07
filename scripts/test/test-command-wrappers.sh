#!/usr/bin/env bash
# Checks compatibility launchers forward arguments, working directories, and exit statuses
# to their canonical scripts.

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
test_dir="$(mktemp -d "${TMPDIR:-/tmp}/battlefield-wrapper-test.XXXXXX")"
cleanup() { rm -rf -- "${test_dir}"; }
trap cleanup EXIT

wrappers=(run-dev.sh run-k8s-cli-agent.sh deploy-local.sh)
for name in test-backend test-go-coverage test-go-race with-test-postgres test-ai-evals fuzz-smoke test-deployment-components test-kind-deployment; do
  wrappers+=("scripts/ci/${name}.sh")
done

for wrapper in "${wrappers[@]}"; do
  case "${wrapper}" in
    run-*) target="scripts/dev/${wrapper}" ;;
    deploy-local.sh) target="scripts/cd/${wrapper}" ;;
    scripts/ci/*) target="scripts/test/${wrapper##*/}" ;;
  esac
  [[ -x "${project_dir}/${wrapper}" && -x "${project_dir}/${target}" ]]
  mkdir -p "${test_dir}/$(dirname "${wrapper}")" "${test_dir}/$(dirname "${target}")"
  cp "${project_dir}/${wrapper}" "${test_dir}/${wrapper}"
  cat >"${test_dir}/${target}" <<'STUB'
#!/usr/bin/env bash
set -Eeuo pipefail
[[ "${WRAPPER_TEST_VALUE}" == 'inherited value' ]]
[[ "$#" == 3 && "$1" == 'argument with spaces' && "$2" == '' && "$3" == '--flag=*' ]]
[[ "$PWD" == "${WRAPPER_TEST_CWD}" ]]
if [[ "${WRAPPER_TEST_SIGNAL:-0}" == 1 ]]; then
  trap 'exit 42' TERM
  printf '%s\n' "$$" >"${WRAPPER_TEST_READY}"
  while :; do sleep 0.05; done
fi
exit 23
STUB
  chmod +x "${test_dir}/${target}"
  export WRAPPER_TEST_VALUE='inherited value' WRAPPER_TEST_CWD="${test_dir}"
  status=0
  (cd "${test_dir}" && "./${wrapper}" 'argument with spaces' '' '--flag=*') || status=$?
  [[ "${status}" == 23 ]] || { echo "Argument/environment/exit forwarding failed: ${wrapper}" >&2; exit 1; }

  export WRAPPER_TEST_SIGNAL=1 WRAPPER_TEST_READY="${test_dir}/ready"
  rm -f "${WRAPPER_TEST_READY}"
  (cd "${test_dir}" && exec "./${wrapper}" 'argument with spaces' '' '--flag=*') &
  wrapper_pid=$!
  for _ in {1..100}; do
    [[ -s "${WRAPPER_TEST_READY}" ]] && break
    sleep 0.05
  done
  if [[ ! -s "${WRAPPER_TEST_READY}" ]] || [[ "$(cat "${WRAPPER_TEST_READY}")" != "${wrapper_pid}" ]]; then
    kill -TERM "${wrapper_pid}" 2>/dev/null || true
    wait "${wrapper_pid}" 2>/dev/null || true
    echo "Wrapper did not exec its target: ${wrapper}" >&2
    exit 1
  fi
  kill -TERM "${wrapper_pid}"
  status=0
  wait "${wrapper_pid}" || status=$?
  [[ "${status}" == 42 ]] || { echo "Signal forwarding failed: ${wrapper}" >&2; exit 1; }
  unset WRAPPER_TEST_SIGNAL WRAPPER_TEST_READY
done

echo "Command wrapper argument, environment, working directory, exit, and signal checks passed."
