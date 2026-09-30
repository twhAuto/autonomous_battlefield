#!/usr/bin/env python3
"""Checksum-pinned Terraform and cloud image, cached outside the checkout."""
from contextlib import contextmanager
import hashlib
import json
import os
from pathlib import Path
import shutil
import stat
import tempfile
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parents[2]


def digest(path):
    with Path(path).open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def private_directory(path):
    path = Path(path).absolute()
    for parent in [*reversed(path.parents), path]:
        if parent.is_symlink():
            raise ValueError('Private directories must not traverse symlinks')
    path.mkdir(parents=True, exist_ok=True, mode=0o700)
    info = path.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise ValueError('VM working directories must be private and owned by the operator')
    return path


def atomic(path, content):
    path = Path(path)
    if path.is_symlink():
        raise ValueError('Refusing to replace a symlink')
    fd, name = tempfile.mkstemp(dir=path.parent, prefix='.vm-')
    try:
        with os.fdopen(fd, 'wb') as stream:
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(name, path)
        fd = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def download(directory, pin, suffix):
    path = directory / (pin['sha256'] + suffix)
    if path.is_symlink():
        raise ValueError('Refusing a symlink in the tool cache')
    if not path.exists():
        fd, temporary = tempfile.mkstemp(dir=directory)
        try:
            with os.fdopen(fd, 'wb') as stream, urllib.request.urlopen(pin['url'], timeout=60) as response:
                shutil.copyfileobj(response, stream)
            if digest(temporary) != pin['sha256']:
                raise ValueError('Downloaded tool/image checksum mismatch')
            os.replace(temporary, path)
        finally:
            if os.path.exists(temporary):
                os.unlink(temporary)
    if digest(path) != pin['sha256']:
        raise ValueError('Cached tool/image checksum mismatch')
    return path


@contextmanager
def terraform_environment(environment):
    # go-plugin uses a UNIX socket in TMPDIR. Jenkins workspace temp paths can
    # exceed sockaddr_un's limit even when the provider binary itself is valid.
    # Only process IPC uses this short, private directory; state stays canonical.
    with tempfile.TemporaryDirectory(prefix='bf-tf-ipc-', dir='/tmp') as directory:
        yield dict(environment, TMPDIR=directory)


def terraform(cache):
    pins = json.loads((ROOT / 'infra/libvirt/tools.json').read_text())
    cache = private_directory(cache)
    archive = download(cache, pins['terraform'], '.zip')
    binary = cache / 'terraform'
    with zipfile.ZipFile(archive) as bundle:
        data = bundle.read('terraform')
    if binary.is_symlink():
        raise ValueError('Refusing a symlink for Terraform')
    if not binary.exists() or binary.read_bytes() != data:
        atomic(binary, data)
    # Repair an interrupted atomic-write/chmod sequence on every invocation.
    binary.chmod(0o700)
    return binary


def load_pins():
    pins = json.loads((ROOT / 'infra/libvirt/tools.json').read_text())
    images = json.loads((ROOT / 'infra/ansible/rehearsal-image.json').read_text())
    pins['image'] = images[pins.pop('image_release')]
    return pins


def prepare(directory):
    pins = load_pins()
    cache = private_directory(directory / 'cache')
    binary = terraform(cache)
    image = download(cache, pins['image'], '.qcow2')
    return binary, image, pins
