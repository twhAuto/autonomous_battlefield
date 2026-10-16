#!/usr/bin/env python3
"""Keep the registered CLI worker running and in sync with production (AB-139, AB-162).

Runs on the physical host, as the operator Jenkins also runs as. `verify` only reads.
`plan` records the repairs it would make in a short-lived receipt; `apply` re-checks
that plan, consumes the receipt, repairs, then verifies. `authorize` is the one-time
operator step that lets Jenkins reach the VM with a dedicated key. The worker secret
always comes from the production application .env, so a rotation reaches the worker
on the next apply. The worker binary comes from the backend image production runs, so
an apply after a release upgrades the worker to the same code. No secret is printed,
logged, written to a report or put in argv.
"""
import argparse
import contextlib
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import sys
import tarfile
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
GUEST_REPAIRS = {'sync-worker-secret', 'restart-worker', 'enable-worker', 'sync-worker-config', 'upgrade-worker', 'resume-worker'}
WORKER_BINARY = '/usr/local/bin/battlefield-agent'
STAGED_BINARY = '/var/lib/battlefield-cli-upgrade/battlefield-agent'
# The in-cluster agent runs the same /app/agent binary from the deployed backend image.
RELEASE_DEPLOYMENT = ['-n', 'battlefield', 'get', 'deployment', 'battlefield-agent',
                      '-o', 'jsonpath={.spec.template.spec.containers[0].image}']
CLUSTER_IDENTITY = ['get', 'namespace', 'kube-system', '-o', 'jsonpath={.metadata.uid}']
RELEASE_IMAGE = re.compile(r'[a-z0-9][a-z0-9./_-]*@sha256:([0-9a-f]{64})')
RELEASE_CACHE = HOME / '.cache/autonomous-battlefield/cli-worker'
RELEASE_MAX_BYTES = 512 * 1024 * 1024
# Decisions time out after two minutes by default; five minutes also covers a claim in flight.
DRAIN_SECONDS = 300


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
    # null requires the key itself to be absent; an explicit `agentConcurrency: null` still breaks older workers.
    concurrency_matches = ('agentConcurrency' not in ai) if concurrency is None else ai.get('agentConcurrency') == concurrency
    if concurrency_matches and all(
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
try:
    status = json.load(urllib.request.urlopen('http://127.0.0.1:8083/status', timeout=5))
except Exception:
    status = None  # Workers older than the drain support have no status endpoint.
print(json.dumps({
    'machine_id': pathlib.Path('/etc/machine-id').read_text().strip(),
    'domain_uuid': pathlib.Path('/sys/class/dmi/id/product_uuid').read_text().strip().lower(),
    'secret_matches': len(secret) == 1 and hashlib.sha256(secret[0]).hexdigest() == expected,
    'config_sha256': hashlib.sha256(pathlib.Path('/etc/battlefield-agent/config.yaml').read_bytes()).hexdigest(),
    'marker_matches': (not files) or files.get('/etc/battlefield-agent/worker.env') == hashlib.sha256(env).hexdigest(),
    'worker_active': unit('is-active') == 'active',
    'worker_enabled': unit('is-enabled') == 'enabled',
    'worker_health': health,
    'binary_sha256': hashlib.sha256(pathlib.Path('/usr/local/bin/battlefield-agent').read_bytes()).hexdigest(),
    'status': status,
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


# Validates a configuration with a worker binary, in the worker's environment. The
# environment reaches the binary as process environment, never argv.
GUEST_CHECK_CONFIG = r'''
def check_config(binary, config):
    env = {'PATH': '/usr/bin:/bin'}
    for line in pathlib.Path('/etc/battlefield-agent/worker.env').read_text().splitlines():
        if line and not line.startswith('#') and '=' in line:
            key, value = line.split('=', 1)
            env[key] = value
    result = subprocess.run([str(binary), '-check-config', '-config', str(config)], env=env, capture_output=True, timeout=30)
    return result.returncode == 0
'''

GUEST_PUBLISH = r'''
def publish(target, content, mode):
    fd, temporary = tempfile.mkstemp(dir=target.parent, prefix='.battlefield-')
    try:
        os.fchmod(fd, mode)
        with os.fdopen(fd, 'wb' if isinstance(content, bytes) else 'w') as stream:
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, target)
    except BaseException:
        pathlib.Path(temporary).unlink(missing_ok=True)
        raise
'''

GUEST_CONFIG_SYNC = r'''
import hashlib, json, os, pathlib, subprocess, sys, tempfile, yaml
request = json.loads(sys.stdin.read())
path = pathlib.Path('/etc/battlefield-agent/config.yaml')
updated = request['content']
yaml.safe_load(updated)
current = hashlib.sha256(path.read_bytes()).hexdigest()
target = hashlib.sha256(updated.encode()).hexdigest()
# Resuming an interrupted sync finds the target content already in place.
if current not in (request['expected_sha256'], target):
    raise SystemExit('worker configuration differs from its registered content; not changed')
''' + GUEST_PUBLISH + GUEST_CHECK_CONFIG + r'''
# A worker with -check-config validates the settings before they replace the working file.
if request.get('check') and current != target:
    fd, candidate = tempfile.mkstemp(dir=path.parent, prefix='.battlefield-check-')
    try:
        with os.fdopen(fd, 'w') as stream:
            stream.write(updated)
        valid = check_config('/usr/local/bin/battlefield-agent', candidate)
    finally:
        pathlib.Path(candidate).unlink(missing_ok=True)
    if not valid:
        raise SystemExit('the worker rejects the managed settings; not changed')
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

# Drain state from the worker, plus a process count for workers without a status endpoint.
GUEST_ACTIVITY = r'''
import json, pathlib, subprocess, urllib.request
def show(prop):
    return subprocess.run(['systemctl', 'show', '-p', prop, '--value', 'battlefield-worker'], capture_output=True, text=True).stdout.strip()
try:
    health = urllib.request.urlopen('http://127.0.0.1:8083/healthz', timeout=5).status
except Exception:
    health = 0
try:
    status = json.load(urllib.request.urlopen('http://127.0.0.1:8083/status', timeout=5))
except Exception:
    status = None
main, group = show('MainPID'), show('ControlGroup')
# CLI decisions run as child processes of the worker. An unreadable count is never idle.
try:
    processes = pathlib.Path('/sys/fs/cgroup' + group + '/cgroup.procs').read_text().split()
    children = len([p for p in processes if p != main])
except OSError:
    children = None
print(json.dumps({'health': health, 'status': status, 'children': children}))
'''

GUEST_CHECK_RELEASE = r'''
import hashlib, json, os, pathlib, subprocess, sys
request = json.loads(sys.stdin.read())
binary = pathlib.Path(request['binary'])
if hashlib.sha256(binary.read_bytes()).hexdigest() != request['sha256']:
    raise SystemExit('staged worker binary does not match the release; nothing was changed')
binary.chmod(0o700)
''' + GUEST_CHECK_CONFIG + r'''
if not check_config(binary, '/etc/battlefield-agent/config.yaml'):
    raise SystemExit('the new worker binary rejects the installed configuration; nothing was changed')
print(json.dumps({'valid': True}))
'''

GUEST_INSTALL = r'''
import hashlib, json, os, pathlib, subprocess, sys, tempfile
request = json.loads(sys.stdin.read())
target = pathlib.Path('/usr/local/bin/battlefield-agent')
source = pathlib.Path(request['source'])
current = hashlib.sha256(target.read_bytes()).hexdigest()
wanted = request['target_sha256']
# Resuming an interrupted install finds the target binary already in place.
if current not in (request['expected_sha256'], wanted):
    raise SystemExit('worker binary differs from its registered release; not changed')
''' + GUEST_PUBLISH + r'''
if current != wanted:
    content = source.read_bytes()
    if hashlib.sha256(content).hexdigest() != wanted:
        raise SystemExit('staged worker binary does not match the release; not changed')
    publish(target, content, 0o755)
marker = pathlib.Path('/etc/battlefield-cli-bootstrap/guest.json')
if marker.exists():
    data = json.loads(marker.read_text())
    if data['files'].get('/usr/local/bin/battlefield-agent') != wanted:
        data['files']['/usr/local/bin/battlefield-agent'] = wanted
        publish(marker, json.dumps(data, indent=2) + '\n', 0o600)
subprocess.run(['systemctl', 'restart', 'battlefield-worker'], check=True)
source.unlink(missing_ok=True)
print(json.dumps({'changed': current != wanted}))
'''


def write_private(path, content):
    """Atomically replace a private host file, keeping mode 0600."""
    path = Path(path)
    fd, temporary = tempfile.mkstemp(dir=path.parent, prefix='.' + path.name + '-')
    try:
        with os.fdopen(fd, 'wb' if isinstance(content, bytes) else 'w') as stream:
            stream.write(content)
        os.replace(temporary, path)
    except BaseException:
        Path(temporary).unlink(missing_ok=True)
        raise


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
        self.release_info = None

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
        self.release_info = self.release()
        checks['release'] = {'image_digest': self.release_info['digest']} if self.release_info else None
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
                guest.update(self.binary_checks(guest.pop('binary_sha256'), guest.pop('status')))
            checks['guest'] = guest
        return checks

    def binary_checks(self, binary, status):
        """Compare the guest binary with its registration and the production release."""
        registered = self.config['guest']['artifacts']['battlefield-agent']['sha256']
        release = self.release_info['sha256'] if self.release_info else None
        current = binary == release
        return {
            'release_known': release is not None,
            'binary_registered': binary == registered,
            'binary_current': current,
            # An interrupted upgrade left the guest updated but the registration behind.
            'binary_pending': current and registered != release,
            'drain_supported': isinstance(status, dict),
            'draining': isinstance(status, dict) and status.get('draining') is True,
            # Older workers reject agentConcurrency, so a set value waits for the upgrade.
            'config_needs_upgrade': managed_settings()['agentConcurrency'] is not None and not current,
        }

    def release(self):
        """The worker binary in the backend image production runs, or None if unreadable."""
        try:
            # Only the registered production cluster, never the ambient kubectl context.
            if self.kubectl(CLUSTER_IDENTITY) != self.config['host']['kube_system_uid']:
                return None
            image = self.kubectl(RELEASE_DEPLOYMENT)
            match = RELEASE_IMAGE.fullmatch(image or '')
            if not match:
                return None
            path = self.release_binary(image, match.group(1))
            return {'image': image, 'digest': match.group(1), 'path': path,
                    'sha256': hashlib.sha256(path.read_bytes()).hexdigest()}
        except (ValueError, OSError, subprocess.SubprocessError, tarfile.TarError):
            return None

    def kubectl(self, arguments):
        host = self.config['host']
        result = self.run(['/usr/bin/kubectl', '--kubeconfig=' + host['kubeconfig'], '--context=' + host['context'],
                           '--request-timeout=15s', *arguments], timeout=30)
        return result.stdout.decode().strip() if result.returncode == 0 else None

    def release_binary(self, image, digest):
        """Copy /app/agent out of the digest-pinned image once, into a private cache.

        The digest pins the image content, so the copy is exactly what production runs.
        """
        RELEASE_CACHE.mkdir(mode=0o700, parents=True, exist_ok=True)
        cached = RELEASE_CACHE / digest
        if cached.exists():
            return self.private(cached)
        present = self.run(['docker', 'image', 'inspect', image]).returncode == 0
        if not present:
            self.run(['docker', 'pull', '--quiet', image], timeout=600, check=True)
        try:
            container = self.run(['docker', 'create', image, '/app/agent'], check=True).stdout.decode().strip()
            try:
                archive = self.run(['docker', 'cp', container + ':/app/agent', '-'], timeout=300, check=True).stdout
            finally:
                self.run(['docker', 'rm', container])
        finally:
            if not present:
                self.run(['docker', 'image', 'rm', image])
        with tarfile.open(fileobj=io.BytesIO(archive)) as archive_file:
            member = archive_file.getmember('agent')
            if not member.isreg() or not 0 < member.size <= RELEASE_MAX_BYTES:
                raise ValueError('The production image has no usable worker binary')
            content = archive_file.extractfile(member).read()
        for old in RELEASE_CACHE.iterdir():
            old.unlink()  # Keep only the release production runs now.
        write_private(cached, content)  # Atomic: an interrupted copy never becomes the cached release.
        return cached

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
        self.repair_guest([r for r in repairs if r in GUEST_REPAIRS], secret, guest)
        self.wait_for(lambda: (self.inspect(secret)['guest'] or {}).get('worker_health') == 204, 90,
                      'The CLI worker did not report healthy after the repair')

    def repair_guest(self, repairs, secret, guest):
        """Perform exactly the reviewed guest repairs, in a safe order.

        A worker with the wrong secret claims nothing, so the secret sync restarts it
        without draining. Restarts for a new binary or settings wait for running decisions.
        """
        key = self.private(AUTOMATION_KEY)
        restart = 'restart-worker' in repairs
        if 'sync-worker-secret' in repairs:
            self.guest_python(GUEST_SYNC, {'secret': secret})  # Restarts the worker itself.
            restart = False
            guest = dict(guest, worker_active=True)  # It can claim now, so later restarts must drain.
        if 'upgrade-worker' in repairs:
            self.upgrade_worker(guest)  # Drains, restarts and verifies the new binary.
            guest = dict(guest, worker_active=True, drain_supported=True)
        if 'sync-worker-config' in repairs:
            self.drain(guest)
            with self.resuming_on_failure(guest):
                self.sync_config(check=guest['binary_current'] or 'upgrade-worker' in repairs)
            restart = True
        if 'resume-worker' in repairs and not restart:
            self.signal_worker('SIGUSR2')
        if restart and self.ssh(key, 'sudo -n systemctl restart battlefield-worker').returncode:
            raise ValueError('Could not restart the CLI worker')
        if 'enable-worker' in repairs and self.ssh(key, 'sudo -n systemctl enable battlefield-worker').returncode:
            raise ValueError('Could not enable the CLI worker for reboot')

    def signal_worker(self, signal):
        command = 'sudo -n systemctl kill --kill-whom=main --signal=' + signal + ' battlefield-worker'
        if self.ssh(self.private(AUTOMATION_KEY), command).returncode:
            raise ValueError('Could not signal the CLI worker')

    @contextlib.contextmanager
    def resuming_on_failure(self, guest):
        """Never leave a drained worker idle: resume claims if the next step fails."""
        try:
            yield
        except BaseException:
            if guest['worker_active'] and guest['drain_supported']:
                with contextlib.suppress(ValueError, OSError, subprocess.SubprocessError):
                    self.signal_worker('SIGUSR2')
            raise

    def drain(self, guest):
        """Wait until no CLI decision is running, so a restart cancels nothing.

        A worker with drain support stops claiming first, and resumes if the wait times
        out. An older worker cannot stop claiming: the job waits for it to be idle and
        restarts at once, so a decision claimed in that moment can still fall back.
        """
        if not guest['worker_active']:
            return
        if not guest['drain_supported']:
            idle = []

            def settled():
                idle.append(self.guest_python(GUEST_ACTIVITY, {})['children'] == 0)
                return idle[-2:] == [True, True]
            self.wait_for(settled, DRAIN_SECONDS, 'CLI decisions were still running after 5 minutes; nothing was changed')
            return
        self.signal_worker('SIGUSR1')
        with self.resuming_on_failure(guest):
            try:
                self.wait_for(lambda: (self.guest_python(GUEST_ACTIVITY, {})['status'] or {}).get('active') == 0,
                              DRAIN_SECONDS, 'drain timed out')
            except ValueError as error:
                raise ValueError('CLI decisions were still running after 5 minutes; the worker resumed claiming '
                                 'and nothing was changed') from error

    def stage_binary(self, content):
        command = 'sudo -n sh -c ' + shlex.quote(f'umask 077; mkdir -p {Path(STAGED_BINARY).parent}; cat > {STAGED_BINARY}')
        if self.ssh(self.private(AUTOMATION_KEY), command, content, timeout=300).returncode:
            raise ValueError('Could not copy the worker binary to the CLI guest')

    def upgrade_worker(self, guest):
        """Install the production release: validate, drain, swap, verify, then register.

        A release that fails verification is replaced by the registered binary again, and
        the registration only changes after the new worker is healthy.
        """
        release = self.release_info
        registered = self.config['guest']['artifacts']['battlefield-agent']['sha256']
        content = Path(release['path']).read_bytes()
        if hashlib.sha256(content).hexdigest() != release['sha256']:
            raise ValueError('The cached worker release changed; run a new plan')
        self.stage_binary(content)
        self.guest_python(GUEST_CHECK_RELEASE, {'binary': STAGED_BINARY, 'sha256': release['sha256']})
        self.drain(guest)
        try:
            self.guest_python(GUEST_INSTALL, {'source': STAGED_BINARY, 'expected_sha256': registered,
                                              'target_sha256': release['sha256']})
            self.wait_for(self.release_healthy, 90, 'not healthy')
        except Exception as error:
            # The install may have replaced the binary before failing, so always restore.
            with self.resuming_on_failure(guest):
                self.restore_binary(registered, release['sha256'])
            raise ValueError('The CLI worker upgrade failed to install or become healthy; '
                             'the registered worker binary was restored') from error
        self.register_binary(content, release['sha256'])

    def release_healthy(self):
        activity = self.guest_python(GUEST_ACTIVITY, {})
        return activity['health'] == 204 and isinstance(activity['status'], dict) and not activity['status']['draining']

    def restore_binary(self, registered, failed):
        artifact = self.private(self.config['guest']['artifacts']['battlefield-agent']['path']).read_bytes()
        if hashlib.sha256(artifact).hexdigest() != registered:
            raise ValueError('The upgraded CLI worker failed and the registered binary copy is unusable; '
                             'restore it with the CLI bootstrap')
        self.stage_binary(artifact)
        self.guest_python(GUEST_INSTALL, {'source': STAGED_BINARY, 'expected_sha256': failed, 'target_sha256': registered})

    def register_binary(self, content, digest):
        """Record the installed release where the CLI bootstrap expects it."""
        artifact = self.private(self.config['guest']['artifacts']['battlefield-agent']['path'])
        write_private(artifact, content)
        registration = json.loads(self.private(CLI_CONFIG).read_text())
        registration['guest']['artifacts']['battlefield-agent']['sha256'] = digest
        self.config = self.bootstrap.validate(registration)
        write_private(CLI_CONFIG, json.dumps(registration, indent=2) + '\n')

    def sync_config(self, check=False):
        """Change the guest config, then the host's registered copy, so bootstrap stays consistent.

        check validates the settings with the installed worker first; only the
        production release supports that.
        """
        registered = self.registered_config.read_text()
        updated = desired_config(registered, managed_settings())
        self.guest_python(GUEST_CONFIG_SYNC, {'content': updated, 'check': check,
                                              'expected_sha256': hashlib.sha256(registered.encode()).hexdigest()})
        write_private(self.registered_config, updated)

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
    upgrade = needs_upgrade(guest)
    if not guest['secret_matches']:
        needed.append('sync-worker-secret')
    elif not upgrade and (not guest['worker_active'] or guest['worker_health'] != 204):
        needed.append('restart-worker')
    elif not upgrade and guest['draining']:
        needed.append('resume-worker')
    if upgrade:
        needed.append('upgrade-worker')
    if not guest['worker_enabled']:
        needed.append('enable-worker')
    if ((guest['config_registered'] or guest['config_pending']) and not guest['config_current']
            and (upgrade or not guest['config_needs_upgrade'])):
        needed.append('sync-worker-config')
    return needed


def needs_upgrade(guest):
    """Upgrade a registered binary that differs from production, or finish an interrupted upgrade."""
    return guest['release_known'] and (guest['binary_pending'] or (guest['binary_registered'] and not guest['binary_current']))


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
        (guest['binary_registered'] or guest['binary_current'], 'CLI worker binary differs from its registered release; inspect it, it is not repaired automatically'),
        (not guest['draining'], 'CLI worker is draining and claims no new decisions'),
        (guest['config_current'] or not guest['config_needs_upgrade'], 'CLI worker settings need the production worker release; apply upgrades it first'),
    ]
    return [message for ok, message in checks if not ok]


def notices(checks):
    """Worth acting on, but not a failure: a release does not stop the old worker from working."""
    guest = checks['guest'] or {}
    if not (guest.get('reachable') and guest.get('identity_matches')):
        return []
    if not guest['release_known']:
        return ['Could not read the production worker release, so the upgrade status is unknown']
    if not guest['binary_current'] or guest['binary_pending']:
        return ['CLI worker binary differs from the production release; run apply to upgrade it']
    return []


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
        report.update(checks=checks, problems=worker.problems(checks), notices=notices(checks))
        write_report(args.report, report)
        for notice in report['notices']:
            print('CLI worker notice: ' + notice, file=sys.stderr)
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
        write_report(receipt, {'schema': 1, 'revision': args.revision, 'created': time.time(), 'repairs': repairs,
                               'release': checks.get('release')})
        return {'checks': checks, 'repairs': repairs, 'problems': worker.problems(checks), 'notices': notices(checks)}
    saved = json.loads(worker.private(receipt).read_text())
    age = time.time() - saved.get('created', 0)
    if saved.get('revision') != args.revision or not 0 <= age <= RECEIPT_SECONDS:
        raise ValueError('Plan revision changed or the plan expired; run and review a new plan')
    # The release is part of the plan: a new delivery means a different worker binary.
    if saved.get('repairs') != repairs or saved.get('release') != checks.get('release'):
        raise ValueError('The CLI worker changed since the reviewed plan; run and review a new plan')
    receipt.unlink()  # A failure or interruption consumes the approval.
    if repairs:
        worker.apply(repairs, secret)
    checks = worker.inspect(secret)
    return {'applied': repairs, 'checks': checks, 'problems': worker.problems(checks), 'notices': notices(checks)}


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
