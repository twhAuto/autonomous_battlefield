#!/usr/bin/env python3
"""Root-side, narrowly scoped native Caddy capture/adoption/restore operations."""
import configparser
import hashlib
import http.client
import ipaddress
import json
import os
from pathlib import Path
import pwd
import re
import secrets
import socket
import ssl
import stat
import subprocess
import sys
import tempfile
import time
import urllib.request

CONFIG = Path('/etc/caddy/Caddyfile')
UNIT = Path('/usr/lib/systemd/system/caddy.service')
HOME = Path('/var/lib/caddy')
CA = HOME / '.local/share/caddy/pki/authorities/local'
STATE = Path('/var/lib/battlefield-https')
TRUST = Path('/usr/local/share/ca-certificates/battlefield-local-https.crt')
REQUIRED = {'battlefield.home.arpa': ('127.0.0.1:8888', 'battlefield.localhost'),
            'grafana.home.arpa': ('127.0.0.1:8888', 'grafana.localhost'),
            'kibana.home.arpa': ('127.0.0.1:8888', 'kibana.localhost'),
            'sonarqube.home.arpa': ('127.0.0.1:9000', None)}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def sha(value):
    return hashlib.sha256(value if isinstance(value, bytes) else value.encode()).hexdigest()


def encoded(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':')).encode()


def command(*args, input=None):
    result = subprocess.run(args, input=input, capture_output=True, timeout=60)
    require(result.returncode == 0, 'A required ' + Path(args[0]).name + ' check failed')
    return result.stdout


def safe(path):
    path = Path(path)
    require(path.is_absolute() and path.resolve() == path, 'Redirected HTTPS path refused')
    for parent in [path, *path.parents]:
        if parent.exists():
            info = parent.stat()
            require(not info.st_mode & 0o002 or parent == Path('/tmp'), 'World-writable HTTPS path refused')
    return path


def read(path, *, private=False, owners=None):
    path = safe(path)
    with os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW), 'rb') as stream:
        info = os.fstat(stream.fileno())
        require(stat.S_ISREG(info.st_mode) and info.st_nlink == 1 and info.st_size <= 1024 * 1024,
                'Expected a small regular HTTPS input file')
        require(owners is None or info.st_uid in owners, 'HTTPS file belongs to another account')
        require(not private or not info.st_mode & 0o077, 'HTTPS private file permissions are too broad')
        return stream.read()


def write(path, content, *, uid=0, gid=0, mode=0o600):
    path = safe(path)
    if path.exists():
        read(path, owners={uid})
    fd, temporary = tempfile.mkstemp(dir=path.parent)
    try:
        with os.fdopen(fd, 'wb') as stream:
            os.fchmod(stream.fileno(), mode)
            os.fchown(stream.fileno(), uid, gid)
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        Path(temporary).unlink(missing_ok=True)


def routes(text):
    return parsed_routes(text)[0]


def parsed_routes(text):
    """Recognize the owned local routes and preserve other loopback routes."""
    require(isinstance(text, str) and len(text) < 128 * 1024, 'Invalid Caddyfile')
    clean = re.sub(r'#[^\n]*', lambda match: ' ' * len(match[0]), text)
    pattern = re.compile(r'\s*([a-z0-9-]+\.home\.arpa)\s*\{\s*tls\s+internal\s+'
                         r'(?:reverse_proxy\s+([0-9.]+:[0-9]{1,5})(?:\s*\{\s*header_up\s+Host\s+'
                         r'([a-z0-9.-]+)\s*\})?|redir\s+(https://[a-z0-9.-]+)\{uri\}\s+307)\s*\}')
    result, spans, position = {}, {}, 0
    while clean[position:].strip():
        match = pattern.match(clean, position)
        require(match is not None, 'Unsupported Caddy configuration; preserve it and review scope')
        host, upstream, header, redirect = match.groups()
        require(host not in result, 'Duplicate HTTPS route')
        if redirect:
            require(host == 'battlefield.home.arpa', 'Only the application alias may redirect')
            result[host] = [redirect, 'redirect']
        else:
            address, port = upstream.rsplit(':', 1)
            require(1 <= int(port) <= 65535 and (address == '127.0.0.1' or
                    (host in ('grafana.home.arpa', 'kibana.home.arpa') and private_gateway(upstream))),
                    'Nonlocal or invalid HTTPS upstream')
            result[host] = [upstream, header]
        spans[host] = (match.start(1), match.end())
        position = match.end()
    require(all(host in result for host in REQUIRED), 'Required HTTPS route is missing')
    for host, expected in REQUIRED.items():
        value = result[host]
        require(value == list(expected) or
                (host == 'battlefield.home.arpa' and value[1] == 'redirect') or
                (host in ('grafana.home.arpa', 'kibana.home.arpa') and value[1] == expected[1] and private_gateway(value[0])),
                'Required HTTPS route differs from the supported layout')
    return result, spans


def private_gateway(value):
    address, port = value.rsplit(':', 1)
    ip = ipaddress.IPv4Address(address)
    return port == '30080' and any(ip in ipaddress.IPv4Network(cidr) for cidr in ('10.0.0.0/8', '172.16.0.0/12', '192.168.0.0/16'))


def desired_configuration(config, routing):
    if routing is None:
        return config
    require(isinstance(routing, dict) and set(routing) == {'gateway', 'public_origin', 'cluster_uid', 'node_uid'} and
            private_gateway(routing['gateway']) and re.fullmatch(r'https://[a-z0-9]+(?:[.-][a-z0-9]+)+', routing['public_origin']) and
            all(re.fullmatch(r'[a-f0-9-]{36}', routing[k]) for k in ('cluster_uid', 'node_uid')), 'Invalid registered K3s routing')
    text = config['caddyfile']
    _, spans = parsed_routes(text)
    for host in sorted(('battlefield.home.arpa', 'grafana.home.arpa', 'kibana.home.arpa'), key=lambda key: spans[key][0], reverse=True):
        start, end = spans[host]
        directive = ('redir ' + routing['public_origin'] + '{uri} 307' if host == 'battlefield.home.arpa' else
                     'reverse_proxy ' + routing['gateway'] + ' {\n        header_up Host ' + REQUIRED[host][1] + '\n    }')
        text = text[:start] + host + ' {\n    tls internal\n    ' + directive + '\n}' + text[end:]
    routes(text)
    return dict(config, caddyfile=text)


def certificate(certificate_pem, key_pem):
    require(isinstance(certificate_pem, str) and isinstance(key_pem, str) and
            len(certificate_pem) < 16384 and len(key_pem) < 16384, 'Missing CA restore material')
    with tempfile.TemporaryDirectory(prefix='battlefield-https-ca-') as directory:
        cert, key = Path(directory) / 'root.crt', Path(directory) / 'root.key'
        cert.write_text(certificate_pem); key.write_text(key_pem); key.chmod(0o600)
        public_cert = command('openssl', 'x509', '-in', str(cert), '-pubkey', '-noout')
        public_key = command('openssl', 'pkey', '-in', str(key), '-pubout')
        require(public_cert == public_key, 'CA certificate and private key do not match')
        command('openssl', 'verify', '-CAfile', str(cert), str(cert))
        command('openssl', 'x509', '-in', str(cert), '-checkend', '86400', '-noout')
        details = command('openssl', 'x509', '-in', str(cert), '-text', '-noout')
        require(b'CA:TRUE' in details, 'Restore certificate is not a CA')
        return sha(command('openssl', 'x509', '-in', str(cert), '-outform', 'DER'))


def validate(config):
    require(isinstance(config, dict) and set(config) == {'schema', 'caddy_version', 'unit_sha256', 'caddyfile',
            'root_certificate', 'root_private_key'}, 'Expected HTTPS capture schema 1')
    require(type(config['schema']) is int and config['schema'] == 1 and
            re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+', config['caddy_version']) and
            re.fullmatch(r'[0-9a-f]{64}', config['unit_sha256']), 'Invalid HTTPS version/unit pin')
    routes(config['caddyfile'])
    return certificate(config['root_certificate'], config['root_private_key'])


def service():
    values = command('systemctl', 'show', 'caddy.service',
                     '--property=User,Group,DropInPaths,ActiveState,UnitFileState,Environment').decode()
    return dict(line.split('=', 1) for line in values.splitlines())


def prerequisites():
    user = pwd.getpwnam('caddy')
    require(user.pw_dir == str(HOME), 'Caddy service home differs from the existing layout')
    parser = configparser.ConfigParser(interpolation=None, strict=True)
    parser.optionxform = str
    parser.read_string(read(UNIT, owners={0}).decode())
    settings = dict(parser['Service'])
    require(settings.get('User') == 'caddy' and settings.get('Group') == 'caddy' and
            settings.get('ExecStart') == '/usr/bin/caddy run --environ --config /etc/caddy/Caddyfile' and
            settings.get('ExecReload') == '/usr/bin/caddy reload --config /etc/caddy/Caddyfile --force' and
            not any(k.startswith(('Environment', 'ExecStartPre', 'ExecStartPost', 'ExecStop')) for k in settings),
            'Unsupported native Caddy service; do not overwrite it')
    require(not any(Path('/etc/systemd/system/caddy.service.d').glob('*')), 'Native Caddy overrides require separate review')
    override = Path('/etc/systemd/system/caddy.service')
    require(not os.path.lexists(override) or (override.is_symlink() and os.readlink(override) == '/dev/null'),
            'Foreign native Caddy unit override')
    version = command('/usr/bin/caddy', 'version').decode().strip()
    require(re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+', version), 'Unsupported Caddy version string')
    return user, version


def active_configuration():
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with opener.open('http://127.0.0.1:2019/config/', timeout=5) as response:
        require(response.geturl() == 'http://127.0.0.1:2019/config/', 'Unexpected Caddy admin redirect')
        data = response.read(1024 * 1024 + 1)
    require(len(data) <= 1024 * 1024, 'Caddy admin response too large')
    expected = json.loads(command('/usr/bin/caddy', 'adapt', '--config', str(CONFIG)))
    require(json.loads(data) == expected, 'Active Caddy configuration differs from its file')


def tls_checks(config, *, require_healthy=True):
    context = ssl.create_default_context(cadata=config['root_certificate'])
    result = {}
    for host, (upstream, header) in routes(config['caddyfile']).items():
        path = '/ab142-route-check?source=local' if header == 'redirect' else '/'
        with socket.create_connection(('127.0.0.1', 443), timeout=10) as connection:
            with context.wrap_socket(connection, server_hostname=host) as secure:
                secure.sendall(('GET ' + path + ' HTTP/1.1\r\nHost: ' + host + '\r\nConnection: close\r\n\r\n').encode())
                response = http.client.HTTPResponse(secure); response.begin()
                status = response.status
                location = response.getheader('Location')
        if header == 'redirect':
            require(status == 307 and location == upstream + path, 'Application redirect does not preserve the approved origin and URI')
            result[host] = {'verified_tls': True, 'status': status, 'location': location, 'healthy': True}
            continue
        direct = http.client.HTTPConnection(upstream, timeout=10)
        try:
            direct.request('GET', '/', headers={'Host': header or host})
            origin = direct.getresponse().status
        finally:
            direct.close()
        require(status == origin, 'TLS route and direct upstream differ: ' + host)
        healthy = 200 <= status < 500
        require(not require_healthy or healthy, 'TLS route or upstream health check failed: ' + host)
        result[host] = {'verified_tls': True, 'status': status, 'upstream_status': origin, 'healthy': healthy}
    return result


def upstream_checks(config):
    result = {}
    for host, (upstream, header) in routes(config['caddyfile']).items():
        if header == 'redirect':
            result[host] = {'redirect': upstream}
            continue
        client = http.client.HTTPConnection(upstream, timeout=10)
        try:
            client.request('GET', '/', headers={'Host': header or host})
            status = client.getresponse().status
            require(200 <= status < 500, 'Restore the HTTPS upstream services before applying local HTTPS: ' + host)
            result[host] = status
        finally:
            client.close()
    return result


def wait_for_tls(config):
    for attempt in range(6):
        try:
            active_configuration()
            return tls_checks(config)
        except (ValueError, OSError, http.client.HTTPException):
            if attempt == 5:
                raise
            time.sleep(2)


def capture():
    user, version = prerequisites()
    state = service()
    require(state['ActiveState'] == 'active' and state['UnitFileState'] == 'enabled' and
            not state['DropInPaths'] and not state['Environment'], 'Native Caddy must be active, enabled and unmodified')
    config = {'schema': 1, 'caddy_version': version, 'unit_sha256': sha(read(UNIT, owners={0})),
              'caddyfile': read(CONFIG, owners={0}).decode(),
              'root_certificate': read(CA / 'root.crt', owners={0, user.pw_uid}).decode(),
              'root_private_key': read(CA / 'root.key', private=True, owners={0, user.pw_uid}).decode()}
    validate(config); active_configuration()
    # Archive existing recovery material even when a retired upstream returns 503.
    # A capture is an inventory, not adoption/installation acceptance.
    observations = tls_checks(config, require_healthy=False)
    return {'config': config, 'routes': observations, 'upstreams_healthy': all(v['healthy'] for v in observations.values())}


class Engine:
    def __init__(self, request):
        require(set(request) == {'config', 'machine_id', 'operator', 'revision', 'mode', 'routing'}, 'Invalid HTTPS request')
        self.request, self.config = request, request['config']
        self.ca_fingerprint = validate(self.config)
        require(request['mode'] in ('adopt', 'install', 'migrate') and re.fullmatch(r'[0-9a-f]{40}', request['revision']), 'Invalid HTTPS mode/revision')
        require(request['mode'] != 'migrate' or request['routing'] is not None, 'Migration requires registered K3s routes')
        self.desired = desired_configuration(self.config, request['routing'])
        require(request['machine_id'] == Path('/etc/machine-id').read_text().strip(), 'Registered host identity changed')
        self.user, version = prerequisites()
        require(version == self.config['caddy_version'] and sha(read(UNIT, owners={0})) == self.config['unit_sha256'],
                'Shared Caddy package/version or native unit differs from captured inputs')
        self.binding = {'inputs': sha(encoded(self.config)), 'routing': request['routing'],
                        'machine_id': request['machine_id'], 'operator': request['operator']}
        safe(STATE)
        if STATE.exists():
            info = STATE.stat()
            require(stat.S_ISDIR(info.st_mode) and info.st_uid == 0 and not info.st_mode & 0o077, 'Foreign HTTPS management directory')
        self.owner_path, self.receipt = STATE / 'owner.json', STATE / 'review.json'
        self.owner = json.loads(read(self.owner_path, private=True, owners={0})) if self.owner_path.exists() else None
        require(not (STATE / 'incomplete.json').exists(), 'Interrupted HTTPS restore; keep native Caddy masked and inspect retained state')
        if self.owner:
            require(self.owner == self.binding, 'HTTPS ownership/inputs changed; review explicit recovery')

    def inspect(self):
        state = service()
        require(not state['DropInPaths'] and not state['Environment'], 'Native Caddy overrides are outside this workflow')
        for path in (CONFIG.parent, HOME):
            safe(path)
        snapshot = {'service': state, 'binary': sha(Path('/usr/bin/caddy').read_bytes()),
                    'unit': sha(read(UNIT, owners={0})), 'owner': self.owner,
                    'etc_files': sorted(p.name for p in CONFIG.parent.iterdir())}
        if self.request['mode'] in ('adopt', 'migrate') or self.owner:
            expected = self.desired if self.owner or self.request['mode'] == 'adopt' else self.config
            require(read(CONFIG, owners={0}).decode() == expected['caddyfile'], 'Existing Caddyfile differs; unrelated configuration is preserved')
            require(read(CA / 'root.crt', owners={0, self.user.pw_uid}).decode() == self.config['root_certificate'] and
                    read(CA / 'root.key', private=True, owners={0, self.user.pw_uid}).decode() == self.config['root_private_key'],
                    'Existing CA differs or restore material is missing; refusing rotation')
            require(state['ActiveState'] == 'active' and state['UnitFileState'] == 'enabled', 'Native Caddy is not active and enabled')
            active_configuration()
            snapshot['configuration'] = sha(read(CONFIG)); snapshot['ca'] = self.ca_fingerprint
            migrating = self.request['mode'] == 'migrate' and not self.owner
            snapshot['tls'] = tls_checks(expected, require_healthy=not migrating)
            if migrating:
                snapshot['upstreams'] = upstream_checks(self.desired)
        else:
            require(state['ActiveState'] == 'inactive' and state['UnitFileState'] == 'masked',
                    'Fresh restore requires the unused native service masked by AB-125')
            require(not CA.exists() and not CA.is_symlink(), 'Existing CA storage is foreign; use explicit adoption')
            require(not TRUST.exists() and not TRUST.is_symlink(), 'Existing trust export is foreign')
            safe(TRUST)
            for path in [CA, *list(CA.parents)[:5]]:
                safe(path)
                if path.exists():
                    require(path.is_dir() and path.stat().st_uid == self.user.pw_uid and not path.stat().st_mode & 0o022,
                            'Foreign CA storage ancestor')
            require(snapshot['etc_files'] == ['Caddyfile'], 'Extra Caddy configuration must be preserved; fresh restore refused')
            records = command('dpkg-query', '-W', '-f=${Conffiles}', 'caddy').decode().splitlines()
            entries = [line.split() for line in records if line.split() and line.split()[0] == str(CONFIG)]
            require(len(entries) == 1 and len(entries[0]) == 2 and
                    hashlib.md5(read(CONFIG, owners={0}), usedforsecurity=False).hexdigest() == entries[0][1],
                    'Fresh restore requires the untouched packaged Caddyfile')
            snapshot['configuration'] = sha(read(CONFIG))
            snapshot['upstreams'] = upstream_checks(self.desired)
        return snapshot

    def report(self, action, observed, **extra):
        changes = {'adopt': ['record HTTPS ownership'],
                   'migrate': ['redirect application alias to registered public origin', 'retarget Grafana and Kibana to registered K3s',
                               'reload native Caddy', 'record HTTPS ownership'],
                   'install': ['restore reviewed Caddyfile and CA', 'publish host trust', 'enable native Caddy', 'record HTTPS ownership']}
        return {'schema': 1, 'action': action, 'revision': self.request['revision'], 'mode': self.request['mode'],
                'ca_sha256': self.ca_fingerprint, 'routes': routes(self.desired['caddyfile']),
                'changes': [] if self.owner else changes[self.request['mode']],
                'tls': observed.get('tls', {}), 'target_upstreams': observed.get('upstreams', {}), **extra}

    def plan(self):
        observed = self.inspect()
        STATE.mkdir(mode=0o700, exist_ok=True)
        receipt = {'request': sha(encoded(self.request)), 'observation': sha(encoded(observed)),
                   'review_id': secrets.token_hex(32), 'created': time.time()}
        write(self.receipt, encoded(receipt))
        return self.report('plan', observed, review_id=receipt['review_id'], expires_in_seconds=1800)

    def apply(self, review_id):
        observed = self.inspect()
        receipt = json.loads(read(self.receipt, private=True, owners={0}))
        require(receipt['request'] == sha(encoded(self.request)) and receipt['observation'] == sha(encoded(observed)) and
                receipt['review_id'] == review_id and 0 <= time.time() - receipt['created'] <= 1800,
                'HTTPS review expired, consumed, or source/inputs/host observation changed')
        changes = self.report('plan', observed)['changes']
        self.receipt.unlink()
        if not self.owner and self.request['mode'] == 'migrate':
            self.migrate()
        if not self.owner and self.request['mode'] == 'install':
            write(STATE / 'incomplete.json', encoded(self.binding))
            write(STATE / 'package-default.Caddyfile', read(CONFIG))
            for path in reversed([CA, *list(CA.parents)[:5]]):
                safe(path)
                if not path.exists():
                    path.mkdir(mode=0o700); os.chown(path, self.user.pw_uid, self.user.pw_gid)
                require(path.stat().st_uid == self.user.pw_uid and not path.stat().st_mode & 0o022,
                        'CA storage is not owned exclusively by Caddy')
            write(CA / 'root.crt', self.config['root_certificate'].encode(), uid=self.user.pw_uid, gid=self.user.pw_gid)
            write(CA / 'root.key', self.config['root_private_key'].encode(), uid=self.user.pw_uid, gid=self.user.pw_gid)
            write(CONFIG, self.desired['caddyfile'].encode(), mode=0o644)
            write(TRUST, self.config['root_certificate'].encode(), mode=0o644)
            command('update-ca-certificates')
            try:
                command('systemctl', 'unmask', 'caddy.service')
                command('systemctl', 'enable', '--now', 'caddy.service')
                wait_for_tls(self.desired)
            except Exception:
                command('systemctl', 'mask', '--now', 'caddy.service')
                raise
        if not self.owner:
            write(self.owner_path, encoded(self.binding)); self.owner = self.binding
        final = self.inspect()
        (STATE / 'incomplete.json').unlink(missing_ok=True)
        return self.report('apply', final, changed=bool(changes), applied_changes=changes)

    def migrate(self):
        original = read(CONFIG, owners={0})
        write(STATE / 'before-migration.Caddyfile', original)
        write(STATE / 'incomplete.json', encoded(self.binding))
        try:
            write(CONFIG, self.desired['caddyfile'].encode(), mode=0o644)
            command('systemctl', 'reload', 'caddy.service')
            wait_for_tls(self.desired)
        except Exception:
            write(CONFIG, original, mode=0o644)
            command('systemctl', 'reload', 'caddy.service')
            active_configuration()
            (STATE / 'incomplete.json').unlink()
            raise

    def verify(self):
        require(self.owner is not None, 'HTTPS has not been adopted/restored')
        return self.report('verify', self.inspect(), changed=False)


def main():
    os.umask(0o077)
    require(os.geteuid() == 0, 'HTTPS capture and lifecycle require sudo')
    if sys.argv[1:] == ['capture']:
        print(json.dumps(capture()))  # stdout is captured directly into Vault by https.py.
        return
    require(len(sys.argv) == 2, 'Expected one private request file')
    source = Path(sys.argv[1])
    payload = json.loads(read(source, private=True))
    account = pwd.getpwnam(payload['request']['operator'])
    require(source.stat().st_uid in {0, account.pw_uid}, 'Request belongs to a different operator')
    target = safe(Path(payload['report']))
    require(target.is_relative_to(Path(account.pw_dir)) and not os.path.lexists(target) and
            target.parent.stat().st_uid == account.pw_uid and not target.parent.stat().st_mode & 0o077,
            'Report must be a fresh private operator-owned path')
    engine = Engine(payload['request'])
    action = payload['action']
    require(action in ('plan', 'apply', 'verify'), 'Invalid HTTPS action')
    report = engine.apply(payload['review_id']) if action == 'apply' else getattr(engine, action)()
    write(target, encoded(report), uid=account.pw_uid, gid=account.pw_gid)
    print(json.dumps({'changed': report.get('changed', False), 'action': action}))


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print('HTTPS operation stopped: ' + (str(error) if type(error) is ValueError else type(error).__name__), file=sys.stderr)
        sys.exit(1)
