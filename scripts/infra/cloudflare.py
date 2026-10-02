#!/usr/bin/env python3
"""Capture, review and reproduce the existing Cloudflare boundary (AB-141)."""
import argparse
from contextlib import nullcontext
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
sys.path = [p for p in sys.path if Path(p).resolve() != Path(__file__).resolve().parent]


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, ROOT / path)
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
    return module


platform = load('cloudflare_platform', 'scripts/infra/platform.py')
server = platform.bootstrap
policy = load('cloudflare_policy', 'infra/ansible/module_utils/battlefield_cloudflare_policy.py')
sys.modules['ansible.module_utils.battlefield_cloudflare_policy'] = policy
cloud = load('cloudflare_engine', 'infra/ansible/module_utils/battlefield_cloudflare.py')

# Read only the deployed connector and its four non-secret routing identifiers.
CAPTURE = r'''import json,os,pathlib,stat,sys
request=json.load(sys.stdin)
assert pathlib.Path('/etc/machine-id').read_text().strip()==request['machine_id']
directory=pathlib.Path.home()/'.config/autonomous-battlefield/production'
def read(path):
 assert path.resolve()==path
 s=path.lstat()
 assert stat.S_ISREG(s.st_mode) and s.st_uid==os.getuid() and not s.st_mode&0o077 and s.st_size<=8*1024*1024
 return path.read_text()
def env(path,keys):
 result={}
 for line in read(path).splitlines():
  key,sep,value=line.partition('=')
  if key in keys:
   assert key not in result
   if len(value)>1 and value[0] in "\"'" and value[-1]==value[0]:value=value[1:-1]
   assert not any(c in value for c in ('\\','`','$','\n','\r'))
   result[key]=value
 assert result.keys()==keys
 return result
app=env(directory/'.env',{'BATTLEFIELD_PUBLIC_ORIGIN','BATTLEFIELD_ACCESS_ISSUER','BATTLEFIELD_ACCESS_AUDIENCE'})
deployment=env(directory/'deployment.env',{'BF_TUNNEL_ID','BF_TUNNEL_CREDENTIALS_FILE'})
p=pathlib.Path(deployment['BF_TUNNEL_CREDENTIALS_FILE'])
if not p.is_absolute():p=directory/p
credentials=json.loads(read(p))
assert app['BATTLEFIELD_PUBLIC_ORIGIN'].startswith('https://')
print(json.dumps({'credentials':credentials,'contract':{'hostname':app['BATTLEFIELD_PUBLIC_ORIGIN'][8:].rstrip('/'),
 'issuer':app['BATTLEFIELD_ACCESS_ISSUER'].rstrip('/'),'audience':app['BATTLEFIELD_ACCESS_AUDIENCE'],'tunnel_id':deployment['BF_TUNNEL_ID']}}))
'''


def credential_path(value):
    path = server.private_file(value); server.outside_repo(path)
    if path.stat().st_nlink != 1:
        raise ValueError('Credential input must not have hard links')
    return path


def new_path(value):
    path = server.safe_path(value); server.outside_repo(path)
    if path.exists() or path.is_symlink():
        raise ValueError('Use a fresh output path; previous inputs/evidence are retained')
    cloud.private_directory(path.parent)
    return path


def read_vault(path, password_args):
    path = credential_path(path)
    if not path.read_bytes().startswith(b'$ANSIBLE_VAULT;'):
        raise ValueError('Cloudflare inputs must be encrypted with Ansible Vault')
    result = subprocess.run([str(server.tools_bin() / 'ansible-vault'), 'view', str(path), *password_args],
                            stdout=subprocess.PIPE, env=server.clean_ansible_env(), check=True)
    return policy.validate(json.loads(result.stdout))


def seal(config, path, password_args):
    with tempfile.TemporaryDirectory(prefix='battlefield-cloudflare-vault-') as temp:
        encrypted = Path(temp) / 'inputs.vault'
        subprocess.run([str(server.tools_bin() / 'ansible-vault'), 'encrypt', '--output', str(encrypted), *password_args],
                       input=json.dumps(config).encode(), env=server.clean_ansible_env(), check=True)
        server.write_new(path, encrypted.read_bytes())


def execute(args):
    if args.vault_password_file:
        credential_path(args.vault_password_file)
    password_args = server.password_args(args.vault_password_file)
    if args.action == 'prepare-create':
        if not args.source_vault or not args.hostname or not args.name:
            raise ValueError('prepare-create requires --source-vault, --hostname and --name')
        target = new_path(args.vault)
        seal(policy.creation(read_vault(args.source_vault, password_args), args.hostname, args.name), target, password_args)
        print('Fresh-create inputs encrypted; existing cloud resources and credentials unchanged.')
        return
    token = credential_path(args.token_file).read_text().strip()
    api = cloud.API(token)
    inventory = server.load_inventory(args.inventory)
    machine = server.remote_python(inventory, 'from pathlib import Path; print(Path("/etc/machine-id").read_text().strip())', local=args.local).decode().strip()
    if machine != inventory['machine_id']:
        raise ValueError('Registered physical host identity changed')
    if args.action == 'capture':
        if not args.zone:
            raise ValueError('capture requires --zone with the existing domain name')
        target = new_path(args.vault)
        captured = json.loads(server.remote_python(inventory, CAPTURE, json.dumps(inventory).encode(), args.local))
        credential = captured['credentials']
        account = policy.identifier(credential['AccountTag'], 'hex')
        tunnel_id = policy.identifier(credential['TunnelID'])
        tunnel = api.get(f'/accounts/{account}/cfd_tunnel/{tunnel_id}')
        config = cloud.capture(api, args.zone, captured['contract']['hostname'], tunnel['name'], credential, captured['contract'])
        seal(config, target, password_args)
        print('Cloudflare identities, policies and connector credentials captured in encrypted versioned inputs. API access was read-only.')
        return
    platform.trusted_revision(args.revision or '')
    config = read_vault(args.vault, password_args)
    if not args.report:
        raise ValueError('plan/apply/verify require --report with a fresh private path')
    report = new_path(args.report)
    review = ''
    if args.action == 'apply':
        if not args.review:
            raise ValueError('apply requires --review with the plan report')
        previous = cloud.read_private(credential_path(args.review))
        if previous.get('action') != 'plan' or previous.get('revision') != args.revision:
            raise ValueError('Review must identify the plan at this exact main revision')
        review = previous['review_id']
    state_root = Path.home() / '.local/state/battlefield/cloudflare'
    cloud.private_directory(state_root)
    with tempfile.TemporaryDirectory(prefix='battlefield-cloudflare-') as temporary:
        inputs = Path(temporary) / 'inputs.json'
        inputs.write_text(json.dumps({'cloudflare_action': args.action, 'cloudflare_token': token,
            'cloudflare_config': config, 'cloudflare_state_root': str(state_root), 'cloudflare_revision': args.revision,
            'cloudflare_machine_id': machine, 'cloudflare_review_id': review, 'cloudflare_report': str(report)}))
        command = [str(server.tools_bin() / 'ansible-playbook'), '-i', 'localhost,',
                   str(ROOT / 'infra/ansible/cloudflare.yml'), '-e', '@' + str(inputs)]
        # Keep both controller state and the existing host-wide deployment lock serialized.
        with server.operation_lock.acquire() as fd:
            lock_context = nullcontext(None) if args.local else server.host_lock(inventory, False)
            with lock_context as lock:
                environment = dict(server.clean_ansible_env(), **{server.operation_lock.ENV: str(fd)})
                descriptors = (fd,) if lock is None else (fd, lock.stdin.fileno())
                with server.operation_lock.supervised(command, env=environment, pass_fds=descriptors) as process:
                    while True:
                        if lock is not None and lock.poll() is not None:
                            raise ValueError('Host lock connection lost; Cloudflare operation stopped')
                        try:
                            if process.wait(timeout=0.2):
                                raise ValueError('Cloudflare task failed; inspect the safe task message')
                            break
                        except subprocess.TimeoutExpired:
                            pass
    print(json.dumps(cloud.read_private(report), indent=2))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['capture', 'prepare-create', 'plan', 'apply', 'verify'])
    private = Path.home() / '.config/autonomous-battlefield'
    parser.add_argument('--inventory', type=server.safe_path, default=private / 'server/inventory.json')
    parser.add_argument('--token-file', type=server.safe_path, default=private / 'bootstrap/cloudflare-api-token')
    parser.add_argument('--vault', type=server.safe_path, required=True)
    parser.add_argument('--vault-password-file', type=server.safe_path)
    parser.add_argument('--source-vault', type=server.safe_path)
    parser.add_argument('--zone')
    parser.add_argument('--hostname')
    parser.add_argument('--name')
    parser.add_argument('--revision', help='Full reviewed origin/main SHA; required for plan/apply/verify')
    parser.add_argument('--report', type=server.safe_path)
    parser.add_argument('--review', type=server.safe_path)
    parser.add_argument('--local', action='store_true', help='The controller is the registered Ubuntu host')
    args = parser.parse_args()
    os.umask(0o077)
    execute(args)


if __name__ == '__main__':
    try:
        main()
    except KeyboardInterrupt:
        sys.exit(130)
    except (ValueError, OSError, KeyError, TypeError, subprocess.SubprocessError):
        print('Cloudflare operation stopped; check the preceding safe task output, private inputs and retained state. No API response or credential is printed.', file=sys.stderr)
        sys.exit(1)
