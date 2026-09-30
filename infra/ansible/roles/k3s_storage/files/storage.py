#!/usr/bin/env python3
"""Own exactly the Terraform-retained guest disk; never adopt unrelated filesystems."""
import argparse
import base64
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import tempfile
import uuid

DEVICE = Path('/dev/disk/by-id/virtio-battlefield-k3s-data')
MOUNT = Path('/srv/battlefield')
OWNER = Path('/etc/battlefield-k3s/storage.json')
MARKER = '.battlefield-storage.json'
FSTAB = Path('/etc/fstab')


def run(*args, allowed=(0,), timeout=60):
    result = subprocess.run(args, capture_output=True, text=True, timeout=timeout, check=False)
    if result.returncode not in allowed:
        raise ValueError(Path(args[0]).name + ' failed during guest storage validation')
    return result.stdout.strip()


def plain(path, directory=False):
    for parent in [path, *path.parents]:
        if parent.is_symlink():
            raise ValueError('Storage paths must not traverse symlinks')
    if path.exists():
        info = path.stat()
        if info.st_uid != 0 or info.st_mode & 0o022 or (directory and not stat.S_ISDIR(info.st_mode)) or (not directory and not stat.S_ISREG(info.st_mode)):
            raise ValueError('Storage ownership path has an unexpected type or owner')


def atomic(path, data, mode=0o600):
    plain(path)
    plain(path.parent, directory=True)
    fd, temporary = tempfile.mkstemp(dir=path.parent, prefix='.k3s-')
    try:
        with os.fdopen(fd, 'wb') as output:
            output.write(data)
            output.flush()
            os.fsync(output.fileno())
        os.chmod(temporary, mode)
        os.replace(temporary, path)
        fd = os.open(path.parent, os.O_DIRECTORY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def identity(contract):
    value = contract.get('storage_id', '')
    if not isinstance(value, str) or not re.fullmatch('[0-9a-f]{64}', value):
        raise ValueError('Expected the registered retained-volume identity')
    return {'schema': 1, 'storage_id': value,
            'fs_uuid': str(uuid.uuid5(uuid.NAMESPACE_URL, 'battlefield-k3s:' + value))}


def decide(facts, expected):
    """The same fail-closed policy runs in plan, apply and every service start."""
    owner = facts['owner']
    if owner is not None and (owner.get('identity') != expected or owner.get('phase') not in ('initializing', 'ready')):
        raise ValueError('Guest storage registration belongs to another retained volume')
    if facts['filesystem']:
        if facts['filesystem'] != 'ext4' or facts['uuid'] != expected['fs_uuid']:
            raise ValueError('Existing guest filesystem is not the registered retained data disk')
        if facts['marker'] != expected and not (facts['marker'] is None and owner == {'identity': expected, 'phase': 'initializing'}):
            raise ValueError('Retained filesystem has no matching ownership marker')
    elif owner is not None and owner['phase'] == 'ready':
        raise ValueError('Previously initialized filesystem is missing; recovery is required')
    elif facts['signatures']:
        raise ValueError('Unknown disk signatures; refusing initialization')
    if facts['mounted'] and (facts['mount_uuid'] != expected['fs_uuid'] or not facts['filesystem']):
        raise ValueError('Guest data mount resolves to another filesystem')
    return {'initialize': not bool(facts['filesystem']), 'mount': not facts['mounted'],
            'register': owner != {'identity': expected, 'phase': 'ready'}}


def inspect(contract):
    expected = identity(contract)
    domain = contract.get('domain_uuid', '')
    if not re.fullmatch('[0-9a-f-]{36}', domain) or Path('/sys/class/dmi/id/product_uuid').read_text().strip().lower() != domain:
        raise ValueError('SSH target is not the Terraform-owned guest UUID')
    size = contract.get('data_bytes')
    if type(size) is not int or size < 1024**3:
        raise ValueError('Expected a valid retained volume capacity')
    if not DEVICE.exists() or not stat.S_ISBLK(DEVICE.stat().st_mode):
        raise ValueError('Retained data device is absent')
    info = json.loads(run('lsblk', '--json', '--bytes', '--output', 'PATH,TYPE,SIZE,SERIAL,MOUNTPOINTS', str(DEVICE)))['blockdevices']
    if len(info) != 1 or info[0]['type'] != 'disk' or info[0]['size'] != size or info[0]['serial'] != 'battlefield-k3s-data' or info[0].get('children'):
        raise ValueError('Retained device type, serial, capacity or partition layout changed')
    if any(path and path != str(MOUNT) for path in info[0].get('mountpoints', [])):
        raise ValueError('Retained disk is mounted at an unexpected path')
    values = dict(line.split('=', 1) for line in run('blkid', '-p', '-o', 'export', str(DEVICE), allowed=(0, 2)).splitlines() if '=' in line)
    if values.get('PTTYPE'):
        raise ValueError('Retained disk has a partition table; refusing adoption')
    plain(OWNER)
    owner = json.loads(OWNER.read_text()) if OWNER.exists() else None
    plain(MOUNT, directory=True)
    mount_text = run('findmnt', '--json', '--mountpoint', str(MOUNT), '--output', 'TARGET,UUID,FSTYPE,OPTIONS', allowed=(0, 1))
    mounts = json.loads(mount_text)['filesystems'] if mount_text else []
    mounted = bool(mounts)
    if mounted and ('rw' not in mounts[0]['options'].split(',') or mounts[0]['fstype'] != 'ext4'):
        raise ValueError('Guest data mount must be writable ext4')
    marker = None
    if values.get('TYPE') == 'ext4' and values.get('UUID') == expected['fs_uuid']:
        if mounted and mounts[0]['uuid'] == expected['fs_uuid']:
            path = MOUNT / MARKER
            plain(path)
            marker = json.loads(path.read_text()) if path.exists() else None
        else:
            # Read the ownership record without mounting or replaying the journal in plan.
            text = run('debugfs', '-R', 'cat /' + MARKER, str(DEVICE))
            marker = json.loads(text) if text else None
    signatures = json.loads(run('wipefs', '--no-act', '--json', str(DEVICE))).get('signatures', [])
    facts = {'owner': owner, 'filesystem': values.get('TYPE', ''), 'uuid': values.get('UUID', ''),
             'marker': marker, 'mounted': mounted, 'mount_uuid': mounts[0]['uuid'] if mounted else '', 'signatures': signatures}
    changes = decide(facts, expected)
    changes['fstab'] = fstab_content(FSTAB.read_text(), expected['fs_uuid']) != FSTAB.read_text()
    if not mounted and MOUNT.exists() and any(MOUNT.iterdir()):
        raise ValueError('Unmounted data path is not empty; refusing to hide existing data')
    return facts, changes, expected


def fstab_content(text, fs_uuid):
    entries = []
    for line in text.splitlines():
        parts = line.split()
        if parts and not parts[0].startswith('#') and len(parts) > 1 and (parts[1] == str(MOUNT) or parts[0] == 'UUID=' + fs_uuid):
            if parts[1] != str(MOUNT) or parts[0] != 'UUID=' + fs_uuid or parts[2:] != ['ext4', 'defaults,nofail', '0', '2']:
                raise ValueError('Existing fstab entry conflicts with the guest data contract')
            continue
        entries.append(line)
    return '\n'.join(entries) + '\nUUID=' + fs_uuid + ' ' + str(MOUNT) + ' ext4 defaults,nofail 0 2\n'


def prepare(contract):
    facts, changes, expected = inspect(contract)
    before = FSTAB.read_text()
    after = fstab_content(before, expected['fs_uuid'])
    plain(OWNER.parent, directory=True)
    OWNER.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    if changes['initialize']:
        # Signatures alone cannot establish emptiness. Check every byte once before
        # formatting the exact retained disk, without a force/overwrite option.
        run('cmp', '--bytes=' + str(contract['data_bytes']), str(DEVICE), '/dev/zero', timeout=1800)
        atomic(OWNER, json.dumps({'identity': expected, 'phase': 'initializing'}).encode())
        run('mkfs.ext4', '-q', '-U', expected['fs_uuid'], '-L', 'battlefield-data', str(DEVICE), timeout=600)
    MOUNT.mkdir(mode=0o700, exist_ok=True)
    if after != before:
        atomic(FSTAB, after.encode(), mode=0o644)
        run('systemctl', 'daemon-reload')
    if changes['mount']:
        run('mount', str(MOUNT))
    # Reconfirm the mount before writing any marker, so failed mounts never write
    # data into the OS filesystem. An interrupted format can resume only by UUID.
    actual = run('findmnt', '--noheadings', '--mountpoint', str(MOUNT), '--output', 'UUID').strip()
    if actual != expected['fs_uuid']:
        raise ValueError('Guest storage mount did not resolve to the retained disk')
    marker = MOUNT / MARKER
    if not marker.exists():
        atomic(marker, (json.dumps(expected, sort_keys=True) + '\n').encode())
    atomic(OWNER, json.dumps({'identity': expected, 'phase': 'ready'}).encode())
    guard(contract)
    return changes


def guard(contract):
    facts, changes, expected = inspect(contract)
    if any(changes.values()) or facts['marker'] != expected:
        raise ValueError('K3s requires its registered retained filesystem mounted and ready')
    if fstab_content(FSTAB.read_text(), expected['fs_uuid']) != FSTAB.read_text():
        raise ValueError('Guest persistent data mount is not registered for boot')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('action', choices=['inspect', 'prepare', 'guard'])
    parser.add_argument('contract', help='Base64 JSON contract, or installed contract JSON path')
    args = parser.parse_args()
    if os.geteuid() != 0:
        raise ValueError('Guest storage checks require root')
    if args.contract.startswith('/'):
        contract = json.loads(Path(args.contract).read_text())
    else:
        contract = json.loads(base64.b64decode(args.contract, validate=True))
    if args.action == 'inspect':
        _, changes, expected = inspect(contract)
        print(json.dumps({'changes': changes, 'filesystem_uuid': expected['fs_uuid']}))
    elif args.action == 'prepare':
        print(json.dumps({'changes': prepare(contract)}))
    else:
        guard(contract)
        print('Retained guest storage verified')


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        raise SystemExit('Guest storage: ' + (str(error) if isinstance(error, ValueError) else 'validation failed'))
