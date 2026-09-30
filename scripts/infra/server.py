#!/usr/bin/env python3
"""Validate, inspect and bootstrap one Ubuntu server. Never accept secret CLI values."""
import argparse
import base64
from contextlib import contextmanager
import json
import importlib.util
import os
from pathlib import Path
import re
import shlex
import stat
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]
ANSIBLE = ROOT / 'infra/ansible'
MAX_INPUT = 8 * 1024 * 1024
_lock_spec = importlib.util.spec_from_file_location('operation_lock', ROOT / 'scripts/infra/operation-lock.py')
operation_lock = importlib.util.module_from_spec(_lock_spec)
_lock_spec.loader.exec_module(operation_lock)


def safe_path(value):
    if not isinstance(value, (str, Path)) or not str(value):
        raise ValueError('Expected a nonempty filesystem path')
    path = Path(value).expanduser().absolute()
    if not re.fullmatch(r'/[A-Za-z0-9_./@-]+', str(path)) or '..' in path.parts:
        raise ValueError('Paths must be absolute without spaces, escapes or parent traversals')
    return path


def private_file(value):
    path = safe_path(value)
    metadata = path.lstat()
    if not stat.S_ISREG(metadata.st_mode) or metadata.st_uid != os.getuid() or metadata.st_mode & 0o177:
        raise ValueError('Input must be a regular file owned by this user with mode 0600 or stricter')
    return path


def outside_repo(path):
    if path.resolve().is_relative_to(ROOT):
        raise ValueError('Machine inventory, credentials and encrypted bundles must live outside Git')


def write_new(path, content):
    outside_repo(path)
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    # Hard link publishes the complete file atomically, and refuses existing files/symlinks.
    fd, temporary = tempfile.mkstemp(prefix='.bootstrap-', dir=path.parent)
    try:
        with os.fdopen(fd, 'wb') as output:
            output.write(content)
            output.flush()
            os.fsync(output.fileno())
        os.link(temporary, path)
    finally:
        os.unlink(temporary)


def publish_registration(inventory, data, manifest_path, manifest):
    write_new(inventory, (json.dumps(data, indent=2) + '\n').encode())
    created = inventory.lstat()
    try:
        write_new(manifest_path, (json.dumps(manifest, indent=2) + '\n').encode())
    except BaseException:
        # Remove only the inventory this attempt created; preserve a concurrently replaced file.
        try:
            current = inventory.lstat()
            if (current.st_dev, current.st_ino) == (created.st_dev, created.st_ino):
                inventory.unlink()
        except FileNotFoundError:
            pass
        raise


def validate_inventory(data):
    required = {'schema', 'host', 'user', 'identity_file', 'machine_id'}
    optional = {'backup_uuid', 'ca_files', 'data_uuid', 'data_mount'}
    if not isinstance(data, dict) or not required <= data.keys() or data.keys() - required - optional:
        raise ValueError('Inventory has missing or unknown fields; use the example schema')
    patterns = {'host': r'[A-Za-z0-9][A-Za-z0-9.-]*', 'user': r'[a-z_][a-z0-9_-]*',
                'machine_id': r'[0-9a-f]{32}', 'backup_uuid': r'[A-Fa-f0-9-]{8,64}'}
    if type(data['schema']) is not int or data['schema'] != 1:
        raise ValueError('Unsupported inventory schema')
    for key, pattern in patterns.items():
        value = data.get(key, '')
        if key == 'backup_uuid' and value == '':
            continue
        if not isinstance(value, str) or not re.fullmatch(pattern, value):
            raise ValueError(f'Invalid inventory field: {key}')
    data_uuid = data.get('data_uuid', '')
    data_mount = data.get('data_mount', '/srv/battlefield-data')
    if not isinstance(data_uuid, str) or (data_uuid and not re.fullmatch(r'[a-fA-F0-9]{8}(?:-[a-fA-F0-9]{4}){3}-[a-fA-F0-9]{12}', data_uuid)):
        raise ValueError('data_uuid must be an empty string or complete filesystem UUID')
    if not isinstance(data_mount, str) or not re.fullmatch(r'/srv/[a-z][a-z0-9-]{0,62}', data_mount):
        raise ValueError('data_mount must be a direct /srv child with a simple lowercase name')
    if data_uuid and data_uuid.replace('-', '').lower() == data.get('backup_uuid', '').replace('-', '').lower():
        raise ValueError('Data and backup UUIDs must differ')
    safe_path(data['identity_file'])
    if not isinstance(data.get('ca_files', []), list):
        raise ValueError('ca_files must be a list of public certificate paths')
    names = set()
    for path in data.get('ca_files', []):
        certificate = safe_path(path)
        if certificate.suffix != '.crt' or certificate.name in names:
            raise ValueError('Public CA files must have unique .crt names')
        names.add(certificate.name)
    return data


def load_inventory(path):
    outside_repo(path)
    return validate_inventory(json.loads(private_file(path).read_text()))


def ssh(data):
    key = private_file(data['identity_file'])
    return ['ssh', '-o', 'BatchMode=yes', '-o', 'IdentitiesOnly=yes',
            '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10',
            '-o', 'ServerAliveInterval=15', '-o', 'ServerAliveCountMax=2',
            '-i', str(key), data['user'] + '@' + data['host']]


def remote_python(data, code, payload=None, local=False):
    args = [sys.executable, '-c', code] if local else ssh(data) + ['python3 -c ' + shlex.quote(code)]
    result = subprocess.run(args, input=payload, capture_output=True, timeout=120, check=False)
    if result.returncode:
        raise ValueError('Host operation failed; check SSH identity, permissions and Python. No captured output was logged.')
    return result.stdout


def backup_candidate(data, report):
    uuid = data.get('backup_uuid', '')
    if not uuid:
        return None
    disks = report.get('disks', {})
    if disks.get('rc') != 0:
        raise ValueError('Cannot verify backup disk identity from lsblk')
    matches, root_disks = [], set()

    def walk(node, disk=None):
        if node.get('type') == 'disk':
            disk = node['name']
        if '/' in (node.get('mountpoints') or []):
            root_disks.add(disk)
        if node.get('uuid') == uuid:
            matches.append((node, disk))
        for child in node.get('children', []):
            walk(child, disk)

    for node in json.loads(disks['output'])['blockdevices']:
        walk(node)
    if len(matches) != 1 or not root_disks or None in root_disks:
        raise ValueError('Backup UUID is missing/ambiguous, or the root physical disk is unknown')
    node, disk = matches[0]
    if disk is None or disk in root_disks or node.get('fstype') != 'ext4':
        raise ValueError('Backup must be an existing ext4 filesystem on a separate physical disk')
    return {'device': node['name'], 'uuid': uuid, 'disk': disk,
            'mountpoints': node.get('mountpoints'), 'contents_verified': False, 'usable': True}


def validate_target(data, report):
    if report['user'] != data['user']:
        raise ValueError('Run local mode as the registered operator account')
    if report['os'] != 'ubuntu' or report['version'] not in ('24.04', '26.04'):
        raise ValueError('This bootstrap targets Ubuntu 24.04 or 26.04 only')
    if report.get('architecture') != 'x86_64':
        raise ValueError('This bootstrap requires x86_64 architecture')


def inspect_host(data, local=False):
    report = json.loads(remote_python(data, (ROOT / 'scripts/infra/inspect-host.py').read_text(), local=local))
    if data['machine_id'] != report['machine_id']:
        raise ValueError('Host machine-id differs from inventory; inspect the replacement before registering it')
    validate_target(data, report)
    try:
        report['backup_candidate'] = backup_candidate(data, report)
    except ValueError as error:
        # Foundation operations do not consume this disk. Preserve diagnostics during recovery.
        report['backup_candidate'] = {'error': str(error), 'usable': False}
    return report


def tools_bin():
    result = subprocess.run([sys.executable, str(ROOT / 'scripts/infra/control-tools.py')],
                            capture_output=False, stdout=subprocess.PIPE, text=True, check=True)
    return Path(result.stdout.strip())


def clean_ansible_env(report_file=None):
    env = {key: value for key, value in os.environ.items()
           if not key.startswith(('ANSIBLE_', 'PYTHON'))}
    env.update(ANSIBLE_CONFIG=str(ANSIBLE / 'ansible.cfg'), ANSIBLE_NOCOLOR='1')
    env.pop('BATTLEFIELD_BOOTSTRAP_REPORT', None)
    if report_file:
        report = safe_path(report_file)
        if report.exists() or report.is_symlink():
            raise ValueError('Report already exists; use a fresh output path')
        env.update(ANSIBLE_STDOUT_CALLBACK='battlefield_report',
                   ANSIBLE_CALLBACK_PLUGINS=str(ANSIBLE / 'callback_plugins'),
                   BATTLEFIELD_BOOTSTRAP_REPORT=str(report))
    return env


def password_args(path):
    if not path and not sys.stdin.isatty():
        raise ValueError('Use a terminal for the Vault prompt, or supply a private --vault-password-file')
    return ['--vault-password-file', str(private_file(path))] if path else ['--ask-vault-pass']


def seal(args, data):
    vault_password_args = password_args(args.vault_password_file)
    manifest_path = args.files or args.inventory.parent / 'inputs.json'
    manifest = json.loads(private_file(manifest_path).read_text())
    if not isinstance(manifest, dict) or not manifest or len(manifest) > 32:
        raise ValueError('Input manifest must contain 1–32 named files')
    result = {}
    remote = {}
    for name, entry in manifest.items():
        if not re.fullmatch(r'[a-z][a-z0-9_-]{0,63}', name):
            raise ValueError('Invalid input name')
        if not isinstance(entry, dict) or set(entry) != {'source', 'path'}:
            raise ValueError('Each input needs source (controller/host) and path')
        path = safe_path(entry['path'])
        if entry['source'] == 'controller':
            content = private_file(path).read_bytes()
            content.decode('utf-8')
            if not 0 < len(content) <= MAX_INPUT:
                raise ValueError('Input file is empty or exceeds 8 MiB')
            result[name] = base64.b64encode(content).decode()
        elif entry['source'] == 'host':
            remote[name] = str(path)
        else:
            raise ValueError('Input source must be controller or host')
    if remote:
        # Metadata/content remain in memory; remote errors never echo input contents.
        code = """import base64,json,pathlib,stat,sys
files=json.load(sys.stdin); result={}
for name, value in files.items():
 p=pathlib.Path(value); s=p.lstat()
 if not stat.S_ISREG(s.st_mode) or not 0 < s.st_size <= 8*1024*1024: sys.exit(2)
 content=p.read_bytes(); content.decode('utf-8')
 result[name]=base64.b64encode(content).decode()
print(json.dumps(result))
"""
        result.update(json.loads(remote_python(data, code, json.dumps(remote).encode(), args.local)))
    output = safe_path(args.vault)
    outside_repo(output)
    if output.exists() or output.is_symlink():
        raise ValueError('Vault already exists; refusing to replace existing credentials')
    binary = tools_bin() / 'ansible-vault'
    # Ask for the password through the terminal; plaintext JSON travels only over stdin.
    with tempfile.TemporaryDirectory(prefix='battlefield-vault-') as directory:
        encrypted = Path(directory) / 'inputs.vault'
        subprocess.run([str(binary), 'encrypt', '--output', str(encrypted), *vault_password_args],
                       input=json.dumps({'bootstrap_inputs': result}).encode(), check=True,
                       env=clean_ansible_env())
        write_new(output, encrypted.read_bytes())
    print(f'Encrypted {len(result)} input files. Existing source files were preserved.')


LOCK_CODE = """import fcntl,os,pathlib,sys
directory=pathlib.Path.home()/'.local/state/battlefield'
directory.mkdir(parents=True,exist_ok=True,mode=0o700)
fd=os.open(directory/'bootstrap.lock',os.O_CREAT|os.O_RDWR|os.O_NOFOLLOW,0o600)
try: fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
except BlockingIOError: sys.exit(75)
print('LOCKED',flush=True)
sys.stdin.read()
"""


@contextmanager
def host_lock(data, local):
    if local and operation_lock.inherited() is not None:
        yield None
        return
    args = [sys.executable, '-c', LOCK_CODE] if local else ssh(data) + ['python3 -c ' + shlex.quote(LOCK_CODE)]
    # A controller process-group crash must not also kill the lock proxy.
    process = subprocess.Popen(args, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                               stderr=subprocess.DEVNULL, start_new_session=True)
    try:
        if process.stdout.readline().strip() != b'LOCKED':
            raise ValueError('Another bootstrap owns the host lock, or the lock connection failed')
        yield process
    finally:
        process.stdin.close()
        process.stdout.close()
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.terminate()
            process.wait(timeout=10)


def public_ca_paths(data):
    result = []
    for value in data.get('ca_files', []):
        path = safe_path(value)
        if not path.is_file() or path.is_symlink() or path.stat().st_size > 65536:
            raise ValueError('CA input must be a regular public certificate, at most 64 KiB')
        content = path.read_text()
        if not re.fullmatch(r'-----BEGIN CERTIFICATE-----\s+[A-Za-z0-9+/=\r\n]+-----END CERTIFICATE-----\s*', content):
            raise ValueError('CA input must contain only one PEM certificate, never a private key')
        result.append(str(path))
    return result


def require_compatible_sudo(sudo_executable):
    if sudo_executable not in ('/usr/bin/sudo', '/usr/bin/sudo.ws'):
        raise ValueError('Compatible sudo is unavailable; install the Ubuntu sudo package before running Ansible')
    return sudo_executable


def ansible_inventory(data, local, sudo_executable):
    require_compatible_sudo(sudo_executable)
    variables = {'ansible_host': data['host'], 'ansible_user': data['user'],
                 'ansible_connection': 'local' if local else 'ssh',
                 'ansible_python_interpreter': '/usr/bin/python3',
                 'ansible_become_exe': sudo_executable,
                 'bootstrap_machine_id': data['machine_id'], 'bootstrap_operator': data['user'],
                 'bootstrap_backup_uuid': data.get('backup_uuid', ''),
                 'bootstrap_data_uuid': data.get('data_uuid', ''),
                 'bootstrap_data_mount': data.get('data_mount', '/srv/battlefield-data'),
                 'bootstrap_ca_files': public_ca_paths(data)}
    if not local:
        variables['ansible_ssh_private_key_file'] = str(private_file(data['identity_file']))
        variables['ansible_ssh_common_args'] = '-o StrictHostKeyChecking=yes -o IdentitiesOnly=yes -o BatchMode=yes'
    return {'all': {'children': {'bootstrap': {'hosts': {'server': variables}}}}}


def playbook(args, data, report):
    vault_password_args = password_args(args.vault_password_file) if args.vault else []
    binary = tools_bin() / 'ansible-playbook'
    if args.command in ('apply', 'plan') and not args.vault:
        raise ValueError('plan/apply require an encrypted --vault; run seal first')
    with tempfile.TemporaryDirectory(prefix='battlefield-ansible-') as directory:
        inventory = Path(directory) / 'inventory.json'
        inventory.write_text(json.dumps(ansible_inventory(data, args.local, report.get('sudo_executable'))))
        command = [str(binary), '-i', str(inventory), str(ANSIBLE / 'site.yml'),
                   '-e', json.dumps({'bootstrap_action': args.command})]
        if args.vault:
            vault = private_file(args.vault)
            outside_repo(vault)
            if not vault.read_bytes().startswith(b'$ANSIBLE_VAULT;'):
                raise ValueError('Expected an Ansible Vault encrypted file, not plaintext')
            # include_vars with no_log protects even malformed decrypted data.
            command += ['-e', json.dumps({'bootstrap_vault_file': str(vault)}),
                        *vault_password_args]
        if args.command == 'plan':
            command += ['--check', '--diff']
        become_password = getattr(args, 'become_password_file', None)
        if become_password:
            password = private_file(become_password)
            outside_repo(password)
            if password.stat().st_mode & 0o111:
                raise ValueError('Sudo password input must not be executable')
            command += ['--become-password-file', str(password)]
        elif not report['sudo_noninteractive'] and not (args.local and os.geteuid() == 0):
            if not sys.stdin.isatty():
                raise ValueError('Run from a terminal for the sudo prompt; never put a sudo password in inventory')
            command.append('--ask-become-pass')
        environment = clean_ansible_env(getattr(args, 'report_file', None))
        if args.command == 'apply':
            with host_lock(data, args.local) as lock:
                run_playbook(command, lock, environment)
        else:
            subprocess.run(command, env=environment, check=True)


def run_playbook(command, lock, environment=None):
    # Inherit either the lock itself or the writer keeping the SSH/local proxy alive.
    # The operation must retain exclusion if its controller is killed unexpectedly.
    fd = operation_lock.inherited() if lock is None else lock.stdin.fileno()
    if fd is None:
        raise ValueError('Apply requires an active host lock')
    with operation_lock.supervised(command, env=environment or clean_ansible_env(), pass_fds=(fd,)) as process:
        while True:
            if lock is not None and lock.poll() is not None:
                raise ValueError('Host lock connection lost; stopped configuration')
            try:
                code = process.wait(timeout=0.2)
                if code:
                    raise subprocess.CalledProcessError(code, command)
                return
            except subprocess.TimeoutExpired:
                pass


def parser():
    result = argparse.ArgumentParser(description=__doc__)
    result.add_argument('command', choices=['init', 'inspect', 'seal', 'plan', 'apply', 'verify', 'tools'])
    default = Path(os.environ.get('XDG_CONFIG_HOME', Path.home() / '.config')) / 'autonomous-battlefield/server/inventory.json'
    result.add_argument('--inventory', type=safe_path, default=default)
    result.add_argument('--host')
    result.add_argument('--user')
    result.add_argument('--identity-file')
    result.add_argument('--local', action='store_true', help='Run on the registered server as its operator')
    result.add_argument('--files', help='Private JSON map of named controller/host files to encrypt')
    result.add_argument('--vault', help='Encrypted input bundle outside Git')
    result.add_argument('--report-file', help='Fresh JSON path for secret-safe task outcomes instead of module output')
    result.add_argument('--become-password-file', help='Private non-executable sudo password file; otherwise prompt')
    result.add_argument('--vault-password-file', help='Private password file for unattended runs; otherwise prompt')
    return result


def main():
    args = parser().parse_args()
    os.umask(0o077)
    if args.command == 'tools':
        print(tools_bin())
        return
    args.inventory = safe_path(args.inventory)
    if args.command == 'init':
        data = validate_inventory({'schema': 1, 'host': args.host, 'user': args.user,
                                   'identity_file': args.identity_file or '', 'machine_id': '0' * 32,
                                   'backup_uuid': '', 'ca_files': []})
        report = json.loads(remote_python(data, (ROOT / 'scripts/infra/inspect-host.py').read_text(), local=args.local))
        data['machine_id'] = report['machine_id']
        validate_inventory(data)
        validate_target(data, report)
        manifest_path = args.inventory.parent / 'inputs.json'
        if args.inventory.exists() or args.inventory.is_symlink() or manifest_path.exists() or manifest_path.is_symlink():
            raise ValueError('Inventory or input manifest already exists; inspect it instead of registering again')
        manifest = {name: {'source': 'host', 'path': path} for name, path in report.get('input_files', {}).items()}
        token = Path(os.environ.get('XDG_CONFIG_HOME', Path.home() / '.config')) / 'autonomous-battlefield/bootstrap/cloudflare-api-token'
        if token.is_file():
            manifest['cloudflare_api_token'] = {'source': 'controller', 'path': str(token.absolute())}
        publish_registration(args.inventory, data, manifest_path, manifest)
        print(f'Registered {report["hostname"]}. Inventory created at {args.inventory}. No server configuration changed.')
        return
    data = load_inventory(args.inventory)
    report = inspect_host(data, args.local)
    if args.command == 'inspect':
        print(json.dumps(report, indent=2))
    elif args.command == 'seal':
        if not args.vault:
            raise ValueError('seal requires --vault; --files defaults to the adjacent inputs.json')
        seal(args, data)
    else:
        playbook(args, data, report)


if __name__ == '__main__':
    try:
        main()
    except KeyboardInterrupt:
        sys.exit(130)
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        # CalledProcessError may contain arguments; never serialize subprocess objects.
        message = str(error) if isinstance(error, ValueError) else 'Operation failed; inspect the preceding safe output and retry.'
        print(f'Bootstrap: {message}', file=sys.stderr)
        sys.exit(1)
