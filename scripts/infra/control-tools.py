#!/usr/bin/env python3
"""Install hash-locked Ansible in a private venv, without system pip or sudo."""
import fcntl
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import urllib.request
import venv

ROOT = Path(__file__).resolve().parents[2]


def install():
    if not (3, 12) <= sys.version_info[:2] <= (3, 14):
        raise ValueError('Control Python must be 3.12–3.14 for the pinned Ansible release')
    requirements = ROOT / 'infra/ansible/requirements.lock'
    pip_lock = ROOT / 'infra/ansible/pip-bootstrap.json'
    digest = hashlib.sha256(requirements.read_bytes() + pip_lock.read_bytes()).hexdigest()[:16]
    cache = Path(os.environ.get('XDG_CACHE_HOME', Path.home() / '.cache')).absolute()
    directory = cache / 'autonomous-battlefield' / f'ansible-{digest}-py{sys.version_info.major}.{sys.version_info.minor}'
    directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    if directory.is_symlink() or directory.stat().st_uid != os.getuid() or directory.stat().st_mode & 0o022:
        raise ValueError('Ansible cache must be owned by this user and not writable by other users')
    with (directory / 'install.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        environment = directory / 'venv'
        marker = directory / 'ready'
        if not marker.exists():
            pin = json.loads(pip_lock.read_text())
            with urllib.request.urlopen(pin['url'], timeout=60) as response:
                wheel = response.read()
            if hashlib.sha256(wheel).hexdigest() != pin['sha256']:
                raise ValueError('pip bootstrap checksum mismatch')
            wheel_path = directory / 'pip.whl'
            wheel_path.write_bytes(wheel)
            # Ubuntu can have venv but no ensurepip; use the verified pip wheel directly.
            venv.EnvBuilder(with_pip=False).create(environment)
            env = {key: value for key, value in os.environ.items()
                   if not key.startswith(('PYTHON', 'PIP_'))}
            env['PYTHONPATH'] = str(wheel_path)
            subprocess.run([str(environment / 'bin/python'), '-m', 'pip', '--isolated', 'install',
                            '--disable-pip-version-check', '--require-hashes', '--only-binary=:all:',
                            '--index-url', 'https://pypi.org/simple', '-r', str(requirements)],
                           env=env, check=True, stdout=sys.stderr)
            marker.write_text(digest + '\n')
        return environment / 'bin'


if __name__ == '__main__':
    try:
        print(install())
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        print(f'Cannot prepare Ansible tools: {error}', file=sys.stderr)
        sys.exit(1)
