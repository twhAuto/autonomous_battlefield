#!/usr/bin/env python3
"""Test AB-126 inside a disposable nested-libvirt host; production resources are untouched."""
import argparse
import importlib.util
import json
import os
import shutil
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('rehearsal', ROOT / 'scripts/infra/rehearse-server.py')
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)


def require_guest_success(value):
    if not isinstance(value, dict) or value.get('passed') is not True:
        raise ValueError('Guest rehearsal did not report success')
    return value


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', type=Path, required=True, help='Pinned Ubuntu 26.04 image for the disposable hypervisor')
    parser.add_argument('--evidence-dir', type=Path, required=True)
    args = parser.parse_args()
    os.umask(0o077)
    if args.evidence_dir.resolve().is_relative_to(ROOT):
        raise ValueError('Evidence must be outside the checkout')
    memory = dict(line.split(':', 1) for line in Path('/proc/meminfo').read_text().splitlines())
    if int(memory['MemAvailable'].split()[0]) < 8 * 1024 * 1024:
        raise ValueError('Need at least 8 GiB available RAM for the disposable nested rehearsal')
    if shutil.disk_usage('/var/lib/libvirt/images').free < 110 * 1024**3:
        raise ValueError('Need at least 110 GiB available in existing libvirt storage for the rehearsal')
    before_domains = {name: base.virsh('domuuid', name) for name in base.virsh('list', '--all', '--name').splitlines() if name}
    before_networks = {name: base.virsh('net-uuid', name) for name in base.virsh('net-list', '--all', '--name').splitlines() if name}
    args.evidence_dir.mkdir(mode=0o700)
    pins = json.loads((ROOT / 'infra/ansible/rehearsal-image.json').read_text())['26.04']
    base.checked_image(args.image, pins['sha256'])
    with tempfile.TemporaryDirectory(prefix='bf-vm-rehearsal-') as tmp:
        work = Path(tmp)
        guest = base.Rehearsal(work, args.evidence_dir, 'default', memory_mib=6144, root_gib=48)
        result = {'passed': False, 'cleaned_up': False, 'name': guest.name, 'identity': guest.identity,
                  'source_sha256': {}}
        for directory in ('infra/libvirt', 'infra/ansible', 'scripts/infra', 'scripts/test'):
            for source in (ROOT / directory).rglob('*'):
                if source.is_file() and '__pycache__' not in source.parts:
                    result['source_sha256'][str(source.relative_to(ROOT))] = base.digest(source)
        try:
            print('Preparing disposable nested hypervisor ' + guest.name, flush=True)
            guest.prepare(args.image)
            # Additional empty disk is scoped to the generated outer pool/UUID.
            base.virsh('vol-create-as', guest.identity, 'data.qcow2', '48G', '--format', 'qcow2', '--allocation', '0')
            guest.volumes.append('data.qcow2')
            base.virsh('attach-disk', guest.identity, guest.pool_path + '/data.qcow2', 'vdb',
                       '--subdriver', 'qcow2', '--targetbus', 'virtio', '--live', '--config')
            with tarfile.open(work / 'source.tar', 'w') as archive:
                for path in ('infra/ansible', 'infra/libvirt', 'scripts/infra', 'scripts/test/rehearse-vm-guest.py'):
                    archive.add(ROOT / path, arcname=path, filter=lambda item: None if '__pycache__' in item.name else item)
            guest.ssh('mkdir -m 700 source && tar -xf - -C source', input=(work / 'source.tar').read_bytes(), text=False)
            release = json.loads((ROOT / 'infra/libvirt/tools.json').read_text())['image_release']
            image_pin = json.loads((ROOT / 'infra/ansible/rehearsal-image.json').read_text())[release]
            cached_image = Path.home() / '.cache/autonomous-battlefield/rehearsal' / (image_pin['sha256'] + '.img')
            if cached_image.is_file():
                base.checked_image(cached_image, image_pin['sha256'])
                target = '.local/state/battlefield/k3s/cache/' + image_pin['sha256'] + '.qcow2'
                guest.ssh('umask 077; mkdir -p .local/state/battlefield/k3s/cache')
                with cached_image.open('rb') as stream:
                    guest.ssh('cat > ' + target, stdin=stream, timeout=600)
            print('Running actual Terraform apply, convergence, replacement and state recovery checks.', flush=True)
            guest.ssh('python3 source/scripts/test/rehearse-vm-guest.py before-reboot', timeout=3600)
            print('Rebooting only the disposable hypervisor to verify VM/network autostart.', flush=True)
            guest.reboot()
            guest.ssh('python3 source/scripts/test/rehearse-vm-guest.py after-reboot', timeout=900)
            result['guest'] = json.loads(guest.ssh('cat evidence/result.json'))
            require_guest_success(result['guest'])
            result['passed'] = result['guest']['passed']
        finally:
            try:
                if guest.ip:
                    content = guest.ssh('tar -czf - -C evidence .', text=False)
                    (args.evidence_dir / 'guest-evidence.tar.gz').write_bytes(content)
            except subprocess.SubprocessError:
                pass
            try:
                guest.cleanup()
                result['cleaned_up'] = True
                after_domains = {name: base.virsh('domuuid', name) for name in base.virsh('list', '--all', '--name').splitlines() if name}
                after_networks = {name: base.virsh('net-uuid', name) for name in base.virsh('net-list', '--all', '--name').splitlines() if name}
                result['existing_resources_preserved'] = before_domains == after_domains and before_networks == after_networks
                if not result['existing_resources_preserved']:
                    raise ValueError('Existing hypervisor resource inventory changed during the rehearsal')
            finally:
                (args.evidence_dir / 'result.json').write_text(json.dumps(result, indent=2) + '\n')
        print('PASS: disposable Terraform VM rehearsal and cleanup', flush=True)


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        print('VM rehearsal: ' + (str(error) if isinstance(error, ValueError) else type(error).__name__), file=sys.stderr)
        sys.exit(1)
