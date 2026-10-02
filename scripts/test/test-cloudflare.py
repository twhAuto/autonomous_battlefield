#!/usr/bin/env python3
"""Cloudflare ownership, failure ordering and credential-preservation regression tests."""
import copy
import base64
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
import urllib.error

ROOT = Path(__file__).resolve().parents[2]

def load(name, path):
    spec = importlib.util.spec_from_file_location(name, ROOT / path)
    result = importlib.util.module_from_spec(spec); spec.loader.exec_module(result)
    return result

policy = load('cf_policy', 'infra/ansible/module_utils/battlefield_cloudflare_policy.py')
sys.modules['ansible.module_utils.battlefield_cloudflare_policy'] = policy
cloud = load('cf_engine', 'infra/ansible/module_utils/battlefield_cloudflare.py')
UUID = lambda n: f'{n:08x}-1111-4111-a111-{n:012x}'
ACCOUNT, ZONE, DNS = 'a' * 32, 'b' * 32, 'c' * 32
APP, TUNNEL, POLICY, IDP, GROUP, TOKEN = (UUID(n) for n in range(1, 7))
REV, MACHINE = 'd' * 40, 'e' * 32
SECRET = base64.b64encode(b'unit-test-only-tunnel-credential-32-bytes').decode()


def fixture():
    return {'schema': 1, 'mode': 'adopt', 'account_id': ACCOUNT, 'zone_id': ZONE, 'zone_name': 'example.com',
        'hostname': 'play.example.com', 'organization': {'auth_domain': 'test.cloudflareaccess.com', 'mfa_required_for_all_apps': True},
        'tunnel': {'id': TUNNEL, 'name': 'play', 'config_src': 'local'},
        'dns': {'id': DNS, 'ttl': 1, 'comment': None, 'tags': [], 'settings': {'flatten_cname': False}},
        'application': {'id': APP, 'aud': 'f' * 64, 'spec': {'type': 'self_hosted', 'name': 'play', 'domain': 'play.example.com',
            'session_duration': '6h', 'allowed_idps': [IDP], 'options_preflight_bypass': False}},
        'policies': [{'id': POLICY, 'reusable': True, 'spec': {'name': 'players', 'decision': 'allow', 'include': [{'group': {'id': GROUP}}],
            'exclude': [], 'require': [{'login_method': {'id': IDP}}], 'precedence': 1}},
            {'id': UUID(7), 'reusable': True, 'spec': {'name': 'smoke', 'decision': 'non_identity', 'include': [{'service_token': {'token_id': TOKEN}}],
            'exclude': [], 'require': [], 'precedence': 2}}],
        'references': {'groups': [{'id': GROUP}], 'service_tokens': [{'id': TOKEN, 'client_id': 'private-client.access'}],
            'identity_providers': [{'id': IDP, 'type': 'google'}]},
        'credentials': {'AccountTag': ACCOUNT, 'TunnelID': TUNNEL, 'TunnelSecret': SECRET}}


class FakeAPI:
    def __init__(self, config, existing=True):
        c = copy.deepcopy(config)
        self.zone = {'id': ZONE, 'name': 'example.com', 'account': {'id': ACCOUNT}, 'status': 'active', 'paused': False}
        self.organization = c['organization']
        self.resources = {'tunnels': [], 'apps': [], 'dns': [], 'policies': [],
            'groups': [{'id': GROUP, 'include': [{'email': {'email': 'private-player@example.com'}}], 'exclude': [], 'require': []}],
            'service_tokens': [{'id': TOKEN, 'client_id': 'private-client.access', 'expires_at': '2099-01-01T00:00:00Z'}],
            'identity_providers': [{'id': IDP, 'type': 'google'}]}
        if existing:
            self.resources['tunnels'] = [c['tunnel']]
            self.resources['policies'] = [{'id': p['id'], 'reusable': p['reusable'], **p['spec']} for p in c['policies']]
            self.resources['apps'] = [{'id': APP, 'aud': c['application']['aud'], **c['application']['spec'],
                'policies': [{'id': p['id'], 'precedence': p['spec']['precedence']} for p in c['policies']]}]
            self.resources['dns'] = [{**c['dns'], 'name': c['hostname'], 'content': TUNNEL + '.cfargotunnel.com', 'type': 'CNAME', 'proxied': True}]
        self.posts = []; self.fail = None; self.next_id = 100

    def kind(self, path):
        if '/cfd_tunnel' in path: return 'tunnels'
        if '/dns_records' in path: return 'dns'
        return path.split('?')[0].split('/')[-1]

    def list(self, path):
        if path.startswith('/zones?'): return [copy.deepcopy(self.zone)]
        if '/apps/' in path and path.split('?')[0].endswith('/policies'):
            app_id = path.split('/apps/')[1].split('/')[0]
            app = next(a for a in self.resources['apps'] if a['id'] == app_id)
            result = []
            for link in app['policies']:
                p = next(p for p in self.resources['policies'] if p['id'] == link['id'])
                result.append({**p, 'precedence': link['precedence']})
            return copy.deepcopy(result)
        result = copy.deepcopy(self.resources[self.kind(path)])
        if '/dns_records?name=' in path:
            name = path.split('name=')[1].split('&')[0]
            result = [r for r in result if r['name'] == name]
        return result

    def get(self, path):
        if path == '/zones/' + ZONE: return copy.deepcopy(self.zone)
        if path.endswith('/organizations'): return copy.deepcopy(self.organization)
        collection, identity = path.rsplit('/', 1)
        return copy.deepcopy(next(item for item in self.list(collection) if item['id'] == identity))

    def post(self, path, data):
        kind = self.kind(path)
        self.posts.append((kind, copy.deepcopy(data)))
        if self.fail == kind:
            raise cloud.APIError(403)
        self.next_id += 1
        identity = f'{self.next_id:032x}' if kind == 'dns' else UUID(self.next_id)
        item = {'id': identity, **copy.deepcopy(data)}
        if kind == 'apps': item['aud'] = f'{self.next_id:064x}'
        if kind == 'policies': item['reusable'] = True
        if kind == 'tunnels': item.pop('tunnel_secret')
        self.resources[kind].append(item)
        return copy.deepcopy(item)


class LifecycleTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.config = fixture(); self.api = FakeAPI(self.config)

    def engine(self, config=None):
        return cloud.Engine(self.api, config or self.config, self.root, REV, MACHINE)

    def adopt(self):
        e = self.engine(); report = e.plan(); return e.apply(report['review_id'])

    def test_capture_checks_deployed_identity_without_api_writes(self):
        c = self.config
        captured = cloud.capture(self.api, c['zone_name'], c['hostname'], c['tunnel']['name'], c['credentials'],
            {'hostname': c['hostname'], 'issuer': 'https://' + c['organization']['auth_domain'], 'audience': c['application']['aud'], 'tunnel_id': TUNNEL})
        self.assertEqual(captured, c)
        self.assertEqual(self.api.posts, [])
        with self.assertRaisesRegex(ValueError, 'does not match'):
            cloud.capture(self.api, c['zone_name'], c['hostname'], 'play', c['credentials'], {})

    def test_adoption_preserves_all_cloud_resources_and_secret_bytes(self):
        before = copy.deepcopy(self.api.resources)
        report = self.adopt()
        self.assertEqual(report['applied_changes'], ['record_ownership'])
        self.assertEqual(before, self.api.resources); self.assertEqual(self.api.posts, [])
        e = self.engine()
        self.assertEqual(cloud.read_private(e.directory / 'tunnel-credentials.json'), self.config['credentials'])
        self.assertEqual(e.verify()['changes'], [])
        report = e.plan(); self.assertEqual(report['changes'], [])
        self.assertFalse(e.apply(report['review_id'])['changed'])

    def test_new_creation_orders_protection_before_dns_and_is_idempotent(self):
        self.api = FakeAPI(self.config, existing=False)
        self.config = policy.creation(self.config, 'fresh.example.com', 'fresh')
        e = self.engine(); e.apply(e.plan()['review_id'])
        self.assertEqual([p[0] for p in self.api.posts], ['policies', 'policies', 'apps', 'tunnels', 'dns'])
        before = copy.deepcopy(self.api.resources)
        e = self.engine(); self.assertFalse(e.apply(e.plan()['review_id'])['changed'])
        self.assertEqual(before, self.api.resources)
        credentials = cloud.read_private(e.directory / 'tunnel-credentials.json')
        self.assertNotEqual(credentials['TunnelSecret'], SECRET)
        self.assertEqual(credentials['TunnelID'], self.api.resources['tunnels'][0]['id'])

    def test_partial_confirmed_create_resumes_without_duplicate_policy_or_app(self):
        self.api = FakeAPI(self.config, existing=False)
        self.config = policy.creation(self.config, 'fresh.example.com', 'fresh')
        self.api.fail = 'tunnels'
        e = self.engine()
        with self.assertRaises(cloud.APIError): e.apply(e.plan()['review_id'])
        self.assertEqual(self.api.resources['dns'], [])
        self.api.fail = None
        e = self.engine(); e.apply(e.plan()['review_id'])
        self.assertEqual(len(self.api.resources['policies']), 2); self.assertEqual(len(self.api.resources['apps']), 1)
        self.assertEqual(e.verify()['changes'], [])

    def test_uncertain_post_is_never_retried_or_adopted_by_name(self):
        self.api = FakeAPI(self.config, existing=False)
        self.config = policy.creation(self.config, 'fresh.example.com', 'fresh')
        e = self.engine(); review = e.plan()
        with patch.object(self.api, 'post', side_effect=cloud.APIError()):
            with self.assertRaises(cloud.APIError): e.apply(review['review_id'])
        with self.assertRaisesRegex(ValueError, 'ambiguous'): self.engine()

    def test_stale_replayed_and_expired_approvals_fail_before_writes(self):
        e = self.engine(); p = e.plan()
        self.api.resources['groups'][0]['include'].append({'email': {'email': 'new@example.com'}})
        with self.assertRaisesRegex(ValueError, 'drift'): e.apply(p['review_id'])
        p = e.plan()
        receipt = cloud.read_private(e.receipt_path); receipt['created'] -= 1801; cloud.write_private(e.receipt_path, receipt)
        with self.assertRaisesRegex(ValueError, 'expired'): e.apply(p['review_id'])
        p = e.plan(); e.apply(p['review_id'])
        with self.assertRaises(FileNotFoundError): e.apply(p['review_id'])
        self.assertEqual(self.api.posts, [])

    def test_wrong_account_issuer_audience_and_service_identity_refused(self):
        mutations = [lambda: self.api.zone['account'].update(id='1'*32),
            lambda: self.api.organization.update(auth_domain='foreign.cloudflareaccess.com'),
            lambda: self.api.resources['apps'][0].update(aud='0'*64),
            lambda: self.api.resources['service_tokens'][0].update(client_id='rotated'),
            lambda: self.api.resources['service_tokens'][0].update(expires_at='2020-01-01T00:00:00Z')]
        for mutate in mutations:
            self.api = FakeAPI(self.config); mutate()
            with self.assertRaises(ValueError): self.engine().plan()
        self.assertEqual(self.api.posts, [])

    def test_unowned_wildcard_path_and_same_name_resources_refused(self):
        new = policy.creation(self.config, 'fresh.example.com', 'fresh')
        for domain in ('fresh.example.com/admin', '*.example.com', 'fresh.example.com'):
            self.api = FakeAPI(self.config, existing=False)
            self.api.resources['apps'] = [{'id': UUID(99), 'domain': domain, 'name': 'foreign'}]
            with self.assertRaisesRegex(ValueError, 'Unowned'): self.engine(new).plan()
        self.api = FakeAPI(self.config, existing=False)
        self.api.resources['tunnels'] = [{'id': TUNNEL, 'name': 'fresh'}]
        with self.assertRaisesRegex(ValueError, 'Unowned'): self.engine(new).plan()

    def test_api_idp_order_is_semantically_equivalent(self):
        self.config['application']['spec']['allowed_idps'].append(UUID(8))
        self.config['references']['identity_providers'].append({'id': UUID(8), 'type': 'onetimepin'})
        self.api = FakeAPI(self.config)
        self.api.resources['identity_providers'].append({'id': UUID(8), 'type': 'onetimepin'})
        e = self.engine(); plan = e.plan()
        self.api.resources['apps'][0]['allowed_idps'].reverse()
        e.apply(plan['review_id'])
        self.assertEqual(self.api.posts, [])

    def test_public_bypass_and_group_escalation_refused(self):
        for modify in [lambda: self.api.resources['dns'][0].update(proxied=False),
            lambda: self.api.resources['policies'][0].update(decision='bypass'),
            lambda: self.api.resources['apps'][0].update(options_preflight_bypass=True),
            lambda: self.api.resources['groups'][0].update(include=[{'everyone': {}}]),
            lambda: self.api.resources['groups'][0].update(include=[{'group': {'id': GROUP}}])]:
            self.api = FakeAPI(self.config); modify()
            with self.assertRaises(ValueError): self.engine().plan()

    def test_external_group_membership_is_observed_but_not_restored(self):
        self.adopt()
        self.api.resources['groups'][0]['include'] = []
        e = self.engine(); self.assertEqual(e.plan()['changes'], [])
        self.assertEqual(self.api.resources['groups'][0]['include'], [])

    def test_replaced_deleted_extra_resources_and_policy_links_fail(self):
        for kind in ('apps', 'tunnels', 'dns', 'policies'):
            self.api = FakeAPI(self.config)
            self.api.resources[kind][0]['id'] = UUID(88) if kind != 'dns' else '8'*32
            with self.assertRaises((ValueError, StopIteration)): self.engine().plan()
        self.api = FakeAPI(self.config)
        self.api.resources['apps'][0]['policies'].pop()
        with self.assertRaisesRegex(ValueError, 'links'): self.engine().plan()

    def test_private_state_symlinks_and_modes_refused(self):
        e = self.engine(); self.adopt()
        os.chmod(e.owner_path, 0o644)
        with self.assertRaisesRegex(ValueError, 'private'): self.engine()
        os.chmod(e.owner_path, 0o600)
        target = self.root / 'foreign'; e.owner_path.rename(target); e.owner_path.symlink_to(target)
        with self.assertRaises(OSError): self.engine()

    def test_reports_never_contain_secret_membership_or_service_identity(self):
        e = self.engine(); p = e.plan(); applied = e.apply(p['review_id'])
        text = json.dumps([p, applied, e.verify()])
        for private in (SECRET, 'private-player', 'private-client', GROUP, TOKEN, ACCOUNT): self.assertNotIn(private, text)

    def test_changed_inputs_and_physical_host_refused_after_adoption(self):
        self.adopt()
        with self.assertRaisesRegex(ValueError, 'host changed'):
            cloud.Engine(self.api, self.config, self.root, REV, 'a'*32)
        changed = copy.deepcopy(self.config); changed['application']['spec']['session_duration'] = '8h'
        with self.assertRaisesRegex(ValueError, 'configuration'): self.engine(changed)


class APIAndInputTests(unittest.TestCase):
    def test_paginated_inventory_and_duplicates(self):
        api = cloud.API('private-token')
        first = {'result': [{'id': str(i)} for i in range(50)], 'result_info': {'total_pages': 2}}
        second = {'result': [{'id': '50'}], 'result_info': {'total_pages': 2}}
        with patch.object(api, 'request', side_effect=[first, second]) as call:
            self.assertEqual(len(api.list('/accounts/a/access/apps')), 51)
            self.assertIn('page=2', call.call_args.args[1])
        with patch.object(api, 'request', side_effect=[first, first]):
            with self.assertRaisesRegex(ValueError, 'Duplicate'): api.list('/accounts/a/access/apps')

    def test_api_errors_do_not_echo_token_or_response_body(self):
        api = cloud.API('private-token')
        error = urllib.error.HTTPError('https://api.cloudflare.com', 403, 'private-response', {}, io.BytesIO(b'private-body'))
        with patch.object(api.opener, 'open', side_effect=error):
            with self.assertRaises(cloud.APIError) as result: api.get('/zones')
        self.assertNotIn('private', str(result.exception))
        self.assertTrue(result.exception.definitive)
        self.assertFalse(cloud.APIError(408).definitive)
        self.assertIsNone(cloud.NoRedirect().redirect_request(None, None, 302, '', {}, 'https://foreign.invalid'))

    def test_malformed_schema_or_broad_identity_rejected(self):
        for change in [lambda c: c.update(account_id='../escape'),
            lambda c: c.update(hostname='*.example.com'),
            lambda c: c['policies'][0]['spec'].update(include=[{'login_method': {'id': IDP}}]),
            lambda c: c['application']['spec'].update(options_preflight_bypass=True),
            lambda c: c['credentials'].update(TunnelID=UUID(99))]:
            c = fixture(); change(c)
            with self.assertRaises(ValueError): policy.validate(c)

    def test_observed_cloudflare_response_shapes_are_supported(self):
        data = json.loads((ROOT/'scripts/test/fixtures/cloudflare-api-shapes.json').read_text())
        app = policy.app_spec(data['app'])
        self.assertEqual(app['session_duration'], '6h')
        self.assertFalse(app['options_preflight_bypass'])
        self.assertEqual(len(data['policies']), 3)
        for value in data['policies']:
            self.assertTrue(value['reusable'])
            self.assertEqual(policy.policy_spec(value)['precedence'], value['precedence'])
        # Future unknown security controls stay fail-closed, rather than silently ignored.
        changed = copy.deepcopy(data['app']); changed['allow_authenticate_via_warp'] = True
        with self.assertRaisesRegex(ValueError, 'Unsupported'): policy.app_spec(changed)

    def test_null_expiry_and_non_object_private_files_have_safe_errors(self):
        with tempfile.TemporaryDirectory() as directory:
            api = FakeAPI(fixture()); api.resources['service_tokens'][0]['expires_at'] = None
            with self.assertRaisesRegex(ValueError, 'expired'):
                cloud.Engine(api, fixture(), Path(directory), REV, MACHINE).plan()
            p = Path(directory)/'invalid.json'; p.write_text('[]'); p.chmod(0o600)
            with self.assertRaisesRegex(ValueError, 'JSON object'): cloud.read_private(p)

    def test_cli_reports_preflight_reason_without_traceback(self):
        with tempfile.TemporaryDirectory() as directory:
            p = Path(directory)/'password'; p.write_text('synthetic-vault-password'); p.chmod(0o600)
            result = subprocess.run([sys.executable, str(ROOT/'scripts/infra/cloudflare.py'), 'prepare-create',
                '--vault', str(Path(directory)/'out.vault'), '--vault-password-file', str(p)], capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('prepare-create requires', result.stderr)
        self.assertNotIn('Traceback', result.stderr)

    @unittest.skipUnless(os.environ.get('BATTLEFIELD_TEST_ANSIBLE_BIN'), 'Pinned Ansible required for templating regression')
    def test_real_ansible_never_templates_api_strings_or_logs_inputs(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory); canary = root/'executed'
            config = fixture()
            expression = "{{ lookup('pipe', 'touch " + str(canary) + "') }}"
            config['policies'][0]['spec']['name'] = expression
            config['dns']['comment'] = expression
            config['application']['spec']['options_preflight_bypass'] = True
            inputs = root/'inputs.json'
            inputs.write_text(json.dumps({'cloudflare_action':'plan', 'cloudflare_token':'synthetic-canary-token',
                'cloudflare_config':config, 'cloudflare_state_root':str(root/'state'), 'cloudflare_revision':REV,
                'cloudflare_machine_id':MACHINE, 'cloudflare_review_id':'', 'cloudflare_report':str(root/'report.json')}))
            inputs.chmod(0o600)
            env = {k:v for k,v in os.environ.items() if not k.startswith(('ANSIBLE_', 'PYTHON'))}
            env.update(ANSIBLE_CONFIG=str(ROOT/'infra/ansible/ansible.cfg'), ANSIBLE_NOCOLOR='1')
            result = subprocess.run([str(Path(os.environ['BATTLEFIELD_TEST_ANSIBLE_BIN'])/'ansible-playbook'),
                '-i','localhost,',str(ROOT/'infra/ansible/cloudflare.yml'), '-e',
                json.dumps({'cloudflare_inputs_file':str(inputs)})], env=env, capture_output=True, text=True, timeout=45)
            output = result.stdout + result.stderr
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('Public OPTIONS bypass is not permitted', output)
            self.assertFalse(canary.exists(), 'API-derived Jinja was executed')
            for secret in (SECRET, 'synthetic-canary-token', expression): self.assertNotIn(secret, output)

    def test_cli_help_outside_checkout_without_pythonpath(self):
        with tempfile.TemporaryDirectory() as directory:
            result = subprocess.run([sys.executable, str(ROOT/'scripts/infra/cloudflare.py'), '--help'], cwd=directory,
                env={k:v for k,v in os.environ.items() if k!='PYTHONPATH'}, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('prepare-create', result.stdout)


if __name__ == '__main__': unittest.main()
