#!/usr/bin/env python3
"""Privileged CI lifecycle operations. Input is private stdin; output is safe JSON.

Invoked only by ci.yml, never by Jenkins. API response bodies, subprocess output,
credential material and exceptions from external libraries are never printed.
"""
import base64
import hashlib
import http.client
import importlib.util
import json
import os
from pathlib import Path
import pwd
import re
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
import time
import traceback
import urllib.error
import urllib.parse
import urllib.request
import uuid
import xml.etree.ElementTree as ET
import zipfile

STATE = Path('/var/lib/battlefield-ci')
CONFIG = Path('/etc/battlefield-ci')
JHOME = Path('/var/lib/jenkins')
SONAR = 'http://127.0.0.1:9000/'
JENKINS = 'http://127.0.0.1:8080/'
NODE = 'battlefield-local'
CREDENTIALS = {
    'github-app-battlefield': 'githubApp', 'battlefield-ghcr': 'usernamePassword',
    'battlefield-local-secret-env': 'file', 'battlefield-cosign-key': 'file',
    'battlefield-cosign-password': 'string', 'battlefield-cosign-public-key': 'file',
    'battlefield-production-kubeconfig': 'file', 'battlefield-production-access-token': 'file',
    'battlefield-production-access-service-token': 'file',
}
FOLDER_CREDENTIALS = ('host-inventory', 'host-inputs', 'host-vault-password', 'host-sudo-password')


class Failure(Exception):
    """Only fixed, non-secret operator messages belong here."""


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class API:
    def __init__(self, base, user, password):
        self.base = base
        self.authorization = 'Basic ' + base64.b64encode((user + ':' + password).encode()).decode()
        self.opener = urllib.request.build_opener(NoRedirect())

    def call(self, path, fields=None, *, missing=False, raw=False):
        data = urllib.parse.urlencode(fields).encode() if fields is not None else None
        request = urllib.request.Request(self.base + path, data=data,
                    headers={'Authorization': self.authorization, 'Content-Type': 'application/x-www-form-urlencoded'})
        try:
            with self.opener.open(request, timeout=20) as response:
                content = response.read()
            return content if raw else (json.loads(content) if content else {})
        except urllib.error.HTTPError as error:
            if missing and error.code == 404:
                return None
            # POST quietDown/cancelQuietDown may redirect locally after success.
            if fields is not None and path in ('quietDown', 'cancelQuietDown') and error.code in (302, 303):
                location = urllib.parse.urljoin(self.base, error.headers.get('Location', ''))
                if location == self.base:
                    return {}
            raise Failure('Service API ' + path.split('?', 1)[0] + ' rejected the request (HTTP ' + str(error.code) + '); check the private input permissions') from None
        except (urllib.error.URLError, TimeoutError, ConnectionError, http.client.HTTPException, json.JSONDecodeError):
            raise Failure('Service API unavailable or returned an invalid response') from None


def run(args, *, timeout=120, data=None):
    result = subprocess.run(args, input=data, capture_output=True, timeout=timeout)
    if result.returncode:
        raise Failure('Host command failed; inspect service health locally')
    return result.stdout


def private(path, content, owner=None, mode=0o600):
    """Atomic replacement; caller first verifies directory ownership and no links."""
    path = Path(path)
    if path.is_symlink() or (path.exists() and not path.is_file()):
        raise Failure('Refusing a redirected CI file')
    content = content.encode() if isinstance(content, str) else content
    if path.exists() and path.read_bytes() == content:
        metadata = path.stat()
        account = pwd.getpwnam(owner) if owner else None
        changed = stat.S_IMODE(metadata.st_mode) != mode or (account is not None and (metadata.st_uid, metadata.st_gid) != (account.pw_uid, account.pw_gid))
        os.chmod(path, mode)
        if account:
            os.chown(path, account.pw_uid, account.pw_gid)
        return changed
    fd, name = tempfile.mkstemp(prefix='.ci-', dir=path.parent)
    try:
        with os.fdopen(fd, 'wb') as output:
            output.write(content)
            output.flush()
            os.fsync(output.fileno())
        os.chmod(name, mode)
        if owner:
            account = pwd.getpwnam(owner)
            os.chown(name, account.pw_uid, account.pw_gid)
        os.replace(name, path)
    finally:
        if os.path.exists(name):
            os.unlink(name)
    return True


def decode_inputs(encoded):
    if not isinstance(encoded, dict):
        raise Failure('Expected named encrypted CI inputs')
    values = {}
    for key, value in encoded.items():
        if key.startswith('ci_'):
            if not isinstance(value, str) or len(value) > 12 * 1024 * 1024:
                raise Failure('Invalid encrypted CI input size')
            try:
                values[key] = base64.b64decode(value, validate=True).decode('utf-8')
            except (ValueError, UnicodeError):
                raise Failure('Invalid encrypted CI input encoding') from None
    return values


def secret(values, name):
    value = values.get(name, '').strip()
    if len(value) < 16 or '\n' in value or '\r' in value or '\0' in value:
        raise Failure('Missing or invalid encrypted CI authentication input: ' + name)
    return value


def credentials(values):
    try:
        result = json.loads(values['ci_credentials'])
    except (KeyError, json.JSONDecodeError):
        raise Failure('Fresh CI requires ci_credentials as an encrypted JSON object') from None
    if not isinstance(result, dict) or set(result) != set(CREDENTIALS) | set(FOLDER_CREDENTIALS):
        raise Failure('Fresh CI credential IDs must match the documented contract')
    for name, entry in result.items():
        kind = CREDENTIALS.get(name, 'file')
        keys = {'type', 'username', 'password'} if kind == 'usernamePassword' else {'type', 'appId', 'privateKey'} if kind == 'githubApp' else {'type', 'value'}
        if not isinstance(entry, dict) or set(entry) != keys or entry['type'] != kind:
            raise Failure('Fresh CI credential type does not match its declared ID')
        if any(not isinstance(v, str) or not v or '\0' in v for v in entry.values()):
            raise Failure('Fresh CI credentials must contain nonempty strings')
        if kind == 'githubApp' and not re.fullmatch(r'[0-9]+', entry['appId']):
            raise Failure('GitHub App ID must be numeric')
        if name == 'host-inputs' and not entry['value'].startswith('$ANSIBLE_VAULT;'):
            raise Failure('Platform host inputs must already be encrypted')
    return result


def managed_path(path):
    for part in [path, *path.parents]:
        if part.is_symlink():
            raise Failure('Refusing a CI path with a symbolic-link parent')


def ownership(config):
    if Path('/etc/machine-id').read_text().strip() != config['machine_id']:
        raise Failure('CI host identity changed')
    for root in (STATE, CONFIG, JHOME, Path(config['home']) / '.config/systemd/user'):
        managed_path(root)
    for path in (Path('/opt/battlefield-ci'), Path('/var/cache/battlefield-ci'), Path('/etc/systemd/system/jenkins.service.d'),
                 JHOME / 'init.groovy.d', JHOME / 'plugins', JHOME / 'config.xml',
                 JHOME / 'battlefield-bootstrap-api-token', JHOME / 'battlefield-agent-secret',
                 Path(config['home']) / '.config/jenkins-agent'):
        managed_path(path)
    if Path('/opt/battlefield-ci').exists() and not (STATE / 'machine-id').exists():
        raise Failure('Existing CI release directory has no matching ownership marker')
    for root in (STATE, CONFIG):
        if root.exists():
            marker = root / 'machine-id'
            if not root.is_dir() or root.stat().st_uid != 0 or root.stat().st_mode & 0o022:
                raise Failure('Unsafe CI state/configuration directory')
            if not marker.is_file() or marker.is_symlink() or marker.read_text().strip() != config['machine_id']:
                raise Failure('CI directory has no matching ownership marker')
    overrides = Path(config['home']) / '.config/systemd/user/jenkins-agent.service.d'
    if overrides.exists() and any(overrides.iterdir()):
        raise Failure('Existing agent overrides require an explicit configuration review')
    for name in ('00-battlefield-maintenance.groovy', '90-battlefield.groovy'):
        file = JHOME / 'init.groovy.d' / name
        if file.exists() and not (STATE / 'machine-id').exists():
            raise Failure('Existing bootstrap script has no matching ownership marker')
    unit = Path(config['home']) / '.config/systemd/user/jenkins-agent.service'
    if unit.exists():
        managed_path(unit)
        content = unit.read_bytes()
        if not content.startswith(b'# battlefield-ci-managed-v1\n') and hashlib.sha256(content).hexdigest() != config['agent_unit_sha256']:
            raise Failure('Agent service changed after registration; review and re-register its fingerprint')
    if config['mode'] == 'install' and (JHOME / 'config.xml').exists() and not (STATE / 'machine-id').exists():
        raise Failure('Fresh CI install refuses an existing controller')
    if config['mode'] == 'adopt' and not (JHOME / 'config.xml').is_file():
        raise Failure('Adoption requires a restored/existing controller home')


def active():
    return subprocess.run(['systemctl', 'is-active', '--quiet', 'jenkins'], capture_output=True).returncode == 0


def jenkins(config, values):
    saved = JHOME / 'battlefield-bootstrap-api-token'
    token = values.get('ci_jenkins_api_token', '').strip()
    if not token and saved.is_file():
        token = saved.read_text().strip()
    if not token:
        raise Failure('Existing controller needs its encrypted ci_jenkins_api_token')
    return API(JENKINS, config['jenkins_admin'], token)



def controller_contract(client, config):
    tags = {'githubApp': 'org.jenkinsci.plugins.github_branch_source.GitHubAppCredentials',
            'usernamePassword': 'com.cloudbees.plugins.credentials.impl.UsernamePasswordCredentialsImpl',
            'file': 'org.jenkinsci.plugins.plaincredentials.impl.FileCredentialsImpl',
            'string': 'org.jenkinsci.plugins.plaincredentials.impl.StringCredentialsImpl'}
    expected = dict(CREDENTIALS, **{'sonarqube-battlefield-token': 'string'})
    for identifier, kind in expected.items():
        content = client.call('credentials/store/system/domain/_/credential/' + identifier + '/config.xml', missing=True, raw=True)
        if content is None:
            if config['mode'] == 'adopt':
                raise Failure('Missing required controller credential: ' + identifier)
        elif ET.fromstring(content).tag.replace('__', '_') != tags[kind]:
            raise Failure('Controller credential has a different type: ' + identifier)
    folder = client.call('job/battlefield-platform/config.xml', missing=True, raw=True)
    if folder is not None:
        root = ET.fromstring(folder)
        if root.tag != 'com.cloudbees.hudson.plugins.folder.Folder' or not root.findtext('description', '').startswith('battlefield-platform-managed-v1'):
            raise Failure('Existing platform folder has a different type or owner')
        for identifier in FOLDER_CREDENTIALS:
            content = client.call('job/battlefield-platform/credentials/store/folder/domain/_/credential/' + identifier + '/config.xml', missing=True, raw=True)
            if content is None:
                if config['mode'] == 'adopt':
                    raise Failure('Missing required platform credential: ' + identifier)
            else:
                root = ET.fromstring(content)
                if root.tag != tags['file'] or root.findtext('description') != 'battlefield-platform-managed-v1':
                    raise Failure('Existing platform credential has a different type or owner')
    elif config['mode'] == 'adopt':
        raise Failure('Adoption needs the existing owned platform folder and its credentials')
    node = client.call('computer/' + NODE + '/config.xml', missing=True, raw=True)
    if node is not None:
        root = ET.fromstring(node)
        launcher = root.find('launcher')
        if (root.findtext('remoteFS') != config['home'] + '/jenkins-agent' or root.findtext('numExecutors') != '4'
                or 'battlefield-production' not in root.findtext('label', '').split()
                or launcher is None or launcher.get('class') != 'hudson.slaves.JNLPLauncher'):
            raise Failure('Existing shared agent differs from the registered layout')
    repository = client.call('job/autonomous-battlefield/config.xml', missing=True, raw=True)
    if repository is not None:
        root = ET.fromstring(repository)
        sources = root.findall('.//source')
        if not root.tag.endswith('.WorkflowMultiBranchProject') or len(sources) != 1 or sources[0].findtext('repository') != 'autonomous_battlefield' or sources[0].findtext('repoOwner') != 'twhAuto':
            raise Failure('Existing repository job belongs to another source; refusing adoption')


def sonar_containers(lock):
    if shutil.which('docker') is None:
        return {}
    result = run(['docker', 'ps', '-aq', '--filter', 'label=com.docker.compose.project=sonarqube']).decode().split()
    if not result:
        # Existing volumes without matching containers require a reviewed restore, not initialize.
        volumes = run(['docker', 'volume', 'ls', '--format', '{{.Name}}']).decode().split()
        if any(x.startswith('sonarqube_') for x in volumes):
            raise Failure('Sonar volumes exist without registered containers; restore the Compose stack before adoption')
        return {}
    containers = json.loads(run(['docker', 'inspect', *result]))
    by_service = {}
    expected = {'database': {'sonarqube_sonarqube_database': '/var/lib/postgresql/data'},
                'sonarqube': {'sonarqube_sonarqube_' + n: '/opt/sonarqube/' + n for n in ('data', 'extensions', 'logs')}}
    for container in containers:
        service = container['Config']['Labels'].get('com.docker.compose.service')
        if service not in expected or service in by_service:
            raise Failure('Unexpected Sonar Compose services; refusing adoption')
        actual = {v['Name']: v['Destination'] for v in container['Mounts'] if v['Type'] == 'volume'}
        if any(actual.get(k) != v for k, v in expected[service].items()):
            raise Failure('Sonar persistent volumes differ from the registered layout')
        image = json.loads(run(['docker', 'image', 'inspect', container['Image']]))[0]
        digest = lock['postgres' if service == 'database' else 'sonarqube'].split('@')[1]
        if not any(x.endswith('@' + digest) for x in image.get('RepoDigests', [])):
            raise Failure('Existing Sonar/database image differs from the pinned release; upgrades need a separate review')
        by_service[service] = container
    if set(by_service) != set(expected):
        raise Failure('Existing Sonar Compose stack is incomplete')
    return by_service


def database_password(containers, values):
    if not containers:
        return secret(values, 'ci_database_password')
    env = {name: dict(x.split('=', 1) for x in c['Config']['Env'] if '=' in x) for name, c in containers.items()}
    password = env['database'].get('POSTGRES_PASSWORD')
    if not password or password != env['sonarqube'].get('SONAR_JDBC_PASSWORD'):
        raise Failure('Existing Sonar database credentials disagree')
    declared = values.get('ci_database_password', '').strip()
    if declared and declared != password:
        raise Failure('CI adoption never rotates an existing Sonar database password')
    return password


def sonar_token(config, values):
    imported = values.get('ci_sonar_token', '').strip()
    saved = STATE / 'sonar-token'
    if active():
        script = """import com.cloudbees.plugins.credentials.SystemCredentialsProvider
import org.jenkinsci.plugins.plaincredentials.impl.StringCredentialsImpl
def c=SystemCredentialsProvider.getInstance().credentials.find {it.id=='sonarqube-battlefield-token'}
if (!(c instanceof StringCredentialsImpl)) {throw new IllegalStateException('Sonar credential is missing or has the wrong type')}
print(c.secret.plainText)
"""
        current = jenkins(config, values).call('scriptText', {'script': script}, raw=True).decode().strip()
        if not re.fullmatch(r'[A-Za-z0-9_-]{16,256}', current):
            raise Failure('Cannot import the declared Sonar credential from Jenkins')
        if imported and current != imported:
            raise Failure('Imported Sonar token differs from the current Jenkins credential; adoption does not rotate it')
        return current
    return imported or (saved.read_text().strip() if saved.exists() else '')


def release_hash(payload):
    return hashlib.sha256(json.dumps(payload, sort_keys=True).encode()).hexdigest()


def check_core_package(lock):
    package = subprocess.run(['dpkg-query', '-W', '-f=${db:Status-Status} ${Version}', 'jenkins'], capture_output=True, text=True)
    if package.returncode == 1:
        return
    if package.returncode:
        raise Failure('Cannot inspect the Jenkins package state')
    status, _, version = package.stdout.strip().partition(' ')
    if status in ('not-installed', 'config-files'):
        return
    if status != 'installed':
        raise Failure('Jenkins package configuration is incomplete; repair Ubuntu package state first')
    if version != lock['core']['version']:
        raise Failure('Installed Jenkins differs from the locked release; upgrades/downgrades require a separate review')


def preflight(payload):
    config, lock = payload['config'], payload['lock']
    values = decode_inputs(payload['inputs'])
    ownership(config)
    if (STATE / 'maintenance.json').exists():
        raise Failure('An interrupted maintenance receipt remains; follow the recovery runbook before another apply')
    check_core_package(lock)
    for name, plugin in lock['plugins'].items():
        for suffix in ('.jpi', '.hpi'):
            path = JHOME / 'plugins' / (name + suffix)
            if path.exists():
                managed_path(path)
                with zipfile.ZipFile(path) as archive:
                    manifest = archive.read('META-INF/MANIFEST.MF').decode().replace('\r\n ', '').replace('\r', '')
                if 'Plugin-Version: ' + plugin['version'] + '\n' not in manifest:
                    raise Failure('Installed Jenkins plugin differs from the lock: ' + name)
                if Path(str(path) + '.disabled').exists() or Path(str(path) + '.pinned').is_symlink():
                    raise Failure('Required Jenkins plugin is disabled or redirected: ' + name)
    unit = subprocess.run(['systemctl', 'show', 'jenkins', '-p', 'LoadState', '-p', 'FragmentPath', '-p', 'User', '-p', 'WorkingDirectory', '-p', 'DropInPaths'], capture_output=True, text=True, timeout=30)
    settings = dict(line.split('=', 1) for line in unit.stdout.splitlines() if '=' in line)
    if unit.returncode and settings.get('LoadState') != 'not-found':
        raise Failure('Cannot inspect the native Jenkins service')
    if settings.get('FragmentPath'):
        if settings['FragmentPath'] not in ('/usr/lib/systemd/system/jenkins.service', '/lib/systemd/system/jenkins.service') or settings.get('User') != 'jenkins' or settings.get('WorkingDirectory') != str(JHOME):
            raise Failure('Existing Jenkins system service differs from the supported native layout')
        if any(path != '/etc/systemd/system/jenkins.service.d/90-battlefield.conf' for path in settings.get('DropInPaths', '').split()):
            raise Failure('Existing controller overrides require an explicit configuration review')
    fresh = not (JHOME / 'config.xml').exists()
    if fresh:
        secret(values, 'ci_admin_password')
        password = secret(values, 'ci_sonar_admin_password')
        if not all(re.search(pattern, password) for pattern in ('[A-Z]', '[a-z]', '[0-9]', '[^A-Za-z0-9]')):
            raise Failure('Fresh Sonar password requires uppercase, lowercase, digit and special characters')
        credentials(values)
    else:
        tree = ET.parse(JHOME / 'config.xml').getroot()
        if tree.findtext('useSecurity') != 'true' or tree.find('securityRealm') is None:
            raise Failure('Adoption requires an authenticated controller; configure its security before adoption')
        if active():
            client = jenkins(config, values)
            identity = client.call('whoAmI/api/json')
            if not identity.get('authenticated') or identity.get('name') != config['jenkins_admin']:
                raise Failure('Jenkins administrator identity differs from registration')
            client.call('pluginManager/api/json?tree=plugins[shortName]', raw=True)
            controller_contract(client, config)
    containers = sonar_containers(lock)
    database_password(containers, values)
    if containers:
        sonar_running = all(c.get('State', {}).get('Running') for c in containers.values())
        if sonar_running:
            wait_sonar()
        token = sonar_token(config, values)
        if token:
            if sonar_running and not API(SONAR, token, '').call('api/authentication/validate').get('valid'):
                raise Failure('Sonar adoption requires valid administration credentials')
        elif config['mode'] != 'install' or not (STATE / 'machine-id').exists():
            raise Failure('Sonar adoption requires an existing Jenkins Sonar credential or encrypted ci_sonar_token')
        else:
            # Resume only our own interrupted fresh installation, never claim an
            # unrelated Sonar service with default credentials.
            secret(values, 'ci_sonar_admin_password')
    record = STATE / 'release.json'
    matches = False
    if record.exists():
        previous = json.loads(record.read_text())
        matches = previous['release'] == release_hash(payload) and bool(previous['files'])
        for name, expected in previous['files'].items():
            path = Path(name)
            if path.is_symlink() or not path.is_file() or hashlib.sha256(path.read_bytes()).hexdigest() != expected:
                matches = False
    if not active() or not containers or not all(c.get('State', {}).get('Running') for c in containers.values()):
        matches = False
    elif matches:
        node = jenkins(config, values).call('computer/' + NODE + '/api/json?tree=offline', missing=True)
        if node is None or node.get('offline'):
            matches = False
    location = JHOME / 'jenkins.model.JenkinsLocationConfiguration.xml'
    if not location.is_file() or ET.parse(location).getroot().findtext('jenkinsUrl', '').rstrip('/') != config['jenkins_url'].rstrip('/'):
        matches = False
    return {'fresh': fresh, 'needs_maintenance': not matches, 'controller_running': active(),
            'sonar_existing': bool(containers), 'core': lock['core']['version'], 'plugins': len(lock['plugins']),
            'changes': [] if matches else ['private pre-change backup', 'pinned CI services/tools', 'scoped JCasC',
                'preserve existing users, credentials and jobs', 'create missing disabled repository/assurance jobs',
                'reuse platform seed definitions', 'reconnect existing agent identity']}


def prepare(payload):
    config, lock = payload['config'], payload['lock']
    values = decode_inputs(payload['inputs'])
    for root in (STATE, CONFIG):
        root.mkdir(mode=0o700, exist_ok=True)
        private(root / 'machine-id', config['machine_id'] + '\n')
    containers = sonar_containers(lock)
    db_password = database_password(containers, values)
    private(STATE / 'database-password', db_password)
    if containers:
        token = sonar_token(config, values)
        if token:
            private(STATE / 'sonar-token', token)
    # JSON strings are valid YAML quoted scalars. No dotenv interpolation of passwords.
    private(CONFIG / 'sonar-secrets.yml', json.dumps({'services': {
        'database': {'environment': {'POSTGRES_PASSWORD': db_password.replace('$', '$$')}},
        'sonarqube': {'environment': {'SONAR_JDBC_PASSWORD': db_password.replace('$', '$$')}}}}))
    return {'prepared': True}


def drain(payload):
    if not active():
        return {'drained': True}
    client = jenkins(payload['config'], decode_inputs(payload['inputs']))
    status = client.call('api/json?tree=quietingDown')
    if status['quietingDown']:
        raise Failure('Controller was already in quiet mode; finish that maintenance before starting this one')
    if (STATE / 'maintenance.json').exists():
        raise Failure('An earlier maintenance receipt remains; resolve it before starting another operation')
    private(STATE / 'maintenance.json', json.dumps({'quiet_owned': True, 'phase': 'draining'}))
    client.call('quietDown', {'reason': 'AB-127 external operator maintenance', 'block': 'false'})
    deadline = time.monotonic() + 300
    while time.monotonic() < deadline:
        # executor counts miss paused Pipeline runs. Recursively inspect all job types.
        if not running_jobs(client):
            return {'drained': True}
        time.sleep(3)
    raise Failure('Build drain timed out; no services were stopped. Let active/paused builds finish, then retry')


def running_jobs(client, path=''):
    listing = client.call(path + 'api/json?tree=jobs[name,_class,builds[number,building]],building')
    if listing.get('building'):
        return True
    for item in listing.get('jobs', []):
        if any(x.get('building') for x in item.get('builds', [])):
            return True
        if item.get('_class', '').endswith(('.Folder', 'WorkflowMultiBranchProject', 'OrganizationFolder')):
            if running_jobs(client, path + 'job/' + urllib.parse.quote(item['name'], safe='') + '/'):
                return True
    return False


def backup_checkpoint(payload):
    marker = STATE / 'maintenance.json'
    running = active()
    if running and (not marker.exists() or json.loads(marker.read_text()).get('phase') != 'draining'):
        raise Failure('Controller backup requires a completed drain')
    private(marker, json.dumps({'quiet_owned': True, 'phase': 'backing_up', 'controller_was_running': running}))
    return {'backup_checkpoint': True}


def changing(payload):
    marker = STATE / 'maintenance.json'
    if not marker.exists() and active():
        raise Failure('Controller changes require a completed drain')
    # A same-source repair must not reuse the previous successful acceptance
    # record if this maintenance fails before verification writes a new one.
    (STATE / 'release.json').unlink(missing_ok=True)
    private(marker, json.dumps({'quiet_owned': True, 'phase': 'changing'}))
    private(JHOME / 'battlefield-maintenance', 'external CI maintenance\n', owner='jenkins')
    return {'maintenance_checkpoint': True}


def controller_backup_bytes(root):
    total = 0
    if not root.exists():
        return total
    for directory, children, files in os.walk(root, followlinks=False):
        if Path(directory) == root:
            children[:] = [name for name in children if name not in ('workspace', 'caches', '.cache')]
        for name in files:
            metadata = (Path(directory) / name).lstat()
            if stat.S_ISREG(metadata.st_mode):
                total += metadata.st_size
    return total


def controller_archive_filter(info):
    parts = Path(info.name).parts
    # Only controller-root caches are disposable. A job/folder named workspace
    # or caches still contains configuration and history that must survive.
    if len(parts) > 1 and parts[0] == 'jenkins' and parts[1] in ('workspace', 'caches', '.cache'):
        return None
    return info


def backup(payload):
    if active():
        raise Failure('Controller must be stopped for a consistent maintenance backup')
    root = Path('/srv/battlefield-data/maintenance') if os.path.ismount('/srv/battlefield-data') else Path('/var/backups/battlefield-ci')
    managed_path(root)
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    if root.stat().st_uid != 0 or root.stat().st_mode & 0o077:
        raise Failure('Maintenance backup directory must be root-owned and private')
    containers = sonar_containers(payload['lock'])
    if containers and not containers['database'].get('State', {}).get('Running'):
        # Start the already validated, unchanged database container so pg_dump can
        # take a consistent backup before Compose reconciliation.
        run(['docker', 'start', containers['database']['Id']])
        for attempt in range(60):
            probe = subprocess.run(['docker', 'exec', containers['database']['Id'], 'pg_isready', '-U', 'sonar', '-d', 'sonar'], capture_output=True)
            if probe.returncode == 0:
                break
            time.sleep(2)
        else:
            raise Failure('Existing Sonar database did not become ready for backup')
    required = controller_backup_bytes(JHOME) + controller_backup_bytes(CONFIG) + controller_backup_bytes(STATE)
    if containers:
        size = run(['docker', 'exec', containers['database']['Id'], 'psql', '-U', 'sonar', '-d', 'sonar', '-Atc',
                    'select pg_database_size(current_database())']).strip()
        if not size.isdigit():
            raise Failure('Cannot estimate the Sonar database backup size')
        required += int(size)
    if shutil.disk_usage(root).free < int(required * 1.2) + 2 * 1024**3:
        raise Failure('Insufficient maintenance backup capacity: keep 20 percent headroom plus 2 GiB free')
    target = root / (time.strftime('%Y%m%dT%H%M%S') + '-' + uuid.uuid4().hex[:12])
    target.mkdir(mode=0o700)
    # Exclude disposable caches/workspaces; preserve jobs/builds, plugins, users and cryptographic identity.
    with tarfile.open(target / 'controller.tar.gz', 'w:gz') as archive:
        if JHOME.exists():
            archive.add(JHOME, arcname='jenkins', filter=controller_archive_filter)
        for path in (CONFIG, STATE, Path('/etc/systemd/system/jenkins.service.d'),
                     Path(payload['config']['home']) / '.config/systemd/user/jenkins-agent.service'):
            if path.exists():
                archive.add(path, arcname=str(path).lstrip('/'))
    if containers:
        partial = target / 'sonar.pgdump.partial'
        with partial.open('xb') as dump:
            result = subprocess.run(['docker', 'exec', containers['database']['Id'], 'pg_dump', '-U', 'sonar', '-d', 'sonar', '-Fc'],
                                    stdout=dump, stderr=subprocess.DEVNULL, timeout=600)
        if result.returncode:
            raise Failure('Sonar database backup failed; the unchanged controller will be resumed')
        partial.rename(target / 'sonar.pgdump')
        private(target / 'sonar-containers.json', json.dumps(containers))
    private(STATE / 'last-backup', str(target) + '\n')
    return {'backup': str(target)}


def wait_sonar():
    deadline = time.monotonic() + 420
    while True:
        try:
            status = API(SONAR, '', '').call('api/system/status')
            if status.get('status') == 'UP':
                break
        except Failure:
            pass
        if time.monotonic() > deadline:
            raise Failure('SonarQube did not become healthy within seven minutes')
        time.sleep(3)


def sonar(payload):
    values = decode_inputs(payload['inputs'])
    wait_sonar()
    token_path = STATE / 'sonar-token'
    token = values.get('ci_sonar_token', '').strip() or (token_path.read_text().strip() if token_path.exists() else '')
    if not token:
        password = secret(values, 'ci_sonar_admin_password')
        admin = API(SONAR, 'admin', password)
        try:
            authenticated = admin.call('api/authentication/validate').get('valid')
        except Failure:
            authenticated = False
        if not authenticated:
            initial = API(SONAR, 'admin', 'admin')
            initial.call('api/users/change_password', {'login': 'admin', 'previousPassword': 'admin', 'password': password}, raw=True)
        token = admin.call('api/user_tokens/generate', {'name': 'battlefield-bootstrap-' + uuid.uuid4().hex})['token']
        private(token_path, token)
    client = API(SONAR, token, '')
    if not client.call('api/authentication/validate').get('valid'):
        raise Failure('Sonar authentication validation failed')
    projects = client.call('api/projects/search?projects=autonomous-battlefield')['components']
    if not projects:
        client.call('api/projects/create', {'project': 'autonomous-battlefield', 'name': 'Autonomous Battlefield', 'visibility': 'private'})
    configure_quality_gate(client, created=not projects)
    config = payload['config']
    data = controller_inputs(config, values, token, payload['lock'])
    private(CONFIG / 'bootstrap.json', json.dumps(data), owner='jenkins', mode=0o600)
    return {'sonar_configured': True}


def configure_quality_gate(client, *, created):
    gates = client.call('api/qualitygates/list')['qualitygates']
    gate = next((g for g in gates if g['name'] == 'Sonar way'), None)
    if gate is None:
        raise Failure('Expected the built-in Sonar way quality gate')
    assigned = client.call('api/qualitygates/get_by_project?project=autonomous-battlefield')['qualityGate']
    if not created and assigned['name'] != 'Sonar way':
        raise Failure('Existing production project has a different quality gate; review it before adoption')
    client.call('api/qualitygates/select', {'projectKey': 'autonomous-battlefield', 'gateName': 'Sonar way'}, raw=True)


def controller_inputs(config, values, token, lock):
    return {'admin': config['jenkins_admin'], 'home': config['home'], 'mode': config['mode'],
            'adminPassword': secret(values, 'ci_admin_password') if config['mode'] == 'install' else '',
            'credentials': credentials(values) if config['mode'] == 'install' else {}, 'sonarToken': token,
            'scannerHome': '/opt/battlefield-ci/tools/sonar-scanner-' + lock['scanner']['version'] + '-linux-x64'}


def scrub_inputs(payload):
    path = CONFIG / 'bootstrap.json'
    data = json.loads(path.read_text())
    metadata = {key: data[key] for key in ('admin', 'home', 'mode', 'scannerHome')}
    private(path, json.dumps(metadata), owner='jenkins')
    return {'bootstrap_secrets_removed': True}


def compose(payload):
    run(['docker', 'compose', '--project-name', 'sonarqube', '-f', str(CONFIG / 'sonar-compose.yaml'),
         '-f', str(CONFIG / 'sonar-secrets.yml'), 'up', '-d', '--wait', '--wait-timeout', '300'], timeout=420)
    return {'sonar_containers_ready': True}


def seed(payload):
    path = Path('/opt/battlefield-ci/scripts/infra/seed-platform.py')
    spec = importlib.util.spec_from_file_location('platform_seed', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    api = jenkins(payload['config'], decode_inputs(payload['inputs']))
    # The existing seeder provides ownership checks, plugin compatibility and the
    # canonical Jenkinsfile.platform/.vm/.k3s definitions. Never trigger builds.
    token = base64.b64decode(api.authorization.split(' ', 1)[1]).decode().split(':', 1)[1]
    client = module.Jenkins(JENKINS, payload['config']['jenkins_admin'], token)
    module.seed_jobs(client)
    return {'platform_jobs_seeded': True}


def resume(payload):
    marker = STATE / 'maintenance.json'
    if marker.exists() and json.loads(marker.read_text()).get('quiet_owned'):
        record = json.loads(marker.read_text())
        if record.get('phase') == 'backing_up':
            if not record.get('controller_was_running'):
                marker.unlink()
                return {'resumed': True}
            if not active():
                run(['systemctl', 'start', 'jenkins'], timeout=300)
                deadline = time.monotonic() + 300
                while True:
                    try:
                        jenkins(payload['config'], decode_inputs(payload['inputs'])).call('whoAmI/api/json')
                        break
                    except Failure:
                        if time.monotonic() >= deadline:
                            raise Failure('Unchanged controller could not restart after backup failure; inspect its service locally') from None
                        time.sleep(3)
        completed = STATE / 'release.json'
        if record.get('phase') == 'changing' and (not completed.exists() or json.loads(completed.read_text())['release'] != release_hash(payload)):
            return {'resume_needed': True}
        if not active():
            return {'resume_needed': True}
        jenkins(payload['config'], decode_inputs(payload['inputs'])).call('cancelQuietDown', {}, raw=True)
        (JHOME / 'battlefield-maintenance').unlink(missing_ok=True)
        marker.unlink()
    return {'resumed': True}


def managed_fingerprints(prefix=Path('/opt/battlefield-ci')):
    # Hash managed configuration and copied job/source definitions, not extracted
    # third-party distributions or their caches. Archive versions live in ci_lock.
    files = {}
    for directory in (CONFIG, JHOME / 'init.groovy.d', Path('/etc/systemd/system/jenkins.service.d'),
                      prefix / 'scripts', prefix / 'infra'):
        if directory.exists():
            for path in directory.rglob('*'):
                if path.is_file() and not path.is_symlink():
                    files[str(path)] = hashlib.sha256(path.read_bytes()).hexdigest()
    for name in ('Jenkinsfile.platform', 'Jenkinsfile.vm', 'Jenkinsfile.k3s'):
        path = prefix / name
        if path.is_file():
            files[str(path)] = hashlib.sha256(path.read_bytes()).hexdigest()
    return files


def verify(payload):
    client = jenkins(payload['config'], decode_inputs(payload['inputs']))
    status = client.call('api/json?tree=useSecurity,numExecutors')
    if status.get('useSecurity') is not True or status.get('numExecutors') != 0:
        raise Failure('Jenkins security/controller executor verification failed')
    location = JHOME / 'jenkins.model.JenkinsLocationConfiguration.xml'
    if not location.is_file() or ET.parse(location).getroot().findtext('jenkinsUrl', '').rstrip('/') != payload['config']['jenkins_url'].rstrip('/'):
        raise Failure('Jenkins root URL does not match the registered origin')
    plugins = client.call('pluginManager/api/json?tree=plugins[shortName,version,active]')['plugins']
    actual = {x['shortName']: x for x in plugins}
    if any(n not in actual or not actual[n]['active'] or actual[n]['version'] != p['version'] for n, p in payload['lock']['plugins'].items()):
        raise Failure('Jenkins locked plugins are missing, inactive or incompatible')
    node = client.call('computer/' + NODE + '/api/json?tree=offline,numExecutors,assignedLabels[name]')
    if node['offline'] or node['numExecutors'] != 4 or 'battlefield-production' not in [x['name'] for x in node['assignedLabels']]:
        raise Failure('Shared CI agent is not online with its expected label/executors')
    for name in ('autonomous-battlefield', 'battlefield-assurance', 'battlefield-platform'):
        client.call('job/' + name + '/api/json?tree=name')
    for name in ('host', 'vm', 'k3s'):
        client.call('job/battlefield-platform/job/' + name + '/api/json?tree=name')
    if not (JHOME / 'battlefield-bootstrap-complete').is_file():
        raise Failure('Controller configuration/credential acceptance did not finish')
    if API(SONAR, '', '').call('api/system/status').get('status') != 'UP':
        raise Failure('Sonar is not healthy')
    if payload.get('save_release'):
        files = managed_fingerprints()
        for name in payload.get('ca', {}):
            path = Path('/usr/local/share/ca-certificates') / ('battlefield-' + name)
            files[str(path)] = hashlib.sha256(path.read_bytes()).hexdigest()
        unit = Path(payload['config']['home']) / '.config/systemd/user/jenkins-agent.service'
        files[str(unit)] = hashlib.sha256(unit.read_bytes()).hexdigest()
        original = {k: v for k, v in payload.items() if k != 'save_release'}
        private(STATE / 'release.json', json.dumps({'release': release_hash(original), 'files': files}))
    return {'verified': True, 'agent_online': True, 'plugins_active': True, 'sonar_healthy': True}


def main():
    os.umask(0o077)
    payload = json.load(sys.stdin)
    phase = sys.argv[1]
    functions = {'preflight': preflight, 'prepare': prepare, 'drain': drain, 'backup': backup,
                 'sonar': sonar, 'resume': resume, 'verify': verify, 'seed': seed, 'changing': changing,
                 'backup-checkpoint': backup_checkpoint, 'scrub-inputs': scrub_inputs, 'compose': compose}
    if phase not in functions:
        raise Failure('Unknown CI lifecycle phase')
    print(json.dumps(functions[phase](payload)))


if __name__ == '__main__':
    try:
        main()
    except Failure as error:
        print(json.dumps({'error': str(error)}))
        sys.exit(1)
    except Exception as error:
        frame = traceback.extract_tb(error.__traceback__)[-1]
        print(json.dumps({'error': 'CI lifecycle failed (' + type(error).__name__ + ' at line ' + str(frame.lineno) + '); sensitive details were suppressed.'}))
        sys.exit(1)
