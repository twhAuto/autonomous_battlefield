#!/usr/bin/env python3
"""Capture and adopt/restore the existing local HTTPS routes and CA (AB-142)."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
sys.path = [p for p in sys.path if Path(p).resolve() != Path(__file__).resolve().parent]


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, ROOT / 'scripts/infra' / filename)
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
    return module


platform = load('https_platform', 'platform.py')
server = platform.bootstrap
runtime = load('https_runtime', 'https-runtime.py')


def registered_routing():
    """Derive only non-secret routing identifiers from the verified production profile."""
    profile = server.private_file(Path.home() / '.config/autonomous-battlefield/production/deployment.env')
    parsed = runtime.command('node', str(ROOT / 'scripts/lib/application-env.cjs'), 'profile', str(profile)).decode()
    values = dict(line.split('=', 1) for line in parsed.splitlines())
    runtime.require(values['BF_DRIVER'] == 'k3s' and values['BF_ENV'] == 'production', 'HTTPS migration requires registered K3s production')
    kubeconfig = server.private_file(values['BF_KUBECONFIG'])
    def get(*args):
        return json.loads(runtime.command('kubectl', '--kubeconfig', str(kubeconfig), '--context', values['BF_CONTEXT'],
                                          '--request-timeout=15s', 'get', *args, '-o', 'json'))
    namespace = get('namespace', 'kube-system')
    runtime.require(namespace['metadata']['uid'] == values['BF_UID'], 'Registered K3s cluster identity changed')
    nodes = get('nodes')['items']
    runtime.require(len(nodes) == 1 and nodes[0]['metadata']['name'] == values['BF_CLUSTER'] and
                    any(c['type'] == 'Ready' and c['status'] == 'True' for c in nodes[0]['status']['conditions']),
                    'Expected the registered ready single-node K3s VM')
    addresses = [a['address'] for a in nodes[0]['status']['addresses'] if a['type'] == 'InternalIP']
    runtime.require(len(addresses) == 1, 'Expected one K3s internal address')
    services = get('services', '-n', 'envoy-gateway-system', '-l',
                   'gateway.envoyproxy.io/owning-gateway-name=battlefield-local,gateway.envoyproxy.io/owning-gateway-namespace=battlefield')['items']
    runtime.require(len(services) == 1 and services[0]['spec']['type'] == 'NodePort' and
                    any(p.get('nodePort') == 30080 and p['port'] == 80 for p in services[0]['spec']['ports']),
                    'Registered K3s monitoring gateway is missing')
    routing = {'gateway': addresses[0] + ':30080', 'public_origin': values['BF_URL'].rstrip('/'),
               'cluster_uid': values['BF_UID'], 'node_uid': nodes[0]['metadata']['uid']}
    return routing


def target(value):
    path = server.safe_path(value); server.outside_repo(path)
    if path.exists() or path.is_symlink():
        raise ValueError('Use a fresh output path; existing recovery material is retained')
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    info = path.parent.stat()
    if info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise ValueError('HTTPS outputs need a private operator-owned directory')
    return path


def execute(args):
    data = server.load_inventory(args.inventory)
    observed = server.inspect_host(data, local=True)
    password_args = server.password_args(args.vault_password_file)
    if args.vault_password_file:
        server.outside_repo(server.private_file(args.vault_password_file))
    report = target(args.report) if args.report else None
    vault = server.safe_path(args.vault); server.outside_repo(vault)
    with server.operation_lock.acquire() as fd:
        if args.action == 'capture':
            target(vault)
            public = target(str(vault) + '.root.crt')
            if not observed['sudo_noninteractive'] and not sys.stdin.isatty():
                raise ValueError('Capture needs a terminal for your sudo password')
            result = subprocess.run([observed['sudo_executable'], '--', sys.executable,
                                     str(ROOT / 'scripts/infra/https-runtime.py'), 'capture'],
                                    stdout=subprocess.PIPE, check=True)
            captured = json.loads(result.stdout)
            config = captured['config']
            fingerprint = runtime.validate(config)
            with tempfile.TemporaryDirectory(prefix='battlefield-https-vault-') as directory:
                encrypted = Path(directory) / 'inputs.vault'
                subprocess.run([str(server.tools_bin() / 'ansible-vault'), 'encrypt', '--output', str(encrypted), *password_args],
                               input=json.dumps(config).encode(), env=server.clean_ansible_env(), check=True)
                server.write_new(vault, encrypted.read_bytes())
            server.write_new(public, config['root_certificate'].encode())
            print(json.dumps({'captured': True, 'ca_sha256': fingerprint, 'routes': captured['routes'],
                              'upstreams_healthy': captured['upstreams_healthy'], 'adoption_verified': False,
                              'vault': str(vault), 'public_root': str(public), 'services_changed': False}))
            return
        platform.trusted_revision(args.revision or '')
        if report is None:
            raise ValueError('Plan/apply/verify need --report with a fresh private path')
        if not server.private_file(vault).read_bytes().startswith(b'$ANSIBLE_VAULT;'):
            raise ValueError('HTTPS restore inputs must be encrypted with Ansible Vault')
        result = subprocess.run([str(server.tools_bin() / 'ansible-vault'), 'view', str(vault), *password_args],
                                stdout=subprocess.PIPE, env=server.clean_ansible_env(), check=True)
        config = json.loads(result.stdout); runtime.validate(config)
        routing = registered_routing() if args.k3s_routes else None
        runtime.desired_configuration(config, routing)
        review_id = ''
        if args.action == 'apply':
            if not args.review:
                raise ValueError('Apply requires the reviewed plan report')
            previous = json.loads(server.private_file(args.review).read_text())
            if previous['action'] != 'plan' or previous['revision'] != args.revision or previous['mode'] != args.mode:
                raise ValueError('Review must match this exact revision and mode')
            review_id = previous['review_id']
        with tempfile.TemporaryDirectory(prefix='battlefield-https-') as directory:
            inputs, inventory = Path(directory) / 'inputs.json', Path(directory) / 'inventory.json'
            inputs.write_text(json.dumps({'action': args.action, 'review_id': review_id, 'report': str(report),
                'request': {'config': config, 'machine_id': data['machine_id'], 'operator': data['user'],
                            'revision': args.revision, 'mode': args.mode, 'routing': routing}}))
            inventory.write_text(json.dumps(server.ansible_inventory(data, True, observed['sudo_executable'])))
            command = [str(server.tools_bin() / 'ansible-playbook'), '-i', str(inventory),
                       str(ROOT / 'infra/ansible/https.yml'), '-e', json.dumps({
                           'https_inputs_file': str(inputs), 'https_runtime': str(ROOT / 'scripts/infra/https-runtime.py')})]
            if args.become_password_file:
                password = server.private_file(args.become_password_file); server.outside_repo(password)
                if password.stat().st_mode & 0o111:
                    raise ValueError('Sudo password file must not be executable')
                command += ['--become-password-file', str(password)]
            elif not observed['sudo_noninteractive']:
                if not sys.stdin.isatty():
                    raise ValueError('Use a terminal for the sudo prompt')
                command += ['--ask-become-pass']
            environment = dict(server.clean_ansible_env(), **{server.operation_lock.ENV: str(fd)})
            with server.operation_lock.supervised(command, env=environment, pass_fds=(fd,)) as process:
                if process.wait():
                    raise ValueError('HTTPS operation failed; inspect the safe task message')
        print(json.dumps(json.loads(server.private_file(report).read_text()), indent=2))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['capture', 'plan', 'apply', 'verify'])
    parser.add_argument('--inventory', type=server.safe_path, default=Path.home() / '.config/autonomous-battlefield/server/inventory.json')
    parser.add_argument('--vault', type=server.safe_path, required=True)
    parser.add_argument('--vault-password-file', type=server.safe_path)
    parser.add_argument('--become-password-file', type=server.safe_path)
    parser.add_argument('--mode', choices=['adopt', 'install', 'migrate'], default='adopt')
    parser.add_argument('--k3s-routes', action='store_true', help='Use the registered production public origin and K3s monitoring gateway')
    parser.add_argument('--revision')
    parser.add_argument('--report', type=server.safe_path)
    parser.add_argument('--review', type=server.safe_path)
    os.umask(0o077)
    execute(parser.parse_args())


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, KeyError, TypeError, subprocess.SubprocessError) as error:
        print('HTTPS: ' + (str(error) if type(error) is ValueError else type(error).__name__), file=sys.stderr)
        sys.exit(1)
