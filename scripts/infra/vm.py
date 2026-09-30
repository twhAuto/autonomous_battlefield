#!/usr/bin/env python3
"""Reviewed local Terraform operations for the persistent K3s VM (AB-126)."""
import argparse
import base64
import hashlib
import importlib.util
import ipaddress
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import time
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[2]


def load(name):
    spec = importlib.util.spec_from_file_location(name.replace('-', '_'), ROOT / 'scripts/infra' / (name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


platform = load('platform')
bootstrap, lock = platform.bootstrap, platform.lock
policy, tools = load('vm-policy'), load('vm-tools')
NAME = 'battlefield-k3s'


def json_bytes(value):
    return (json.dumps(value, sort_keys=True, indent=2) + '\n').encode()


def state_resources(path):
    if not path.exists():
        return {}
    data = json.loads(bootstrap.private_file(path).read_text())
    if data.get('version') != 4:
        raise ValueError('Unsupported Terraform state')
    result = {}
    for resource in data.get('resources', []):
        address = resource['type'] + '.' + resource['name']
        if address not in policy.RESOURCES or resource.get('module') or resource.get('mode') != 'managed':
            raise ValueError('State contains resources outside the K3s VM module')
        instances = resource['instances']
        if len(instances) != 1 or instances[0].get('deposed') or 'index_key' in instances[0]:
            raise ValueError('State contains ambiguous/deposed resources; recover it before proceeding')
        result[address] = instances[0]['attributes']
    return result


class VM:
    def __init__(self, args, fd):
        self.args, self.fd = args, fd
        self.inventory = bootstrap.load_inventory(args.inventory)
        self.config = policy.config(json.loads(args.config.read_text()))
        self.host = bootstrap.inspect_host(self.inventory, local=True)
        self.sudo = bootstrap.require_compatible_sudo(self.host.get('sudo_executable'))
        if not self.inventory.get('data_uuid'):
            raise ValueError('VM provisioning requires the AB-134 data mount')
        for name in ('become_password_file', 'vault_password_file'):
            bootstrap.private_file(getattr(args, name))
            bootstrap.outside_repo(getattr(args, name))
        self.directory = tools.private_directory(Path.home() / '.local/state/battlefield/k3s')
        self.state = self.directory / 'terraform.tfstate'
        self.key = self.directory / 'id_ed25519'
        self.known_hosts = self.directory / 'known_hosts'
        self.images = Path(self.inventory.get('data_mount', '/srv/battlefield-data')) / 'libvirt/images'
        self.env = {k: v for k, v in os.environ.items() if not k.startswith(('TF_', 'LIBVIRT_'))}
        self.env.update(LC_ALL='C.UTF-8', TF_IN_AUTOMATION='1', TF_INPUT='0', TF_DATA_DIR=str(self.directory / 'provider'),
                        TF_CLI_CONFIG_FILE=str(self.directory / 'terraform.rc'), **{lock.ENV: str(fd)})
        tools.atomic(self.directory / 'terraform.rc', b'disable_checkpoint = true\n')
        self.existing = state_resources(self.state)

    def run(self, command, *, data=None, timeout=180, allowed=(0,), env=None):
        with lock.supervised([str(x) for x in command], env=env or self.env, pass_fds=(self.fd,),
                             stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE) as process:
            stdout, stderr = process.communicate(data, timeout=timeout)
            if process.returncode not in allowed:
                tools.atomic(self.directory / 'last-error.log', stdout + b'\n' + stderr)
                raise ValueError(Path(command[0]).name + ' failed; private diagnostics: ' + str(self.directory / 'last-error.log'))
        return stdout.decode()

    def virsh(self, *args):
        return self.run(['virsh', '--connect', 'qemu:///system', *args]).strip()

    def sudo_run(self, arguments):
        password = bootstrap.private_file(self.args.become_password_file).read_bytes().rstrip(b'\r\n') + b'\n'
        return self.run([self.sudo, '-S', '-k', '-p', '', '--', *arguments], data=password)

    def guard(self):
        self.sudo_run(['/usr/bin/python3', '/etc/battlefield-data-storage/guard.py', 'guard',
                       self.inventory['data_uuid'], self.inventory.get('data_mount', '/srv/battlefield-data'),
                       self.inventory['user']])

    def preflight(self):
        self.guard()
        owner = {k: self.inventory.get(k, '') for k in ('machine_id', 'user', 'data_uuid', 'data_mount')}
        marker = self.directory / 'owner.json'
        if marker.exists():
            if json.loads(bootstrap.private_file(marker).read_text()) != owner:
                raise ValueError('Canonical VM state belongs to a different host/storage identity')
        elif self.existing or self.key.exists():
            raise ValueError('State/key has no ownership record; use the recovery procedure')
        else:
            bootstrap.write_new(marker, json_bytes(owner))
        for kind, listing, dump, address in [
                ('domain', 'list', 'dumpxml', 'libvirt_domain.k3s'),
                ('network', 'net-list', 'net-dumpxml', 'libvirt_network.k3s'),
                ('pool', 'pool-list', 'pool-dumpxml', 'libvirt_pool.k3s')]:
            names = self.virsh(listing, '--all', '--name').splitlines()
            if NAME in names:
                root = ET.fromstring(self.virsh(dump, NAME))
                identity_field = 'uuid' if kind == 'domain' else 'id'
                if self.existing.get(address, {}).get(identity_field) != root.findtext('uuid'):
                    raise ValueError('Existing ' + kind + ' is not owned by canonical state; restore/import after identity review')
                if kind == 'pool' and (root.get('type') != 'dir' or root.findtext('target/path') != str(self.images)):
                    raise ValueError('K3s pool targets different storage')
            elif address in self.existing and address in policy.RETAINED:
                raise ValueError('Retained ' + kind + ' is missing; recover it before provisioning')
        networks = []
        for name in self.virsh('net-list', '--all', '--name').splitlines():
            if not name or name == NAME:
                continue
            for node in ET.fromstring(self.virsh('net-dumpxml', name)).findall('ip'):
                networks.append(node.get('address') + '/' + (node.get('prefix') or node.get('netmask')))
        routes = json.loads(self.run(['ip', '-j', 'route', 'show', 'table', 'all']))
        networks += [r['dst'] for r in routes if r.get('dst', 'default') != 'default'
                     and not (r.get('dev') == 'bf-k3s0' and 'libvirt_network.k3s' in self.existing) and r.get('type') not in ('local', 'broadcast')]
        policy.no_overlap(self.config['network_cidr'], networks)
        if self.config['cpus'] > os.cpu_count():
            raise ValueError('VM CPU request exceeds host CPUs')
        current_memory = 0
        if 'libvirt_domain.k3s' in self.existing and self.virsh('domstate', NAME) == 'running':
            current_memory = int(self.existing['libvirt_domain.k3s'].get('memory', 0))
        available = int(self.host['memory_available'].split()[0]) * 1024
        if available < (max(0, self.config['memory_mib'] - current_memory) + 2048) * 1024**2:
            raise ValueError('Insufficient available memory; leave at least 2 GiB for host services')
        # Reserve full virtual capacity, even though qcow2 files grow sparsely.
        allocated = self.allocated_bytes()
        required = (self.config['os_gib'] + self.config['data_gib'] + self.config['reserve_gib'] + 4) * 1024**3
        if shutil.disk_usage(self.images.parent).free + allocated < required:
            raise ValueError('Insufficient data-partition space for full VM disks plus reserved headroom')

    def allocated_bytes(self):
        names = json.loads(self.sudo_run(['/usr/bin/python3', '-c',
            'import json,os,sys; print(json.dumps(os.listdir(sys.argv[1])))', str(self.images)]))
        if 'libvirt_pool.k3s' not in self.existing:
            if names:
                raise ValueError('Unowned files occupy the VM image directory; review adoption before provisioning')
            return 0
        expected = {attributes['name'] for address, attributes in self.existing.items() if address.startswith('libvirt_volume.')}
        if set(names) - expected:
            raise ValueError('Unowned files occupy the K3s storage pool; inspect them before proceeding')
        allocated = 0
        for name in names:
            root = ET.fromstring(self.virsh('vol-dumpxml', name, '--pool', NAME))
            allocated += int(root.findtext('allocation', '0'))
        return allocated

    def prepare(self):
        if not self.key.exists():
            if self.existing:
                raise ValueError('VM administration key is missing; restore it rather than generating a replacement')
            self.run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', self.key])
        bootstrap.private_file(self.key)
        public = ' '.join(self.run(['ssh-keygen', '-y', '-f', self.key]).strip().split()[:2])
        if not re.fullmatch(r'ssh-ed25519 [A-Za-z0-9+/=]+', public):
            raise ValueError('VM administration key must be Ed25519')
        self.binary, image, pins = tools.prepare(self.directory)
        # No private keys, passwords, tokens or application settings enter cloud-init.
        cloud = {'hostname': NAME, 'ssh_pwauth': False, 'disable_root': True,
                 'users': [{'name': 'battlefield', 'shell': '/bin/bash', 'lock_passwd': True,
                            'sudo': 'ALL=(ALL) NOPASSWD:ALL', 'ssh_authorized_keys': [public]}],
                 'packages': ['qemu-guest-agent', 'python3'],
                 'runcmd': [['systemctl', 'enable', '--now', 'qemu-guest-agent']]}
        metadata = {'instance-id': NAME, 'local-hostname': NAME}
        network = {'version': 2, 'ethernets': {'primary': {'match': {'macaddress': self.config['mac']}, 'dhcp4': True}}}
        content = json_bytes([cloud, metadata, network])
        seed_digest = hashlib.sha256(content).hexdigest()
        seed = self.directory / ('seed-' + seed_digest + '.iso')
        if not seed.exists():
            with tempfile.TemporaryDirectory(dir=self.directory) as tmp:
                tmp = Path(tmp)
                (tmp / 'user').write_bytes(b'#cloud-config\n' + json_bytes(cloud))
                (tmp / 'meta').write_bytes(json_bytes(metadata))
                (tmp / 'network').write_bytes(json_bytes(network))
                self.run(['cloud-localds', '--network-config', tmp / 'network', tmp / 'seed.iso', tmp / 'user', tmp / 'meta'])
                tools.atomic(seed, (tmp / 'seed.iso').read_bytes())
        bootstrap.private_file(seed)
        values = {k: v for k, v in self.config.items() if k not in ('schema', 'reserve_gib')}
        values.update(images_directory=str(self.images), image_file=str(image), image_sha256=pins['image']['sha256'],
                      seed_file=str(seed), seed_sha256=seed_digest)
        self.variables = self.directory / 'variables.json'
        tools.atomic(self.variables, json_bytes(values))
        self.identity = hashlib.sha256(json_bytes([self.inventory, self.config, public, pins, tools.digest(seed)])).hexdigest()
        self.tf('init', '-input=false', '-lockfile=readonly', '-reconfigure', '-backend-config=path=' + str(self.state))
        self.tf('validate')

    def tf(self, *args, timeout=900):
        command = [self.binary, '-chdir=' + str(ROOT / 'infra/libvirt'), *args]
        if args[0] not in ('output', 'show'):
            command.append('-no-color')
        with tools.terraform_environment(self.env) as environment:
            return self.run(command, timeout=timeout, env=environment)

    def plan(self, path):
        arguments = ['plan', '-input=false', '-lock-timeout=0s', '-var-file=' + str(self.variables), '-out=' + str(path)]
        if self.args.replace_os:
            arguments += ['-replace=libvirt_volume.os', '-replace=libvirt_domain.k3s']
        self.tf(*arguments)
        plan = json.loads(self.tf('show', '-json', str(path)))
        tools.atomic(self.directory / 'last-plan.json', json_bytes(plan))
        changes = policy.plan_changes(plan, self.existing, self.args.replace_os)
        return plan, changes

    def state_digest(self):
        return tools.digest(self.state) if self.state.exists() else ''

    def snapshot(self):
        if not self.state.exists():
            return None
        resources = state_resources(self.state)
        if not resources:
            return None
        payload = {'schema': 1, 'owner': json.loads((self.directory / 'owner.json').read_text()),
                   'state': json.loads(self.state.read_text()), 'key': self.key.read_text(),
                   'config': self.config, 'revision': self.args.revision}
        snapshots = tools.private_directory(self.directory / 'recovery')
        target = snapshots / (str(time.time_ns()) + '.vault')
        # Capture no plaintext file. Vault consumes the bundle over stdin.
        self.run([bootstrap.tools_bin() / 'ansible-vault', 'encrypt', '--output', target,
                  '--vault-password-file', self.args.vault_password_file], data=json_bytes(payload),
                 env=bootstrap.clean_ansible_env(), timeout=120)
        return target

    def connection(self):
        outputs = json.loads(self.tf('output', '-json'))
        value = outputs.get('connection', {}).get('value')
        if not isinstance(value, dict):
            raise ValueError('Terraform did not produce connection facts')
        policy.inventory(value, self.key, self.known_hosts)
        if value['address'] != self.config['guest_ip'] or value['data_volume'] != str(self.images / 'battlefield-k3s-data.qcow2'):
            raise ValueError('Terraform connection facts differ from the approved configuration')
        domain = ET.fromstring(self.virsh('dumpxml', NAME))
        if value['domain_uuid'] != domain.findtext('uuid'):
            raise ValueError('Running VM identity differs from Terraform state')
        channels = domain.findall('devices/channel')
        if not any(channel.get('type') == 'unix' and channel.find('target') is not None
                   and channel.find('target').get('name') == 'org.qemu.guest_agent.0' for channel in channels):
            raise ValueError('VM is missing its expected UNIX guest-agent channel')
        if self.virsh('domstate', value['domain_uuid']) != 'running':
            raise ValueError('K3s VM is not running')
        return value

    def guest_public_key(self, identity):
        request = {'execute': 'guest-exec', 'arguments': {'path': '/usr/bin/cat',
                   'arg': ['/etc/ssh/ssh_host_ed25519_key.pub'], 'capture-output': True}}
        process = json.loads(self.virsh('qemu-agent-command', identity, json.dumps(request)))['return']['pid']
        for _ in range(30):
            status = json.loads(self.virsh('qemu-agent-command', identity, json.dumps(
                {'execute': 'guest-exec-status', 'arguments': {'pid': process}})))['return']
            if status.get('exited'):
                if status.get('exitcode') != 0:
                    raise ValueError('Guest host-key discovery failed')
                key = base64.b64decode(status['out-data'], validate=True).decode().strip().split()
                if len(key) < 2 or key[0] != 'ssh-ed25519' or not re.fullmatch('[A-Za-z0-9+/=]+', key[1]):
                    raise ValueError('Guest returned an invalid SSH public key')
                return ' '.join(key[:2])
            time.sleep(1)
        raise ValueError('Guest agent did not return its SSH public key')

    def verify(self, initialize=False):
        self.guard()
        value = self.connection()
        deadline = time.monotonic() + (420 if initialize else 30)
        while True:
            try:
                public = self.guest_public_key(value['domain_uuid'])
                break
            except ValueError:
                if time.monotonic() >= deadline:
                    raise
                time.sleep(3)
        known = (value['address'] + ' ' + public + '\n').encode()
        if initialize:
            # The hypervisor agent channel establishes trust, never ssh-keyscan/TOFU.
            tools.atomic(self.known_hosts, known)
        elif not self.known_hosts.exists() or bootstrap.private_file(self.known_hosts).read_bytes() != known:
            raise ValueError('SSH identity changed; review a VM apply before accepting it')
        self.run(['ssh', '-i', self.key, '-o', 'IdentitiesOnly=yes', '-o', 'BatchMode=yes',
                  '-o', 'StrictHostKeyChecking=yes', '-o', 'UserKnownHostsFile=' + str(self.known_hosts),
                  '-o', 'ConnectTimeout=10', '-o', 'ServerAliveInterval=15', '-o', 'ServerAliveCountMax=2',
                  value['user'] + '@' + value['address'],
                  'sudo cloud-init status --wait >/dev/null && test -b /dev/disk/by-id/virtio-battlefield-k3s-data'], timeout=600)
        generated = policy.inventory(value, self.key, self.known_hosts)
        tools.atomic(self.directory / 'inventory.json', json_bytes(generated))
        return {'ssh': True, 'cloud_init': True, 'retained_data_attached': True,
                'inventory': str(self.directory / 'inventory.json')}


def execute(args):
    platform.trusted_revision(args.revision)
    for path in (args.receipt, args.report):
        bootstrap.outside_repo(path)
    with lock.acquire() as fd:
        vm = VM(args, fd)
        vm.preflight()
        vm.prepare()
        private = tools.private_directory(args.receipt.parent)
        saved = private / 'vm.tfplan'
        report = {'schema': 1, 'revision': args.revision, 'action': args.action, 'failed': False,
                  'settings': vm.config, 'images_directory': str(vm.images)}
        if args.action == 'plan':
            if args.receipt.exists() or saved.exists():
                raise ValueError('Use a fresh private directory for each VM plan')
            plan, changes = vm.plan(saved)
            receipt = {'schema': 1, 'created': time.time(), 'revision': args.revision, 'identity': vm.identity,
                       'state_digest': vm.state_digest(), 'plan_digest': policy.plan_digest(plan),
                       'saved_digest': tools.digest(saved), 'replace_os': args.replace_os}
            bootstrap.write_new(args.receipt, json_bytes(receipt))
            report['changes'] = changes
        elif args.action == 'apply':
            receipt = json.loads(bootstrap.private_file(args.receipt).read_text())
            policy.receipt(receipt, args.revision, vm.identity, vm.state_digest(), args.replace_os)
            if tools.digest(bootstrap.private_file(saved)) != receipt.get('saved_digest'):
                raise ValueError('Saved Terraform plan changed; review a new plan')
            with tempfile.TemporaryDirectory(dir=vm.directory) as temporary:
                current, changes = vm.plan(Path(temporary) / 'fresh.tfplan')
            if policy.plan_digest(current) != receipt['plan_digest']:
                raise ValueError('Infrastructure drift changed the reviewed plan; review a new plan')
            vm.snapshot()
            vm.guard()
            args.receipt.unlink()  # A failure or interruption consumes the approval.
            try:
                vm.tf('apply', '-input=false', '-lock-timeout=0s', str(saved))
            finally:
                # Terraform may have persisted partial progress on cancellation/failure.
                vm.snapshot()
                saved.unlink(missing_ok=True)
            report['changes'] = changes
            report['verification'] = vm.verify(initialize=True)
        else:
            if args.replace_os:
                raise ValueError('verify cannot request OS replacement')
            report['verification'] = vm.verify()
            with tempfile.TemporaryDirectory(dir=vm.directory) as temporary:
                _, changes = vm.plan(Path(temporary) / 'verify.tfplan')
            report['changes'] = changes
            if changes:
                raise ValueError('VM verification found drift; run and review a plan')
        bootstrap.write_new(args.report, json_bytes(report))
    print('VM ' + args.action + ' completed; sanitized report written')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['plan', 'apply', 'verify'])
    parser.add_argument('--revision', required=True)
    parser.add_argument('--config', type=bootstrap.safe_path, default=ROOT / 'infra/libvirt/vm.json')
    parser.add_argument('--replace-os', action='store_true', help='Explicitly review replacement of the OS and domain; data deletion remains forbidden')
    for name in ('inventory', 'become-password-file', 'vault-password-file', 'receipt', 'report'):
        parser.add_argument('--' + name, type=bootstrap.safe_path, required=True)
    os.umask(0o077)
    execute(parser.parse_args())


if __name__ == '__main__':
    try:
        main()
    except KeyboardInterrupt:
        sys.exit(130)
    except (ValueError, OSError, subprocess.SubprocessError, ET.ParseError) as error:
        print('VM: ' + (str(error) if isinstance(error, ValueError) else 'Operation failed; inspect private VM diagnostics'), file=sys.stderr)
        sys.exit(1)
