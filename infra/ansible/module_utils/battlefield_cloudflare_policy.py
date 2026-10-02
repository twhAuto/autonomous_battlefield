"""Narrow, versioned Cloudflare contract for the existing self-hosted application."""
import base64
import copy
import hashlib
import json
import re

APP_FIELDS = {'type', 'name', 'domain', 'session_duration', 'allowed_idps', 'app_launcher_visible',
              'auto_redirect_to_identity', 'enable_binding_cookie', 'http_only_cookie_attribute',
              'options_preflight_bypass', 'eager_redirect_cookie_setting', 'tags', 'mfa_config',
              'same_site_cookie_attribute', 'path_cookie_attribute'}
APP_READONLY = {'id', 'uid', 'aud', 'created_at', 'updated_at', 'policies', 'self_hosted_domains', 'destinations'}
POLICY_FIELDS = {'name', 'decision', 'include', 'exclude', 'require', 'precedence', 'connection_rules',
                 'mfa_config', 'session_duration'}
POLICY_READONLY = {'id', 'uid', 'created_at', 'updated_at', 'reusable', 'app_count'}
ORG_FIELDS = {'auth_domain', 'allow_authenticate_via_warp', 'warp_auth_non_browser_401',
              'deny_unmatched_requests', 'strict_service_token_auth', 'mfa_required_for_all_apps',
              'mfa_config', 'trusted_accounts'}


def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


def require(condition, message):
    if not condition:
        raise ValueError(message)


def identifier(value, kind='uuid'):
    pattern = r'[0-9a-f]{32}' if kind == 'hex' else r'[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}'
    require(isinstance(value, str) and re.fullmatch(pattern, value), 'Invalid Cloudflare resource identity')
    return value


def hostname(value):
    require(isinstance(value, str) and len(value) <= 253 and '.' in value and
            all(re.fullmatch(r'[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?', label) for label in value.split('.')),
            'Expected an exact lowercase DNS hostname, without wildcard or path')
    return value


def selected(value, fields):
    return {key: copy.deepcopy(value[key]) for key in sorted(fields) if key in value}


def app_spec(value):
    require(isinstance(value, dict) and not value.keys() - APP_FIELDS - APP_READONLY,
            'Unsupported Access application fields; extend the contract before capture')
    result = selected(value, APP_FIELDS)
    require(result.get('type') == 'self_hosted' and isinstance(result.get('name'), str) and result['name'],
            'Only a named self-hosted Access application is supported')
    host = hostname(result.get('domain'))
    require(value.get('self_hosted_domains', [host]) == [host], 'Additional Access domains are not owned')
    destinations = value.get('destinations', [{'type': 'public', 'uri': host}])
    require(destinations == [{'type': 'public', 'uri': host}], 'Additional Access destinations are not owned')
    require(result.get('options_preflight_bypass') is False, 'Public OPTIONS bypass is not permitted')
    require(isinstance(result.get('allowed_idps'), list) and result['allowed_idps'], 'Explicit identity providers required')
    for identity in result['allowed_idps']:
        identifier(identity)
    require(len(result['allowed_idps']) == len(set(result['allowed_idps'])), 'Duplicate identity provider')
    result['allowed_idps'] = sorted(result['allowed_idps'])
    require(re.fullmatch(r'[1-9][0-9]*[mh]', result.get('session_duration', '')), 'Explicit session duration required')
    return result


def rules(values, *, service=False):
    require(isinstance(values, list), 'Access rules must be lists')
    for rule in values:
        require(isinstance(rule, dict) and len(rule) == 1, 'Malformed Access rule')
        kind, entry = next(iter(rule.items()))
        require(kind in ({'service_token'} if service else {'email', 'group', 'login_method', 'service_token'}),
                'Unsupported or broad Access selector; public bypass is refused')
        require(isinstance(entry, dict), 'Malformed Access selector')
        key = {'email': 'email', 'group': 'id', 'login_method': 'id', 'service_token': 'token_id'}[kind]
        require(set(entry) == {key}, 'Unsupported Access selector fields')
        if kind == 'email':
            require(isinstance(entry[key], str) and re.fullmatch(r'[^\s@*]+@[^\s@*]+\.[^\s@*]+', entry[key]),
                    'Only explicit email identities are supported')
        else:
            identifier(entry[key])


def policy_spec(value):
    require(isinstance(value, dict) and not value.keys() - POLICY_FIELDS - POLICY_READONLY,
            'Unsupported Access policy fields')
    result = selected(value, POLICY_FIELDS)
    require(result.get('decision') in ('allow', 'non_identity', 'deny'), 'Bypass and unknown policy decisions refused')
    require(isinstance(result.get('name'), str) and result['name'], 'Policy name required')
    require(type(result.get('precedence')) is int and result['precedence'] > 0, 'Positive policy precedence required')
    require(result.get('include'), 'Policy must include explicit identities')
    for field in ('include', 'exclude', 'require'):
        rules(result.get(field, []), service=result['decision'] == 'non_identity' and field == 'include')
        result.setdefault(field, [])
    # Include-only login_method would admit every user of an identity provider.
    if result['decision'] == 'allow':
        require(all('email' in r or 'group' in r for r in result['include']), 'Human allow rules require explicit email/group membership')
    return result


def references(policies, app):
    refs = {'groups': set(), 'service_tokens': set(), 'identity_providers': set(app['allowed_idps'])}
    for policy in policies:
        for field in ('include', 'exclude', 'require'):
            for rule in policy['spec'].get(field, []):
                for kind, target, key in [('group', 'groups', 'id'), ('service_token', 'service_tokens', 'token_id'),
                                          ('login_method', 'identity_providers', 'id')]:
                    if kind in rule:
                        refs[target].add(rule[kind][key])
    return refs


def validate(config):
    require(isinstance(config, dict) and set(config) == {'schema', 'mode', 'account_id', 'zone_id', 'zone_name',
            'hostname', 'organization', 'tunnel', 'dns', 'application', 'policies', 'references', 'credentials'},
            'Invalid Cloudflare input schema')
    require(type(config['schema']) is int and config['schema'] == 1 and config['mode'] in ('adopt', 'create'),
            'Unsupported Cloudflare contract version/mode')
    for key in ('account_id', 'zone_id'):
        identifier(config[key], 'hex')
    host, zone = hostname(config['hostname']), hostname(config['zone_name'])
    require(host.endswith('.' + zone), 'Application must be beneath the exact registered zone')
    require(re.fullmatch(r'[a-z0-9-]+\.cloudflareaccess\.com', config['organization'].get('auth_domain', '')),
            'Expected the existing Cloudflare Access issuer')
    require(set(config['organization']) <= ORG_FIELDS, 'Unsupported organization contract')
    tunnel = config['tunnel']
    require(set(tunnel) == {'id', 'name', 'config_src'} and tunnel['config_src'] == 'local' and
            isinstance(tunnel['name'], str) and re.fullmatch(r'[a-zA-Z0-9_-]{1,100}', tunnel['name']),
            'Expected a named locally managed tunnel')
    app = config['application']
    require(set(app) == {'id', 'aud', 'spec'} and not app['spec'].keys() - APP_FIELDS and app_spec(app['spec'])['domain'] == host,
            'Invalid application contract')
    dns = config['dns']
    require(set(dns) == {'id', 'ttl', 'comment', 'tags', 'settings'} and type(dns['ttl']) is int and dns['ttl'] >= 1,
            'Invalid DNS contract')
    require(isinstance(config['policies'], list) and config['policies'], 'At least one Access policy required')
    precedences = []
    for policy in config['policies']:
        require(set(policy) == {'id', 'spec', 'reusable'}, 'Invalid policy contract')
        require(type(policy['reusable']) is bool, 'Expected policy ownership type')
        require(policy_spec(policy['spec']) == policy['spec'], 'Invalid policy specification')
        precedences.append(policy['spec']['precedence'])
    require(len(precedences) == len(set(precedences)), 'Policy precedences must be unique')
    identities = [tunnel['id'], app['id'], dns['id'], *(p['id'] for p in config['policies'])]
    if config['mode'] == 'adopt':
        identifier(dns['id'], 'hex')
        for identity in identities[:2] + identities[3:]:
            identifier(identity)
        require(re.fullmatch('[0-9a-f]{64}', app['aud']), 'Expected existing Access audience')
        credential = config['credentials']
        require(isinstance(credential, dict) and set(credential) == {'AccountTag', 'TunnelID', 'TunnelSecret'} and
                credential['AccountTag'] == config['account_id'] and credential['TunnelID'] == tunnel['id'],
                'Connector credentials must match the adopted account and tunnel')
        try:
            require(len(base64.b64decode(credential['TunnelSecret'], validate=True)) >= 32, 'Invalid connector secret')
        except (ValueError, TypeError):
            raise ValueError('Invalid connector secret') from None
    else:
        require(all(x is None for x in identities) and app['aud'] is None and config['credentials'] is None,
                'Fresh creation must not reuse resource identities or connector credentials')
    refs = references(config['policies'], app['spec'])
    require(set(config['references']) == set(refs), 'Invalid referenced identity inventory')
    for kind, ids in refs.items():
        values = config['references'][kind]
        require(isinstance(values, list) and {x['id'] for x in values} == ids and len(values) == len(ids),
                'Referenced identity inventory is incomplete or ambiguous')
    return config


def creation(config, host, name):
    result = copy.deepcopy(validate(config))
    result.update(mode='create', hostname=hostname(host), credentials=None)
    result['tunnel'].update(id=None, name=name)
    result['dns']['id'] = None
    result['application'].update(id=None, aud=None)
    result['application']['spec'].update(domain=host, name=name)
    for index, policy in enumerate(result['policies'], 1):
        policy.update(id=None, reusable=True)
        policy['spec']['name'] = f'{name}-policy-{index}'
    return validate(result)
