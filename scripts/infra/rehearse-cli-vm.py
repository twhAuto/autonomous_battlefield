#!/usr/bin/env python3
"""Exercise CLI VM provisioning/adoption in a disposable nested Ubuntu hypervisor."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[2]
sys.path = [entry for entry in sys.path if Path(entry).resolve() != Path(__file__).resolve().parent]
spec = importlib.util.spec_from_file_location('rehearsal', ROOT / 'scripts/infra/rehearse-server.py')
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)


def inventory():
    return {kind: {name: base.virsh(command, name) for name in base.virsh(listing, '--all', '--name').splitlines() if name}
            for kind, command, listing in [('domains', 'domuuid', 'list'), ('networks', 'net-uuid', 'net-list'), ('pools', 'pool-uuid', 'pool-list')]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', type=Path, required=True, help='Pinned Ubuntu 26.04 outer hypervisor image')
    parser.add_argument('--evidence-dir', type=Path, required=True)
    args = parser.parse_args()
    os.umask(0o077)
    if args.evidence_dir.resolve().is_relative_to(ROOT):
        raise ValueError('Rehearsal evidence must be outside the checkout')
    memory = dict(line.split(':', 1) for line in Path('/proc/meminfo').read_text().splitlines())
    if int(memory['MemAvailable'].split()[0]) < 10 * 1024 * 1024:
        raise ValueError('Need 10 GiB available RAM for the 8 GiB disposable hypervisor and host headroom')
    if shutil.disk_usage('/var/lib/libvirt/images').free < 60 * 1024**3:
        raise ValueError('Need 60 GiB free for the disposable CLI VM rehearsal')
    pins = json.loads((ROOT / 'infra/ansible/rehearsal-image.json').read_text())
    base.checked_image(args.image, pins['26.04']['sha256'])
    before = inventory()
    args.evidence_dir.mkdir(mode=0o700)
    result = {'passed': False, 'source_sha256': {}, 'outer_image': pins['26.04'], 'inner_image': pins['24.04']}
    with tempfile.TemporaryDirectory(prefix='bf-cli-vm-rehearsal-') as temporary:
        work = Path(temporary)
        guest = base.Rehearsal(work, args.evidence_dir, 'default', memory_mib=8192, root_gib=48, cpus=4)
        result['disposable_uuid'] = guest.identity
        try:
            print('Preparing the disposable CLI VM hypervisor', flush=True)
            guest.prepare(args.image)
            with tarfile.open(work / 'source.tar', 'w') as archive:
                for name in ('infra/ansible', 'infra/deploy/agent-vm', 'scripts/infra', 'scripts/ops', 'scripts/test/rehearse-cli-vm-guest.py'):
                    archive.add(ROOT / name, arcname=name, filter=lambda item: None if '__pycache__' in item.name else item)
                    path = ROOT / name
                    for source in ([path] if path.is_file() else path.rglob('*')):
                        if source.is_file() and '__pycache__' not in str(source):
                            result['source_sha256'][str(source.relative_to(ROOT))] = base.digest(source)
            guest.ssh('mkdir -m 700 source && tar -xf - -C source', input=(work / 'source.tar').read_bytes(), text=False)
            cached = Path.home() / '.cache/autonomous-battlefield/rehearsal' / (pins['24.04']['sha256'] + '.img')
            if cached.exists():
                base.checked_image(cached, pins['24.04']['sha256'])
                guest.ssh('umask 077; mkdir -p .cache/autonomous-battlefield/rehearsal')
                with cached.open('rb') as stream:
                    guest.ssh('umask 077; cat > .cache/autonomous-battlefield/rehearsal/' + cached.name, stdin=stream, timeout=600)
            for phase in ('before-reboot', 'after-reboot'):
                if phase == 'after-reboot':
                    print('Rebooting only the disposable hypervisor', flush=True)
                    guest.reboot()
                print('Running CLI VM ' + phase + ' fixtures', flush=True)
                try:
                    output = guest.ssh('python3 source/scripts/test/rehearse-cli-vm-guest.py ' + phase + ' --identity ' + guest.identity, timeout=3600)
                    (args.evidence_dir / (phase + '.log')).write_text(output)
                except subprocess.CalledProcessError as error:
                    (args.evidence_dir / (phase + '.log')).write_text((error.stdout or '') + (error.stderr or ''))
                    raise
            result['guest'] = json.loads(guest.ssh('cat evidence/result.json'))
            if result['guest'].get('passed') is not True:
                raise ValueError('CLI VM rehearsal did not report success')
            result['passed'] = True
        finally:
            if guest.ip:
                try:
                    (args.evidence_dir / 'guest-evidence.tar.gz').write_bytes(guest.ssh('tar -czf - -C evidence .', text=False))
                except subprocess.SubprocessError:
                    result['evidence_capture_failed'] = True
            guest.cleanup()
            result['existing_resources_preserved'] = inventory() == before
            result['cleaned_up'] = True
            (args.evidence_dir / 'result.json').write_text(json.dumps(result, indent=2) + '\n')
            if not result['existing_resources_preserved']:
                raise ValueError('Existing hypervisor resource UUID inventory changed during rehearsal')
    print('PASS: CLI VM creation/adoption/reboot and disposable cleanup', flush=True)


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        print('CLI VM rehearsal: ' + (str(error) if isinstance(error, ValueError) else type(error).__name__), file=sys.stderr)
        sys.exit(1)
