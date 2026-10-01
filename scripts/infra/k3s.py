#!/usr/bin/env python3
"""Review-bound Ansible operations on the verified Terraform K3s guest (AB-128)."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time
import importlib.util

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('vm', ROOT / 'scripts/infra/vm.py')
vm = importlib.util.module_from_spec(spec)
spec.loader.exec_module(vm)
config = vm.load('k3s-config')
bootstrap, lock, platform, tools = vm.bootstrap, vm.lock, vm.platform, vm.tools


def registry_from_recovery(path, username, password):
    try:
        record = json.loads(bootstrap.private_file(path).read_text())
        return config.registry({'username': username, 'password': password, 'probe_image': record['backend']})
    except (OSError, ValueError, KeyError, TypeError):
        raise ValueError('K3s requires private GHCR credentials and a successful main delivery with a verified backend recovery record; see docs/operations/k3s-platform.md') from None


def pins_and_assets(directory):
    pins = json.loads((ROOT / 'infra/k3s/pins.json').read_text())
    if set(pins) != {'schema', 'k3s', 'envoy', 'kyverno'} or pins['schema'] != 1:
        raise ValueError('Unexpected platform pins')
    assets = {}
    for name in ('k3s', 'envoy', 'kyverno'):
        pin = pins[name]
        if not re.fullmatch('[0-9a-f]{64}', pin['sha256']) or not pin['url'].startswith('https://github.com/'):
            raise ValueError('Expected checksum-pinned official platform assets')
        assets[name] = str(tools.download(directory, pin, '.asset'))
    return pins, assets


def variables(contract, pins, assets, credentials):
    return {'k3s_contract': contract, 'k3s_pins': pins, 'k3s_assets': assets,
            'k3s_server_config': config.server_config(contract), 'k3s_namespaces': config.namespaces(),
            'k3s_platform_manifests': config.platform(contract, credentials['probe_image']),
            'k3s_registry_manifests': config.secrets(credentials)}


def run_action(action, inventory, values, directory, fd):
    report = directory / (action + '-' + str(time.time_ns()) + '.json')
    extra = directory / ('variables-' + str(time.time_ns()) + '.json')
    command = [str(bootstrap.tools_bin() / 'ansible-playbook'), str(ROOT / 'infra/ansible/k3s.yml'),
               '-i', str(inventory), '--extra-vars', '@' + str(extra)]
    if action == 'plan':
        command += ['--check', '--diff']
    try:
        tools.atomic(extra, vm.json_bytes(dict(values, k3s_action=action)))
        environment = bootstrap.clean_ansible_env(report)
        environment[lock.ENV] = str(fd)
        with lock.supervised(command, env=environment, pass_fds=(fd,), stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL) as child:
            code = child.wait(timeout=3000)
    finally:
        extra.unlink(missing_ok=True)
    value = platform.read_action_report(report)
    if code or not value or value['failed']:
        tasks = ', '.join(task['task'] for task in value.get('tasks', []) if task['status'] in ('failed', 'unreachable'))
        raise ValueError('K3s ' + action + ' failed at: ' + (tasks or 'playbook startup') + '; private module output was not published')
    return value


def unchanged(report):
    if any(task['changed'] for task in report['tasks']):
        raise ValueError('K3s still has configuration drift; run and review a new plan')


def execute(args):
    platform.trusted_revision(args.revision)
    for path in (args.receipt, args.report, args.registry_file):
        bootstrap.outside_repo(path)
    credentials = config.registry(json.loads(bootstrap.private_file(args.registry_file).read_text()))
    with lock.acquire() as fd, tempfile.TemporaryDirectory(prefix='bf-k3s-') as tmp:
        directory = Path(tmp)
        machine = vm.VM(args, fd)
        machine.preflight()
        if not machine.existing:
            raise ValueError('Run the reviewed VM job first')
        machine.prepare()
        _, changes = machine.plan(directory / 'vm.tfplan')
        if changes:
            raise ValueError('VM configuration has unapplied changes; review the VM job first')
        machine.verify()
        connection = machine.connection()
        routes = json.loads(machine.run(['ip', '-j', 'route', 'show', 'table', 'all']))
        networks = [r['dst'] for r in routes if r.get('dst', 'default') != 'default'] + [machine.config['network_cidr']]
        for name in machine.virsh('net-list', '--all', '--name').splitlines():
            for node in vm.ET.fromstring(machine.virsh('net-dumpxml', name)).findall('ip'):
                networks.append(node.get('address') + '/' + (node.get('prefix') or node.get('netmask')))
        settings = config.validate(json.loads(args.settings.read_text()), networks)
        cache = tools.private_directory(machine.directory / 'platform-cache')
        pins, assets = pins_and_assets(cache)
        import ipaddress
        host = dict(machine.inventory, gateway=str(ipaddress.IPv4Network(machine.config['network_cidr']).network_address + 1))
        contract = config.contract(host, connection, machine.config['data_gib'] * 1024**3, settings, pins)
        values = variables(contract, pins, assets, credentials)
        inventory = machine.directory / 'inventory.json'
        identity = hashlib.sha256(vm.json_bytes([contract, pins, settings, credentials, machine.state_digest()])).hexdigest()
        if args.action == 'plan':
            report = run_action('plan', inventory, values, directory, fd)
            platform.write_new(args.receipt, {'schema': 1, 'revision': args.revision, 'inputs': identity,
                               'created': time.time(), 'change_digest': report['change_digest']})
        elif args.action == 'apply':
            receipt = json.loads(bootstrap.private_file(args.receipt).read_text())
            platform.check_receipt(receipt, args, identity)
            fresh = run_action('plan', inventory, values, directory, fd)
            if receipt['change_digest'] != fresh['change_digest']:
                raise ValueError('Guest configuration changed after review; run a new plan')
            args.receipt.unlink()
            applied = run_action('apply', inventory, values, directory, fd)
            report = run_action('verify', inventory, values, directory, fd)
            unchanged(run_action('plan', inventory, values, directory, fd))
            report['applied_tasks'] = applied['tasks']
        else:
            report = run_action('verify', inventory, values, directory, fd)
            unchanged(run_action('plan', inventory, values, directory, fd))
        if args.action != 'plan':
            export_access(machine, connection)
        report.update(action=args.action, revision=args.revision, versions=contract['versions'],
                      storage={'data_gib': machine.config['data_gib'], 'retained': True},
                      datastore='sqlite', scheduled_backup=False, public_tunnel=False,
                      observability_prerequisites=settings['observability'], langfuse=False)
        platform.write_new(args.report, report)
    print(args.action + ' completed; sanitized K3s report written')


def export_access(machine, connection):
    command = ['ssh', '-i', machine.key, '-o', 'IdentitiesOnly=yes', '-o', 'BatchMode=yes',
               '-o', 'StrictHostKeyChecking=yes', '-o', 'UserKnownHostsFile=' + str(machine.known_hosts),
               connection['user'] + '@' + connection['address']]
    # Preserve current Kind profiles. These separate files are inputs for AB-129.
    kubeconfig = machine.run(command + ['sudo cat /etc/rancher/k3s/k3s.yaml'])
    if kubeconfig.count('https://127.0.0.1:6443') != 1:
        raise ValueError('Unexpected K3s kubeconfig endpoint')
    kubeconfig = kubeconfig.replace('https://127.0.0.1:6443', 'https://' + connection['address'] + ':6443')
    ca = machine.run(command + ['sudo cat /srv/battlefield/k3s/server/tls/server-ca.crt'])
    if '-----BEGIN CERTIFICATE-----' not in ca:
        raise ValueError('Expected the K3s serving CA')
    tools.atomic(machine.directory / 'kubeconfig', kubeconfig.encode())
    tools.atomic(machine.directory / 'kubelet-ca.crt', (ca + '\n').encode())


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['plan', 'apply', 'verify'])
    for name in ('inventory', 'vault-password-file', 'become-password-file', 'registry-file', 'receipt', 'report'):
        parser.add_argument('--' + name, type=bootstrap.safe_path, required=True)
    parser.add_argument('--revision', required=True)
    parser.add_argument('--config', type=bootstrap.safe_path, default=ROOT / 'infra/libvirt/vm.json')
    parser.add_argument('--settings', type=bootstrap.safe_path, default=ROOT / 'infra/k3s/settings.json')
    args = parser.parse_args()
    args.replace_os = False
    os.umask(0o077)
    execute(args)


if __name__ == '__main__':
    try:
        main()
    except KeyboardInterrupt:
        sys.exit(130)
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        print('K3s: ' + (str(error) if isinstance(error, ValueError) else 'operation failed; no private output published'), file=sys.stderr)
        sys.exit(1)
