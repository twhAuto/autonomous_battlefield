#!/usr/bin/env python3
"""Keep the registered CLI worker running and in sync with production (AB-139, AB-162).

Runs on the physical host, as the operator Jenkins also runs as. `verify` only reads.
`plan` records the repairs it would make in a short-lived receipt; `apply` re-checks
that plan, consumes the receipt, repairs, then verifies. `authorize` is the one-time
operator step that lets Jenkins reach the VM with a dedicated key. The worker secret
always comes from the production application .env, so a rotation reaches the worker
on the next apply. No secret is printed, logged, written to a report or put in argv.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[2]


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


HOME = Path(os.environ.get('BATTLEFIELD_CLI_HOME', str(Path.home())))
CLI_CONFIG = HOME / '.config/autonomous-battlefield/server/cli.json'
PRODUCTION_ENV = HOME / '.config/autonomous-battlefield/production/.env'
AUTOMATION_KEY = HOME / '.ssh/battlefield-cli-automation'
GUEST = 'vm-admin@192.168.234.2'
DOMAIN = 'battlefield-agent'
WORKER_ENV = '/etc/battlefield-agent/worker.env'
MARKER = '/etc/battlefield-cli-bootstrap/guest.json'
SECRET_KEY = 'BATTLEFIELD_AGENT_SECRET'
SECRET = re.compile(r'[A-Za-z0-9_+/=-]{32,512}')
PUBLISHED = ('replace-with-',)
RETIRED = 'local-development-capability-secret-change-me'
HOST_UNITS = ['battlefield-agent-firewall', 'battlefield-mcp-bridge', 'battlefield-mcp-proxy', 'squid']
LIBEXEC = '/usr/local/libexec/battlefield'
RECEIPT_SECONDS = 1800
MANAGED_SETTINGS = ROOT / 'infra/deploy/agent-vm/managed-settings.json'
GUEST_CONFIG = '/etc/battlefield-agent/config.yaml'
MODEL = re.compile(r'[A-Za-z0-9][A-Za-z0-9._:-]{0,79}')
GUEST_REPAIRS = {'sync-worker-secret', 'restart-worker', 'enable-worker', 'sync-worker-config'}


def production_worker_secret(path=PRODUCTION_ENV):
    """Read BATTLEFIELD_AGENT_SECRET from the literal production env file and validate it."""
    values = {}
    for line in Path(path).read_text().splitlines():
        if line.strip() and not line.lstrip().startswith('#') and '=' in line:
            key, value = line.split('=', 1)
            if key in values:
                raise ValueError('Duplicate setting in the production env file')
            values[key] = value
    secret = values.get(SECRET_KEY, '')
    if not SECRET.fullmatch(secret) or secret.startswith(PUBLISHED) or secret == RETIRED:
        raise ValueError(SECRET_KEY + ' in the production env file is missing, malformed or a published placeholder')
    return secret


def with_worker_secret(environment, secret):
    """Replace the worker secret line; bootstrap and this job build identical bytes."""
    pattern = re.compile(r'(?m)^' + SECRET_KEY + r'=.*$')
    if len(pattern.findall(environment)) != 1:
        raise ValueError('The worker environment must contain exactly one ' + SECRET_KEY + ' line')
    return pattern.sub(lambda _: SECRET_KEY + '=' + secret, environment)


def managed_settings(path=MANAGED_SETTINGS):
    """Reviewed worker settings this job owns: concurrency and the CLI profile models.

    agentConcurrency null means the setting must be absent: the adopted worker binary
    predates it and rejects a config that contains it. Set 1-4 once the worker supports it.
    """
    settings = json.loads(Path(path).read_text())
    concurrency, models = settings.get('agentConcurrency'), settings.get('models')
    if (set(settings) != {'agentConcurrency', 'models'}
            or not (concurrency is None or (type(concurrency) is int and 1 <= concurrency <= 4))
            or not isinstance(models, dict) or not models
            or not all(isinstance(k, str) and isinstance(v, str) and MODEL.fullmatch(v) for k, v in models.items())):
        raise ValueError('Managed CLI worker settings need agentConcurrency null or 1-4, and profile models')
    return settings


def desired_config(current, settings):
    """Apply only the managed fields; return the current text unchanged when they already match."""
    import yaml
    config = yaml.safe_load(current)
    ai = config['ai']
    profiles = {profile['id']: profile for profile in ai['profiles']}
    if set(settings['models']) - set(profiles):
        raise ValueError('Managed settings name a profile the worker configuration does not have')
    concurrency = settings['agentConcurrency']
    if ai.get('agentConcurrency') == concurrency and all(
            profiles[name].get('model') == model for name, model in settings['models'].items()):
        return current
    if concurrency is None:
        ai.pop('agentConcurrency', None)
    else:
        ai['agentConcurrency'] = concurrency
    for name, model in settings['models'].items():
        profiles[name]['model'] = model
    return yaml.safe_dump(config, sort_keys=False, default_flow_style=False)


def host_files(bridge_port):
    """AB-162 boot start and the bridge watchdog, owned by this job rather than bootstrap."""
    start = f'''#!/bin/sh
# Start the CLI VM after boot. libvirt autostart can fail while the host firewall is
# still settling (ebtables rule errors), and nothing retries it.
set -u
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12; do
  state=$(virsh -c qemu:///system domstate {DOMAIN} 2>/dev/null)
  [ "$state" = running ] && exit 0
  virsh -c qemu:///system start {DOMAIN} >/dev/null 2>&1 && exit 0
  sleep 10
done
echo "{DOMAIN} did not start after 12 attempts" >&2
exit 1
'''
    watchdog = '''#!/bin/sh
# kubectl port-forward stays bound to the gateway pod it started with and keeps
# listening after a rollout replaces it. Restart the bridge when it stops answering.
set -u
port="$1"
failures=0
while :; do
  sleep 15
  if curl -fsS --max-time 5 -o /dev/null "http://127.0.0.1:${port}/readyz"; then
    failures=0
    continue
  fi
  failures=$((failures + 1))
  if [ "$failures" -ge 3 ]; then
    echo "MCP bridge unreachable for 3 checks; restarting battlefield-mcp-bridge" >&2
    systemctl restart battlefield-mcp-bridge.service || true
    failures=0
    sleep 30
  fi
done
'''
    start_unit = f'''[Unit]
Description=Start the Battlefield CLI VM after libvirt and the host firewall are ready
Wants=network-online.target
After=network-online.target libvirtd.service virtqemud.service nftables.service battlefield-agent-firewall.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart={LIBEXEC}/start-cli-vm

[Install]
WantedBy=multi-user.target
'''
    watchdog_unit = f'''[Unit]
Description=Restart the Battlefield MCP bridge when it loses the AI gateway
After=battlefield-mcp-bridge.service

[Service]
ExecStart={LIBEXEC}/mcp-bridge-watchdog {int(bridge_port)}
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
'''
    return {
        LIBEXEC + '/start-cli-vm': ('0755', start),
        LIBEXEC + '/mcp-bridge-watchdog': ('0755', watchdog),
        '/etc/systemd/system/battlefield-cli-vm-start.service': ('0644', start_unit),
        '/etc/systemd/system/battlefield-mcp-bridge-watchdog.service': ('0644', watchdog_unit),
    }


GUEST_INSPECT = r'''
import hashlib, json, pathlib, subprocess, sys, urllib.request
expected = json.loads(sys.stdin.read())['secret_sha256']
env = pathlib.Path('/etc/battlefield-agent/worker.env').read_bytes()
secret = [line.split(b'=', 1)[1] for line in env.splitlines() if line.startswith(b'BATTLEFIELD_AGENT_SECRET=')]
marker = pathlib.Path('/etc/battlefield-cli-bootstrap/guest.json')
try:
    files = json.loads(marker.read_text())['files'] if marker.exists() else {}
except ValueError:
    files = {}
def unit(*args):
    return subprocess.run(['systemctl', *args, 'battlefield-worker'], capture_output=True, text=True).stdout.strip()
try:
    health = urllib.request.urlopen('http://127.0.0.1:8083/healthz', timeout=5).status
except Exception:
    health = 0
print(json.dumps({
    'machine_id': pathlib.Path('/etc/machine-id').read_text().strip(),
    'domain_uuid': pathlib.Path('/sys/class/dmi/id/product_uuid').read_text().strip().lower(),
    'secret_matches': len(secret) == 1 and hashlib.sha256(secret[0]).hexdigest() == expected,
    'config_sha256': hashlib.sha256(pathlib.Path('/etc/battlefield-agent/config.yaml').read_bytes()).hexdigest(),
    'marker_matches': (not files) or files.get('/etc/battlefield-agent/worker.env') == hashlib.sha256(env).hexdigest(),
    'worker_active': unit('is-active') == 'active',
    'worker_enabled': unit('is-enabled') == 'enabled',
    'worker_health': health,
}))
'''

GUEST_SYNC = r'''
import hashlib, json, os, pathlib, re, subprocess, sys, tempfile
secret = json.loads(sys.stdin.read())['secret']
path = pathlib.Path('/etc/battlefield-agent/worker.env')
current = path.read_text()
marker = pathlib.Path('/etc/battlefield-cli-bootstrap/guest.json')
registered = json.loads(marker.read_text())['files'].get(str(path)) if marker.exists() else None
# Only a secret rotation is in scope: refuse any other drift from the registered file.
if registered is not None and hashlib.sha256(current.encode()).hexdigest() != registered:
    raise SystemExit('worker environment differs from its registered content; not changed')
pairs = dict(line.split('=', 1) for line in current.splitlines() if line and not line.startswith('#'))
if (set(pairs) != {'BATTLEFIELD_AGENT_SECRET', 'BATTLEFIELD_ENV', 'BATTLEFIELD_CLI_ISOLATION_VERIFIED'}
        or pairs['BATTLEFIELD_ENV'] != 'production' or pairs['BATTLEFIELD_CLI_ISOLATION_VERIFIED'] not in ('true', 'false')):
    raise SystemExit('worker environment has unexpected settings; not changed')
pattern = re.compile(r'(?m)^BATTLEFIELD_AGENT_SECRET=.*$')
if len(pattern.findall(current)) != 1:
    raise SystemExit('worker environment must contain exactly one BATTLEFIELD_AGENT_SECRET line')
updated = pattern.sub(lambda _: 'BATTLEFIELD_AGENT_SECRET=' + secret, current)
def publish(target, content, mode):
    fd, temporary = tempfile.mkstemp(dir=target.parent, prefix='.battlefield-')
    try:
        os.fchmod(fd, mode)
        with os.fdopen(fd, 'w') as stream:
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, target)
    except BaseException:
        pathlib.Path(temporary).unlink(missing_ok=True)
        raise
changed = updated != current
if changed:
    publish(path, updated, 0o400)
if marker.exists():
    data = json.loads(marker.read_text())
    digest = hashlib.sha256(updated.encode()).hexdigest()
    if data.get('files', {}).get(str(path)) != digest:
        data['files'][str(path)] = digest
        publish(marker, json.dumps(data, indent=2) + '\n', 0o600)
subprocess.run(['systemctl', 'restart', 'battlefield-worker'], check=True)
print(json.dumps({'changed': changed}))
'''


GUEST_CONFIG_SYNC = r'''
import hashlib, json, os, pathlib, sys, tempfile, yaml
request = json.loads(sys.stdin.read())
path = pathlib.Path('/etc/battlefield-agent/config.yaml')
updated = request['content']
yaml.safe_load(updated)
current = hashlib.sha256(path.read_bytes()).hexdigest()
target = hashlib.sha256(updated.encode()).hexdigest()
# Resuming an interrupted sync finds the target content already in place.
if current not in (request['expected_sha256'], target):
    raise SystemExit('worker configuration differs from its registered content; not changed')
def publish(target, content, mode):
    fd, temporary = tempfile.mkstemp(dir=target.parent, prefix='.battlefield-')
    try:
        os.fchmod(fd, mode)
        with os.fdopen(fd, 'w') as stream:
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, target)
    except BaseException:
        pathlib.Path(temporary).unlink(missing_ok=True)
        raise
if current != target:
    publish(path, updated, 0o644)
marker = pathlib.Path('/etc/battlefield-cli-bootstrap/guest.json')
if marker.exists():
    data = json.loads(marker.read_text())
    if data['files'].get('/etc/battlefield-agent/config.yaml') != target:
        data['files']['/etc/battlefield-agent/config.yaml'] = target
        publish(marker, json.dumps(data, indent=2) + '\n', 0o600)
print(json.dumps({'changed': current != target}))
'''


class Worker:
    def __init__(self, args):
        self.args = args
        self.bootstrap = load('cli_bootstrap', ROOT / 'scripts/infra/cli-bootstrap.py')
        self.config = self.bootstrap.validate(json.loads(self.private(CLI_CONFIG).read_text()))
        if self.config['guest'] is None:
            raise ValueError('No CLI guest is registered; run the CLI bootstrap adoption first')
        # Registrations captured through the managed VM handoff pin the guest host key there.
        known = self.config['guest'].get('known_hosts_file') or HOME / '.local/state/battlefield/cli-vm/known_hosts'
        self.known_hosts = self.private(known)
        self.port = self.config['host']['bridge_port']
        # The host's registered copy of the guest config, which bootstrap reconciles against.
        self.registered_config = self.private(self.config['guest']['config'])

    @staticmethod
    def private(path):
        path = Path(path)
        metadata = path.lstat()
        if not path.is_file() or path.is_symlink() or metadata.st_uid != os.getuid() or metadata.st_mode & 0o077:
            raise ValueError(f'{path} must be a regular file owned by this user with mode 0600 or stricter')
        return path

    def run(self, command, *, data=None, timeout=60, check=False):
        result = subprocess.run(command, input=data, capture_output=True, timeout=timeout)
        if check and result.returncode:
            raise ValueError('Command failed: ' + shlex.join(command[:3]))
        return result

    def ssh(self, key, remote, data=None, timeout=120):
        command = ['ssh', '-i', str(key), '-o', 'BatchMode=yes', '-o', 'IdentitiesOnly=yes',
                   '-o', 'StrictHostKeyChecking=yes', '-o', 'UserKnownHostsFile=' + str(self.known_hosts),
                   '-o', 'ConnectTimeout=10', GUEST, remote]
        return self.run(command, data=data, timeout=timeout)

    def guest_python(self, code, payload):
        key = self.private(AUTOMATION_KEY)
        result = self.ssh(key, 'sudo -n python3 -c ' + shlex.quote(code), json.dumps(payload).encode())
        if result.returncode:
            raise ValueError('The CLI guest command failed; check the automation key, vm-admin sudo and the worker files')
        return json.loads(result.stdout)

    def gateway(self, path, secret=None):
        request = urllib.request.Request(f'http://127.0.0.1:{self.port}{path}')
        if secret:
            request.add_header('Authorization', 'Bearer ' + secret)
        try:
            return urllib.request.urlopen(request, timeout=5).status
        except urllib.error.HTTPError as error:
            return error.code
        except OSError:
            return 0

    def domain_state(self):
        result = self.run(['virsh', '-c', 'qemu:///system', 'domstate', DOMAIN])
        return result.stdout.decode().strip() if result.returncode == 0 else 'unknown'

    def unit_state(self, unit, query='is-active'):
        return self.run(['systemctl', query, unit]).stdout.decode().strip()

    def host_files_current(self):
        stale = []
        for path, (mode, content) in host_files(self.port).items():
            target = Path(path)
            if not target.is_file() or target.read_text() != content or oct(target.stat().st_mode & 0o777)[2:] != mode.lstrip('0'):
                stale.append(path)
        return stale

    def inspect(self, secret):
        checks = {'domain': self.domain_state(), 'host_units': {u: self.unit_state(u) for u in HOST_UNITS},
                  'boot_start_enabled': self.unit_state('battlefield-cli-vm-start', 'is-enabled') == 'enabled',
                  'bridge_watchdog_active': self.unit_state('battlefield-mcp-bridge-watchdog') == 'active',
                  'stale_host_files': self.host_files_current(),
                  'watchdog_enabled': self.unit_state('battlefield-mcp-bridge-watchdog', 'is-enabled') == 'enabled',
                  'bridge_reachable': self.gateway('/readyz') // 100 == 2}
        enabled = self.gateway('/internal/agent/enabled', secret)
        checks['gateway_accepts_worker_secret'] = enabled in (204, 503)
        checks['automation_key'] = AUTOMATION_KEY.is_file()
        checks['guest'] = None
        if checks['domain'] == 'running' and checks['automation_key']:
            try:
                guest = self.guest_python(GUEST_INSPECT, {'secret_sha256': hashlib.sha256(secret.encode()).hexdigest()})
            except (ValueError, subprocess.TimeoutExpired):
                guest = {'reachable': False}
            else:
                guest['reachable'] = True
                guest['identity_matches'] = (guest.pop('machine_id') == self.config['guest']['machine_id']
                                             and guest.pop('domain_uuid') == self.config['host']['domain_uuid'])
                registered = self.registered_config.read_text()
                desired = desired_config(registered, managed_settings())
                actual = guest.pop('config_sha256')
                guest['config_registered'] = actual == hashlib.sha256(registered.encode()).hexdigest()
                guest['config_current'] = desired == registered
                # An interrupted sync left the guest updated but the registered copy behind.
                guest['config_pending'] = not guest['config_current'] and actual == hashlib.sha256(desired.encode()).hexdigest()
            checks['guest'] = guest
        return checks

    @staticmethod
    def repairs(checks):
        needed = []
        if host_units_missing(checks):
            needed.append('install-host-units')
        if checks['domain'] != 'running':
            needed.append('start-vm')
        if not checks['bridge_reachable']:
            needed.append('restart-bridge')
        guest = checks['guest'] or {}
        if checks['domain'] == 'running' and not guest.get('reachable'):
            needed.append('reach-guest')
        if guest.get('reachable') and guest['identity_matches']:
            needed += guest_repairs(guest)
        return needed

    @staticmethod
    def problems(checks):
        """Everything that keeps CLI battles from running, in operator terms."""
        found = [f'host unit {u} is {s}' for u, s in checks['host_units'].items() if s != 'active']
        if checks['domain'] != 'running':
            found.append(f'CLI VM is {checks["domain"]}')
        if not checks['bridge_reachable']:
            found.append('MCP bridge does not reach the AI gateway')
        elif not checks['gateway_accepts_worker_secret']:
            found.append('AI gateway rejects the production worker secret')
        if not checks['automation_key']:
            found.append('Jenkins automation key is missing; run cli-worker.py authorize once')
        found += guest_problems(checks['guest'])
        if host_units_missing(checks):
            found.append('boot start or bridge watchdog is not installed')
        return found

    def sudo(self, command):
        """Host root operations, with the sudo password on stdin rather than argv."""
        password = b''
        if self.args.become_password_file:
            password = self.private(self.args.become_password_file).read_bytes().rstrip(b'\n') + b'\n'
        prefix = ['sudo', '-S', '-p', ''] if password else ['sudo', '-n']
        result = self.run(prefix + command, data=password, timeout=120)
        if result.returncode:
            raise ValueError('Host sudo command failed: ' + ' '.join(command[:2]))

    def install_host_units(self):
        with tempfile.TemporaryDirectory(prefix='battlefield-cli-units-') as directory:
            self.sudo(['install', '-d', '-m', '0755', LIBEXEC])
            for path, (mode, content) in host_files(self.port).items():
                staged = Path(directory) / Path(path).name
                staged.write_text(content)
                self.sudo(['install', '-m', mode, '-o', 'root', '-g', 'root', str(staged), path])
        self.sudo(['systemctl', 'daemon-reload'])
        self.sudo(['systemctl', 'enable', 'battlefield-cli-vm-start.service'])
        self.sudo(['systemctl', 'enable', 'battlefield-mcp-bridge-watchdog.service'])
        self.sudo(['systemctl', 'restart', 'battlefield-mcp-bridge-watchdog.service'])

    def wait_for(self, condition, seconds, message):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            if condition():
                return
            time.sleep(5)
        raise ValueError(message)

    def apply(self, repairs, secret):
        if 'install-host-units' in repairs:
            self.install_host_units()
        if 'start-vm' in repairs:
            self.run(['virsh', '-c', 'qemu:///system', 'start', DOMAIN], check=True)
            self.wait_for(lambda: self.ssh(self.private(AUTOMATION_KEY), 'true', timeout=30).returncode == 0,
                          240, 'The CLI VM started but did not accept SSH within 4 minutes')
        if 'restart-bridge' in repairs:
            self.sudo(['systemctl', 'restart', 'battlefield-mcp-bridge.service'])
            self.wait_for(lambda: self.gateway('/readyz') // 100 == 2, 60, 'The MCP bridge did not reach the AI gateway after a restart')
        guest = self.inspect(secret)['guest'] or {}
        if not guest.get('reachable'):
            raise ValueError('The CLI guest is not reachable with the automation key; run cli-worker.py authorize')
        if not guest['identity_matches']:
            raise ValueError('The CLI guest identity differs from the registration; nothing was changed in the guest')
        if not guest['marker_matches'] and set(repairs) & GUEST_REPAIRS:
            raise ValueError('The CLI worker environment differs from its registered content; nothing was changed in the guest')
        self.repair_guest([r for r in repairs if r in GUEST_REPAIRS], secret)
        self.wait_for(lambda: (self.inspect(secret)['guest'] or {}).get('worker_health') == 204, 90,
                      'The CLI worker did not report healthy after the repair')

    def repair_guest(self, repairs, secret):
        """Perform exactly the reviewed guest repairs, in a safe order."""
        key = self.private(AUTOMATION_KEY)
        restart = 'restart-worker' in repairs
        if 'sync-worker-config' in repairs:
            self.sync_config()
            restart = True
        if 'sync-worker-secret' in repairs:
            self.guest_python(GUEST_SYNC, {'secret': secret})  # Restarts the worker itself.
            restart = False
        if restart and self.ssh(key, 'sudo -n systemctl restart battlefield-worker').returncode:
            raise ValueError('Could not restart the CLI worker')
        if 'enable-worker' in repairs and self.ssh(key, 'sudo -n systemctl enable battlefield-worker').returncode:
            raise ValueError('Could not enable the CLI worker for reboot')

    def sync_config(self):
        """Change the guest config, then the host's registered copy, so bootstrap stays consistent."""
        registered = self.registered_config.read_text()
        updated = desired_config(registered, managed_settings())
        self.guest_python(GUEST_CONFIG_SYNC, {'content': updated,
                                              'expected_sha256': hashlib.sha256(registered.encode()).hexdigest()})
        fd, temporary = tempfile.mkstemp(dir=self.registered_config.parent, prefix='.config-')
        try:
            with os.fdopen(fd, 'w') as stream:
                stream.write(updated)
            os.replace(temporary, self.registered_config)
        except BaseException:
            Path(temporary).unlink(missing_ok=True)
            raise

    def authorize(self):
        """One-time operator step: create the Jenkins key and authorize it for vm-admin."""
        admin_key = self.private(self.config['guest']['identity_file'])
        if not AUTOMATION_KEY.exists():
            self.run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-C', 'battlefield-cli-automation',
                      '-f', str(AUTOMATION_KEY)], check=True)
        public = Path(str(AUTOMATION_KEY) + '.pub').read_text().strip()
        if not re.fullmatch(r'ssh-ed25519 [A-Za-z0-9+/=]+ battlefield-cli-automation', public):
            raise ValueError('Unexpected automation public key')
        install = ('umask 077; mkdir -p ~/.ssh; touch ~/.ssh/authorized_keys; '
                   'read -r key; grep -qxF "$key" ~/.ssh/authorized_keys || printf "%s\\n" "$key" >> ~/.ssh/authorized_keys')
        command = ['ssh', '-i', str(admin_key), '-o', 'IdentitiesOnly=yes', '-o', 'StrictHostKeyChecking=yes',
                   '-o', 'UserKnownHostsFile=' + str(self.known_hosts), GUEST, install]
        if subprocess.run(command, input=(public + '\n').encode(), timeout=120).returncode:
            raise ValueError('Could not authorize the automation key; unlock the admin key in ssh-agent and retry')
        if self.ssh(self.private(AUTOMATION_KEY), 'sudo -n true', timeout=30).returncode:
            raise ValueError('The automation key logs in but vm-admin needs passwordless sudo for the CLI job')
        print('Authorized the Jenkins automation key for vm-admin on the CLI VM. Revoke it by removing the '
              '"battlefield-cli-automation" line from ~vm-admin/.ssh/authorized_keys.')


def host_units_missing(checks):
    return bool(checks['stale_host_files'] or not checks['boot_start_enabled']
                or not checks['bridge_watchdog_active'] or not checks['watchdog_enabled'])


def guest_repairs(guest):
    """Drift beyond the secret or the managed settings is never repaired; it needs a reviewed change.

    A drifted worker environment blocks every guest action, so nothing restarts the
    worker on settings nobody reviewed.
    """
    if not guest['marker_matches']:
        return []
    needed = []
    if not guest['secret_matches']:
        needed.append('sync-worker-secret')
    elif not guest['worker_active'] or guest['worker_health'] != 204:
        needed.append('restart-worker')
    if not guest['worker_enabled']:
        needed.append('enable-worker')
    if (guest['config_registered'] or guest['config_pending']) and not guest['config_current']:
        needed.append('sync-worker-config')
    return needed


def guest_problems(guest):
    if guest is None:
        return []
    if not guest.get('reachable'):
        return ['CLI guest is not reachable with the automation key']
    checks = [
        (guest['identity_matches'], 'CLI guest identity differs from the registration'),
        (guest['marker_matches'], 'CLI worker environment differs from its registered content; inspect it, it is not repaired automatically'),
        (guest['secret_matches'], 'CLI worker secret differs from production'),
        (guest['worker_active'] and guest['worker_health'] == 204, 'CLI worker service is not healthy'),
        (guest['worker_enabled'], 'CLI worker service is not enabled for reboot'),
        (guest['config_registered'] or guest['config_pending'], 'CLI worker configuration differs from its registered content; inspect it, it is not repaired automatically'),
        (guest['config_current'], 'CLI worker settings differ from infra/deploy/agent-vm/managed-settings.json'),
    ]
    return [message for ok, message in checks if not ok]


def trusted_revision(expected):
    platform = load('platform', ROOT / 'scripts/infra/platform.py')
    platform.trusted_revision(expected)


def write_report(path, value):
    path = Path(path)
    fd = os.open(path, os.O_CREAT | os.O_TRUNC | os.O_WRONLY, 0o600)
    with os.fdopen(fd, 'w') as stream:
        json.dump(value, stream, indent=2, sort_keys=True)
        stream.write('\n')


def execute(args):
    worker = Worker(args)
    if args.action == 'authorize':
        worker.authorize()
        return 0
    secret = production_worker_secret()
    report = {'schema': 1, 'action': args.action}
    if args.action == 'verify':
        checks = worker.inspect(secret)
        report.update(checks=checks, problems=worker.problems(checks))
        write_report(args.report, report)
        for problem in report['problems']:
            print('CLI worker: ' + problem, file=sys.stderr)
        return 3 if report['problems'] else 0
    trusted_revision(args.revision)
    lock = load('operation_lock', ROOT / 'scripts/infra/operation-lock.py')
    with lock.acquire():
        try:
            report.update(reviewed_change(worker, args, secret))
        except ValueError as error:
            report['error'] = str(error)  # Still publish what was checked when a repair fails.
            write_report(args.report, report)
            raise
        write_report(args.report, report)
    if args.action == 'apply' and report['problems']:
        raise ValueError('CLI worker still has problems after apply: ' + '; '.join(report['problems']))
    print(f'CLI worker {args.action} completed; report written')
    return 0


def reviewed_change(worker, args, secret):
    """plan records repairs in a receipt; apply re-checks that exact plan, consumes it, repairs."""
    checks = worker.inspect(secret)
    repairs = worker.repairs(checks)
    receipt = Path(args.receipt)
    if args.action == 'plan':
        write_report(receipt, {'schema': 1, 'revision': args.revision, 'created': time.time(), 'repairs': repairs})
        return {'checks': checks, 'repairs': repairs, 'problems': worker.problems(checks)}
    saved = json.loads(worker.private(receipt).read_text())
    age = time.time() - saved.get('created', 0)
    if saved.get('revision') != args.revision or not 0 <= age <= RECEIPT_SECONDS:
        raise ValueError('Plan revision changed or the plan expired; run and review a new plan')
    if saved.get('repairs') != repairs:
        raise ValueError('The CLI worker changed since the reviewed plan; run and review a new plan')
    receipt.unlink()  # A failure or interruption consumes the approval.
    if repairs:
        worker.apply(repairs, secret)
    checks = worker.inspect(secret)
    return {'applied': repairs, 'checks': checks, 'problems': worker.problems(checks)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['authorize', 'plan', 'apply', 'verify'])
    parser.add_argument('--revision', help='Reviewed origin/main revision (plan/apply)')
    parser.add_argument('--receipt', help='Private plan receipt path (plan/apply)')
    parser.add_argument('--report', help='Secret-free JSON report path (plan/apply/verify)')
    parser.add_argument('--become-password-file', help='Private host sudo password file (apply)')
    args = parser.parse_args()
    if args.action != 'authorize' and not args.report:
        parser.error('--report is required')
    if args.action in ('plan', 'apply') and not (args.revision and args.receipt):
        parser.error('plan and apply require --revision and --receipt')
    os.umask(0o077)
    return execute(args)


if __name__ == '__main__':
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        sys.exit(130)
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        print('CLI worker: ' + (str(error) if isinstance(error, ValueError) else 'Operation failed; no private output published'), file=sys.stderr)
        sys.exit(1)
