#!/usr/bin/env python3
"""K3s ownership, network, registry and review boundary regressions."""
import argparse
import base64
import hashlib
import urllib.error
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import time
import unittest
from unittest.mock import patch, MagicMock

ROOT = Path(__file__).resolve().parents[2]


def module(name, path):
    spec = importlib.util.spec_from_file_location(name, ROOT / path)
    loaded = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(loaded)
    return loaded


k3s = module('k3s', 'scripts/infra/k3s.py')
storage = module('storage', 'infra/ansible/roles/k3s_storage/files/storage.py')
guest = module('guest', 'infra/ansible/roles/k3s_runtime/files/guest.py')
seed = module('seed', 'scripts/infra/seed-platform.py')


class StorageTests(unittest.TestCase):
    def setUp(self):
        self.expected = storage.identity({'storage_id': 'a' * 64})
        self.empty = dict(owner=None, filesystem='', uuid='', marker=None, mounted=False, mount_uuid='', signatures=[])
        self.ready = dict(owner={'identity': self.expected, 'phase': 'ready'}, filesystem='ext4', uuid=self.expected['fs_uuid'],
                          marker=self.expected, mounted=True, mount_uuid=self.expected['fs_uuid'], signatures=[{'type': 'ext4'}])

    def test_only_blank_unowned_disk_initializes(self):
        self.assertTrue(storage.decide(self.empty, self.expected)['initialize'])
        for changes in ({'signatures': [{'type': 'ntfs'}]}, {'filesystem': 'ext4', 'uuid': 'foreign'},
                        {'owner': {'identity': self.expected, 'phase': 'ready'}},
                        {'owner': {'identity': {}, 'phase': 'initializing'}}):
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                storage.decide(dict(self.empty, **changes), self.expected)

    def test_repeated_apply_and_os_replacement_never_reformat_retained_disk(self):
        self.assertFalse(any(storage.decide(self.ready, self.expected).values()))
        after_replacement = dict(self.ready, mounted=False, mount_uuid='', owner=None)
        changes = storage.decide(after_replacement, self.expected)
        self.assertEqual(changes, {'initialize': False, 'mount': True, 'register': True})

    def test_interrupted_format_requires_matching_intent_and_uuid(self):
        interrupted = dict(self.ready, marker=None, owner={'identity': self.expected, 'phase': 'initializing'})
        self.assertFalse(storage.decide(interrupted, self.expected)['initialize'])
        for changes in ({'owner': None}, {'uuid': 'unknown'}, {'marker': {'foreign': True}}):
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                storage.decide(dict(interrupted, **changes), self.expected)

    def test_wrong_mount_and_missing_marker_fail_closed(self):
        for changes in ({'mount_uuid': 'foreign'}, {'marker': None}, {'marker': {}}):
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                storage.decide(dict(self.ready, **changes), self.expected)

    def test_fstab_is_repeatable_and_does_not_replace_unrelated_entries(self):
        original = 'UUID=os / ext4 defaults 0 1\n# retain comments\n'
        expected = storage.fstab_content(original, self.expected['fs_uuid'])
        self.assertTrue(expected.startswith(original))
        self.assertEqual(expected, storage.fstab_content(expected, self.expected['fs_uuid']))
        for text in ('UUID=other /srv/battlefield ext4 defaults 0 2\n',
                     'UUID=' + self.expected['fs_uuid'] + ' /unrelated ext4 defaults 0 2\n'):
            with self.assertRaises(ValueError):
                storage.fstab_content(text, self.expected['fs_uuid'])

    def test_identity_survives_vm_os_replacement_but_not_volume_relocation(self):
        settings = json.loads((ROOT / 'infra/k3s/settings.json').read_text())
        host = {'machine_id': 'host', 'data_uuid': 'partition', 'gateway': '192.168.235.1'}
        conn = {'domain_uuid': 'old', 'data_volume': '/data/volume', 'address': '192.168.235.10'}
        first = k3s.config.contract(host, conn, 8 * 1024**3, settings, {'schema': 1})
        second = k3s.config.contract(host, dict(conn, domain_uuid='new'), 8 * 1024**3, settings, {'schema': 1})
        other = k3s.config.contract(host, dict(conn, data_volume='/foreign/volume'), 8 * 1024**3, settings, {'schema': 1})
        self.assertEqual(first['storage_id'], second['storage_id'])
        self.assertNotEqual(first['storage_id'], other['storage_id'])


class ConfigurationTests(unittest.TestCase):
    def setUp(self):
        self.settings = json.loads((ROOT / 'infra/k3s/settings.json').read_text())

    def test_networks_do_not_overlap_host_vm_or_kind(self):
        self.assertEqual(self.settings, k3s.config.validate(self.settings, ['192.168.235.0/24', '172.18.0.0/16', '10.244.0.0/16']))
        for network in ('10.62.1.0/24', '10.63.0.10/32'):
            with self.assertRaisesRegex(ValueError, 'overlap'):
                k3s.config.validate(self.settings, [network])
        with self.assertRaisesRegex(ValueError, 'DNS'):
            k3s.config.validate(dict(self.settings, cluster_dns='10.63.0.1'), [])
        with self.assertRaisesRegex(ValueError, 'Langfuse'):
            k3s.config.validate(dict(self.settings, langfuse=True), [])

    def test_registry_accepts_only_private_credentials_and_immutable_verified_repo(self):
        value = {'username': 'operator', 'password': 'synthetic', 'probe_image': 'ghcr.io/twh09/autonomous-battlefield-backend@sha256:' + 'a' * 64}
        self.assertEqual(k3s.config.registry(value), value)
        for image in ('ghcr.io/twh09/autonomous-battlefield-backend:latest', 'evil.invalid/backend@sha256:' + 'a' * 64):
            with self.assertRaises(ValueError):
                k3s.config.registry(dict(value, probe_image=image))
        secret = k3s.config.secrets(value)
        self.assertNotIn('synthetic', json.dumps(secret))
        self.assertEqual(len(secret['items']), 3)

    def test_controller_job_lifecycle_is_stable_and_changes_with_immutable_spec(self):
        import subprocess
        binary = Path(os.environ.get('BATTLEFIELD_TEST_ANSIBLE_BIN') or k3s.bootstrap.tools_bin()) / 'python'
        code = r"""
import importlib.util,json
s=importlib.util.spec_from_file_location('filters','infra/ansible/filter_plugins/k3s_platform.py')
m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
doc={'apiVersion':'batch/v1','kind':'Job','metadata':{'name':'eg-gateway-helm-certgen','namespace':'envoy-gateway-system'},
     'spec':{'ttlSecondsAfterFinished':30,'template':{'spec':{'containers':[{'name':'cert','image':'official:v1'}]}}}}
a=m.controller_manifest(json.dumps(doc))['items'][0]
b=m.controller_manifest(json.dumps(doc))['items'][0]
if a!=b or 'ttlSecondsAfterFinished' in a['spec']:raise ValueError('Job did not converge')
doc['spec']['template']['spec']['containers'][0]['image']='official:v2'
c=m.controller_manifest(json.dumps(doc))['items'][0]
if a['metadata']['name']==c['metadata']['name']:raise ValueError('Upgrade would patch immutable job')
doc['metadata']['name']='unreviewed-new-hook'
try:m.controller_manifest(json.dumps(doc))
except ValueError:pass
else:raise ValueError('Unknown lifecycle silently adopted')
"""
        subprocess.run([str(binary), '-c', code], cwd=ROOT, check=True)

    def test_no_public_exposure_or_application_deployment(self):
        c = dict(self.settings, guest_ip='192.168.235.10')
        config = k3s.config.server_config(c)
        self.assertEqual(config['disable'], ['traefik', 'servicelb'])
        self.assertEqual(config['data-dir'], '/srv/battlefield/k3s')
        self.assertNotIn('disable-network-policy', config)
        manifests = k3s.config.platform(c, 'fixture/image@sha256:' + 'a' * 64)['items']
        self.assertFalse(any(x['kind'] == 'Deployment' and x['metadata'].get('namespace') == 'battlefield' for x in manifests))
        storageclass = next(x for x in manifests if x['kind'] == 'StorageClass')
        self.assertEqual(storageclass['reclaimPolicy'], 'Retain')
        proxy = next(x for x in manifests if x['kind'] == 'EnvoyProxy')
        self.assertEqual(proxy['spec']['provider']['kubernetes']['envoyService']['type'], 'NodePort')

    def test_all_jobs_use_trusted_main_without_automatic_triggers(self):
        import xml.etree.ElementTree as ET
        job = ET.fromstring(seed.job_xml('k3s'))
        self.assertEqual(job.findtext('.//scriptPath'), 'Jenkinsfile.k3s')
        self.assertEqual(job.findtext('.//hudson.plugins.git.BranchSpec/name'), '*/main')
        self.assertFalse(list(job.find('triggers')))


class RegistryTests(unittest.TestCase):
    def test_fresh_authenticated_manifest_checks_digest_without_restarting_a_pod(self):
        body = b'{"schemaVersion":2}'
        digest = hashlib.sha256(body).hexdigest()
        auth = base64.b64encode(b'fixture:synthetic-private').decode()
        response = MagicMock()
        response.__enter__.return_value.read.return_value = body
        opener = MagicMock()
        opener.open.return_value = response
        self.assertTrue(guest.registry_manifest('registry.invalid/probe@sha256:' + digest,
                        {'registry.invalid': {'auth': auth}}, opener))
        request = opener.open.call_args.args[0]
        self.assertEqual(request.get_header('Authorization'), 'Basic ' + auth)
        self.assertTrue(request.full_url.startswith('https://registry.invalid/'))
        with self.assertRaisesRegex(ValueError, 'different image digest'):
            guest.registry_manifest('registry.invalid/probe@sha256:' + 'a' * 64, {'registry.invalid': {'auth': auth}}, opener)

    def test_ghcr_exchange_keeps_credentials_on_the_registry_and_scopes_pull(self):
        body = b'{"schemaVersion":2}'
        digest = hashlib.sha256(body).hexdigest()
        auth = base64.b64encode(b'fixture:synthetic-private').decode()
        requests = []
        class Response:
            def __init__(self, body): self.body = body
            def __enter__(self): return self
            def __exit__(self, *_args): pass
            def read(self, _size): return self.body
        def request(req, **_kwargs):
            requests.append(req)
            if len(requests) == 1:
                raise urllib.error.HTTPError(req.full_url, 401, 'unauthorized',
                    {'WWW-Authenticate': 'Bearer realm="https://ghcr.io/token",service="ghcr.io",scope="ignored"'}, None)
            if len(requests) == 2:
                return Response(b'{"token":"synthetic-bearer"}')
            return Response(body)
        opener = MagicMock()
        opener.open.side_effect = request
        self.assertTrue(guest.registry_manifest('ghcr.io/org/probe@sha256:' + digest, {'ghcr.io': {'auth': auth}}, opener))
        self.assertIn('scope=repository%3Aorg%2Fprobe%3Apull', requests[1].full_url)
        self.assertEqual(requests[2].get_header('Authorization'), 'Bearer synthetic-bearer')

    def test_rejected_auth_and_foreign_realm_never_forward_or_print_secrets(self):
        auth = base64.b64encode(b'fixture:synthetic-private').decode()
        for challenge in ('Basic realm="fixture"', 'Bearer realm="https://evil.invalid/token",service="ghcr.io"'):
            opener = MagicMock()
            opener.open.side_effect = urllib.error.HTTPError('https://ghcr.io/v2/', 401, 'unauthorized',
                                                            {'WWW-Authenticate': challenge}, None)
            with self.assertRaises(ValueError) as failed:
                guest.registry_manifest('ghcr.io/org/probe@sha256:' + 'a' * 64, {'ghcr.io': {'auth': auth}}, opener)
            self.assertEqual(opener.open.call_count, 1)
            self.assertNotIn('synthetic-private', str(failed.exception))
            self.assertNotIn(auth, str(failed.exception))
        self.assertIsNone(guest.NoRedirect().redirect_request(None, None, 302, '', {}, 'https://evil.invalid'))
        with self.assertRaisesRegex(ValueError, 'do not belong'):
            guest.registry_manifest('evil.invalid/probe@sha256:' + 'a' * 64, {'ghcr.io': {'auth': auth}}, opener)


class WorkflowTests(unittest.TestCase):
    def test_apply_consumes_receipt_before_mutation_and_rejects_drift(self):
        for drift in (False, True):
            with self.subTest(drift=drift), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                args = argparse.Namespace(action='plan', revision='a' * 40, receipt=root / 'receipt', report=root / 'plan.json',
                    registry_file=root / 'registry.json', settings=ROOT / 'infra/k3s/settings.json')
                creds = {'username': 'fixture', 'password': 'synthetic-private',
                    'probe_image': 'ghcr.io/twh09/autonomous-battlefield-backend@sha256:' + 'a' * 64}
                k3s.tools.atomic(args.registry_file, json.dumps(creds).encode())
                machine = MagicMock()
                machine.existing = {'owned': True}
                machine.directory = root
                machine.inventory = {'machine_id': 'a' * 32, 'data_uuid': 'volume'}
                machine.config = {'network_cidr': '192.168.235.0/24', 'data_gib': 8}
                machine.plan.return_value = ({}, [])
                machine.run.return_value = '[]'
                machine.virsh.return_value = ''
                machine.connection.return_value = {'address': '192.168.235.10', 'domain_uuid': 'vm', 'data_volume': '/data/volume'}
                machine.state_digest.return_value = 'stable-state'
                pins = json.loads((ROOT / 'infra/k3s/pins.json').read_text())
                calls = []
                def action(name, *_args):
                    calls.append(name)
                    if name == 'apply':
                        self.assertFalse(args.receipt.exists(), 'Approval must be consumed before mutation')
                    return {'schema': 1, 'tasks': [{'changed': False}], 'failed': False,
                        'change_digest': 'b' * 64 if drift and len(calls) > 1 else 'a' * 64}
                with patch.object(k3s.platform, 'trusted_revision'), patch.object(k3s.vm, 'VM', return_value=machine), \
                     patch.object(k3s.lock.Path, 'home', return_value=root), patch.object(k3s, 'pins_and_assets', return_value=(pins, {})), \
                     patch.object(k3s, 'run_action', side_effect=action), patch.object(k3s, 'export_access'):
                    k3s.execute(args)
                    args.action, args.report = 'apply', root / 'apply.json'
                    if drift:
                        with self.assertRaisesRegex(ValueError, 'changed after review'):
                            k3s.execute(args)
                        self.assertEqual(calls, ['plan', 'plan'])
                        self.assertTrue(args.receipt.exists())
                    else:
                        k3s.execute(args)
                        self.assertEqual(calls, ['plan', 'plan', 'apply', 'verify', 'plan'])
                        with self.assertRaises(FileNotFoundError):
                            k3s.execute(args)
                        self.assertNotIn('synthetic-private', args.report.read_text())

    def test_verify_refuses_configuration_drift(self):
        k3s.unchanged({'tasks': [{'changed': False}]})
        with self.assertRaisesRegex(ValueError, 'drift'):
            k3s.unchanged({'tasks': [{'changed': True}]})

    def test_receipt_cannot_cross_configuration_or_revision_and_expires(self):
        args = argparse.Namespace(revision='a' * 40)
        receipt = {'schema': 1, 'revision': args.revision, 'inputs': 'opaque-inputs', 'created': time.time()}
        k3s.platform.check_receipt(receipt, args, 'opaque-inputs')
        for changes in ({'revision': 'b' * 40}, {'inputs': 'changed'}, {'created': time.time() - 1900}):
            with self.assertRaises(ValueError):
                k3s.platform.check_receipt(dict(receipt, **changes), args, 'opaque-inputs')

    def test_guest_immutable_identity_rejects_network_changes(self):
        c = {'storage_id': 'a' * 64, 'pod_cidr': '10.62.0.0/16', 'service_cidr': '10.63.0.0/16',
             'cluster_dns': '10.63.0.10', 'datastore': 'sqlite'}
        with patch.object(guest, 'read', side_effect=[dict(c, pod_cidr='10.70.0.0/16'), None]):
            with self.assertRaisesRegex(ValueError, 'migration'):
                guest.inspect(c)


if __name__ == '__main__':
    unittest.main()
