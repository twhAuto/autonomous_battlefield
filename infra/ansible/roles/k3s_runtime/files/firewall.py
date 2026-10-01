#!/usr/bin/env python3
"""Verify the live owned nftables table against its last applied configuration."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess

CONFIG = Path('/etc/battlefield-k3s/firewall.nft')
STATE = Path('/etc/battlefield-k3s/firewall.json')


def normalize(value):
    if isinstance(value, dict):
        return {key: normalize(item) for key, item in value.items() if key != 'handle'}
    if isinstance(value, list):
        return [normalize(item) for item in value if not isinstance(item, dict) or 'metainfo' not in item]
    return value


def current():
    if CONFIG.is_symlink() or STATE.is_symlink():
        raise ValueError('Firewall ownership files must not be symlinks')
    result = subprocess.run(['/usr/sbin/nft', '--json', 'list', 'table', 'inet', 'battlefield_k3s'],
                            capture_output=True, text=True, check=False, timeout=15)
    if result.returncode:
        raise ValueError('Managed firewall table is missing')
    rules = json.dumps(normalize(json.loads(result.stdout)), sort_keys=True).encode()
    return {'schema': 1, 'config_sha256': hashlib.sha256(CONFIG.read_bytes()).hexdigest(),
            'rules_sha256': hashlib.sha256(rules).hexdigest()}


def matches():
    try:
        actual = current()
        return json.loads(STATE.read_text()) == actual
    except (ValueError, OSError, subprocess.SubprocessError):
        return False


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('action', choices=['check', 'record', 'verify'])
    args = parser.parse_args()
    if os.geteuid() != 0:
        raise ValueError('Firewall checks require root')
    if args.action == 'record':
        # systemd calls this only after nft successfully commits the owned table.
        # Both helpers are installed together in the same private directory.
        from storage import atomic
        atomic(STATE, (json.dumps(current(), sort_keys=True) + '\n').encode())
    elif args.action == 'verify' and not matches():
        raise ValueError('Managed firewall has runtime drift')
    else:
        print(json.dumps({'changed': not matches()}))


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        raise SystemExit('Guest firewall: ' + (str(error) if isinstance(error, ValueError) else 'verification failed'))
