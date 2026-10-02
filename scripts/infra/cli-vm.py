#!/usr/bin/env python3
"""Reviewed Ansible adoption/creation of the existing isolated CLI VM layout (AB-140)."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
# The sibling platform.py must never shadow Python's standard library platform.
sys.path = [entry for entry in sys.path if Path(entry).resolve() != Path(__file__).resolve().parent]


def load(name):
    spec = importlib.util.spec_from_file_location(name.replace('-', '_'), ROOT / 'scripts/infra' / (name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


platform = load('platform')
server = platform.bootstrap


def request(args, data):
    assets = {kind: (ROOT / 'infra/deploy/agent-vm' / (kind + '.xml')).read_text() for kind in ('domain', 'network', 'filter')}
    paths = ['scripts/infra/cli-vm.py', 'infra/ansible/cli-vm.yml', 'infra/ansible/library/battlefield_cli_vm.py',
             'infra/ansible/module_utils/battlefield_cli_vm.py', 'infra/ansible/module_utils/battlefield_cli_vm_policy.py']
    source = hashlib.sha256()
    for path in paths:
        source.update((ROOT / path).read_bytes())
        source.update(b'\0')
    source.update(json.dumps(assets, sort_keys=True).encode())
    image = json.loads((ROOT / 'infra/ansible/rehearsal-image.json').read_text())['24.04']
    result = {'machine_id': data['machine_id'], 'operator': data['user'], 'home': str(Path.home()),
              'revision': args.revision, 'source_digest': source.hexdigest(), 'mode': args.mode,
              'image': {k: image[k] for k in ('url', 'sha256')}, 'adoption': None}
    if args.mode == 'adopt':
        path = args.cli_config or args.inventory.parent / 'cli.json'
        config = load('cli-bootstrap').validate(json.loads(server.private_file(path).read_text()))
        if config['host']['machine_id'] != data['machine_id'] or config['host']['operator'] != data['user'] or not config['guest']:
            raise ValueError('Adoption requires the existing AB-125 host and guest registration on this server')
        result['adoption'] = {**{k: config['host'][k] for k in ('domain_uuid', 'network_uuid')},
                              **{k: config['guest'][k] for k in ('machine_id', 'identity_file')},
                              'known_hosts_file': config['guest'].get('known_hosts_file', str(Path.home() / '.ssh/known_hosts'))}
    return result, assets


def execute(args):
    platform.trusted_revision(args.revision)
    data = server.load_inventory(args.inventory)
    report = server.inspect_host(data, local=True)
    output = server.safe_path(args.report)
    server.outside_repo(output)
    if output.exists() or output.is_symlink():
        raise ValueError('Use a fresh report path; previous review evidence is retained')
    output.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    review_id = ''
    if args.action == 'apply':
        if not args.review:
            raise ValueError('Apply requires --review with the reviewed plan JSON')
        reviewed = json.loads(server.private_file(args.review).read_text())
        if reviewed.get('action') != 'plan' or reviewed.get('revision') != args.revision:
            raise ValueError('Review must be a plan for this exact main revision')
        review_id = reviewed['review_id']
    desired, assets = request(args, data)
    inventory = server.ansible_inventory(data, True, report.get('sudo_executable'))
    with server.operation_lock.acquire() as fd, tempfile.TemporaryDirectory(prefix='battlefield-cli-vm-') as temporary:
        directory = Path(temporary)
        inv, inputs = directory / 'inventory.json', directory / 'inputs.json'
        inv.write_text(json.dumps(inventory))
        inputs.write_text(json.dumps({'cli_vm_request': desired, 'cli_vm_assets': assets,
                                     'cli_vm_action': args.action, 'cli_vm_review_id': review_id,
                                     'cli_vm_report': str(output)}))
        command = [str(server.tools_bin() / 'ansible-playbook'), '-i', str(inv),
                   str(ROOT / 'infra/ansible/cli-vm.yml'), '-e', '@' + str(inputs)]
        if args.become_password_file:
            password = server.private_file(args.become_password_file)
            server.outside_repo(password)
            if password.stat().st_mode & 0o111:
                raise ValueError('Sudo password file must not be executable')
            command += ['--become-password-file', str(password)]
        elif not report['sudo_noninteractive']:
            if not sys.stdin.isatty():
                raise ValueError('Run in a terminal for the host sudo prompt')
            command += ['--ask-become-pass']
        environment = dict(server.clean_ansible_env(), **{server.operation_lock.ENV: str(fd)})
        with server.operation_lock.supervised(command, env=environment, pass_fds=(fd,)) as process:
            if process.wait():
                raise ValueError('CLI VM operation stopped; retained resources were not deleted')
    print(json.dumps(json.loads(server.private_file(output).read_text()), indent=2))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['plan', 'apply', 'verify'])
    parser.add_argument('--mode', choices=['adopt', 'create'], required=True)
    parser.add_argument('--revision', required=True, help='Full reviewed origin/main SHA')
    parser.add_argument('--inventory', type=server.safe_path, default=Path.home() / '.config/autonomous-battlefield/server/inventory.json')
    parser.add_argument('--cli-config', type=server.safe_path, help='Existing AB-125 registration for adoption')
    parser.add_argument('--review', type=server.safe_path, help='Reviewed plan report; required for apply')
    parser.add_argument('--report', type=server.safe_path, required=True, help='Fresh private JSON path outside the checkout')
    parser.add_argument('--become-password-file', type=server.safe_path, help='Optional private file; default prompts interactively')
    args = parser.parse_args()
    os.umask(0o077)
    execute(args)


if __name__ == '__main__':
    try:
        main()
    except KeyboardInterrupt:
        sys.exit(130)
    except (ValueError, OSError, KeyError, subprocess.SubprocessError) as error:
        print('CLI VM: ' + (str(error) if isinstance(error, ValueError) else type(error).__name__), file=sys.stderr)
        sys.exit(1)
