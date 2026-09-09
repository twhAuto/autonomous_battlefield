#!/usr/bin/env python3
"""Validate rendered launch settings and prepare files without modifying live workloads."""
import argparse
import base64
import importlib.util
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import tempfile


def read_env(path):
    values = {}
    for line in path.read_text().splitlines():
        if not line.strip() or line.lstrip().startswith('#'):
            continue
        key, sep, value = line.partition('=')
        if not sep or not re.fullmatch(r'[A-Z_][A-Z0-9_]*', key) or key in values:
            raise ValueError('Invalid or duplicate setting in ' + str(path))
        values[key] = value
    return values


def validate_ai(values):
    keys = ('BATTLEFIELD_AGENT_SECRET', 'BATTLEFIELD_MCP_CAPABILITY_SECRET')
    if any(len(values.get(k, '')) < 32 for k in keys) or values[keys[0]] == values[keys[1]]:
        raise ValueError('AI file requires distinct worker/capability secrets of at least 32 characters')
    for key in ('CODEX_CLI_PROFILE_AVAILABLE', 'GEMINI_CLI_PROFILE_AVAILABLE',
                'OPENAI_PROFILE_AVAILABLE', 'GEMINI_PROFILE_AVAILABLE'):
        if values.get(key, '').lower() != 'false':
            raise ValueError(key + ' must be false for initial owner-only launch')


def write_env(path, values):
    for key, value in values.items():
        if not re.fullmatch(r'[A-Z_][A-Z0-9_]*', key) or any(c in value for c in '\r\n\0'):
            raise ValueError('Secret cannot be represented as an env file')
    with path.open('x') as file:
        file.write(''.join(f'{k}={v}\n' for k, v in values.items()))
    path.chmod(0o600)


def kube(*args, **kwargs):
    return subprocess.run(['kubectl', '--context', os.environ['KUBERNETES_CONTEXT'],
                           '--request-timeout=60s', '-n', 'battlefield', *args], check=True, **kwargs)


def validate_rendered(directory, credential_dir):
    auth = read_env(directory / 'auth.env')
    rendered = json.loads((directory / 'tunnel.json').read_text())
    cm = next(item for item in rendered['items'] if item['kind'] == 'ConfigMap')
    deployment = next(item for item in rendered['items'] if item['kind'] == 'Deployment')
    config = json.loads(cm['data']['config.yml'])
    roles = json.loads(auth['BATTLEFIELD_ACCESS_ROLES'])
    issuer = auth['BATTLEFIELD_ACCESS_ISSUER']
    match = re.fullmatch(r'https://([a-z0-9-]+)\.cloudflareaccess\.com', issuer)
    origin = auth['BATTLEFIELD_PUBLIC_ORIGIN']
    if not match or not origin.startswith('https://'):
        raise ValueError('Invalid production origin or issuer')
    if not re.fullmatch(r'[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}', config['tunnel']):
        raise ValueError('Invalid tunnel UUID')
    credential = credential_dir / (config['tunnel'] + '.json')
    if credential.stat().st_mode & 0o077:
        raise ValueError('Tunnel credentials must be private (chmod 600)')
    if json.loads(credential.read_text()).get('TunnelID') != config['tunnel']:
        raise ValueError('Tunnel credential UUID does not match rendered settings')
    spec = importlib.util.spec_from_file_location('renderer', Path(__file__).with_name('render-limited-launch.py'))
    renderer = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(renderer)
    with tempfile.TemporaryDirectory() as scratch:
        args = argparse.Namespace(hostname=origin.removeprefix('https://'), team=match[1],
            audience=auth['BATTLEFIELD_ACCESS_AUDIENCE'], tunnel_id=config['tunnel'],
            cloudflared_image=deployment['spec']['template']['spec']['containers'][0]['image'],
            admin=roles.get('admin', []), developer=roles.get('developer', []),
            player=roles.get('player', []), output=scratch)
        renderer.render(args)
        expected = Path(scratch)
        if auth != read_env(expected / 'auth.env') or rendered != json.loads((expected / 'tunnel.json').read_text()):
            raise ValueError('Rendered files differ from the protected launch template; regenerate them')
    return auth


def prepare_ai(path):
    if path.exists():
        validate_ai(read_env(path))
        return
    result = kube('get', 'secret', 'battlefield-ai', '--ignore-not-found', '-o', 'json', capture_output=True, text=True)
    stored = json.loads(result.stdout).get('data', {}) if result.stdout.strip() else {}
    values = {key: base64.b64decode(value, validate=True).decode() for key, value in stored.items()}
    for key in ('BATTLEFIELD_AGENT_SECRET', 'BATTLEFIELD_MCP_CAPABILITY_SECRET'):
        if not values.get(key):
            values[key] = secrets.token_hex(32)
    for key in ('CODEX_CLI_PROFILE_AVAILABLE', 'GEMINI_CLI_PROFILE_AVAILABLE',
                'OPENAI_PROFILE_AVAILABLE', 'GEMINI_PROFILE_AVAILABLE', 'BATTLEFIELD_CLI_ISOLATION_VERIFIED'):
        values[key] = 'false'
    for key in ('OPENAI_API_KEY', 'OPENAI_MODEL', 'GEMINI_API_KEY', 'GEMINI_MODEL'):
        values.setdefault(key, '')
    validate_ai(values)
    write_env(path, values)


def backup_database(directory):
    directory.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(prefix='pre-access-', suffix='.dump', dir=directory, delete=False) as file:
        path = Path(file.name)
        kube('exec', 'battlefield-postgres-0', '--', 'pg_dump', '-U', 'battlefield', '-d', 'battlefield', '-Fc', stdout=file)
    if path.stat().st_size == 0:
        raise ValueError('Empty database backup')
    with path.open('rb') as source, Path(str(path) + '.list').open('x') as listing:
        kube('exec', '-i', 'battlefield-postgres-0', '--', 'pg_restore', '--list', stdin=source, stdout=listing)
    print('Database backup archive validated:', path)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--launch-dir', type=Path, default=Path.home()/'.config/autonomous-battlefield/production/friends-launch')
    parser.add_argument('--credential-dir', type=Path, default=Path.home()/'.cloudflared')
    parser.add_argument('--backup', action='store_true', help='Create and validate a fresh database archive')
    args = parser.parse_args()
    os.umask(0o077)
    auth = validate_rendered(args.launch_dir, args.credential_dir)
    auth_path = Path(os.environ['BATTLEFIELD_AUTH_SECRET_FILE'])
    ai_path = Path(os.environ['BATTLEFIELD_AI_SECRET_FILE'])
    # Refuse to overwrite operator edits or change the current production login implicitly.
    if auth_path.exists() and read_env(auth_path) != auth:
        raise ValueError('Production auth file differs from rendered settings; review before replacing it')
    auth_path.parent.mkdir(parents=True, exist_ok=True)
    ai_path.parent.mkdir(parents=True, exist_ok=True)
    prepare_ai(ai_path)
    if not auth_path.exists():
        write_env(auth_path, auth)
    auth_path.chmod(0o600)
    ai_path.chmod(0o600)
    if args.backup:
        backup_database(Path.home()/'.local/state/battlefield/backups')
    print('Production launch files checked; existing credentials preserved. No live workloads changed.')
    print('Before public activation: verify NetworkPolicy enforcement, direct-origin IPv4/IPv6 blocking and MFA.')
    print('Pipeline prerequisite: BATTLEFIELD_ACCESS_TOKEN_FILE must reference a fresh invited-user Access JWT for production smoke tests.')
    print('This command does not create DNS records, start the tunnel, deploy images, or certify public readiness.')


if __name__ == '__main__':
    try:
        main()
    except (ValueError, KeyError, OSError, StopIteration, subprocess.CalledProcessError):
        raise SystemExit('Preparation failed: check private files, configuration consistency and kubectl connectivity. No secret values are printed.')
