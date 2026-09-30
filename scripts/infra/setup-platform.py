#!/usr/bin/env python3
"""One-time workstation setup: register, seal inputs and seed the Jenkins host job."""
from contextlib import contextmanager
import argparse
import getpass
import importlib.util
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('seed_platform', ROOT/'scripts/infra/seed-platform.py')
seed = importlib.util.module_from_spec(spec)
spec.loader.exec_module(seed)
bootstrap = seed.bootstrap


def password(label, confirm=False):
    value = getpass.getpass(label + ': ')
    if not value or '\n' in value or '\r' in value:
        raise ValueError('A nonempty single-line secret is required')
    if confirm and value != getpass.getpass('Confirm ' + label + ': '):
        raise ValueError('Values did not match')
    return value


@contextmanager
def tunnel(inventory):
    with socket.socket() as probe:
        probe.bind(('127.0.0.1', 0))
        port = probe.getsockname()[1]
    command = bootstrap.ssh(inventory)
    command[-1:-1] = ['-o', 'ExitOnForwardFailure=yes', '-N', '-L', f'127.0.0.1:{port}:127.0.0.1:8080']
    process = subprocess.Popen(command, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        for _ in range(100):
            if process.poll() is not None:
                raise ValueError('Jenkins SSH tunnel failed; check host key and SSH access')
            try:
                with socket.create_connection(('127.0.0.1', port), timeout=.2):
                    break
            except OSError:
                time.sleep(.1)
        else:
            raise ValueError('Jenkins SSH tunnel did not become ready')
        yield f'http://127.0.0.1:{port}'
    finally:
        process.terminate()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill(); process.wait()


def prepare_inventory(args):
    if not args.inventory.exists():
        subprocess.run([sys.executable,str(ROOT/'scripts/infra/server.py'),'init',
            '--inventory',str(args.inventory),'--host',args.host,'--user',args.user,
            '--identity-file',str(args.identity_file)],check=True)
    inventory = bootstrap.load_inventory(args.inventory)
    if inventory['host'] != args.host or inventory['user'] != args.user or inventory['identity_file'] != str(args.identity_file):
        raise ValueError('Existing inventory identifies a different host/operator/key; inspect it first')
    if inventory.get('ca_files'):
        raise ValueError('CA paths require host-side validation before Jenkins local mode; use the manual seed procedure')
    if args.data_uuid:
        if inventory.get('data_uuid') and (inventory['data_uuid'] != args.data_uuid or inventory.get('data_mount','/srv/battlefield-data') != args.data_mount):
            raise ValueError('Existing data storage selection differs; do not remap it through setup')
        inventory.update(data_uuid=args.data_uuid, data_mount=args.data_mount)
        bootstrap.validate_inventory(inventory)
        # This file is operator-owned and outside Git; keep the previous registration recoverable.
        if 'data_uuid' not in json.loads(args.inventory.read_text()):
            backup=args.inventory.with_name('inventory.before-platform.json')
            if not backup.exists():bootstrap.write_new(backup,args.inventory.read_bytes())
        with tempfile.NamedTemporaryFile(dir=args.inventory.parent, prefix='.inventory-', delete=False) as temporary:
            pending = Path(temporary.name)
            try:
                temporary.write((json.dumps(inventory,indent=2)+'\n').encode())
                temporary.flush()
                os.fsync(temporary.fileno())
                os.replace(pending, args.inventory)
            finally:
                pending.unlink(missing_ok=True)
    inspected = bootstrap.inspect_host(inventory)
    return inventory, bootstrap.require_compatible_sudo(inspected.get('sudo_executable'))


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--host',required=True)
    parser.add_argument('--user',required=True)
    parser.add_argument('--identity-file',required=True,type=bootstrap.safe_path)
    parser.add_argument('--inventory',type=bootstrap.safe_path,default=Path.home()/'.config/autonomous-battlefield/server/inventory.json')
    parser.add_argument('--data-uuid',default='')
    parser.add_argument('--data-mount',default='/srv/battlefield-data')
    parser.add_argument('--jenkins-user',default='whtan')
    parser.add_argument('--token-file',type=bootstrap.safe_path)
    parser.add_argument('--install-plugins',action='store_true',help='Install missing required plugins using the tested pins; never restart Jenkins')
    args=parser.parse_args()
    if not sys.stdin.isatty():
        raise ValueError('Run this one-time setup in a terminal for non-echoing secret prompts')
    os.umask(0o077)
    inventory,sudo_executable=prepare_inventory(args)
    token=bootstrap.private_file(args.token_file).read_text().strip() if args.token_file else password('Jenkins API token')
    with tunnel(inventory) as url:
        client=seed.Jenkins(url,args.jenkins_user,token)
        seed.check_plugins(client,install=args.install_plugins)
        vault=args.inventory.parent/'inputs.vault'
        with tempfile.TemporaryDirectory(prefix='battlefield-platform-setup-') as tmp:
            private=Path(tmp)
            vault_password=private/'vault-password'
            bootstrap.write_new(vault_password,password('Ansible Vault passphrase',confirm=not vault.exists()).encode())
            if not vault.exists():
                subprocess.run([sys.executable,str(ROOT/'scripts/infra/server.py'),'seal',
                    '--inventory',str(args.inventory),'--vault',str(vault),
                    '--vault-password-file',str(vault_password)],check=True)
            subprocess.run([str(bootstrap.tools_bin()/'ansible-vault'),'view',str(vault),'--vault-password-file',str(vault_password)], env=bootstrap.clean_ansible_env(), stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=True)
            sudo=password('Server sudo password')
            check=subprocess.run(bootstrap.ssh(inventory)+[f"{sudo_executable} -S -p '' -k true"],input=(sudo+'\n').encode(),stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
            if check.returncode:
                raise ValueError('Server sudo authentication failed; no Jenkins credentials were written')
            sudo_file=private/'sudo-password';bootstrap.write_new(sudo_file,sudo.encode())
            seed.seed(client,{'host-inventory':args.inventory,'host-inputs':vault,
                'host-vault-password':vault_password,'host-sudo-password':sudo_file})
    print('Open battlefield-platform/host in Jenkins and run plan; after host apply/verify, use battlefield-platform/vm. Keep the Vault passphrase in your password manager.')


if __name__=='__main__':
    try:main()
    except KeyboardInterrupt:sys.exit(130)
    except (ValueError,OSError,subprocess.SubprocessError) as error:
        print('Platform setup: '+(str(error) if isinstance(error,ValueError) else 'Operation failed; private inputs were not printed'),file=sys.stderr)
        sys.exit(1)
