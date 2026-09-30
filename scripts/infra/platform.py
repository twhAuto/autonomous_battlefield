#!/usr/bin/env python3
"""Review-bound host Ansible operations for the dedicated Jenkins job and operator."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('bootstrap', ROOT / 'scripts/infra/server.py')
bootstrap = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bootstrap)
lock = bootstrap.operation_lock


def git(*args):
    return subprocess.check_output(['git', '-C', str(ROOT), *args], stderr=subprocess.DEVNULL, text=True).strip()


def trusted_revision(expected):
    if os.environ.get('CHANGE_ID') or os.environ.get('CHANGE_TARGET'):
        raise ValueError('PR jobs cannot invoke platform operations')
    if not re.fullmatch('[0-9a-f]{40}', expected):
        raise ValueError('A full reviewed revision is required')
    if git('rev-parse', 'HEAD') != expected or git('rev-parse', 'refs/remotes/origin/main') != expected:
        raise ValueError('Platform operations require the checked-out origin/main revision')
    if git('status', '--porcelain', '--untracked-files=normal'):
        raise ValueError('Platform checkout must be clean; use an evidence directory outside the checkout')


def fingerprint(args, inventory):
    files = [args.inventory, args.vault, *inventory.get('ca_files', [])]
    digest = hashlib.sha256()
    for value in files:
        path = bootstrap.safe_path(value)
        digest.update(path.read_bytes())
        digest.update(b'\0')
    return digest.hexdigest()


def check_inputs(args):
    inventory = bootstrap.load_inventory(args.inventory)
    for value in [args.vault, args.vault_password_file, args.become_password_file]:
        path = bootstrap.private_file(value)
        bootstrap.outside_repo(path)
        if path.stat().st_mode & 0o111:
            raise ValueError('Credential files must not be executable')
    if not bootstrap.private_file(args.vault).read_bytes().startswith(b'$ANSIBLE_VAULT;'):
        raise ValueError('Expected encrypted Vault inputs')
    bootstrap.inspect_host(inventory, local=True)
    return inventory


def run_action(args, action, directory, fd):
    report = directory / (action + '.json')
    command = [sys.executable, str(ROOT / 'scripts/infra/server.py'), action, '--local',
               '--inventory', str(args.inventory), '--vault', str(args.vault),
               '--vault-password-file', str(args.vault_password_file),
               '--become-password-file', str(args.become_password_file), '--report-file', str(report)]
    # The reviewed callback is the only evidence channel. Never echo captured module output.
    code = lock.run(command, fd, quiet=True)
    if code or not report.is_file():
        raise ValueError(f'{action} failed; run the bootstrap interactively for diagnostics (no captured output published)')
    data = json.loads(report.read_text())
    if data.get('schema') != 1 or data.get('failed') or not data.get('tasks'):
        raise ValueError(f'{action} did not produce successful runtime evidence')
    return data


def write_new(path, value):
    bootstrap.write_new(path, (json.dumps(value, indent=2) + '\n').encode())


def check_receipt(receipt, args, identity):
    if receipt.get('schema') != 1 or receipt.get('revision') != args.revision or receipt.get('inputs') != identity:
        raise ValueError('Plan target, revision or encrypted inputs changed; run and review a new plan')
    age = time.time() - receipt.get('created', 0)
    if age < 0 or age > 1800:
        raise ValueError('Plan expired; run and review a new plan')


def execute(args):
    trusted_revision(args.revision)
    receipt_path = bootstrap.safe_path(args.receipt)
    bootstrap.outside_repo(receipt_path)
    with lock.acquire() as fd, tempfile.TemporaryDirectory(prefix='battlefield-platform-') as temporary:
        inventory = check_inputs(args)
        identity = fingerprint(args, inventory)
        directory = Path(temporary)
        if args.action == 'plan':
            report = run_action(args, 'plan', directory, fd)
            write_new(receipt_path, {'schema': 1, 'revision': args.revision, 'inputs': identity,
                                    'created': time.time(), 'change_digest': report['change_digest']})
        elif args.action == 'apply':
            receipt = json.loads(bootstrap.private_file(receipt_path).read_text())
            check_receipt(receipt, args, identity)
            fresh = run_action(args, 'plan', directory, fd)
            if fresh['change_digest'] != receipt['change_digest']:
                raise ValueError('Host drift changed the reviewed plan; run and review a new plan')
            # Consume approval before mutation. Interrupted or failed apply requires a fresh review.
            receipt_path.unlink()
            applied = run_action(args, 'apply', directory, fd)
            report = run_action(args, 'verify', directory, fd)
            report['applied_tasks'] = applied['tasks']
        else:
            report = run_action(args, 'verify', directory, fd)
        report.update(action=args.action, revision=args.revision, target={
            key: inventory.get(key, '') for key in ('host', 'user', 'machine_id', 'data_uuid', 'data_mount')})
        write_new(bootstrap.safe_path(args.report), report)
    print(f'{args.action} completed; secret-safe report written')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['plan', 'apply', 'verify'])
    for name in ['inventory', 'vault', 'vault-password-file', 'become-password-file', 'receipt', 'report']:
        parser.add_argument('--' + name, required=True, type=bootstrap.safe_path)
    parser.add_argument('--revision', required=True)
    args = parser.parse_args()
    os.umask(0o077)
    execute(args)


if __name__ == '__main__':
    try:
        main()
    except KeyboardInterrupt:
        sys.exit(130)
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        print('Platform: ' + (str(error) if isinstance(error, ValueError) else 'Operation failed; no private output published'), file=sys.stderr)
        sys.exit(1)
