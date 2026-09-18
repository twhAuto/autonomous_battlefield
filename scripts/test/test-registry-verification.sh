#!/usr/bin/env bash
# Exercise the real verification/signing launchers against Cosign-format fixtures.
set -Eeuo pipefail
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
python3 - "${project_dir}" <<'PY'
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

root = Path(sys.argv[1])
revision = 'b' * 40
backend = 'ghcr.io/test/backend@sha256:' + 'a' * 64
frontend = 'ghcr.io/test/frontend@sha256:' + 'c' * 64
with tempfile.TemporaryDirectory(prefix='battlefield-verification-') as directory:
    tmp = Path(directory)
    project = tmp / 'project with spaces'
    for name in ('scripts/cd/verify-registry-images.sh', 'scripts/ci/attest-and-sign-images.sh',
                 'scripts/ci/tool-versions.env'):
        target = project / name
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy(root / name, target)
    generator = project / 'scripts/ci/generate-ai-manifest.sh'
    generator.write_text('#!/bin/sh\nexit 0\n')
    generator.chmod(0o755)
    bins = tmp / 'bin'
    bins.mkdir()
    docker = bins / 'docker'
    docker.write_text('''#!/usr/bin/env python3
import json, os, sys
args = sys.argv[1:]
with open(os.environ['TEST_CALLS'], 'a') as log:
    log.write(json.dumps(args) + '\\n')
if args[:3] == ['buildx', 'imagetools', 'inspect']:
    print('{"builder":{"id":"fixture"}}')
elif 'verify' in args:
    if os.environ.get('TEST_SIGNATURE_FAILURE') == '1':
        sys.exit(1)
    legacy = '--new-bundle-format=false' in args
    revision = os.environ.get('TEST_REVISION', 'b' * 40)
    if legacy and '--annotations' in args:
        if args[args.index('--annotations') + 1] != 'revision=' + revision:
            sys.exit(1)
    # Cosign 3.0.2 bundle summaries have null optional metadata, even when
    # the command accepts an annotation argument. The legacy path retains it.
    print(json.dumps([{'optional': {'revision': revision} if legacy else None}]))
elif 'verify-attestation' in args:
    if os.environ.get('TEST_ATTESTATION_FAILURE') == '1':
        sys.exit(1)
    print('{}')
else:
    print('{}')
''')
    docker.chmod(0o755)
    key = tmp / 'key.pem'
    key.write_text('fixture public key')
    images = tmp / 'images.env'
    images.write_text(f'IMAGE_TAG={revision}\nBACKEND_IMAGE_REF={backend}\nFRONTEND_IMAGE_REF={frontend}\n')
    log = tmp / 'calls'
    env = {k: v for k, v in os.environ.items() if not k.startswith(('TEST_', 'BACKEND_', 'FRONTEND_', 'VERIFY_'))}
    env.update(PATH=str(bins) + ':' + os.environ['PATH'], TEST_CALLS=str(log),
               COSIGN_PUBLIC_KEY_FILE=str(key), COSIGN_KEY_FILE=str(key),
               BUILD_IMAGES_FILE=str(images), DOCKER_CONFIG=str(tmp))

    def run(script, args=(), success=True, **settings):
        log.write_text('')
        result = subprocess.run(['bash', str(project / script), *args],
                                env=env | settings, capture_output=True, text=True)
        assert (result.returncode == 0) == success, (result.stdout, result.stderr)
        return [json.loads(line) for line in log.read_text().splitlines()]

    verify = 'scripts/cd/verify-registry-images.sh'
    for settings in ({}, {'BACKEND_IMAGE_REVISION': revision, 'FRONTEND_IMAGE_REVISION': revision}):
        calls = run(verify, (backend, frontend), **settings)
        signatures = [call for call in calls if 'verify' in call]
        assert len(signatures) == 2
        assert all('--new-bundle-format=false' in call for call in signatures)
        attestations = [call for call in calls if 'verify-attestation' in call]
        assert len(attestations) == 6
        assert all('--new-bundle-format=false' not in call for call in attestations)
        for component in ('backend', 'frontend'):
            report = project / f'artifacts/deployment-verification/{component}-signature.json'
            assert json.loads(report.read_text())[0]['optional']['revision'] == revision

    calls = run(verify, (backend, frontend), success=False, TEST_SIGNATURE_FAILURE='1')
    assert len(calls) == 1, 'signature failure must stop before attestations or frontend'
    for value in ('', 'short', 'x' * 40):
        calls = run(verify, (backend,), success=False, TEST_REVISION=value)
        assert not any('verify-attestation' in call for call in calls)
    run(verify, (backend,), success=False, BACKEND_IMAGE_REVISION='d' * 40)
    assert not run(verify, (backend,), success=False, BACKEND_IMAGE_REVISION='short')
    run(verify, (backend,), success=False, TEST_ATTESTATION_FAILURE='1')
    assert not run(verify, ('ghcr.io/test/backend:latest',), success=False)

    calls = run('scripts/ci/attest-and-sign-images.sh')
    signatures = [call for call in calls if 'verify' in call]
    assert len(signatures) == 2
    assert all('--new-bundle-format=false' in call and 'revision=' + revision in call for call in signatures)
    signs = [call for call in calls if 'sign' in call]
    assert len(signs) == 4
    assert sum('--new-bundle-format=false' in call for call in signs) == 2
    assert len([call for call in calls if 'verify-attestation' in call]) == 6
    run('scripts/ci/attest-and-sign-images.sh', success=False, TEST_REVISION='d' * 40)
    print('Registry revision verification and publication regression checks passed.')
PY
