#!/usr/bin/env python3
"""CLI bundle installation/adoption through the existing host bootstrap entry point."""
import base64
import hashlib
import importlib.util
import inspect
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


boundary = load('cli_boundary', ROOT / 'infra/ansible/module_utils/battlefield_cli.py')
renderer = load('cli_renderer', ROOT / 'scripts/ops/render-agent-vm-host.py')
UUID = r'[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}'
SHA = r'[0-9a-f]{64}'


def absolute(value, *, controller=False):
    pattern = r'/[A-Za-z0-9_./@-]+' if controller else r'/[A-Za-z0-9_./-]+'
    if not isinstance(value, str) or not re.fullmatch(pattern, value) or '..' in Path(value).parts:
        raise ValueError('CLI paths must be absolute without shell syntax, spaces or parent traversals')
    return value


def validate(config):
    if not isinstance(config, dict) or not {'schema', 'host', 'bundle', 'guest'} <= set(config) or set(config) - {'schema', 'host', 'bundle', 'guest', 'squid_package_default'} or type(config['schema']) is not int or config['schema'] != 1:
        raise ValueError('Expected CLI configuration schema 1')
    if 'squid_package_default' in config and (not isinstance(config['squid_package_default'], str) or not re.fullmatch(SHA, config['squid_package_default'])):
        raise ValueError('Invalid Squid package default fingerprint')
    host = config['host']
    fields = {'machine_id', 'operator', 'home', 'domain_uuid', 'network_uuid', 'kubeconfig', 'context', 'kube_system_uid', 'bridge_port'}
    if not isinstance(host, dict) or set(host) != fields:
        raise ValueError('CLI host registration has missing/unknown fields')
    for key in ('domain_uuid', 'network_uuid', 'kube_system_uid'):
        if not isinstance(host[key], str) or not re.fullmatch(UUID, host[key]):
            raise ValueError('Invalid CLI identity: ' + key)
    if not isinstance(host['machine_id'], str) or not re.fullmatch(r'[0-9a-f]{32}', host['machine_id']):
        raise ValueError('Invalid CLI host machine identity')
    absolute(host['home']); absolute(host['kubeconfig']); absolute(config['bundle'], controller=True)
    if not isinstance(host['operator'], str) or not isinstance(host['context'], str) or not re.fullmatch(r'[a-z_][a-z0-9_-]*', host['operator']) or not re.fullmatch(r'[A-Za-z0-9_][A-Za-z0-9_.-]*', host['context']):
        raise ValueError('Invalid CLI operator or context')
    if type(host['bridge_port']) is not int or not 1024 <= host['bridge_port'] <= 65535 or host['bridge_port'] in range(18081, 18086) or host['bridge_port'] == 3128:
        raise ValueError('CLI bridge port must avoid the Jenkins smoke ports and proxy port')
    guest = config['guest']
    if guest is not None:
        if not isinstance(guest, dict) or not {'machine_id', 'identity_file', 'artifacts', 'config', 'service', 'adopt_existing'} <= set(guest) or set(guest) - {'machine_id', 'identity_file', 'artifacts', 'config', 'service', 'adopt_existing', 'isolation_script', 'known_hosts_file'}:
            raise ValueError('Invalid CLI guest configuration; use the example schema')
        if not isinstance(guest['machine_id'], str) or not re.fullmatch(r'[0-9a-f]{32}', guest['machine_id']) or type(guest['adopt_existing']) is not bool:
            raise ValueError('Invalid CLI guest identity/adoption setting')
        if guest['machine_id'] == host['machine_id']:
            raise ValueError('CLI guest identity must differ from the physical host')
        absolute(guest['identity_file'], controller=True); absolute(guest['config'], controller=True); absolute(guest['service'], controller=True)
        if 'known_hosts_file' in guest:
            absolute(guest['known_hosts_file'], controller=True)
        if 'isolation_script' in guest:
            absolute(guest['isolation_script'], controller=True)
        artifacts = guest['artifacts']
        if not isinstance(artifacts, dict) or not {'battlefield-agent', 'codex', 'codex-code-mode-host'} <= artifacts.keys() or artifacts.keys() - {'battlefield-agent', 'codex', 'codex-code-mode-host', 'claude', 'agy'}:
            raise ValueError('Guest requires the worker and complete Codex release; optional providers need declared artifacts')
        for artifact in artifacts.values():
            if not isinstance(artifact, dict) or set(artifact) != {'path', 'sha256'} or not isinstance(artifact['sha256'], str) or not re.fullmatch(SHA, artifact['sha256']):
                raise ValueError('Each CLI artifact requires its path and verified release SHA256')
            absolute(artifact['path'], controller=True)
    return config


def file_entry(source, destination, mode):
    source = Path(source)
    if not source.is_file() or source.is_symlink():
        raise ValueError('Missing/redirected CLI source: ' + str(source))
    content = source.read_bytes()
    return {'src': str(source), 'dest': destination, 'mode': mode, 'sha256': boundary.sha(content),
            'checksum': hashlib.sha1(content, usedforsecurity=False).hexdigest()}


def host_files(config):
    directory = Path(config['bundle'])
    if directory.is_symlink() or not directory.is_dir():
        raise ValueError('CLI bundle must be a regular directory')
    names = {path.name for path in directory.iterdir()}
    if names - (set(boundary.HOST_FILES) | {'20-loopback.conf'}) or not set(boundary.HOST_FILES) <= names:
        raise ValueError('CLI bundle must contain exactly the renderer files and optional reviewed loopback override')
    files = [file_entry(directory / name, destination, '0644') for name, destination in boundary.HOST_FILES.items()]
    if 'squid_package_default' in config:
        next(item for item in files if item['dest'] == '/etc/squid/squid.conf')['initial_sha256'] = config['squid_package_default']
    if '20-loopback.conf' in names:
        files.append(file_entry(directory / '20-loopback.conf', boundary.LOOPBACK, '0644'))
    validate_bundle(config, {path.name: path.read_text() for path in directory.iterdir()})
    return files


def validate_bundle(config, files):
    """Preserve reviewed older unit variants, but require the same effective boundary."""
    host = config['host']
    with tempfile.TemporaryDirectory() as work:
        output = Path(work) / 'rendered'
        renderer.render(output, host['operator'], host['home'], host['kubeconfig'], host['context'], host['bridge_port'])
        canonical = {path.name: path.read_text() for path in output.iterdir()}
    for name in ('mcp-proxy.Caddyfile', 'squid.conf', 'firewall.nft'):
        def tokens(value):
            return re.findall(r'[A-Za-z0-9_./:@=-]+|[^\w\s;]', re.sub(r'#.*', '', value))
        if tokens(files[name]) != tokens(canonical[name]):
            raise ValueError('CLI boundary file differs from the reviewed renderer: ' + name)
    domains = [line.strip() for line in files['provider-domains.txt'].splitlines() if line.strip() and not line.lstrip().startswith('#')]
    if not domains or any(not re.fullmatch(r'[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?\.[a-z]{2,}', domain) for domain in domains):
        raise ValueError('CLI provider allowlist must contain explicit DNS names only')
    expected_command = next(line for line in canonical['battlefield-mcp-bridge.service'].splitlines() if line.startswith('ExecStart='))
    command = expected_command.removeprefix('ExecStart=')
    effective = directives(files.get('20-loopback.conf', files['battlefield-mcp-bridge.service']))
    if effective.get('ExecStart') != (['', command] if '20-loopback.conf' in files else [command]):
        raise ValueError('CLI bridge must use the registered loopback endpoint and kubeconfig')
    if '20-loopback.conf' in files and unit_sections(files['20-loopback.conf']) != {'Service': {'ExecStart': ['', command]}}:
        raise ValueError('Unrecognized CLI bridge override')
    bridge_commands = directives(files['battlefield-mcp-bridge.service']).get('ExecStart', [])
    legacy_command = expected_command.removeprefix('ExecStart=').replace('--address=127.0.0.1', '--address=192.168.234.1').replace(str(host['bridge_port']) + ':8084', '18084:8084')
    if bridge_commands not in ([expected_command.removeprefix('ExecStart=')], [legacy_command] if '20-loopback.conf' in files else []):
        raise ValueError('Unrecognized CLI bridge base command')
    for name in ('battlefield-mcp-bridge.service', 'battlefield-mcp-proxy.service'):
        settings = directives(files[name])
        if settings.get('User') != [host['operator']] or settings.get('NoNewPrivileges') != ['true']:
            raise ValueError('CLI service user or privilege boundary differs')
        expected_environment = (['HOME=' + host['home']] if name == 'battlefield-mcp-bridge.service'
                                else ['XDG_CONFIG_HOME=/run/battlefield-mcp-proxy'])
        if settings.get('Environment', []) not in (expected_environment, [] if name == 'battlefield-mcp-proxy.service' else expected_environment):
            raise ValueError('Unexpected CLI host service environment')
        if settings.get('EnvironmentFile'):
            raise ValueError('CLI host units must not import environment files')
    firewall = directives(files['battlefield-agent-firewall.service'])
    allowed_nft = ['/usr/sbin/nft --file /etc/battlefield-agent/firewall.nft', '/usr/sbin/nft -f /etc/battlefield-agent/firewall.nft']
    if any(len(firewall.get(key, [])) != 1 or firewall[key][0] not in allowed_nft for key in ('ExecStart', 'ExecReload')):
        raise ValueError('Unrecognized CLI firewall command')
    proxy_command = '/usr/bin/caddy run --config /etc/battlefield-agent/mcp-proxy.Caddyfile --adapter caddyfile'
    if directives(files['battlefield-mcp-proxy.service']).get('ExecStart') != [proxy_command]:
        raise ValueError('Unrecognized CLI proxy command')
    for name, content in files.items():
        if any(word in content.upper() for word in ('PASSWORD=', 'TOKEN=', 'SECRET=')):
            raise ValueError('CLI host bundle must not contain credentials')
        if name.endswith(('.service', '.conf')) and name != 'squid.conf':
            allowed_exec = ({'ExecStart', 'ExecReload'} if name == 'battlefield-agent-firewall.service'
                            else set() if name == 'squid-override.conf' else {'ExecStart'})
            for section, values in unit_sections(content).items():
                if any(key.startswith('Exec') and (section != 'Service' or key not in allowed_exec) for key in values):
                    raise ValueError('Unexpected executable host unit directive')
                if section != 'Service' and any(key in values for key in ('User', 'NoNewPrivileges', 'Environment', 'EnvironmentFile')):
                    raise ValueError('CLI service boundary directive is outside [Service]')


def register(args, data, report, server):
    target = args.cli_config or args.inventory.parent / 'cli.json'
    target = server.safe_path(target)
    server.outside_repo(target)
    bundle = target.with_suffix('.files')
    if target.exists() or bundle.exists() or target.is_symlink() or bundle.is_symlink():
        raise ValueError('CLI registration already exists; inspect it instead of replacing identity')
    home = absolute(report['home'])
    kubeconfig = absolute(args.cli_kubeconfig or home + '/.config/autonomous-battlefield/production/kubeconfig')
    context = args.cli_context
    if not re.fullmatch(r'[A-Za-z0-9_][A-Za-z0-9_.-]*', context):
        raise ValueError('Invalid CLI context')
    payload = {'kubeconfig': kubeconfig, 'context': context, 'files': boundary.HOST_FILES, 'loopback': boundary.LOOPBACK}
    code = inspect.getsource(boundary.package_default) + '''
import json,pathlib,subprocess,sys,stat
p=json.load(sys.stdin)
def run(args, operation):
 result=subprocess.run(args,capture_output=True,text=True,timeout=30)
 if result.returncode: raise ValueError(operation)
 return result.stdout.strip()
try:
 files={}
 for name, path in dict(p['files'], **{'20-loopback.conf':p['loopback']}).items():
  f=pathlib.Path(path)
  if f.exists() or f.is_symlink():
   s=f.lstat()
   limit=1048576 if name=='squid.conf' else 65536
   if not stat.S_ISREG(s.st_mode) or s.st_uid != 0 or s.st_mode & 0o022 or s.st_size > limit: raise ValueError('Unsafe or oversized host bundle file: '+name)
   files[name]=f.read_text()
 default=None
 if 'squid.conf' in files:
  metadata=subprocess.run(['dpkg-query','-W','-f=${Conffiles}\\n','squid','squid-common'],capture_output=True,text=True)
  if metadata.returncode==0: default=package_default('/etc/squid/squid.conf',metadata.stdout)
  active=subprocess.run(['systemctl','is-active','squid.service'],capture_output=True,text=True).stdout.strip()
  if active=='active': default=None
 print(json.dumps({'domain_uuid':run(['virsh','-c','qemu:///system','domuuid','battlefield-agent'],'Cannot read CLI domain; verify operator libvirt group membership in a new login'),
  'network_uuid':run(['virsh','-c','qemu:///system','net-uuid','battlefield-agent'],'Cannot read the registered isolated CLI network'),
  'kube_system_uid':run(['/usr/bin/kubectl','--kubeconfig='+p['kubeconfig'],'--context='+p['context'],'--request-timeout=15s','get','namespace','kube-system','-o','jsonpath={.metadata.uid}'],'Cannot read production cluster identity; verify the operator kubeconfig and context'),
  'files':files,'squid_package_default':default}))
except (OSError,ValueError,subprocess.TimeoutExpired) as error:
 print(json.dumps({'error':str(error) if isinstance(error,ValueError) else 'CLI registration probe could not read host metadata'}))
'''
    observed = json.loads(server.remote_python(data, code, json.dumps(payload).encode(), args.local))
    if 'error' in observed:
        raise ValueError(observed['error'])
    host = dict(machine_id=data['machine_id'], operator=data['user'], home=home, kubeconfig=kubeconfig,
                context=context, bridge_port=args.cli_bridge_port,
                **{key: observed[key] for key in ('domain_uuid', 'network_uuid', 'kube_system_uid')})
    config = validate({'schema': 1, 'host': host, 'bundle': str(bundle), 'guest': None})
    with tempfile.TemporaryDirectory() as directory:
        staged = Path(directory) / 'bundle'
        if args.cli_install:
            if observed['files']:
                if set(observed['files']) != {'squid.conf'} or not observed.get('squid_package_default'):
                    raise ValueError('Fresh CLI install requires no host bundle, or only an inactive unmodified Squid package default; existing custom services need adoption')
                config['squid_package_default'] = observed['squid_package_default']
            renderer.render(staged, host['operator'], home, kubeconfig, context, host['bridge_port'])
        else:
            if not set(boundary.HOST_FILES) <= observed['files'].keys():
                raise ValueError('Existing CLI bundle is incomplete; inspect it or use --cli-install on a fresh target')
            validate_bundle(config, observed['files'])
            staged.mkdir()
            for name, content in observed['files'].items():
                (staged / name).write_text(content)
        bundle.mkdir(mode=0o700, parents=True)
        try:
            for source in staged.iterdir():
                server.write_new(bundle / source.name, source.read_bytes())
            server.write_new(target, (json.dumps(config, indent=2) + '\n').encode())
        except BaseException:
            # Only our new files are removed. Never remove a pre-existing registration.
            for path in bundle.iterdir():
                path.unlink()
            bundle.rmdir()
            raise
    print('Registered CLI host bundle without changing services. Guest installation is optional; review cli.json before plan/apply.')


def guest_file_entries(guest, server):
    files = [file_entry(server.private_file(guest['service']), '/etc/systemd/system/battlefield-worker.service', '0644'),
             file_entry(server.private_file(guest['isolation_script']) if 'isolation_script' in guest else ROOT / 'scripts/ops/verify-agent-isolation.py', '/usr/local/bin/verify-agent-isolation.py', '0755'),
             file_entry(server.private_file(guest['config']), '/etc/battlefield-agent/config.yaml', '0644')]
    for name, artifact in guest['artifacts'].items():
        entry = file_entry(artifact['path'], '/usr/local/bin/' + name, '0755')
        if entry['sha256'] != artifact['sha256']:
            raise ValueError('CLI artifact differs from verified release SHA256: ' + name)
        files.append(entry)
    return files


def managed_handoff(args, data, config, server):
    """Resolve a verified host-local VM connection without copying an admin key."""
    if not isinstance(config, dict) or not isinstance(config.get('host'), dict) or ('guest' not in config) or (config['guest'] is not None and not isinstance(config['guest'], dict)):
        raise ValueError('Invalid CLI registration for managed VM handoff')
    explicit = getattr(args, 'cli_vm_handoff', None)
    if explicit and not args.local:
        raise ValueError('Managed CLI VM handoff belongs to the physical host; use --local there')
    path = explicit or (Path.home() / '.local/state/battlefield/cli-vm/handoff.json' if args.local else None)
    if not path or (not explicit and not path.exists()):
        return None
    server.outside_repo(path)
    value = json.loads(server.private_file(path).read_text())
    fields = {'schema', 'host_machine_id', 'machine_id', 'domain_uuid', 'network_uuid',
              'identity_file', 'known_hosts_file', 'ansible_host', 'ansible_user'}
    if (not isinstance(value, dict) or set(value) != fields or type(value['schema']) is not int or value['schema'] != 1 or
            value['host_machine_id'] != data['machine_id'] or value['ansible_host'] != '192.168.234.2' or
            value['ansible_user'] != 'vm-admin' or not isinstance(value['machine_id'], str) or not re.fullmatch(r'[0-9a-f]{32}', value['machine_id']) or
            any(value[key] != config['host'][key] for key in ('domain_uuid', 'network_uuid'))):
        raise ValueError('Managed CLI VM handoff differs from the registered host/VM identities')
    for key in ('identity_file', 'known_hosts_file'):
        server.outside_repo(server.private_file(value[key]))
    if config['guest'] is not None:
        for key in ('machine_id', 'identity_file'):
            if config['guest'].get(key) and config['guest'][key] != value[key]:
                raise ValueError('CLI service registration targets another guest or admin key')
            config['guest'][key] = value[key]
        config['guest']['known_hosts_file'] = value['known_hosts_file']
    return value


def playbook(args, data, report, server):
    config_path = args.cli_config or args.inventory.parent / 'cli.json'
    server.outside_repo(Path(config_path))
    config = json.loads(server.private_file(config_path).read_text())
    managed_handoff(args, data, config, server)
    validate(config)
    if config['host']['machine_id'] != data['machine_id'] or config['host']['operator'] != data['user']:
        raise ValueError('CLI registration belongs to a different host/operator')
    files = host_files(config)
    inventory = server.ansible_inventory(data, args.local, report.get('sudo_executable'))
    host = inventory['all']['children']['bootstrap']['hosts']['server']
    inventory['all']['children']['cli_host'] = inventory['all']['children'].pop('bootstrap')
    host.update(cli_kind='host', cli_contract=config['host'], cli_files=files,
                cli_units=boundary.HOST_UNITS, cli_firewall=(Path(config['bundle']) / 'firewall.nft').read_text(), cli_filter_xml=(ROOT / 'deploy/agent-vm/filter.xml').read_text())
    guest = config['guest']
    if guest is not None:
        validate_guest_unit(server.private_file(guest['service']).read_text())
        key = server.private_file(guest['identity_file'])
        guest_files = guest_file_entries(guest, server)
        connection = '-o StrictHostKeyChecking=yes -o IdentitiesOnly=yes -o BatchMode=yes'
        if 'known_hosts_file' in guest:
            connection += ' -o ' + shlex.quote('UserKnownHostsFile=' + str(server.private_file(guest['known_hosts_file'])))
        if not args.local:
            proxy = shlex.join(server.ssh(data) + ['-W', '%h:%p'])
            connection += ' -o ' + shlex.quote('ProxyCommand=' + proxy)
        inventory['all']['children']['cli_guest'] = {'hosts': {'worker': {
            'ansible_host': '192.168.234.2', 'ansible_user': 'vm-admin', 'ansible_connection': 'ssh',
            'ansible_python_interpreter': '/usr/bin/python3', 'ansible_become_exe': '/usr/bin/sudo',
            'ansible_ssh_private_key_file': str(key), 'ansible_ssh_common_args': connection,
            'cli_kind': 'guest', 'cli_units': ['battlefield-worker'], 'cli_files': guest_files,
            'cli_contract': {'machine_id': guest['machine_id'], 'domain_uuid': config['host']['domain_uuid']},
            'cli_adopt_existing': guest['adopt_existing'], 'cli_config_source': guest['config'],
            'cli_artifact_names': list(guest['artifacts']),
        }}}
        if not args.vault:
            raise ValueError('Guest bootstrap requires --vault with cli_worker_environment; never pass a secret in inventory')
    action = args.command.removeprefix('cli-')
    with tempfile.TemporaryDirectory(prefix='battlefield-cli-ansible-') as directory:
        inv = Path(directory) / 'inventory.json'
        inv.write_text(json.dumps(inventory))
        command = [str(server.tools_bin() / 'ansible-playbook'), '-i', str(inv), str(ROOT / 'infra/ansible/cli.yml'),
                   '-e', json.dumps({'cli_action': action})]
        if args.vault:
            vault = server.private_file(args.vault)
            server.outside_repo(vault)
            if not vault.read_bytes().startswith(b'$ANSIBLE_VAULT;'):
                raise ValueError('Expected encrypted CLI Vault inputs')
            command += ['-e', json.dumps({'cli_vault_file': str(vault)}), *server.password_args(args.vault_password_file)]
        if action == 'plan':
            command += ['--check', '--diff']
        if args.become_password_file:
            password = server.private_file(args.become_password_file)
            server.outside_repo(password)
            if password.stat().st_mode & 0o111:
                raise ValueError('Sudo password file must not be executable')
            command += ['--become-password-file', str(password)]
        elif not report['sudo_noninteractive']:
            if not __import__('sys').stdin.isatty():
                raise ValueError('Use a terminal for the host sudo prompt or a private --become-password-file')
            command += ['--ask-become-pass']
        env = server.clean_ansible_env(args.report_file)
        if action == 'apply':
            with server.host_lock(data, args.local) as lock:
                server.run_playbook(command, lock, env)
        else:
            subprocess.run(command, env=env, check=True)


def guest_ssh(data, key, local, server, known_hosts=None):
    command = ['ssh', '-i', str(key), '-o', 'BatchMode=yes', '-o', 'IdentitiesOnly=yes',
               '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10']
    if known_hosts:
        command += ['-o', 'UserKnownHostsFile=' + str(server.private_file(known_hosts))]
    if not local:
        command += ['-o', 'ProxyCommand=' + shlex.join(server.ssh(data) + ['-W', '%h:%p'])]
    return command + ['vm-admin@192.168.234.2']


def capture_guest(args, data, server):
    """Capture existing guest inputs for repeatable adoption; never modify that guest."""
    path = args.cli_config or args.inventory.parent / 'cli.json'
    config = validate(json.loads(server.private_file(path).read_text()))
    if config['host']['machine_id'] != data['machine_id'] or config['host']['operator'] != data['user']:
        raise ValueError('CLI registration belongs to a different host/operator')
    if config['guest'] is not None:
        raise ValueError('Guest already registered; do not overwrite its adoption/release contract')
    handoff = managed_handoff(args, data, config, server)
    guest_key = args.cli_guest_key or (handoff['identity_file'] if handoff else None)
    if not guest_key or not args.vault:
        raise ValueError('cli-capture-guest requires a trusted guest key/handoff and a new encrypted --vault output')
    if handoff and guest_key != handoff['identity_file']:
        raise ValueError('Explicit guest key differs from the managed VM handoff')
    password_args = server.password_args(args.vault_password_file)
    key = server.private_file(guest_key)
    vault = server.safe_path(args.vault)
    server.outside_repo(vault)
    if vault.exists() or vault.is_symlink():
        raise ValueError('CLI capture requires a new Vault output; existing bundles are preserved')
    binaries = ['battlefield-agent', 'codex', 'codex-code-mode-host']
    code = '''import hashlib,json,pathlib,stat
root=pathlib.Path('/etc/battlefield-agent')
result={'machine_id':pathlib.Path('/etc/machine-id').read_text().strip(),
 'domain_uuid':pathlib.Path('/sys/class/dmi/id/product_uuid').read_text().strip().lower(),'files':{}}
for name,path in [('config.yaml',root/'config.yaml'),('worker.service',pathlib.Path('/etc/systemd/system/battlefield-worker.service')),('worker.env',root/'worker.env'),('verify-agent-isolation.py',pathlib.Path('/usr/local/bin/verify-agent-isolation.py'))]:
 s=path.lstat()
 if not stat.S_ISREG(s.st_mode) or s.st_uid != 0 or s.st_mode & 0o022 or s.st_size > 65536: raise ValueError('Unsafe guest input')
 result['files'][name]=path.read_text()
result['artifacts']={}
for name in ['battlefield-agent','codex','codex-code-mode-host','claude','agy']:
 p=pathlib.Path('/usr/local/bin')/name
 if not p.exists(): continue
 s=p.lstat()
 if not stat.S_ISREG(s.st_mode) or s.st_uid != 0 or s.st_mode & 0o022 or not 0 < s.st_size <= 536870912: raise ValueError('Unsafe guest executable')
 with p.open('rb') as f: result['artifacts'][name]=hashlib.file_digest(f,'sha256').hexdigest()
print(json.dumps(result))
'''
    command = guest_ssh(data, key, args.local, server, handoff['known_hosts_file'] if handoff else None)
    result = subprocess.run(command + ['sudo -n python3 -c ' + shlex.quote(code)], capture_output=True, timeout=120)
    if result.returncode:
        raise ValueError('Guest capture failed; verify its trusted SSH key, unlocked admin key and vm-admin sudo access. No captured output was logged.')
    observed = json.loads(result.stdout)
    if observed['domain_uuid'] != config['host']['domain_uuid']:
        raise ValueError('Guest hardware UUID differs from registered libvirt domain; no artifacts or credentials saved')
    if not set(binaries) <= observed['artifacts'].keys():
        raise ValueError('Existing guest does not contain the complete worker/Codex release')
    output = Path(path).with_suffix('.guest')
    server.outside_repo(output)
    output.mkdir(mode=0o700, exist_ok=False)
    try:
        for name, expected in observed['artifacts'].items():
            target = output / name
            fd = os.open(target, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
            with os.fdopen(fd, 'wb') as stream:
                copied = subprocess.run(command + ['cat /usr/local/bin/' + name], stdout=stream, stderr=subprocess.DEVNULL, timeout=180)
            if copied.returncode or boundary.sha(target.read_bytes()) != expected:
                raise ValueError('Guest artifact changed during capture or transfer failed: ' + name)
        for name in ('config.yaml', 'worker.service', 'verify-agent-isolation.py'):
            server.write_new(output / name, observed['files'][name].encode())
        config['guest'] = {'machine_id': observed['machine_id'], 'identity_file': str(key),
                           'config': str(output / 'config.yaml'), 'service': str(output / 'worker.service'),
                           'adopt_existing': True, 'isolation_script': str(output / 'verify-agent-isolation.py'),
                           'artifacts': {name: {'path': str(output / name), 'sha256': value} for name, value in observed['artifacts'].items()}}
        if handoff:
            if observed['machine_id'] != handoff['machine_id']:
                raise ValueError('Guest machine identity differs from the managed VM handoff')
            config['guest']['known_hosts_file'] = handoff['known_hosts_file']
        validate(config)
        inputs = {'bootstrap_inputs': {'cli_worker_environment': base64.b64encode(observed['files']['worker.env'].encode()).decode()}}
        with tempfile.TemporaryDirectory(prefix='battlefield-cli-vault-') as directory:
            encrypted = Path(directory) / 'cli.vault'
            subprocess.run([str(server.tools_bin() / 'ansible-vault'), 'encrypt', '--output', str(encrypted), *password_args],
                           input=json.dumps(inputs).encode(), env=server.clean_ansible_env(), check=True)
            server.write_new(vault, encrypted.read_bytes())
        # Replace only the registration whose exact contents we read before contacting the guest.
        original = json.loads(server.private_file(path).read_text())
        if original['guest'] is not None or original['host'] != config['host'] or original['bundle'] != config['bundle']:
            raise ValueError('CLI registration changed during capture; encrypted inputs retained, registration not replaced')
        temporary = Path(path).with_suffix('.new')
        server.write_new(temporary, (json.dumps(config, indent=2) + '\n').encode())
        os.replace(temporary, path)
    except BaseException:
        # Keep successfully encrypted evidence; captured artifacts contain no credentials.
        for item in output.iterdir():
            item.unlink()
        output.rmdir()
        raise
    print('Captured guest configuration and executable hashes; worker environment encrypted. Login directories and live services were untouched.')


def validate_guest_unit(content):
    required = {'User': 'battlefield-worker', 'Group': 'battlefield-worker',
                'WorkingDirectory': '/var/lib/battlefield-worker',
                'EnvironmentFile': '/etc/battlefield-agent/worker.env',
                'ExecStart': '/usr/local/bin/battlefield-agent -config /etc/battlefield-agent/config.yaml',
                'ProtectSystem': 'strict', 'ProtectHome': 'true', 'NoNewPrivileges': 'true',
                'ReadWritePaths': '/var/lib/battlefield-worker', 'CapabilityBoundingSet': '',
                'KillMode': 'control-group'}
    values = directives(content)
    for name, section in unit_sections(content).items():
        if any(key.startswith('Exec') and key != 'ExecStart' for key in section):
            raise ValueError('Guest unit contains an unexpected executable directive')
        if name != 'Service' and any(key in section for key in set(required) | {'Environment', 'BindPaths', 'BindReadOnlyPaths'}):
            raise ValueError('Guest service directive appears outside its required section')
    if any(values.get(key) != [value] for key, value in required.items()):
        raise ValueError('Guest unit differs from the required worker account and isolation boundary')
    allowed = {'HOME=/var/lib/battlefield-worker', 'HTTPS_PROXY=http://192.168.234.1:3128',
               'HTTP_PROXY=http://192.168.234.1:3128', 'NO_PROXY=192.168.234.1,127.0.0.1,localhost'}
    if set(values.get('Environment', [])) != allowed or values.get('BindPaths') or values.get('BindReadOnlyPaths'):
        raise ValueError('Guest unit contains unexpected environment or host paths')


def unit_sections(content):
    """Parse the restricted systemd format accepted for privileged bootstrap inputs."""
    sections, current = {}, None
    for raw in content.splitlines():
        line = raw.strip()
        if not line or line.startswith(('#', ';')):
            continue
        if line.endswith(chr(92)):
            raise ValueError('Continued systemd directives are not supported by CLI bootstrap')
        if re.fullmatch(r'\[[A-Za-z]+\]', line):
            current = line[1:-1]
            sections.setdefault(current, {})
        elif current is not None and '=' in line:
            key, value = (part.strip() for part in line.split('=', 1))
            if not re.fullmatch(r'[A-Za-z][A-Za-z0-9]*', key):
                raise ValueError('Invalid CLI systemd directive')
            sections[current].setdefault(key, []).append(value)
        else:
            raise ValueError('Invalid CLI systemd section or directive')
    return sections


def directives(content):
    return unit_sections(content).get('Service', {})
