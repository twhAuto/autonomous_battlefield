#!/usr/bin/env python3
"""External Jenkins/SonarQube maintenance; public registration, encrypted inputs."""
import json
import hashlib
import os
from pathlib import Path
import re
import secrets
import subprocess
import sys
import tempfile
import urllib.parse

ROOT = Path(__file__).resolve().parents[2]
CONFIG_KEYS = {'schema', 'machine_id', 'operator', 'home', 'mode', 'jenkins_admin',
               'jenkins_url', 'agent_unit_sha256'}


def validate(config):
    if not isinstance(config, dict) or set(config) != CONFIG_KEYS or type(config['schema']) is not int or config['schema'] != 1:
        raise ValueError('Expected CI registration schema 1')
    for key, pattern in {'machine_id': r'[0-9a-f]{32}', 'operator': r'[a-z_][a-z0-9_-]*',
                         'jenkins_admin': r'[A-Za-z0-9_-]+', 'home': r'/home/[a-z_][a-z0-9_-]*',
                         'agent_unit_sha256': r'(?:[0-9a-f]{64})?'}.items():
        if not isinstance(config[key], str) or not re.fullmatch(pattern, config[key]):
            raise ValueError('Invalid CI registration field: ' + key)
    if config['home'] != '/home/' + config['operator'] or config['mode'] not in ('install', 'adopt'):
        raise ValueError('CI requires the registered operator home and an explicit install/adopt mode')
    url = urllib.parse.urlsplit(config['jenkins_url'])
    if url.scheme not in ('https', 'http') or not url.hostname or url.username or url.password or url.query or url.fragment or url.path not in ('', '/'):
        raise ValueError('CI root URL must be an HTTP(S) origin without credentials')
    if not re.fullmatch(r'https?://[A-Za-z0-9.-]+(?::[0-9]{1,5})?/?', config['jenkins_url']):
        raise ValueError('Invalid CI root URL')
    return config


def register(args, data, report, server):
    destination = args.ci_config or args.inventory.parent / 'ci.json'
    code = '''import hashlib,json,pathlib,sys
p=json.load(sys.stdin); unit=pathlib.Path(p['home'])/'.config/systemd/user/jenkins-agent.service'
if unit.is_symlink(): raise ValueError('Redirected agent unit')
print(json.dumps({'agent_unit_sha256':hashlib.sha256(unit.read_bytes()).hexdigest() if unit.is_file() else '',
 'controller_exists':pathlib.Path('/var/lib/jenkins/config.xml').exists()}))
'''
    observed = json.loads(server.remote_python(data, code, json.dumps({'home': report['home']}).encode(), args.local))
    if args.ci_install and (observed['controller_exists'] or observed['agent_unit_sha256']):
        raise ValueError('Fresh CI install requires no existing controller/agent; use adoption')
    if not args.ci_install and not observed['controller_exists']:
        raise ValueError('Adoption requires an existing/restored Jenkins home; use --ci-install for a clean host')
    config = validate({'schema': 1, 'machine_id': data['machine_id'], 'operator': data['user'],
                       'home': report['home'], 'mode': 'install' if args.ci_install else 'adopt',
                       'jenkins_admin': args.ci_admin, 'jenkins_url': args.ci_url or 'http://' + data['host'] + ':8080/',
                       'agent_unit_sha256': observed['agent_unit_sha256']})
    manifest_path = destination.parent / 'ci-files.json'
    token_path = Path(os.environ.get('XDG_CONFIG_HOME', Path.home() / '.config')) / 'jenkins/token'
    if args.ci_install:
        sources = {'ci_admin_password': 'ci-admin-password', 'ci_sonar_admin_password': 'ci-sonar-admin-password',
                   'ci_database_password': 'ci-database-password', 'ci_credentials': 'ci-credentials.json'}
        manifest = {name: {'source': 'controller', 'path': str(destination.parent / filename)} for name, filename in sources.items()}
    else:
        manifest = {'ci_jenkins_api_token': {'source': 'controller', 'path': str(token_path)}}
    server.publish_registration(destination, config, manifest_path, manifest)
    if args.ci_install:
        for name in ('ci_admin_password', 'ci_sonar_admin_password', 'ci_database_password'):
            target = Path(manifest[name]['path'])
            if not target.exists():
                server.write_new(target, ('aA1!' + secrets.token_urlsafe(36) + '\n').encode())
    print('Registered CI identity and input manifest. No server services or credentials changed.')
    print('Input manifest: ' + str(manifest_path))


def source_hash():
    files = [ROOT / name for name in ('scripts/infra/ci-bootstrap.py', 'scripts/infra/ci-runtime.py',
             'scripts/infra/seed-platform.py', 'scripts/infra/server.py', 'scripts/infra/operation-lock.py',
             'infra/ansible/ci.yml', 'infra/jenkins/Jenkinsfile.platform', 'infra/jenkins/Jenkinsfile.vm', 'infra/jenkins/Jenkinsfile.k3s')]
    for directory in ('infra/jenkins', 'infra/ansible/roles/ci_services'):
        files.extend(p for p in (ROOT / directory).rglob('*') if p.is_file())
    return hashlib.sha256(json.dumps({str(p.relative_to(ROOT)): hashlib.sha256(p.read_bytes()).hexdigest()
                                     for p in sorted(files)}, sort_keys=True).encode()).hexdigest()


def playbook(args, data, report, server):
    if any(os.environ.get(key) for key in ('JENKINS_HOME', 'BUILD_URL', 'JENKINS_NODE_COOKIE')):
        raise ValueError('Controller maintenance must run outside Jenkins, from an operator terminal')
    path = args.ci_config or args.inventory.parent / 'ci.json'
    server.outside_repo(path)
    config = validate(json.loads(server.private_file(path).read_text()))
    if (config['machine_id'], config['operator']) != (data['machine_id'], data['user']):
        raise ValueError('CI registration belongs to a different host/operator')
    if not args.vault:
        raise ValueError('CI commands require --vault with encrypted CI inputs')
    vault = server.private_file(args.vault)
    server.outside_repo(vault)
    if not vault.read_bytes().startswith(b'$ANSIBLE_VAULT;'):
        raise ValueError('Expected encrypted CI Vault inputs')
    action = args.command.removeprefix('ci-')
    inventory = server.ansible_inventory(data, args.local, report.get('sudo_executable'))
    variables = inventory['all']['children']['bootstrap']['hosts']['server']
    variables.update(ci_config=config, ci_action=action, ci_vault_file=str(vault),
                     ci_source=str(ROOT), ci_source_hash=source_hash(),
                     ci_ca_hashes={Path(p).name: hashlib.sha256(Path(p).read_bytes()).hexdigest() for p in variables['bootstrap_ca_files']}, ci_lock=json.loads((ROOT / 'infra/jenkins/controller-lock.json').read_text()))
    with tempfile.TemporaryDirectory(prefix='battlefield-ci-') as directory:
        target = Path(directory) / 'inventory.json'
        target.write_text(json.dumps(inventory))
        command = [str(server.tools_bin() / 'ansible-playbook'), '-i', str(target), str(ROOT / 'infra/ansible/ci.yml'),
                   *server.password_args(args.vault_password_file)]
        if action == 'plan':
            command += ['--check', '--diff']
        if args.become_password_file:
            password = server.private_file(args.become_password_file)
            server.outside_repo(password)
            if password.stat().st_mode & 0o111:
                raise ValueError('Sudo password input must not be executable')
            command += ['--become-password-file', str(password)]
        elif not report['sudo_noninteractive']:
            if not sys.stdin.isatty():
                raise ValueError('Use a terminal for sudo, or a private --become-password-file')
            command += ['--ask-become-pass']
        environment = server.clean_ansible_env(args.report_file)
        if action == 'apply':
            with server.host_lock(data, args.local) as lock:
                server.run_playbook(command, lock, environment)
        else:
            subprocess.run(command, env=environment, check=True)
