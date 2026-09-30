#!/usr/bin/python3
"""Adopt one existing ext4 data partition; never format or migrate data."""
import argparse
import fcntl
import json
import os
from pathlib import Path
import pwd
import re
import stat
import subprocess
import sys
import tempfile

FSTAB = Path('/etc/fstab')
CONTRACT = Path('/etc/battlefield-data-storage.json')
UUID_PATTERN = r'[a-fA-F0-9]{8}(?:-[a-fA-F0-9]{4}){3}-[a-fA-F0-9]{12}'
MOUNT_PATTERN = r'/srv/[a-z][a-z0-9-]{0,62}'


def run(*args):
    return subprocess.check_output(args, text=True, timeout=30).strip()


def validate(uuid, target, operator):
    if not re.fullmatch(UUID_PATTERN, uuid):
        raise ValueError('data_uuid must be a complete filesystem UUID')
    if not re.fullmatch(MOUNT_PATTERN, str(target)):
        raise ValueError('data_mount must be a direct /srv child using lowercase letters, digits and hyphens')
    if not re.fullmatch(r'[a-z_][a-z0-9_-]*', operator):
        raise ValueError('Invalid operator')


def safe_directory(path):
    # Check every ancestor: Path.is_symlink() alone misses parent symlinks.
    for part in reversed([path, *path.parents]):
        if part.is_symlink() or (part.exists() and not part.is_dir()):
            raise ValueError(f'Unsafe directory: {part}')


def flatten(nodes):
    for node in nodes:
        yield node
        yield from flatten(node.get('children', []))


def mounted(uuid, target):
    safe_directory(target)
    result = subprocess.run(['findmnt', '--json', '--mountpoint', str(target),
                             '-o', 'TARGET,UUID,FSTYPE,OPTIONS,FSROOT'],
                            text=True, capture_output=True, timeout=30, check=False)
    if result.returncode == 1 and not result.stdout.strip():
        return False
    if result.returncode:
        raise ValueError('Cannot inspect the data mount')
    mounts = json.loads(result.stdout).get('filesystems', [])
    if len(mounts) != 1:
        raise ValueError('Ambiguous data mount')
    entry = mounts[0]
    if (entry['target'] != str(target) or (entry.get('uuid') or '').lower() != uuid.lower()
            or entry['fstype'] != 'ext4' or entry['fsroot'] != '/'
            or 'rw' not in entry['options'].split(',')):
        raise ValueError('Data mount must be the expected writable ext4 filesystem, not another mount or bind subtree')
    return True


def require_mount(uuid, target):
    if not mounted(uuid, target):
        raise ValueError('Expected data filesystem is not mounted; refusing root-filesystem fallback')


def selected_device(uuid, target):
    devices = json.loads(run('lsblk', '--json', '--bytes', '--paths', '-o',
                             'NAME,TYPE,FSTYPE,LABEL,UUID,PARTUUID,MOUNTPOINTS'))
    matches = [d for d in flatten(devices['blockdevices']) if (d.get('uuid') or '').lower() == uuid.lower()]
    if len(matches) != 1:
        raise ValueError('Expected exactly one device with data_uuid')
    device = matches[0]
    if device['type'] != 'part' or device['fstype'] != 'ext4':
        raise ValueError('Data storage must be an existing ext4 partition; automatic formatting is forbidden')
    if any(p and p != str(target) for p in device.get('mountpoints', [])):
        raise ValueError('Selected data partition is mounted elsewhere; refusing to relocate it')
    return device


def fstab_entry(uuid, target):
    return f'UUID={uuid} {target} ext4 defaults,nofail,x-systemd.device-timeout=10s 0 2'


def fstab_content(original, uuid, target, device):
    entry = fstab_entry(uuid, target)
    aliases = {f'UUID={uuid}', device['name'], f'/dev/disk/by-uuid/{uuid}'}
    for key in ('label', 'partuuid'):
        if device.get(key):
            aliases.add(('LABEL=' if key == 'label' else 'PARTUUID=') + device[key])
    matches = 0
    for line in original.splitlines():
        fields = [re.sub(r'\\([0-7]{3})', lambda m: chr(int(m[1], 8)), x)
                  for x in line.split('#', 1)[0].split()]
        if len(fields) < 2:
            continue
        source = fields[0]
        same_tag = source.startswith(('UUID=', 'PARTUUID=')) and source.lower() in {x.lower() for x in aliases if x.startswith(('UUID=', 'PARTUUID='))}
        same_device = same_tag or source in aliases or (source.startswith('/dev/') and str(Path(source).resolve()) == device['name'])
        if same_device or fields[1] == str(target):
            if fields != entry.split():
                raise ValueError('Conflicting fstab entry; inspect it before adoption')
            matches += 1
    if matches > 1:
        raise ValueError('Duplicate data mount entries in fstab')
    return original if matches else original.rstrip('\n') + '\n\n# Battlefield data: existing filesystem, never formatted\n' + entry + '\n'


def inspect(uuid, target):
    safe_directory(target)
    device = selected_device(uuid, target)
    active = mounted(uuid, target)
    if not active and target.exists() and any(target.iterdir()):
        raise ValueError('Unmounted target contains files; refusing to hide them')
    if FSTAB.is_symlink() or not FSTAB.is_file():
        raise ValueError('Expected a regular fstab')
    original = FSTAB.read_text()
    return active, original, fstab_content(original, uuid, target, device), device


def directory_specs(target, operator, mode):
    owner = pwd.getpwnam(operator)
    try:
        qemu = pwd.getpwnam('libvirt-qemu')
        qemu_ids = (qemu.pw_uid, qemu.pw_gid)
    except KeyError:
        if mode != 'plan':
            raise ValueError('libvirt-qemu account is missing; apply the host foundation first') from None
        qemu_ids = (-1, -1)  # Plan on a fresh host precedes package installation.
    return [(target, 0, 0, 0o711),
            (target / 'workspace', owner.pw_uid, owner.pw_gid, 0o700),
            (target / 'libvirt', 0, 0, 0o711),
            (target / 'libvirt/images', *qemu_ids, 0o750)]


def directory_changes(specs):
    changes = []
    for path, uid, gid, permissions in specs:
        safe_directory(path)
        if not path.exists():
            changes.append((path, uid, gid, permissions))
        else:
            if path != specs[0][0] and path.stat().st_dev != specs[0][0].stat().st_dev:
                raise ValueError(f'Managed directory is on another filesystem: {path}')
            info = path.stat()
            # Never adopt another user's workspace or an unexpected managed subtree.
            if path != specs[0][0] and info.st_uid not in (0, uid):
                raise ValueError(f'Unexpected owner for {path}; inspect before adoption')
            if (info.st_uid, info.st_gid, stat.S_IMODE(info.st_mode)) != (uid, gid, permissions):
                changes.append((path, uid, gid, permissions))
    return changes


def write_fstab(original, proposed, entry):
    fd, temporary = tempfile.mkstemp(prefix='.battlefield-fstab-', dir=FSTAB.parent)
    try:
        with os.fdopen(fd, 'w') as output:
            output.write(proposed)
            output.flush()
            os.fsync(output.fileno())
        os.chmod(temporary, stat.S_IMODE(FSTAB.stat().st_mode))
        # Validate our entry in isolation; retain unrelated pre-existing entries verbatim.
        with tempfile.NamedTemporaryFile(mode='w', prefix='.battlefield-entry-', dir=FSTAB.parent) as check:
            check.write(entry + '\n')
            check.flush()
            run('findmnt', '--verify', '--tab-file', check.name)
        if FSTAB.read_text() != original:
            raise ValueError('fstab changed concurrently; retry after inspection')
        fd, backup = tempfile.mkstemp(prefix='fstab.before-battlefield-data.', dir=FSTAB.parent)
        with os.fdopen(fd, 'w') as output:
            output.write(original)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, FSTAB)
        run('systemctl', 'daemon-reload')
    finally:
        Path(temporary).unlink(missing_ok=True)


def contract_content(uuid, target, operator):
    return {'schema': 1, 'uuid': uuid, 'mount': str(target), 'operator': operator,
            'domain': 'battlefield-k3s', 'images': str(target / 'libvirt/images')}


def inspect_contract(expected):
    if CONTRACT.is_symlink():
        raise ValueError('Storage contract must not be a symlink')
    if CONTRACT.exists():
        info = CONTRACT.stat()
        if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022:
            raise ValueError('Storage contract must be a protected root-owned regular file')
        if json.loads(CONTRACT.read_text()) != expected:
            raise ValueError('Storage identity changed; use a reviewed migration instead of ordinary apply')
        return True
    return False


def write_contract(expected):
    fd, temporary = tempfile.mkstemp(prefix='.battlefield-storage-', dir=CONTRACT.parent)
    try:
        with os.fdopen(fd, 'w') as output:
            json.dump(expected, output, indent=2)
            output.write('\n')
            output.flush()
            os.fsync(output.fileno())
        # Refuse concurrent replacement rather than overwriting another identity.
        os.link(temporary, CONTRACT)
    finally:
        Path(temporary).unlink(missing_ok=True)


def reconcile(uuid, target, operator, mode):
    validate(uuid, target, operator)
    expected = contract_content(uuid, target, operator)
    owned = inspect_contract(expected)
    if mode == 'guard':
        if not owned:
            raise ValueError('Storage ownership contract is missing; run apply')
        require_mount(uuid, target)
        safe_directory(target / 'libvirt/images')
        if not (target / 'libvirt/images').is_dir():
            raise ValueError('Prepared libvirt image directory is missing')
        if (target / 'libvirt/images').stat().st_dev != target.stat().st_dev:
            raise ValueError('VM image directory is on another filesystem')
        return None
    active, original, proposed, device = inspect(uuid, target)
    specs = directory_specs(target, operator, mode)
    changes = directory_changes(specs) if active else specs
    changed = not owned or not active or original != proposed or bool(changes)
    if mode == 'verify' and changed:
        raise ValueError('Data storage differs from inventory; run plan/apply to reconcile it')
    if mode == 'apply':
        if os.geteuid() != 0:
            raise ValueError('Data mount apply requires sudo')
        # If interrupted after mount, a repeat safely adopts it and persists fstab.
        if not active:
            target.mkdir(mode=0o000, exist_ok=True)
            run('mount', '-t', 'ext4', '-o', 'rw,relatime', device['name'], str(target))
        require_mount(uuid, target)
        # Inspect contents after mounting too, before any permissions change.
        changes = directory_changes(specs)
        if original != proposed:
            write_fstab(original, proposed, fstab_entry(uuid, target))
        for path, uid, gid, permissions in changes:
            require_mount(uuid, target)
            path.mkdir(mode=permissions, exist_ok=True)
            os.chown(path, uid, gid)
            path.chmod(permissions)
        if not owned:
            write_contract(expected)
    result = {'changed': changed, 'uuid': uuid, 'mount': str(target),
              'workspace': str(target / 'workspace'), 'images': str(target / 'libvirt/images')}
    if mounted(uuid, target):
        usage = os.statvfs(target)
        result['available_bytes'] = usage.f_bavail * usage.f_frsize
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('mode', choices=['plan', 'apply', 'verify', 'guard'])
    parser.add_argument('uuid')
    parser.add_argument('mount', type=Path)
    parser.add_argument('operator')
    args = parser.parse_args()
    os.umask(0o077)
    if args.mode == 'apply':
        # A process lock complements the wrapper lock for direct Ansible execution.
        with open('/run/battlefield-data-storage.lock', 'a') as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            result = reconcile(args.uuid, args.mount, args.operator, args.mode)
    else:
        result = reconcile(args.uuid, args.mount, args.operator, args.mode)
    if result is not None:  # libvirt hooks must not emit JSON into the XML filter chain.
        print(json.dumps(result))


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        print(f'Data storage: {error}', file=sys.stderr)
        sys.exit(1)
