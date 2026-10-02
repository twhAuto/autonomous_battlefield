"""Cloudflare API capture and review-bound lifecycle. No updates, deletion or rotation."""
import base64
import copy
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import secrets
import stat
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request

from ansible.module_utils.battlefield_cloudflare_policy import (
    ORG_FIELDS, app_spec, digest, hostname, identifier,
    policy_spec, references, require, rules, selected, validate,
)

LIMIT = 8 * 1024 * 1024


class APIError(ValueError):
    def __init__(self, status=None):
        self.definitive = status in (400, 401, 403, 404, 405, 409, 422, 429)
        super().__init__('Cloudflare request rejected (HTTP %s); check scoped permissions' % status if self.definitive
                         else 'Cloudflare response unavailable or ambiguous; no automatic mutation retry')


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class API:
    def __init__(self, token, opener=None):
        require(isinstance(token, str) and token and not any(c.isspace() for c in token), 'Invalid API token input')
        self.token = token
        self.before_request = lambda: None
        self.opener = opener or urllib.request.build_opener(NoRedirect())

    def request(self, method, path, data=None):
        self.before_request()
        require(path.startswith(('/accounts/', '/zones')) and '..' not in path and '#' not in path,
                'Invalid Cloudflare API path')
        req = urllib.request.Request('https://api.cloudflare.com/client/v4' + path,
            data=json.dumps(data).encode() if data is not None else None, method=method,
            headers={'Authorization': 'Bearer ' + self.token, 'Content-Type': 'application/json'})
        try:
            with self.opener.open(req, timeout=30) as response:
                raw = response.read(LIMIT + 1)
                require(len(raw) <= LIMIT, 'Cloudflare response exceeds the size limit')
                result = json.loads(raw)
            if result.get('success') is not True or 'result' not in result:
                raise APIError()
            return result
        except urllib.error.HTTPError as error:
            raise APIError(error.code) from None
        except (OSError, json.JSONDecodeError, UnicodeError):
            raise APIError() from None

    def get(self, path):
        return self.request('GET', path)['result']

    def list(self, path):
        result, identities = [], set()
        for page in range(1, 101):
            data = self.request('GET', path + ('&' if '?' in path else '?') + f'page={page}&per_page=50')
            entries, info = data['result'], data.get('result_info', {})
            require(isinstance(entries, list), 'Unexpected paginated API response')
            for item in entries:
                require(isinstance(item, dict) and isinstance(item.get('id'), str) and item['id'] not in identities,
                        'Duplicate or malformed API identities; pagination refused')
                identities.add(item['id']); result.append(item)
            total = info.get('total_pages')
            require(total is None or (type(total) is int and total >= 0), 'Invalid API pagination metadata')
            if total is not None and page >= total:
                return result
            if total is None and len(entries) < 50:
                return result
            require(bool(entries), 'Incomplete API pagination')
        raise ValueError('Cloudflare inventory exceeds 100 pages; refusing incomplete ownership checks')

    def post(self, path, data):
        return self.request('POST', path, data)['result']


def unique(items, predicate, label):
    matches = [item for item in items if predicate(item)]
    require(len(matches) == 1, 'Expected exactly one ' + label + '; missing/ambiguous resources are not adopted')
    return matches[0]


def reference_inventory(api, account, policies, app):
    needed = references(policies, app)
    result, observations = {}, {}
    for kind, ids in needed.items():
        entries = api.list(f'/accounts/{account}/access/{kind}') if ids else []
        result[kind], observations[kind] = [], []
        for identity in sorted(ids):
            entry = unique(entries, lambda x: x['id'] == identity, 'referenced identity')
            if kind == 'groups':
                # Membership is runtime-owned. Observe it for stale-plan checks, never restore it.
                for field in ('include', 'exclude', 'require'):
                    rules(entry.get(field, []))
                    require(all('email' in r for r in entry.get(field, [])),
                            'Only direct email membership groups are supported; nested/broad groups refused')
                result[kind].append({'id': identity})
                observations[kind].append(selected(entry, {'id', 'include', 'exclude', 'require'}))
            elif kind == 'service_tokens':
                expiry = entry.get('expires_at', '')
                try:
                    active = isinstance(expiry, str) and datetime.fromisoformat(expiry.replace('Z', '+00:00')) > datetime.now(timezone.utc)
                except (ValueError, TypeError):
                    active = False
                require(active and entry.get('client_id'), 'Referenced service token is expired or missing its client identity')
                value = selected(entry, {'id', 'client_id'})
                result[kind].append(value)
                observations[kind].append({**value, 'expires_at': expiry})
            else:
                value = selected(entry, {'id', 'type'})
                require(value.get('type'), 'Identity-provider type is missing')
                result[kind].append(value); observations[kind].append(value)
    return result, observations


def capture(api, zone_name, host, tunnel_name, credentials, contract):
    hostname(zone_name); hostname(host)
    zone = unique(api.list('/zones?name=' + urllib.parse.quote(zone_name)), lambda x: x['name'] == zone_name, 'zone')
    account, zone_id = identifier(zone['account']['id'], 'hex'), identifier(zone['id'], 'hex')
    prefix = f'/accounts/{account}'
    tunnel = unique(api.list(prefix + '/cfd_tunnel?is_deleted=false'), lambda x: x['name'] == tunnel_name, 'tunnel')
    app = unique(api.list(prefix + '/access/apps'), lambda x: x.get('domain') == host, 'Access app')
    app = api.get(prefix + '/access/apps/' + identifier(app['id']))
    policies = api.list(prefix + '/access/apps/' + app['id'] + '/policies')
    policy_data = [{'id': p['id'], 'reusable': p.get('reusable', False), 'spec': policy_spec(p)} for p in policies]
    dns = unique(api.list(f'/zones/{zone_id}/dns_records?name=' + host), lambda x: x['name'] == host, 'DNS record')
    require(dns['type'] == 'CNAME' and dns['proxied'] is True and
            isinstance(dns.get('content'), str) and dns['content'].rstrip('.') == tunnel['id'] + '.cfargotunnel.com', 'DNS does not match the protected local tunnel')
    organization = selected(api.get(prefix + '/access/organizations'), ORG_FIELDS)
    spec = app_spec(app)
    refs, _ = reference_inventory(api, account, policy_data, spec)
    require(contract == {'hostname': host, 'issuer': 'https://' + organization['auth_domain'],
                         'audience': app['aud'], 'tunnel_id': tunnel['id']},
            'Production delivery issuer/audience/hostname/tunnel does not match Cloudflare')
    result = {'schema': 1, 'mode': 'adopt', 'account_id': account, 'zone_id': zone_id, 'zone_name': zone_name,
              'hostname': host, 'organization': organization, 'tunnel': selected(tunnel, {'id', 'name', 'config_src'}),
              'dns': {k: dns.get(k, {'tags': [], 'settings': {}}.get(k)) for k in ('id', 'ttl', 'comment', 'tags', 'settings')},
              'application': {'id': app['id'], 'aud': app['aud'], 'spec': spec}, 'policies': policy_data,
              'references': refs, 'credentials': selected(credentials, {'AccountTag', 'TunnelID', 'TunnelSecret'})}
    return validate(result)


def private_directory(path):
    path = Path(path)
    require(path.is_absolute() and path.resolve() == path, 'Private state path must not traverse symlinks')
    path.mkdir(parents=True, exist_ok=True, mode=0o700)
    info = path.lstat()
    require(stat.S_ISDIR(info.st_mode) and info.st_uid == os.getuid() and not info.st_mode & 0o077,
            'Cloudflare state directory must be private and owned by this operator')
    return path


def read_private(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, 'rb') as source:
        info = os.fstat(source.fileno())
        require(stat.S_ISREG(info.st_mode) and info.st_uid == os.getuid() and not info.st_mode & 0o177 and
                info.st_nlink == 1 and info.st_size <= LIMIT, 'Expected a private regular file owned by this operator')
        value = json.loads(source.read(LIMIT + 1))
        require(isinstance(value, dict), 'Private Cloudflare file must contain a JSON object')
        return value


def write_private(path, data):
    directory = private_directory(path.parent)
    if path.exists() or path.is_symlink():
        read_private(path)
    fd, temporary = tempfile.mkstemp(dir=directory, prefix='.cloudflare-')
    try:
        with os.fdopen(fd, 'w') as output:
            json.dump(data, output, indent=2); output.write('\n'); output.flush(); os.fsync(output.fileno())
        os.replace(temporary, path)
        fd = os.open(directory, os.O_RDONLY | os.O_DIRECTORY)
        try: os.fsync(fd)
        finally: os.close(fd)
    finally:
        if os.path.exists(temporary): os.unlink(temporary)


def covers(value, target):
    """Conservatively reject more specific paths and wildcard applications as well."""
    host = value.split('/')[0]
    return host == target or (host.startswith('*.') and target.endswith(host[1:]))


class Engine:
    def __init__(self, api, config, state_root, revision, machine_id):
        self.api, self.config = api, validate(copy.deepcopy(config))
        config = self.config
        self.prefix = '/accounts/' + config['account_id']
        self.zone = '/zones/' + config['zone_id']
        self.identity = digest({'account': config['account_id'], 'zone': config['zone_id'], 'host': config['hostname']})
        self.directory = private_directory(Path(state_root) / self.identity)
        self.owner_path, self.receipt_path = self.directory / 'owner.json', self.directory / 'review.json'
        self.binding = {'schema': 1, 'revision': revision, 'machine_id': machine_id, 'inputs': digest(config)}
        self.owner = read_private(self.owner_path) if self.owner_path.exists() else None
        if self.owner:
            require(self.owner['inputs'] == self.binding['inputs'] and self.owner['machine_id'] == machine_id,
                    'Owned configuration or physical host changed; explicit recovery required')
            require(not self.owner.get('pending'), 'An earlier API mutation has an ambiguous result; inspect the private journal before recovery')

    def identities(self):
        if self.owner:
            return self.owner['ids']
        c = self.config
        return {'tunnel': c['tunnel']['id'], 'application': c['application']['id'], 'dns': c['dns']['id'],
                'policies': [p['id'] for p in c['policies']], 'aud': c['application']['aud']}

    def observe(self):
        c, ids = self.config, self.identities()
        zone = self.api.get(self.zone)
        require(zone.get('id') == c['zone_id'] and zone.get('name') == c['zone_name'] and
                zone.get('account', {}).get('id') == c['account_id'] and zone.get('status') == 'active' and not zone.get('paused'),
                'Wrong/inactive account or zone; refusing Cloudflare operation')
        org = selected(self.api.get(self.prefix + '/access/organizations'), ORG_FIELDS)
        require(org == c['organization'], 'Access organization/issuer boundary changed')
        refs, ref_observation = reference_inventory(self.api, c['account_id'], c['policies'], c['application']['spec'])
        require(refs == c['references'], 'Referenced service or identity-provider identities changed')
        tunnels = [x for x in self.api.list(self.prefix + '/cfd_tunnel?is_deleted=false') if x['name'] == c['tunnel']['name'] or x['id'] == ids['tunnel']]
        apps = []
        for item in self.api.list(self.prefix + '/access/apps'):
            domains = [item.get('domain', ''), *item.get('self_hosted_domains', [])]
            domains += [d.get('uri', '') for d in item.get('destinations', []) if isinstance(d, dict)]
            if item['id'] == ids['application'] or any(covers(value, c['hostname']) for value in domains) or item.get('name') == c['application']['spec']['name']:
                apps.append(item)
        dns = self.api.list(self.zone + '/dns_records?name=' + c['hostname'])
        pools = {'tunnel': tunnels, 'application': apps, 'dns': dns}
        observed, changes = {'organization': org, 'references': ref_observation}, []
        for kind, entries in pools.items():
            identity = ids[kind]
            if identity is None:
                require(not entries, 'Unowned resource or overlapping application already exists; explicit adoption required')
                changes.append('create_' + kind)
                observed[kind] = None
            else:
                require(len(entries) == 1 and entries[0]['id'] == identity, 'Owned resource missing, replaced or ambiguous')
                if kind == 'tunnel':
                    value = selected(entries[0], {'id', 'name', 'config_src'})
                    require(value == {**c['tunnel'], 'id': identity}, 'Tunnel ownership/configuration drift')
                elif kind == 'application':
                    value = self.api.get(self.prefix + '/access/apps/' + identifier(identity))
                    require(app_spec(value) == app_spec(c['application']['spec']) and value['aud'] == ids['aud'],
                            'Application settings or audience drift')
                    value = {'id': identity, 'aud': value['aud'], 'spec': app_spec(value)}
                else:
                    record = entries[0]
                    value = {k: record.get(k, {'tags': [], 'settings': {}}.get(k)) for k in c['dns']}
                    require(value == {**c['dns'], 'id': identity} and record.get('type') == 'CNAME' and
                            record.get('proxied') is True and isinstance(record.get('content'), str) and record['content'].rstrip('.') == ids['tunnel'] + '.cfargotunnel.com',
                            'DNS record drift or public bypass detected')
                observed[kind] = value
        policies = self.api.list(self.prefix + '/access/apps/' + ids['application'] + '/policies') if ids['application'] else []
        if ids['application']:
            require({p['id'] for p in policies} == set(ids['policies']), 'Application policy links changed')
        reusable = self.api.list(self.prefix + '/access/policies') if c['mode'] == 'create' else []
        observed['policies'] = []
        for index, policy in enumerate(c['policies']):
            identity = ids['policies'][index]
            if c['mode'] == 'create':
                matches = [p for p in reusable if p['name'] == policy['spec']['name'] or p['id'] == identity]
                require(len(matches) == (1 if identity else 0) and (not identity or matches[0]['id'] == identity),
                        'Unowned, missing or ambiguous reusable policy')
            if identity is None:
                changes.append('create_policy_' + str(index + 1)); observed['policies'].append(None)
                continue
            if ids['application']:
                value = unique(policies, lambda p: p['id'] == identity, 'owned policy')
            else:
                value = self.api.get(self.prefix + '/access/policies/' + identifier(identity))
                value['precedence'] = policy['spec']['precedence']
            require(policy_spec(value) == policy['spec'] and value.get('reusable', False) == policy['reusable'],
                    'Access policy configuration or ownership drift')
            observed['policies'].append({'id': identity, 'spec': policy_spec(value), 'reusable': value.get('reusable', False)})
        if not self.owner:
            changes.insert(0, 'record_ownership')
        observed['owner'] = digest(self.owner) if self.owner else None
        return observed, changes

    def report(self, action, observed, changes, **extra):
        return {'schema': 1, 'action': action, 'revision': self.binding['revision'], 'mode': self.config['mode'],
                'hostname': self.config['hostname'], 'identity_fingerprint': self.identity,
                'observation_digest': digest(observed), 'changes': changes,
                'policy_count': len(self.config['policies']), 'configuration_boundary_validated': True, **extra}

    def plan(self):
        observed, changes = self.observe()
        receipt = {**self.binding, 'review_id': secrets.token_hex(16), 'created': time.time(),
                   'observation': digest(observed), 'changes': changes}
        write_private(self.receipt_path, receipt)
        return self.report('plan', observed, changes, review_id=receipt['review_id'], expires_in_seconds=1800)

    def verify(self):
        observed, changes = self.observe()
        require(not changes, 'Cloudflare is not fully adopted/provisioned; run and review a plan')
        return self.report('verify', observed, [], changed=False)

    def save(self):
        write_private(self.owner_path, self.owner)

    def post(self, resource, path, body):
        # Persist uncertainty before POST. Never retry an ambiguous result/name-match.
        self.owner['pending'] = resource; self.save()
        try:
            result = self.api.post(path, body)
        except APIError as error:
            if error.definitive:
                self.owner['pending'] = None; self.save()
            raise
        identity = identifier(result.get('id'), 'hex' if resource == 'dns' else 'uuid')
        if resource.startswith('policy_'):
            self.owner['ids']['policies'][int(resource.split('_')[1])] = identity
        else:
            self.owner['ids'][resource] = identity
        if resource == 'application':
            require(isinstance(result.get('aud'), str) and len(result['aud']) == 64, 'Created application audience is missing')
            self.owner['ids']['aud'] = result['aud']
        self.owner['pending'] = None; self.save()

    def apply(self, review_id):
        receipt = read_private(self.receipt_path)
        require(all(receipt.get(k) == v for k, v in self.binding.items()) and receipt.get('review_id') == review_id and
                0 <= time.time() - receipt.get('created', 0) <= 1800, 'Review expired or source/inputs/host changed')
        observed, changes = self.observe()
        require(receipt['observation'] == digest(observed) and receipt['changes'] == changes, 'Cloudflare drift changed the reviewed plan')
        self.receipt_path.unlink()
        original_changes = list(changes)
        if not self.owner:
            self.owner = {'schema': 1, 'inputs': self.binding['inputs'], 'machine_id': self.binding['machine_id'],
                          'ids': self.identities(), 'pending': None, 'credentials': self.config['credentials']}
            if self.config['mode'] == 'create':
                self.owner['credentials'] = {'AccountTag': self.config['account_id'], 'TunnelID': None,
                                            'TunnelSecret': base64.b64encode(secrets.token_bytes(32)).decode()}
            self.save()
        if self.config['mode'] == 'create':
            self.provision(observed['references'])
        verified, remaining = self.observe()
        require(not remaining, 'Provisioning is incomplete; preserve state and review a fresh plan')
        self.export()
        return self.report('apply', verified, [], changed=bool(original_changes), applied_changes=original_changes)

    def provision(self, reference_snapshot):
        c, ids = self.config, self.owner['ids']
        def guard():
            observed, _ = self.observe()
            require(observed['references'] == reference_snapshot, 'Referenced access membership changed during apply')
        for index, policy in enumerate(c['policies']):
            if not ids['policies'][index]:
                guard()
                self.post('policy_' + str(index), self.prefix + '/access/policies',
                          {k: v for k, v in policy['spec'].items() if k != 'precedence'})
        if not ids['application']:
            guard()
            links = [{'id': identity, 'precedence': policy['spec']['precedence']} for identity, policy in zip(ids['policies'], c['policies'])]
            self.post('application', self.prefix + '/access/apps', {**c['application']['spec'], 'policies': links})
        if not ids['tunnel']:
            guard()
            self.post('tunnel', self.prefix + '/cfd_tunnel', {'name': c['tunnel']['name'], 'config_src': 'local',
                      'tunnel_secret': self.owner['credentials']['TunnelSecret']})
            self.owner['credentials']['TunnelID'] = ids['tunnel']; self.save()
        if not ids['dns']:
            guard()  # Access and its exact policy links must be verified before publishing DNS.
            body = {k: v for k, v in c['dns'].items() if k != 'id' and v is not None}
            self.post('dns', self.zone + '/dns_records', {**body, 'type': 'CNAME', 'name': c['hostname'],
                      'content': ids['tunnel'] + '.cfargotunnel.com', 'proxied': True})

    def export(self):
        ids, c = self.owner['ids'], self.config
        credentials = {**self.owner['credentials'], 'TunnelID': ids['tunnel']}
        # Files are private and deterministic; no service-token secrets are obtained/rotated.
        write_private(self.directory / 'tunnel-credentials.json', credentials)
        write_private(self.directory / 'handoff.json', {'schema': 1, 'application': {
            'BATTLEFIELD_AUTH_MODE': 'cloudflare', 'BATTLEFIELD_PUBLIC_ORIGIN': 'https://' + c['hostname'],
            'BATTLEFIELD_ACCESS_ISSUER': 'https://' + c['organization']['auth_domain'],
            'BATTLEFIELD_ACCESS_AUDIENCE': ids['aud']}, 'deployment': {'BF_TUNNEL_ID': ids['tunnel'],
            'BF_TUNNEL_CREDENTIALS_FILE': str(self.directory / 'tunnel-credentials.json')}})
