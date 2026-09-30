#!/usr/bin/env python3
"""Copy encrypted VM recovery bundles to the selected second disk, or restore state."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('vm', ROOT / 'scripts/infra/vm.py')
vm = importlib.util.module_from_spec(spec)
spec.loader.exec_module(vm)
bootstrap, tools, lock = vm.bootstrap, vm.tools, vm.lock


def run(command, fd, **kwargs):
    with lock.supervised([str(x) for x in command], pass_fds=(fd,), stdin=subprocess.PIPE,
                         stdout=subprocess.PIPE, stderr=subprocess.PIPE, **kwargs) as child:
        out, _ = child.communicate(timeout=120)
        if child.returncode:
            raise ValueError('Recovery command failed; no private output published')
        return out


def restore(payload, inventory, directory, read_xml):
    expected = {k: inventory.get(k, '') for k in ('machine_id', 'user', 'data_uuid', 'data_mount')}
    if payload.get('schema') != 1 or payload.get('owner') != expected:
        raise ValueError('Bundle belongs to a different host/storage identity')
    vm.policy.config(payload['config'])
    if payload['state'].get('version') != 4:
        raise ValueError('Unsupported recovery state')
    # Validate every existing domain/network/pool against the saved UUID before
    # publishing any file. Recovery never creates/adopts a same-named stranger.
    for resource in payload['state'].get('resources', []):
        address = resource['type'] + '.' + resource['name']
        if address not in vm.policy.RESOURCES or resource.get('module') or len(resource['instances']) != 1:
            raise ValueError('Unexpected recovery resource')
        attributes = resource['instances'][0]['attributes']
        if resource['type'] in ('libvirt_domain', 'libvirt_network', 'libvirt_pool'):
            command = {'libvirt_domain': 'dumpxml', 'libvirt_network': 'net-dumpxml', 'libvirt_pool': 'pool-dumpxml'}[resource['type']]
            root = ET.fromstring(read_xml(command, vm.NAME))
            identity_field = 'uuid' if resource['type'] == 'libvirt_domain' else 'id'
            if root.findtext('uuid') != attributes[identity_field]:
                raise ValueError('Live resource identity differs from recovery state')
            if resource['type'] == 'libvirt_pool' and root.findtext('target/path') != str(Path(inventory.get('data_mount', '/srv/battlefield-data')) / 'libvirt/images'):
                raise ValueError('Live storage pool differs from recovery state')
        elif resource['type'] == 'libvirt_volume':
            root = ET.fromstring(read_xml('vol-dumpxml', attributes['id'], '--pool', vm.NAME))
            expected_path = str(Path(inventory.get('data_mount', '/srv/battlefield-data')) / 'libvirt/images' / attributes['name'])
            if root.findtext('key') != attributes['id'] or root.findtext('target/path') != expected_path:
                raise ValueError('Live volume identity differs from recovery state')
    files = {'owner.json': vm.json_bytes(expected), 'terraform.tfstate': vm.json_bytes(payload['state']),
             'id_ed25519': payload['key'].encode(), 'recovered-config.json': vm.json_bytes(payload['config'])}
    for name, content in files.items():
        path = directory / name
        if path.exists() or path.is_symlink():
            if bootstrap.private_file(path).read_bytes() != content:
                raise ValueError('Recovery would replace existing local files; preserve and inspect them first')
    for name, content in files.items():
        path = directory / name
        if not path.exists():
            bootstrap.write_new(path, content)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['copy', 'restore'])
    parser.add_argument('--inventory', required=True, type=bootstrap.safe_path)
    parser.add_argument('--bundle', required=True, type=bootstrap.safe_path)
    parser.add_argument('--vault-password-file', type=bootstrap.safe_path)
    parser.add_argument('--destination', type=bootstrap.safe_path)
    args = parser.parse_args()
    os.umask(0o077)
    inventory = bootstrap.load_inventory(args.inventory)
    bundle = bootstrap.private_file(args.bundle)
    bootstrap.outside_repo(bundle)
    if not bundle.read_bytes().startswith(b'$ANSIBLE_VAULT;'):
        raise ValueError('Expected an encrypted VM recovery bundle')
    with lock.acquire() as fd:
        inspected = bootstrap.inspect_host(inventory, local=True)
        if args.action == 'copy':
            if not args.destination:
                raise ValueError('copy requires --destination on the registered backup disk')
            candidate = inspected.get('backup_candidate') or {}
            if not candidate.get('usable'):
                raise ValueError('Register and mount the separate backup disk before copying state')
            if not args.destination.is_dir():
                raise ValueError('Destination must be an existing private directory on the backup disk')
            directory = args.destination
            mount = json.loads(run(['findmnt', '--json', '--target', directory, '-o', 'UUID,FSTYPE,OPTIONS'], fd))['filesystems'][0]
            if (mount.get('uuid') != inventory.get('backup_uuid') or mount.get('fstype') != 'ext4'
                    or 'rw' not in mount.get('options', '').split(',')):
                raise ValueError('Destination is not on the selected writable backup filesystem')
            tools.private_directory(directory)
            bootstrap.write_new(directory / bundle.name, bundle.read_bytes())
        else:
            if not args.vault_password_file:
                raise ValueError('restore requires --vault-password-file')
            password = bootstrap.private_file(args.vault_password_file)
            payload = json.loads(run([bootstrap.tools_bin() / 'ansible-vault', 'view', bundle,
                                      '--vault-password-file', password], fd, env=bootstrap.clean_ansible_env()))
            directory = tools.private_directory(Path.home() / '.local/state/battlefield/k3s')
            restore(payload, inventory, directory, lambda *arguments: run(['virsh', '-c', 'qemu:///system', *arguments], fd))
    print('VM state ' + args.action + ' completed; no VM/disk resources were changed')


if __name__ == '__main__':
    try:
        main()
    except KeyboardInterrupt:
        sys.exit(130)
    except (ValueError, OSError, subprocess.SubprocessError, ET.ParseError) as error:
        print('VM recovery: ' + (str(error) if isinstance(error, ValueError) else 'Operation failed; no private output published'), file=sys.stderr)
        sys.exit(1)
