"""Privileged, create-once/adopt-only CLI VM lifecycle behind the Ansible entry point."""
import grp
import hashlib
import json
import os
from pathlib import Path
import pwd
import re
import shutil
import stat
import subprocess
import tempfile
import time
import urllib.request
import uuid
import xml.etree.ElementTree as ET
from ansible.module_utils.battlefield_cli_vm_policy import (
    NAME, DIRECTORY, IP, encoded, xml_digest, require_identity, validate_layout, no_overlap, check_receipt,
)

STATE = Path('/var/lib/battlefield-cli-vm')
DISKS = Path(DIRECTORY)


def run(*args, timeout=120, allowed=(0,)):
    result = subprocess.run([str(a) for a in args], capture_output=True, text=True, timeout=timeout,
                            env=dict(os.environ, LC_ALL='C', LANG='C'))
    if result.returncode not in allowed:
        operation = Path(args[0]).name
        if operation == 'virsh' and len(args) > 3:
            operation += ' ' + str(args[3])
        if STATE.exists():
            write(STATE / 'last-error.log', (operation + '\n' + result.stdout + '\n' + result.stderr).encode())
        raise ValueError('CLI VM command failed: ' + operation + '; root-only diagnostics: /var/lib/battlefield-cli-vm/last-error.log')
    return result.stdout.strip()


def virsh(*args):
    return run('/usr/bin/virsh', '--connect', 'qemu:///system', *args)


def safe(path, owners=(0,), private=False):
    path = Path(path)
    if not path.is_absolute() or '..' in path.parts:
        raise ValueError('CLI VM path must be absolute')
    for parent in [*reversed(path.parents), path]:
        if not parent.exists() and not parent.is_symlink():
            continue
        info = parent.lstat()
        if stat.S_ISLNK(info.st_mode) or info.st_uid not in owners or info.st_mode & 0o022:
            raise ValueError('Unsafe CLI VM path ownership or permissions: ' + str(parent))
        if parent != path and not stat.S_ISDIR(info.st_mode):
            raise ValueError('CLI VM path traverses a non-directory')
    if private and path.exists() and path.stat().st_mode & 0o077:
        raise ValueError('CLI VM state/key must be private')
    return path


def file_identity(path, owners, *, private=False, checksum=False):
    path = safe(path, owners, private)
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1:
        raise ValueError('CLI VM file must be a regular file without hard links')
    result = {'device': info.st_dev, 'inode': info.st_ino, 'uid': info.st_uid,
              'gid': info.st_gid, 'mode': stat.S_IMODE(info.st_mode)}
    if checksum:
        with path.open('rb') as stream:
            result['sha256'] = hashlib.file_digest(stream, 'sha256').hexdigest()
    return result


def write(path, content, uid=0, gid=0):
    path = Path(path)
    safe(path, (0, uid))
    if path.exists() and path.read_bytes() == content:
        return False
    fd, temporary = tempfile.mkstemp(prefix='.cli-vm-', dir=path.parent)
    try:
        with os.fdopen(fd, 'wb') as stream:
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
        os.chown(temporary, uid, gid)
        os.replace(temporary, path)
        directory = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)
    return True


def copy_image(source, destination, limit=4 * 1024**3):
    """Bound both cached and downloaded inputs before a bad response can fill the host."""
    size = 0
    while block := source.read(min(1024**2, limit - size + 1)):
        size += len(block)
        if size > limit:
            raise ValueError('CLI VM cloud image exceeds the 4 GiB input limit')
        destination.write(block)


def disk_directory(path, gid):
    # mkdir mode is filtered by the caller's private umask; QEMU still needs traversal.
    path.mkdir(mode=0o700)
    os.chown(path, 0, gid)
    path.chmod(0o750)


def read(path):
    file_identity(path, (0,), private=True)
    return json.loads(path.read_text())


def information(kind):
    return dict(line.split(':', 1) for line in virsh(kind, NAME).splitlines() if ':' in line)


class Engine:
    def __init__(self, request, assets):
        self.request, self.assets = request, assets
        if os.getuid() != 0 or Path('/etc/machine-id').read_text().strip() != request['machine_id']:
            raise ValueError('CLI VM operation requires root on the registered physical host')
        self.account = pwd.getpwnam(request['operator'])
        if self.account.pw_uid == 0 or self.account.pw_dir != request['home']:
            raise ValueError('CLI VM operator/home differs from the host account')
        if request['mode'] not in ('adopt', 'create') or not re.fullmatch(r'[0-9a-f]{40}', request['revision']):
            raise ValueError('Explicit CLI VM intent and source revision are required')
        if not re.fullmatch(r'[0-9a-f]{64}', request['source_digest']):
            raise ValueError('Invalid CLI VM source fingerprint')
        pin = request['image']
        if set(pin) != {'url', 'sha256'} or not pin['url'].startswith('https://cloud-images.ubuntu.com/') or not re.fullmatch(r'[0-9a-f]{64}', pin['sha256']):
            raise ValueError('Expected the repository-pinned Ubuntu cloud image')
        self.owner_path, self.receipt_path = STATE / 'owner.json', STATE / 'review.json'
        safe(STATE, private=True)
        self.owner = read(self.owner_path) if self.owner_path.exists() else None
        self.home = Path(request['home'])
        self.export = self.home / '.local/state/battlefield/cli-vm'
        safe(self.export, (0, self.account.pw_uid), private=True)
        self.host_identity = {k: request[k] for k in ('machine_id', 'operator', 'home')}
        if self.owner and (self.owner.get('schema') != 1 or self.owner.get('host') != self.host_identity):
            raise ValueError('CLI VM ownership belongs to another physical host/operator')
        if self.owner and request['mode'] == 'adopt':
            adoption = request['adoption']
            if (any(adoption[key + '_uuid'] != self.owner['identities'][key] for key in ('domain', 'network')) or
                    any(adoption[key] != self.owner['connection'][key] for key in ('machine_id', 'identity_file'))):
                raise ValueError('Adoption inputs differ from the existing CLI VM ownership')
        if not self.owner and (STATE / 'incomplete.json').exists():
            raise ValueError('Interrupted CLI VM creation is retained; inspect the root-owned journal before recovery')
        self.qemu = pwd.getpwnam('libvirt-qemu')
        self.kvm_gid = grp.getgrnam('kvm').gr_gid

    def trusted_connection(self):
        if self.owner:
            connection = self.owner['connection']
        elif self.request['mode'] == 'adopt':
            connection = self.request['adoption']
            for key in ('domain_uuid', 'network_uuid'):
                require_identity(connection[key])
            if not re.fullmatch(r'[0-9a-f]{32}', connection['machine_id']):
                raise ValueError('Adoption needs the existing guest machine identity')
        else:
            return None
        key = Path(connection['identity_file'])
        if not key.is_relative_to(self.home):
            raise ValueError('CLI admin key must remain on its operator host')
        key_metadata = file_identity(key, (0, self.account.pw_uid), private=True, checksum=True)
        if key_metadata['uid'] != self.account.pw_uid:
            raise ValueError('CLI admin key must belong to the operator')
        known = Path(connection['known_hosts_file'])
        safe(known, (0, self.account.pw_uid))
        if not known.is_file():
            raise ValueError('Adoption requires an already trusted guest SSH host key')
        matches = run('/usr/bin/ssh-keygen', '-F', IP, '-f', known, allowed=(0, 1))
        host_keys = []
        for line in matches.splitlines():
            fields = line.split()
            if line.startswith('#') or not fields:
                continue
            if len(fields) < 3 or not re.fullmatch(r'(ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp(?:256|384|521))', fields[1]) or not re.fullmatch(r'[A-Za-z0-9+/=]+', fields[2]):
                raise ValueError('Unsupported guest host-key trust entry')
            host_keys.append(IP + ' ' + fields[1] + ' ' + fields[2])
        if not host_keys:
            raise ValueError('Adoption requires an already trusted guest SSH host key')
        return {'identity_file': str(key), 'key_identity': key_metadata,
                'known_hosts': '\n'.join(sorted(set(host_keys))) + '\n',
                'machine_id': connection.get('machine_id', '')}

    def names(self):
        # Listing must succeed; connection failures never mean a clean host.
        domains = virsh('list', '--all', '--name').splitlines()
        networks = virsh('net-list', '--all', '--name').splitlines()
        filters = [line.split()[-1] for line in virsh('nwfilter-list').splitlines()
                   if re.match(r'\s*[0-9a-f]{8}-', line)]
        return domains, networks, filters

    def capacity(self):
        if not Path('/dev/kvm').is_char_device() or os.cpu_count() < 2:
            raise ValueError('CLI VM requires KVM and at least two host CPUs')
        memory = dict(line.split(':', 1) for line in Path('/proc/meminfo').read_text().splitlines())
        if int(memory['MemAvailable'].split()[0]) < 6 * 1024 * 1024:
            raise ValueError('Fresh CLI VM needs 6 GiB available RAM, including 2 GiB host headroom')
        if shutil.disk_usage(DISKS.parent).free < 30 * 1024**3:
            raise ValueError('Fresh CLI VM needs 30 GiB free on the existing root-disk filesystem')

    def overlap(self, networks, owned):
        addresses = []
        for name in networks:
            if owned and name == NAME:
                continue
            for ip in ET.fromstring(virsh('net-dumpxml', name)).findall('ip'):
                addresses.append(ip.get('address') + '/' + (ip.get('prefix') or ip.get('netmask')))
        for route in json.loads(run('/usr/sbin/ip', '-j', 'route', 'show', 'table', 'all')):
            if route.get('dst', 'default') == 'default' or route.get('type') in ('local', 'broadcast'):
                continue
            if owned and route.get('dev') == 'bf-agent0':
                continue
            addresses.append(route['dst'])
        no_overlap(addresses)

    def inspect(self):
        domains, networks, filters = self.names()
        present = [NAME in group for group in (domains, networks, filters)]
        if not self.owner and self.request['mode'] == 'create':
            if any(present) or DISKS.exists() or DISKS.is_symlink() or self.export.exists():
                raise ValueError('Fresh creation refuses existing VM, network, filter, disks or connection state; use explicit adoption')
            self.overlap(networks, False)
            self.capacity()
            return {'exists': False}
        if not all(present):
            raise ValueError('Existing CLI resources are incomplete; never recreate a missing adopted disk or VM')
        self.overlap(networks, True)
        persistent = {kind: virsh(command, NAME, *extra) for kind, command, extra in (
            ('domain', 'dumpxml', ['--inactive']), ('network', 'net-dumpxml', ['--inactive']),
            ('filter', 'nwfilter-dumpxml', []))}
        identities = {kind: require_identity(ET.fromstring(xml).findtext('uuid')) for kind, xml in persistent.items()}
        expected = self.owner['identities'] if self.owner else dict(identities,
            domain=self.request['adoption']['domain_uuid'], network=self.request['adoption']['network_uuid'])
        for inactive in (True, False):
            domain = persistent['domain'] if inactive else virsh('dumpxml', NAME)
            network = persistent['network'] if inactive else virsh('net-dumpxml', NAME)
            validate_layout(domain, network, persistent['filter'], self.assets['filter'], expected)
        for kind in ('dominfo', 'net-info'):
            values = information(kind)
            if values.get('Persistent', '').strip() != 'yes' or values.get('Autostart', '').strip() not in ('enable', 'yes'):
                raise ValueError('Adoption requires the current VM and network to be persistent with autostart enabled')
        if virsh('domstate', NAME) != 'running' or information('net-info').get('Active', '').strip() != 'yes':
            raise ValueError('Registered CLI VM/network must already be running; bootstrap never silently restarts them')
        owners = (0, self.qemu.pw_uid)
        disk = file_identity(DISKS / 'root.qcow2', owners)
        seed = file_identity(DISKS / 'seed.iso', owners, checksum=True)
        info = json.loads(run('/usr/bin/qemu-img', 'info', '--force-share', '--output=json', DISKS / 'root.qcow2'))
        if info.get('format') != 'qcow2' or info.get('virtual-size') != 24 * 1024**3 or info.get('backing-filename') or info.get('encrypted'):
            raise ValueError('CLI root must be a standalone 24 GiB qcow2 disk without encryption/backing images')
        snapshot = {'exists': True, 'identities': identities,
                    'xml': {kind: xml_digest(xml) for kind, xml in persistent.items()}, 'disk': disk, 'seed': seed}
        if self.owner and snapshot != self.owner['snapshot']:
            raise ValueError('CLI VM identity, retained file or persistent XML drifted; stop and review recovery')
        snapshot['connection'] = self.trusted_connection()
        if self.owner and snapshot['connection'] != self.owner['trusted_connection']:
            raise ValueError('CLI administrator key or guest SSH trust changed; review recovery')
        return snapshot

    def summary(self, observation):
        changes = [] if self.owner else (['record existing VM ownership', 'export trusted guest connection']
            if observation['exists'] else ['create pinned standalone root disk and private seed', 'define isolated filter/network/VM', 'enable autostart and start new VM', 'export trusted guest connection'])
        if self.owner and not self.owner.get('guest_verified'):
            changes.append('verify guest identity and finish connection handoff')
        return {'schema': 1, 'name': NAME, 'action': 'plan', 'mode': self.request['mode'],
                'revision': self.request['revision'], 'changes': changes, 'vm_replacement': False,
                'disk': str(DISKS / 'root.qcow2'), 'root_gib': 24, 'memory_mib': 4096, 'cpus': 2,
                'guest_ip': IP, 'provider_login': 'manual; existing login homes untouched',
                'identities': observation.get('identities', {}),
                'storage_modes': {name: format(observation[name]['mode'], '04o')
                                  for name in ('disk', 'seed') if name in observation}}

    def plan(self):
        observation = self.inspect()
        safe(STATE, private=True).mkdir(mode=0o700, exist_ok=True)
        receipt = {'schema': 1, 'created': time.time(), 'request': self.request,
                   'observation': observation, 'review_id': os.urandom(32).hex()}
        write(self.receipt_path, encoded(receipt))
        return dict(self.summary(observation), review_id=receipt['review_id'], expires_in_seconds=1800)

    def prepare_export(self):
        for path in [self.home / '.local', self.home / '.local/state', self.home / '.local/state/battlefield', self.export]:
            safe(path, (0, self.account.pw_uid))
            if not path.exists():
                path.mkdir(mode=0o700)
                os.chown(path, self.account.pw_uid, self.account.pw_gid)
        safe(self.export, (0, self.account.pw_uid), private=True)

    def cache_image(self):
        pin = self.request['image']
        cache = safe(Path('/var/cache/battlefield-cli-vm'))
        cache.mkdir(mode=0o700, exist_ok=True)
        image = cache / (pin['sha256'] + '.qcow2')
        if not image.exists():
            with tempfile.TemporaryDirectory(dir=cache) as temporary:
                downloaded = Path(temporary) / 'image'
                candidate = self.home / '.cache/autonomous-battlefield/rehearsal' / (pin['sha256'] + '.img')
                if candidate.exists():
                    file_identity(candidate, (0, self.account.pw_uid))
                    with candidate.open('rb') as source, downloaded.open('wb') as output:
                        copy_image(source, output)
                else:
                    with urllib.request.urlopen(pin['url'], timeout=60) as response, downloaded.open('wb') as output:
                        copy_image(response, output)
                if file_identity(downloaded, (0,), checksum=True)['sha256'] != pin['sha256']:
                    raise ValueError('CLI VM cloud-image checksum mismatch')
                os.replace(downloaded, image)
                image.chmod(0o600)
        if file_identity(image, (0,), private=True, checksum=True)['sha256'] != pin['sha256']:
            raise ValueError('Cached CLI VM cloud-image checksum mismatch')
        return image

    def create(self):
        image = self.cache_image()
        # Downloads can be slow; recheck absence/capacity immediately before reserving resources.
        self.inspect()
        identities = {kind: str(uuid.uuid4()) for kind in ('domain', 'network', 'filter')}
        # A durable reservation prevents guessing ownership after interrupted creation.
        write(STATE / 'incomplete.json', encoded({'host': self.host_identity, 'identities': identities,
                                                'request': self.request, 'created': time.time()}))
        self.prepare_export()
        key = self.export / 'id_ed25519'
        run('/usr/bin/ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', key)
        for path in (key, key.with_suffix('.pub')):
            os.chown(path, self.account.pw_uid, self.account.pw_gid)
            path.chmod(0o600)
        public = key.with_suffix('.pub').read_text().strip()
        disk_directory(safe(DISKS), self.kvm_gid)
        # The image is standalone; no backing file can later be deleted or replaced underneath it.
        run('/usr/bin/qemu-img', 'convert', '-O', 'qcow2', image, DISKS / 'root.qcow2', timeout=600)
        run('/usr/bin/qemu-img', 'resize', DISKS / 'root.qcow2', '24G')
        with tempfile.TemporaryDirectory(dir=STATE) as temporary:
            work = Path(temporary)
            host_key = work / 'host'
            run('/usr/bin/ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', host_key)
            host_public = ' '.join(host_key.with_suffix('.pub').read_text().split()[:2])
            cloud = {'hostname': NAME, 'disable_root': True, 'ssh_pwauth': False,
                     'package_update': False, 'package_upgrade': False,
                     'ssh_keys': {'ed25519_private': host_key.read_text(), 'ed25519_public': host_public},
                     'users': [{'name': 'vm-admin', 'lock_passwd': True, 'shell': '/bin/bash',
                                'sudo': ['ALL=(ALL) NOPASSWD:ALL'], 'ssh_authorized_keys': [public]},
                               {'name': 'battlefield-worker', 'lock_passwd': True, 'shell': '/bin/bash',
                                'homedir': '/var/lib/battlefield-worker'}]}
            (work / 'user').write_bytes(b'#cloud-config\n' + encoded(cloud))
            (work / 'meta').write_bytes(encoded({'instance-id': identities['domain'], 'local-hostname': NAME}))
            (work / 'network').write_bytes(encoded({'version': 2, 'ethernets': {'worker': {
                'match': {'macaddress': '52:54:00:ba:77:01'}, 'set-name': 'eth0', 'dhcp4': False,
                'dhcp6': False, 'accept-ra': False, 'addresses': [IP + '/24']}}}))
            run('/usr/bin/cloud-localds', '--network-config=' + str(work / 'network'), DISKS / 'seed.iso', work / 'user', work / 'meta')
        for filename in ('root.qcow2', 'seed.iso'):
            path = DISKS / filename
            os.chown(path, self.qemu.pw_uid, self.kvm_gid)
            path.chmod(0o600)
        write(self.export / 'known_hosts', (IP + ' ' + host_public + '\n').encode(), self.account.pw_uid, self.account.pw_gid)
        with tempfile.TemporaryDirectory(dir=STATE) as temporary:
            for kind, command in [('filter', 'nwfilter-define'), ('network', 'net-define'), ('domain', 'define')]:
                node = ET.fromstring(self.assets[kind])
                ET.SubElement(node, 'uuid').text = identities[kind]
                path = Path(temporary) / (kind + '.xml')
                path.write_text(ET.tostring(node, encoding='unicode'))
                virsh(command, path)
            virsh('net-autostart', NAME)
            virsh('net-start', NAME)
            virsh('autostart', NAME)
            virsh('start', NAME)
        return identities, {'identity_file': str(key), 'known_hosts_file': str(self.export / 'known_hosts'), 'machine_id': ''}

    def apply(self, review_id):
        observation = self.inspect()
        check_receipt(read(self.receipt_path), self.request, observation, review_id)
        applied_changes = self.summary(observation)['changes']
        # Consume review before any infrastructure mutation. Every retry needs a new plan.
        self.receipt_path.unlink()
        changed = not self.owner
        if not self.owner:
            if observation['exists']:
                identities = observation['identities']
                connection = dict(self.request['adoption'])
                self.prepare_export()
                write(self.export / 'known_hosts', observation['connection']['known_hosts'].encode(), self.account.pw_uid, self.account.pw_gid)
                connection['known_hosts_file'] = str(self.export / 'known_hosts')
            else:
                identities, connection = self.create()
            self.owner = {'schema': 1, 'host': self.host_identity, 'identities': identities,
                          'connection': connection, 'guest_verified': False, 'image': self.request['image'] if not observation['exists'] else None}
            # Inspect the just-created resources as an explicit adoption; no second create is possible.
            if not observation['exists']:
                old_request = self.request
                self.request = dict(old_request, mode='adopt', adoption=dict(connection,
                    domain_uuid=identities['domain'], network_uuid=identities['network'], machine_id='0' * 32))
                self.owner = None
                observation = self.inspect()
                self.request = old_request
                self.owner = {'schema': 1, 'host': self.host_identity, 'identities': identities,
                              'connection': connection, 'guest_verified': False, 'image': self.request['image']}
            snapshot = dict(observation)
            snapshot.pop('connection')
            self.owner.update(snapshot=snapshot, trusted_connection=self.trusted_connection())
            write(self.owner_path, encoded(self.owner))
            (STATE / 'incomplete.json').unlink(missing_ok=True)
        return {'changed': changed, 'applied_changes': applied_changes,
                'connection': self.connection(), 'identities': self.owner['identities']}

    def connection(self):
        return {'identity_file': self.owner['connection']['identity_file'],
                'known_hosts_file': str(self.export / 'known_hosts'), 'ansible_host': IP,
                'ansible_user': 'vm-admin', 'domain_uuid': self.owner['identities']['domain']}

    def verify(self):
        if not self.owner:
            raise ValueError('CLI VM has no adoption/creation ownership record')
        self.inspect()
        return {'changed': False, 'connection': self.connection(), 'identities': self.owner['identities']}

    def finish(self, guest):
        self.verify()
        if set(guest) != {'machine_id', 'domain_uuid'} or guest['domain_uuid'] != self.owner['identities']['domain'] or not re.fullmatch(r'[0-9a-f]{32}', guest['machine_id']) or guest['machine_id'] == self.request['machine_id']:
            raise ValueError('SSH guest identity does not match the registered VM')
        expected = self.owner['connection']['machine_id']
        if expected and expected != guest['machine_id']:
            raise ValueError('Guest machine-id changed; retained provider homes require explicit recovery')
        changed = not self.owner['guest_verified']
        self.owner['connection']['machine_id'] = guest['machine_id']
        self.owner['trusted_connection']['machine_id'] = guest['machine_id']
        self.owner['guest_verified'] = True
        write(self.owner_path, encoded(self.owner))
        connection = self.connection()
        handoff = dict(connection, schema=1, machine_id=guest['machine_id'],
                       host_machine_id=self.request['machine_id'], network_uuid=self.owner['identities']['network'])
        changed |= write(self.export / 'handoff.json', encoded(handoff), self.account.pw_uid, self.account.pw_gid)
        inventory = {'all': {'children': {'cli_vm': {'hosts': {NAME: {
            'ansible_host': IP, 'ansible_user': 'vm-admin', 'ansible_connection': 'ssh',
            'ansible_python_interpreter': '/usr/bin/python3', 'ansible_become_exe': '/usr/bin/sudo',
            'ansible_ssh_private_key_file': connection['identity_file'],
            'ansible_ssh_common_args': '-o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=' + connection['known_hosts_file'],
            'cli_contract': {'machine_id': guest['machine_id'], 'domain_uuid': guest['domain_uuid']},
        }}}}}}
        changed |= write(self.export / 'inventory.json', encoded(inventory), self.account.pw_uid, self.account.pw_gid)
        return {'changed': changed, 'report': dict(self.summary(self.inspect()), action='verify', changes=[],
                guest_verified=True, handoff=str(self.export / 'handoff.json'), inventory=str(self.export / 'inventory.json'))}
