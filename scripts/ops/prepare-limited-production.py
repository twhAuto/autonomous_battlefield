#!/usr/bin/env python3
"""Validate consolidated production settings and render temporary tunnel resources."""
import argparse
import importlib.util
import json
import hashlib
import os
from pathlib import Path
import subprocess
import tempfile

HELPER = Path(__file__).resolve().parents[1] / 'lib/application-env.cjs'

def read_env(path):
    result = subprocess.run(['node', str(HELPER), 'json', str(path)], capture_output=True, text=True)
    if result.returncode:
        raise ValueError('Invalid literal configuration file')
    return json.loads(result.stdout)

def prepare(application, deployment, output):
    result = subprocess.run(['node', str(HELPER), 'validate', str(application), 'production'], capture_output=True)
    if result.returncode:
        raise ValueError('Invalid production application configuration')
    app = read_env(application)
    profile = read_env(deployment)
    if profile.get('BF_ENV') != 'production':
        raise ValueError('Production profile required')
    credential = Path(profile['BF_TUNNEL_CREDENTIALS_FILE'])
    if not credential.is_absolute():
        credential = deployment.parent / credential
    if credential.stat().st_mode & 0o077:
        raise ValueError('Tunnel credentials must be private (chmod 600)')
    data = json.loads(credential.read_text())
    if data.get('TunnelID') != profile['BF_TUNNEL_ID'] or not data.get('TunnelSecret') or not data.get('AccountTag'):
        raise ValueError('Tunnel credentials are incomplete or do not match the profile')
    spec = importlib.util.spec_from_file_location('renderer', Path(__file__).with_name('render-limited-launch.py'))
    renderer = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(renderer)
    roles = json.loads(app['BATTLEFIELD_ACCESS_ROLES'])
    args = argparse.Namespace(hostname=app['BATTLEFIELD_PUBLIC_ORIGIN'].removeprefix('https://').rstrip('/'),
        team=app['BATTLEFIELD_ACCESS_ISSUER'].removeprefix('https://').rstrip('/').removesuffix('.cloudflareaccess.com'),
        audience=app['BATTLEFIELD_ACCESS_AUDIENCE'], tunnel_id=profile['BF_TUNNEL_ID'],
        cloudflared_image=profile['BF_TUNNEL_IMAGE'], admin=roles.get('admin',[]),
        tester=roles.get('tester',[]), player=roles.get('player',[]), output=str(output),
        guest_hostname=app.get('BATTLEFIELD_GUEST_ORIGIN', '').removeprefix('https://').rstrip('/'),
        guest_audience=app.get('BATTLEFIELD_ACCESS_GUEST_AUDIENCE', ''),
        credential_checksum=hashlib.sha256(credential.read_bytes()).hexdigest())
    renderer.render(args)

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, help='Private deployment scratch directory; omitted for validation only')
    args = parser.parse_args()
    os.umask(0o077)
    deployment = Path(os.environ['BATTLEFIELD_ENV_FILE'])
    application = deployment.parent / '.env'
    if args.output:
        prepare(application, deployment, args.output)
    else:
        with tempfile.TemporaryDirectory(prefix='battlefield-tunnel-') as scratch:
            prepare(application, deployment, Path(scratch))
    print('Production application and tunnel configuration validated. No live workloads changed.')

if __name__ == '__main__':
    try:
        main()
    except (ValueError, KeyError, OSError, subprocess.CalledProcessError):
        raise SystemExit('Preparation failed: check production .env, deployment.env and tunnel credentials. No secret values are printed.')
