#!/usr/bin/env bash
# Build the real frontend from a private checkout and exercise its runtime entrypoint.
set -Eeuo pipefail
export DOCKER_BUILDKIT=1
command -v docker >/dev/null || { echo "Docker is required for this check." >&2; exit 1; }
docker buildx version >/dev/null 2>&1 || { echo "Docker BuildKit is required for this check." >&2; exit 1; }
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
test_dir="$(mktemp -d "${TMPDIR:-/tmp}/battlefield-frontend-image.XXXXXX")"
image="battlefield-frontend-permissions:$(basename "${test_dir}" | tr '[:upper:]' '[:lower:]')"
broken_image="${image}-unreadable"
permissive_image="${image}-permissive"
cleanup() {
  docker image rm "${permissive_image}" >/dev/null 2>&1 || true
  docker image rm "${broken_image}" >/dev/null 2>&1 || true
  docker image rm "${image}" >/dev/null 2>&1 || true
  rm -rf -- "${test_dir}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# Copy tracked worktree files, including uncommitted edits, without local secrets or
# node_modules. Force the same private modes as an agent checkout with UMask=0077.
python3 - "${project_dir}" "${test_dir}/context" <<'CONTEXT'
from pathlib import Path
import shutil, subprocess, sys
root, context = map(Path, sys.argv[1:])
files = subprocess.check_output(['git', '-C', str(root), 'ls-files', '-z', 'frontend/']).split(b'\0')
for name in filter(None, files):
    source = root / name.decode()
    if source.is_symlink():
        raise SystemExit('Frontend context must not contain symlinks')
    target = context / source.relative_to(root / 'frontend')
    target.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source, target)
    target.chmod(0o600)
for path in [context, *context.rglob('*')]:
    if path.is_dir():
        path.chmod(0o700)
CONTEXT

docker build --builder default --tag "${image}" "${test_dir}/context"
python3 "${project_dir}/scripts/ci/verify-frontend-image.py" "${image}"

# A successful Nginx build is insufficient: restore the observed root-owned 0600
# template defect and prove the same gate rejects its startup, without any deploy.
mkdir "${test_dir}/broken"
cat >"${test_dir}/broken/Dockerfile" <<'BROKEN'
ARG BASE=scratch
FROM ${BASE}
USER root
RUN chown 0:0 /etc/nginx/templates/default.conf.template && chmod 0600 /etc/nginx/templates/default.conf.template
USER 101:101
BROKEN
docker build --builder default --build-arg "BASE=${image}" --tag "${broken_image}" "${test_dir}/broken"
if python3 "${project_dir}/scripts/ci/verify-frontend-image.py" "${broken_image}" >"${test_dir}/broken.log" 2>&1; then
  echo 'Unreadable frontend template unexpectedly passed the runtime gate.' >&2
  exit 1
fi
grep -Fq 'Permission denied' "${test_dir}/broken.log" || { cat "${test_dir}/broken.log" >&2; exit 1; }

# The gate must also reject a Content-Security-Policy that stops restricting scripts.
mkdir "${test_dir}/permissive"
cat >"${test_dir}/permissive/Dockerfile" <<'PERMISSIVE'
ARG BASE=scratch
FROM ${BASE}
USER root
RUN sed -i "s/script-src 'self'; //" /etc/nginx/templates/default.conf.template
USER 101:101
PERMISSIVE
docker build --builder default --build-arg "BASE=${image}" --tag "${permissive_image}" "${test_dir}/permissive"
if python3 "${project_dir}/scripts/ci/verify-frontend-image.py" "${permissive_image}" >"${test_dir}/permissive.log" 2>&1; then
  echo 'Frontend policy without script-src unexpectedly passed the runtime gate.' >&2
  exit 1
fi
grep -Fq 'Unexpected Content-Security-Policy' "${test_dir}/permissive.log" || { cat "${test_dir}/permissive.log" >&2; exit 1; }
echo 'Private-checkout frontend startup, unreadable-template and permissive-policy rejection passed.'
