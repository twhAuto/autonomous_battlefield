#!/usr/bin/env python3
"""Rehearse the foundation in a disposable local libvirt VM; never configure the host."""
import argparse
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.request
import uuid
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[2]
URI = 'qemu:///system'


def run(*args, **kwargs):
    try:
        output = subprocess.run(args, check=True, text=kwargs.pop('text', True), capture_output=True,
                                timeout=kwargs.pop('timeout', 120), **kwargs).stdout
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired) as error:
        if args[0] in ('virsh', 'qemu-img', 'cloud-localds'):
            # Never copy captured child output into result.json or console diagnostics.
            operation = args[3] if args[0] == 'virsh' else (args[1] if args[0] == 'qemu-img' else 'seed creation')
            reason = 'timed out' if isinstance(error, subprocess.TimeoutExpired) else f'exited {error.returncode}'
            raise ValueError(f'{args[0]} {operation}: {reason}') from error
        raise
    return output.strip() if isinstance(output, str) else output


def virsh(*args, **kwargs):
    return run('virsh', '--connect', URI, *args, **kwargs)


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def checked_image(path, expected):
    if not path.is_file() or path.is_symlink() or digest(path) != expected:
        raise ValueError('Cloud image does not match the pinned SHA-256')
    info = json.loads(run('qemu-img', 'info', '--output=json', str(path)))
    if info['format'] != 'qcow2' or info.get('backing-filename'):
        raise ValueError('Expected a standalone qcow2 cloud image')


def parse_xml(xml):
    try:
        return ET.fromstring(xml)
    except ET.ParseError as error:
        raise ValueError('Invalid libvirt XML; inspect the selected resource') from error


def owned_xml(xml, identity, kind):
    element = parse_xml(xml)
    if element.tag != kind or element.findtext('uuid') != identity:
        raise ValueError('Resource identity changed; refusing cleanup')
    return element


def network_details(xml):
    element = parse_xml(xml)
    forward = element.find('forward')
    addresses = [node for node in element.findall('ip')
                 if node.find('dhcp/range') is not None
                 and ipaddress.ip_address(node.get('address')).version == 4]
    if forward is None or forward.get('mode') != 'nat' or len(addresses) != 1:
        raise ValueError('Rehearsal needs an existing active NAT network with one IPv4 DHCP range')
    node = addresses[0]
    subnet = ipaddress.ip_network(f"{node.get('address')}/{node.get('netmask') or node.get('prefix')}", strict=False)
    if subnet.version != 4:
        raise ValueError("Rehearsal requires IPv4 DHCP")
    return subnet


def wait_for(check, message, timeout=300):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            value = check()
            if value:
                return value
        except (subprocess.SubprocessError, OSError):
            pass
        time.sleep(2)
    raise ValueError(message)


class Rehearsal:
    def __init__(self, work, evidence, network, *, memory_mib=3072, root_gib=16, cpus=2):
        self.work, self.evidence, self.network = work, evidence, network
        self.memory_mib, self.root_gib, self.cpus = memory_mib, root_gib, cpus
        self.identity = str(uuid.uuid4())
        self.name = 'bf-rehearsal-' + self.identity[:12]
        self.pool_path = '/var/lib/libvirt/images/' + self.name
        self.pool_defined = False
        self.pool_started = False
        self.pool_built = False
        self.domain_defined = False
        self.volumes = []
        self.ip = None
        self.subnet = None
        self.mac = '52:54:00:' + ':'.join(self.identity.replace('-', '')[i:i+2] for i in (0, 2, 4))

    def prepare(self, image):
        if (self.name in virsh('list', '--all', '--name').splitlines()
                or self.name in virsh('pool-list', '--all', '--name').splitlines()
                or os.path.lexists(self.pool_path)):
            raise ValueError('Generated rehearsal resource already exists; refusing adoption')
        subnet = network_details(virsh('net-dumpxml', self.network))
        if self.network not in virsh('net-list', '--name').splitlines():
            raise ValueError('Selected NAT network is not active')
        self.subnet = subnet
        for key in ('client', 'host'):
            run('ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', str(self.work / key))
        cloud = {
            'hostname': self.name, 'ssh_pwauth': False, 'disable_root': True,
            'users': [{'name': 'rehearsal', 'shell': '/bin/bash', 'lock_passwd': True,
                       'sudo': 'ALL=(ALL) NOPASSWD:ALL',
                       'ssh_authorized_keys': [(self.work / 'client.pub').read_text().strip()]}],
            'ssh_keys': {'ed25519_private': (self.work / 'host').read_text(),
                         'ed25519_public': (self.work / 'host.pub').read_text().strip()},
            'write_files': [{'path': '/etc/battlefield-rehearsal', 'permissions': '0444',
                             'content': self.identity + '\n'}],
        }
        (self.work / 'user-data').write_text('#cloud-config\n' + json.dumps(cloud))
        (self.work / 'meta-data').write_text(json.dumps({'instance-id': self.identity}))
        run('cloud-localds', str(self.work / 'seed.iso'), str(self.work / 'user-data'),
            str(self.work / 'meta-data'))
        run('qemu-img', 'convert', '-O', 'qcow2', str(image), str(self.work / 'root.qcow2'), timeout=600)
        run('qemu-img', 'resize', str(self.work / 'root.qcow2'), str(self.root_gib) + 'G')
        pool = f"<pool type='dir'><name>{self.name}</name><uuid>{self.identity}</uuid><target><path>{self.pool_path}</path></target></pool>"
        (self.work / 'pool.xml').write_text(pool)
        virsh('pool-define', str(self.work / 'pool.xml'))
        self.pool_defined = True
        # Directory pools create only this new directory. Filesystem-formatting flags do not apply.
        virsh('pool-build', self.identity)
        self.pool_built = True
        virsh('pool-start', self.identity)
        self.pool_started = True
        for filename, fmt in [('root.qcow2', 'qcow2'), ('seed.iso', 'raw')]:
            source = self.work / filename
            # Upload the complete standalone qcow2, not its expanded guest capacity.
            virsh('vol-create-as', self.identity, filename, str(source.stat().st_size), '--format', fmt)
            self.volumes.append(filename)
            virsh('vol-upload', filename, str(source), '--pool', self.identity, '--sparse', timeout=600)
        domain = f"""<domain type='kvm'>
 <name>{self.name}</name><uuid>{self.identity}</uuid>
 <memory unit='MiB'>{self.memory_mib}</memory><vcpu>{self.cpus}</vcpu>
 <os><type arch='x86_64'>hvm</type><boot dev='hd'/></os>
 <features><acpi/><apic/></features><cpu mode='host-passthrough'/>
 <on_reboot>restart</on_reboot><on_crash>destroy</on_crash>
 <devices>
  <disk type='file' device='disk'><driver name='qemu' type='qcow2'/><source file='{self.pool_path}/root.qcow2'/><target dev='vda' bus='virtio'/></disk>
  <disk type='file' device='cdrom'><driver name='qemu' type='raw'/><source file='{self.pool_path}/seed.iso'/><target dev='sda' bus='sata'/><readonly/></disk>
  <interface type='network'><mac address='{self.mac}'/><source network='{self.network}'/><model type='virtio'/></interface>
  <serial type='pty'><target port='0'/></serial><console type='pty'><target type='serial' port='0'/></console>
 </devices>
</domain>"""
        (self.work / 'domain.xml').write_text(domain)
        virsh('define', str(self.work / 'domain.xml'))
        self.domain_defined = True
        virsh('start', self.identity)
        self.ip = wait_for(self.lease, 'Guest did not receive an IPv4 DHCP lease')
        (self.work / 'known_hosts').write_text(self.ip + ' ' + (self.work / 'host.pub').read_text())
        wait_for(lambda: self.ssh('true') or True, 'Guest SSH did not become ready')
        self.wait_cloud_init()
        self.ssh('test "$(cat /etc/battlefield-rehearsal)" = ' + shlex.quote(self.identity))

    def lease(self):
        output = virsh('net-dhcp-leases', self.network, '--mac', self.mac)
        for line in output.splitlines():
            fields = line.split()
            if self.mac in fields:
                for field in fields:
                    if '/' in field:
                        try:
                            address = ipaddress.ip_interface(field).ip
                        except ValueError:
                            continue
                        if self.subnet is not None and address in self.subnet:
                            return str(address)
        return None

    def ssh(self, command, **kwargs):
        return run('ssh', '-i', str(self.work / 'client'), '-o', 'IdentitiesOnly=yes',
                   '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes',
                   '-o', 'UserKnownHostsFile=' + str(self.work / 'known_hosts'),
                   '-o', 'ConnectTimeout=5', '-o', 'ServerAliveInterval=15',
                   '-o', 'ServerAliveCountMax=2', 'rehearsal@' + self.ip, command, **kwargs)

    def wait_cloud_init(self):
        try:
            self.ssh('sudo cloud-init status --wait', timeout=600)
        except subprocess.CalledProcessError as error:
            reason = 'completed with errors (degraded)' if error.returncode == 2 else f'exited {error.returncode}'
            raise ValueError('Guest cloud-init ' + reason + '; inspect guest initialization and network egress') from error
        except subprocess.TimeoutExpired as error:
            raise ValueError('Guest cloud-init timed out; inspect guest initialization and network egress') from error

    def boot_id(self):
        return str(uuid.UUID(self.ssh('cat /proc/sys/kernel/random/boot_id')))

    def reboot(self):
        previous = self.boot_id()
        try:
            self.ssh('sudo systemctl --no-block reboot')
        except subprocess.CalledProcessError as error:
            # SSH may disconnect before returning status; other command failures are not expected.
            if error.returncode != 255:
                raise
        wait_for(lambda: self.boot_id() != previous, 'Guest did not return with a new boot ID after reboot')

    def exercise(self):
        with tarfile.open(self.work / 'source.tar', 'w') as archive:
            for path in ('infra/ansible', 'scripts/infra', 'scripts/test/rehearse-server-guest.py'):
                archive.add(ROOT / path, arcname=path, filter=lambda item: None if '__pycache__' in item.name else item)
        self.ssh('mkdir -m 700 source && tar -xf - -C source', input=(self.work / 'source.tar').read_bytes(), text=False)
        command = 'python3 source/scripts/test/rehearse-server-guest.py --identity ' + self.identity
        print('Guest ready; running interrupted installation, resume, repeat and drift checks.', flush=True)
        try:
            self.ssh(command + ' before-reboot', timeout=2400)
            print('Initial checks passed; rebooting only the disposable guest.', flush=True)
            self.reboot()
            self.ssh(command + ' after-reboot', timeout=900)
        finally:
            # Only public test evidence; the guest keeps private fixtures in a separate directory.
            try:
                content = self.ssh('tar -czf - -C evidence .', text=False)
                (self.evidence / 'guest-evidence.tar.gz').write_bytes(content)
            except subprocess.SubprocessError:
                pass
        result = json.loads(self.ssh('cat evidence/result.json'))
        if result.get('passed') is not True:
            raise ValueError('Guest rehearsal did not report success')
        return result

    def cleanup(self):
        if self.domain_defined:
            owned_xml(virsh('dumpxml', self.identity), self.identity, 'domain')
            if virsh('domstate', self.identity) != 'shut off':
                virsh('destroy', self.identity)
            virsh('undefine', self.identity)
        if self.pool_defined:
            pool = owned_xml(virsh('pool-dumpxml', self.identity), self.identity, 'pool')
            if pool.get('type') != 'dir' or pool.findtext('target/path') != self.pool_path:
                raise ValueError('Pool path changed; refusing cleanup')
            if self.pool_started:
                for filename in self.volumes:
                    virsh('vol-delete', filename, '--pool', self.identity)
                virsh('pool-destroy', self.identity)
            # Deletes only the now-empty directory; never use recursive filesystem removal.
            if self.pool_built or os.path.lexists(self.pool_path):
                virsh('pool-delete', self.identity)
            virsh('pool-undefine', self.identity)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--evidence-dir', type=Path, required=True, help='New private directory outside Git')
    parser.add_argument('--ubuntu', choices=['24.04', '26.04'], default='26.04', help='Pinned guest release (default: 26.04)')
    parser.add_argument('--image', type=Path, help='Optional already downloaded image; must match the pinned image')
    parser.add_argument('--network', default='default', help='Existing active IPv4 NAT/DHCP network; never modified')
    args = parser.parse_args()
    os.umask(0o077)
    if not args.network.replace('-', '').replace('_', '').isalnum():
        raise ValueError('Invalid network name')
    for binary in ('virsh', 'qemu-img', 'cloud-localds', 'ssh', 'ssh-keygen'):
        if not shutil.which(binary):
            raise ValueError('Required existing hypervisor tool is missing: ' + binary)
    if args.evidence_dir.resolve().is_relative_to(ROOT):
        raise ValueError('Evidence must live outside Git')
    memory = dict(line.split(':', 1) for line in Path('/proc/meminfo').read_text().splitlines())
    if int(memory['MemAvailable'].split()[0]) < 5 * 1024 * 1024:
        raise ValueError('Need at least 5 GiB available RAM before allocating the 3 GiB guest')
    if not Path('/var/lib/libvirt/images').is_dir():
        raise ValueError('Missing /var/lib/libvirt/images; prepare libvirt storage before running the rehearsal')
    if shutil.disk_usage('/var/lib/libvirt/images').free < 20 * 1024**3 or shutil.disk_usage(tempfile.gettempdir()).free < 8 * 1024**3:
        raise ValueError('Need 20 GiB free in libvirt storage and 8 GiB in temporary storage')
    args.evidence_dir.mkdir(mode=0o700)
    specification = json.loads((ROOT / 'infra/ansible/rehearsal-image.json').read_text())[args.ubuntu]
    with tempfile.TemporaryDirectory(prefix='bf-rehearsal-') as directory:
        work = Path(directory)
        image = args.image
        if image is None:
            cache = Path(os.environ.get('XDG_CACHE_HOME', Path.home() / '.cache')) / 'autonomous-battlefield/rehearsal'
            cache.mkdir(parents=True, exist_ok=True, mode=0o700)
            image = cache / (specification['sha256'] + '.img')
            if not image.exists() and not image.is_symlink():
                download = work / 'ubuntu.img'
                print('Downloading the pinned Ubuntu cloud image.', flush=True)
                with urllib.request.urlopen(specification['url'], timeout=60) as response, download.open('wb') as output:
                    shutil.copyfileobj(response, output)
                checked_image(download, specification['sha256'])
                # The cache may be on another filesystem; publish a verified complete copy there.
                with tempfile.NamedTemporaryFile(dir=cache, prefix='.download-') as staging:
                    with download.open('rb') as source:
                        shutil.copyfileobj(source, staging)
                    staging.flush()
                    os.fsync(staging.fileno())
                    try:
                        os.link(staging.name, image)
                    except FileExistsError:
                        pass
        checked_image(image, specification['sha256'])
        rehearsal = Rehearsal(work, args.evidence_dir, args.network)
        metadata = {'identity': rehearsal.identity, 'name': rehearsal.name, 'image': specification,
                    'source_sha256': {}, 'passed': False, 'cleaned_up': False, 'phase': 'prepare'}
        for directory_name in ('infra/ansible', 'scripts/infra'):
            for source in sorted((ROOT / directory_name).rglob('*')):
                if source.is_file() and '__pycache__' not in source.parts:
                    metadata['source_sha256'][str(source.relative_to(ROOT))] = digest(source)
        metadata['source_sha256']['scripts/test/rehearse-server-guest.py'] = digest(ROOT / 'scripts/test/rehearse-server-guest.py')
        evidence_file = args.evidence_dir / 'result.json'
        evidence_file.write_text(json.dumps(metadata, indent=2) + '\n')
        print('Creating disposable VM ' + rehearsal.name, flush=True)
        try:
            rehearsal.prepare(image)
            metadata['phase'] = 'guest checks and reboot'
            metadata['guest'] = rehearsal.exercise()
            metadata['passed'] = True
        except (ValueError, OSError, subprocess.SubprocessError) as error:
            metadata['failure'] = metadata['phase'] + ': ' + (str(error) if isinstance(error, ValueError) else type(error).__name__)
            raise
        finally:
            try:
                rehearsal.cleanup()
                metadata['cleaned_up'] = True
            finally:
                evidence_file.write_text(json.dumps(metadata, indent=2) + '\n')
        print('PASS: foundation rehearsal and VM cleanup; evidence: ' + str(evidence_file), flush=True)


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        # Captured child output can include fixtures. Keep errors generic; use private evidence.
        print('Rehearsal failed: ' + (str(error) if isinstance(error, ValueError) else type(error).__name__), file=sys.stderr)
        sys.exit(1)
