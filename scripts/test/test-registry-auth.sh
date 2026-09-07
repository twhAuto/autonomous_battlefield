#!/usr/bin/env bash

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
test_dir="$(mktemp -d "${TMPDIR:-/tmp}/battlefield-registry-test.XXXXXX")"
trap 'rm -rf -- "${test_dir}"' EXIT
mkdir -p "${test_dir}/bin" "${test_dir}/original config" "${test_dir}/tmp with spaces"
printf 'original credentials\n' >"${test_dir}/original config/config.json"
export REGISTRY_TEST_DIR="${test_dir}"
export DOCKER_CONFIG="${test_dir}/original config"
export GHCR_USER=test-user GHCR_TOKEN='dummy token with spaces and $literal'
export TMPDIR="${test_dir}/tmp with spaces"

cat >"${test_dir}/bin/docker" <<'STUB'
#!/usr/bin/env bash
set -Eeuo pipefail
[[ "${DOCKER_CONFIG}" != "${REGISTRY_TEST_DIR}/original config" ]]
[[ "$(stat -c '%a' "${DOCKER_CONFIG}")" == 700 ]]
printf '%s\n' "${DOCKER_CONFIG}" >"${REGISTRY_TEST_DIR}/last-config"
case "$1" in
  login)
    [[ "$#" == 5 && "$2" == ghcr.io && "$3" == --username && "$4" == test-user && "$5" == --password-stdin ]]
    [[ "$(cat)" == "${GHCR_TOKEN}" ]]
    printf 'temporary credentials\n' >"${DOCKER_CONFIG}/config.json"
    exit "${REGISTRY_TEST_LOGIN_STATUS:-0}"
    ;;
  logout)
    [[ "$#" == 2 && "$2" == ghcr.io ]]
    touch "${REGISTRY_TEST_DIR}/logout"
    ;;
  *) exit 99 ;;
esac
STUB
cat >"${test_dir}/bin/check-command" <<'STUB'
#!/usr/bin/env bash
set -Eeuo pipefail
[[ "$(cat "${DOCKER_CONFIG}/config.json")" == 'temporary credentials' ]]
[[ "${BUILDX_CONFIG}" == "${REGISTRY_TEST_BUILDX}" ]]
[[ "$PWD" == "${REGISTRY_TEST_CWD}" ]]
[[ "$#" == 3 && "$1" == 'argument with spaces' && "$2" == '' && "$3" == '--flag=*' ]]
read -r input
[[ "${input}" == 'command input' ]]
touch "${REGISTRY_TEST_DIR}/command-ran"
if [[ "${REGISTRY_TEST_WAIT:-0}" == 1 ]]; then
  printf '%s\n' "$$" >"${REGISTRY_TEST_DIR}/command-pid"
  exec sleep 30
fi
exit "${REGISTRY_TEST_COMMAND_STATUS:-0}"
STUB
chmod +x "${test_dir}/bin/"*
export PATH="${test_dir}/bin:${PATH}"
export REGISTRY_TEST_CWD="$PWD"
launcher="${project_dir}/scripts/ci/with-ghcr-auth.sh"
printf 'command input\n' >"${test_dir}/stdin"

assert_cleanup() {
  [[ ! -e "$(cat "${test_dir}/last-config")" ]]
  [[ -e "${test_dir}/logout" ]]
  [[ "$(cat "${test_dir}/original config/config.json")" == 'original credentials' ]]
}

for status in 0 23; do
  unset BUILDX_CONFIG
  export REGISTRY_TEST_BUILDX="${HOME}/.docker/buildx"
  if [[ "${status}" == 23 ]]; then
    export BUILDX_CONFIG="${test_dir}/custom builder"
    export REGISTRY_TEST_BUILDX="${BUILDX_CONFIG}"
  fi
  result=0
  REGISTRY_TEST_COMMAND_STATUS="${status}" "${launcher}" check-command \
    'argument with spaces' '' '--flag=*' <"${test_dir}/stdin" || result=$?
  [[ "${result}" == "${status}" && -e "${test_dir}/command-ran" ]]
  assert_cleanup
  rm "${test_dir}/command-ran" "${test_dir}/logout"
done

result=0
REGISTRY_TEST_LOGIN_STATUS=17 "${launcher}" check-command <"${test_dir}/stdin" || result=$?
[[ "${result}" == 17 && ! -e "${test_dir}/command-ran" ]]
assert_cleanup
rm "${test_dir}/logout"

REGISTRY_TEST_WAIT=1 "${launcher}" check-command \
  'argument with spaces' '' '--flag=*' <"${test_dir}/stdin" &
launcher_pid=$!
for _ in {1..100}; do
  [[ -s "${test_dir}/command-pid" ]] && break
  sleep 0.05
done
kill -TERM "${launcher_pid}"
result=0
wait "${launcher_pid}" || result=$?
[[ "${result}" == 143 ]]
assert_cleanup
if kill -0 "$(cat "${test_dir}/command-pid")" 2>/dev/null; then
  echo 'Registry wrapper left its command running after cancellation.' >&2
  exit 1
fi

echo 'Registry authentication isolation, forwarding, failure, and cancellation checks passed.'
