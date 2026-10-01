#!/usr/bin/env python3
"""CI configuration/secret boundaries and maintenance exclusion regressions."""
import base64
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import tarfile
import unittest
from unittest.mock import Mock, patch
import urllib.error

ROOT = Path(__file__).resolve().parents[2]


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


ci = load('ci_bootstrap', ROOT / 'scripts/infra/ci-bootstrap.py')
runtime = load('ci_runtime', ROOT / 'scripts/infra/ci-runtime.py')


def profile():
    return {'schema': 1, 'machine_id': 'a' * 32, 'operator': 'operator', 'home': '/home/operator',
            'mode': 'adopt', 'jenkins_admin': 'admin', 'jenkins_url': 'http://192.0.2.1:8080/', 'agent_unit_sha256': ''}


def inputs():
    values = {}
    for name, kind in runtime.CREDENTIALS.items():
        entry = {'type': kind}
        if kind == 'githubApp': entry.update(appId='123', privateKey='private-key-fixture')
        elif kind == 'usernamePassword': entry.update(username='operator', password='canary-secret')
        else: entry['value'] = 'canary-secret'
        values[name] = entry
    for name in runtime.FOLDER_CREDENTIALS:
        values[name] = {'type': 'file', 'value': '$ANSIBLE_VAULT;1.1;AES256\nfixture' if name == 'host-inputs' else 'canary-secret'}
    return {'ci_credentials': json.dumps(values)}


class RegistrationTests(unittest.TestCase):
    def test_rejects_wrong_identity_fields_and_shell_interpolation(self):
        good = profile()
        self.assertEqual(ci.validate(good), good)
        for key, value in [('schema', True), ('mode', 'overwrite'), ('operator', 'root;id'),
                           ('home', '/home/other'), ('jenkins_url', 'http://user:secret@localhost/'),
                           ('jenkins_url', 'https://localhost/${readFile:/tmp/secret}'), ('agent_unit_sha256', 'xyz')]:
            with self.subTest(key=key, value=value), self.assertRaises(ValueError):
                ci.validate(dict(good, **{key: value}))
        with self.assertRaises(ValueError): ci.validate(dict(good, password='secret'))

    def test_jenkins_cannot_run_controller_maintenance(self):
        with patch.dict(ci.os.environ, {'JENKINS_NODE_COOKIE': 'fixture'}):
            with self.assertRaisesRegex(ValueError, 'outside Jenkins'):
                ci.playbook(Mock(), {}, {}, Mock())

    def test_release_fingerprint_tracks_managed_source(self):
        self.assertRegex(ci.source_hash(), r'^[0-9a-f]{64}$')
        self.assertEqual(ci.source_hash(), ci.source_hash())


class CredentialTests(unittest.TestCase):
    def test_all_ids_and_types_required_for_clean_install(self):
        values = inputs()
        self.assertEqual(set(runtime.credentials(values)), set(runtime.CREDENTIALS) | set(runtime.FOLDER_CREDENTIALS))
        for alteration in ('missing', 'extra', 'wrong_type', 'unencrypted'):
            altered = json.loads(values['ci_credentials'])
            if alteration == 'missing': del altered['battlefield-production-kubeconfig']
            elif alteration == 'extra': altered['other-secret'] = {'type': 'file', 'value': 'secret'}
            elif alteration == 'wrong_type': altered['battlefield-cosign-key']['type'] = 'string'
            else: altered['host-inputs']['value'] = 'plain-secret'
            with self.subTest(alteration=alteration), self.assertRaises(runtime.Failure):
                runtime.credentials({'ci_credentials': json.dumps(altered)})

    def test_generated_password_file_newline_is_not_part_of_the_login_password(self):
        values = inputs()
        values['ci_admin_password'] = 'Synthetic-password-123!\n'
        data = runtime.controller_inputs(dict(profile(), mode='install'), values, 'token', {'scanner': {'version': '8.1.0.6389'}})
        self.assertEqual(data['adminPassword'], 'Synthetic-password-123!')
        self.assertTrue(data['scannerHome'].endswith('sonar-scanner-8.1.0.6389-linux-x64'))
        self.assertEqual(data['credentials']['battlefield-cosign-key']['value'], 'canary-secret')

    def test_invalid_encrypted_input_errors_never_contain_values(self):
        for value in ('canary-plaintext%', 123, base64.b64encode(b'\xff').decode()):
            with self.subTest(value=value), self.assertRaises(runtime.Failure) as error:
                runtime.decode_inputs({'ci_jenkins_api_token': value})
            self.assertNotIn(str(value), str(error.exception))
        self.assertEqual(runtime.decode_inputs({'ci_admin_password': base64.b64encode(b'canary-value').decode(), 'unrelated': 'ignored'}),
                         {'ci_admin_password': 'canary-value'})

    def test_adoption_refuses_database_password_rotation(self):
        containers = {'database': {'Config': {'Env': ['POSTGRES_PASSWORD=existing']}},
                      'sonarqube': {'Config': {'Env': ['SONAR_JDBC_PASSWORD=existing']}}}
        self.assertEqual(runtime.database_password(containers, {}), 'existing')
        with self.assertRaisesRegex(runtime.Failure, 'never rotates'):
            runtime.database_password(containers, {'ci_database_password': 'different'})
        containers['sonarqube']['Config']['Env'] = ['SONAR_JDBC_PASSWORD=different']
        with self.assertRaisesRegex(runtime.Failure, 'disagree'):
            runtime.database_password(containers, {})

    def test_adoption_imports_existing_sonar_credential_without_rotation(self):
        api = Mock()
        api.call.return_value = b'squ_existingcanarytoken0123456789'
        with patch.object(runtime, 'active', return_value=True), patch.object(runtime, 'jenkins', return_value=api):
            self.assertEqual(runtime.sonar_token(profile(), {}), api.call.return_value.decode())
            with self.assertRaisesRegex(runtime.Failure, 'does not rotate'):
                runtime.sonar_token(profile(), {'ci_sonar_token': 'a-different-token-value'})
        self.assertNotIn('print(', api.call.call_args.kwargs.get('path', ''))

    def test_api_errors_suppress_sensitive_response_bodies(self):
        api = runtime.API('http://127.0.0.1:1/', 'admin', 'canary-token')
        error = urllib.error.HTTPError('http://localhost', 403, 'canary-credential', {}, io.BytesIO(b'canary-password'))
        api.opener.open = Mock(side_effect=error)
        with self.assertRaisesRegex(runtime.Failure, 'HTTP 403') as caught:
            api.call('scriptText', {'script': 'canary-script'})
        self.assertNotIn('canary', str(caught.exception))

    def test_starting_service_connection_resets_become_retryable_safe_errors(self):
        api = runtime.API('http://127.0.0.1:1/', 'admin', 'canary-token')
        api.opener.open = Mock(side_effect=ConnectionResetError('canary-body'))
        with self.assertRaisesRegex(runtime.Failure, 'unavailable') as caught:
            api.call('api/system/status')
        self.assertNotIn('canary', str(caught.exception))

    def test_redirects_are_not_followed_with_authentication(self):
        self.assertIsNone(runtime.NoRedirect().redirect_request(None, None, 302, '', {}, 'https://foreign.example'))
        api = runtime.API('http://127.0.0.1:8080/', 'admin', 'secret')
        api.opener.open = Mock(side_effect=urllib.error.HTTPError('url', 302, '', {'Location': 'https://foreign.example'}, None))
        with self.assertRaises(runtime.Failure): api.call('quietDown', {})


class LifecycleTests(unittest.TestCase):
    def test_paused_nested_pipeline_prevents_drain_even_without_busy_executors(self):
        api = Mock()
        api.call.side_effect = [
            {'jobs': [{'name': 'repo', '_class': 'org.jenkinsci.plugins.workflow.multibranch.WorkflowMultiBranchProject'}]},
            {'jobs': [{'name': 'PR-1', '_class': 'org.jenkinsci.plugins.workflow.job.WorkflowJob', 'builds': [{'number': 3, 'building': True}]}]},
        ]
        self.assertTrue(runtime.running_jobs(api))
        self.assertIn('job/repo/', api.call.call_args.args[0])
        api.call.side_effect = None
        api.call.return_value = {'jobs': [{'name': 'job', 'builds': [{'number': 1, 'building': False}]}]}
        self.assertFalse(runtime.running_jobs(api))

    def test_existing_quiet_mode_is_never_claimed(self):
        api = Mock()
        api.call.return_value = {'quietingDown': True}
        with patch.object(runtime, 'active', return_value=True), patch.object(runtime, 'jenkins', return_value=api), patch.object(runtime, 'private') as write:
            with self.assertRaisesRegex(runtime.Failure, 'already in quiet mode'):
                runtime.drain({'config': profile(), 'inputs': {}})
            write.assert_not_called()
            self.assertEqual(api.call.call_count, 1)

    def test_no_quiet_receipt_means_no_resume_request(self):
        with tempfile.TemporaryDirectory() as work, patch.object(runtime, 'STATE', Path(work)), patch.object(runtime, 'jenkins') as api:
            self.assertEqual(runtime.resume({'config': profile(), 'inputs': {}}), {'resumed': True})
            api.assert_not_called()

    def test_resume_receipt_survives_an_unavailable_controller(self):
        with tempfile.TemporaryDirectory() as work, patch.object(runtime, 'STATE', Path(work)), patch.object(runtime, 'active', return_value=False):
            marker = Path(work) / 'maintenance.json'
            marker.write_text('{"quiet_owned":true}')
            self.assertEqual(runtime.resume({}), {'resume_needed': True})
            self.assertTrue(marker.exists())

    def test_failed_configuration_stays_quiet_until_acceptance_is_recorded(self):
        payload = {'config': profile(), 'inputs': {}, 'lock': {}, 'source': 'fixture'}
        with tempfile.TemporaryDirectory() as work, patch.object(runtime, 'STATE', Path(work)), patch.object(runtime, 'JHOME', Path(work)), patch.object(runtime, 'active', return_value=True), patch.object(runtime, 'jenkins') as api:
            marker = Path(work) / 'maintenance.json'
            marker.write_text('{"quiet_owned":true,"phase":"changing"}')
            flag = Path(work) / 'battlefield-maintenance'
            flag.write_text('maintenance')
            self.assertEqual(runtime.resume(payload), {'resume_needed': True})
            api.assert_not_called()
            self.assertTrue(marker.exists())
            (Path(work) / 'release.json').write_text(json.dumps({'release': runtime.release_hash(payload)}))
            self.assertEqual(runtime.resume(payload), {'resumed': True})
            api.return_value.call.assert_called_once_with('cancelQuietDown', {}, raw=True)
            self.assertFalse(marker.exists())
            self.assertFalse(flag.exists())

    def test_same_release_repair_cannot_resume_using_stale_acceptance(self):
        payload = {'config': profile(), 'inputs': {}, 'lock': {}, 'source': 'unchanged'}
        with tempfile.TemporaryDirectory() as work, patch.object(runtime, 'STATE', Path(work)), patch.object(runtime, 'JHOME', Path(work)), patch.object(runtime, 'active', return_value=True), patch.object(runtime, 'jenkins') as api:
            marker = Path(work) / 'maintenance.json'
            marker.write_text('{"quiet_owned":true,"phase":"draining"}')
            release = Path(work) / 'release.json'
            release.write_text(json.dumps({'release': runtime.release_hash(payload)}))
            writer = runtime.private
            with patch.object(runtime, 'private', side_effect=lambda path, content, **kwargs: writer(path, content)):
                runtime.changing(payload)
            self.assertFalse(release.exists())
            self.assertEqual(runtime.resume(payload), {'resume_needed': True})
            api.assert_not_called()
            self.assertTrue((Path(work) / 'battlefield-maintenance').exists())

    def test_private_file_replacement_is_idempotent_and_refuses_symlinks(self):
        with tempfile.TemporaryDirectory() as work:
            file = Path(work) / 'credential'
            self.assertTrue(runtime.private(file, 'synthetic'))
            inode = file.stat().st_ino
            self.assertFalse(runtime.private(file, 'synthetic'))
            self.assertEqual(file.stat().st_ino, inode)
            self.assertEqual(file.stat().st_mode & 0o777, 0o600)
            link = Path(work) / 'link'
            link.symlink_to(file)
            with self.assertRaises(runtime.Failure): runtime.private(link, 'overwrite')
            self.assertEqual(file.read_text(), 'synthetic')
            file.chmod(0o644)
            self.assertTrue(runtime.private(file, 'synthetic'))
            self.assertEqual(file.stat().st_mode & 0o777, 0o600)

    def test_foreign_controller_credential_is_rejected_in_preflight(self):
        api = Mock()
        api.call.return_value = b'<org.jenkinsci.plugins.plaincredentials.impl.StringCredentialsImpl/>'
        with self.assertRaisesRegex(runtime.Failure, 'different type'):
            runtime.controller_contract(api, profile())
        api.call.return_value = None
        with self.assertRaisesRegex(runtime.Failure, 'Missing required'):
            runtime.controller_contract(api, profile())

    def test_backup_estimate_preserves_history_and_excludes_only_caches(self):
        with tempfile.TemporaryDirectory() as work:
            root = Path(work)
            (root / 'jobs').mkdir()
            (root / 'jobs/build.xml').write_bytes(b'history')
            (root / 'jobs/workspace').mkdir()
            (root / 'jobs/workspace/config.xml').write_bytes(b'job config')
            (root / 'workspace').mkdir()
            (root / 'workspace/cache').write_bytes(b'not backed up')
            (root / 'redirect').symlink_to(root / 'workspace/cache')
            self.assertEqual(runtime.controller_backup_bytes(root), len(b'historyjob config'))
            output = io.BytesIO()
            with tarfile.open(fileobj=output, mode='w') as archive:
                archive.add(root, arcname='jenkins', filter=runtime.controller_archive_filter)
            output.seek(0)
            with tarfile.open(fileobj=output, mode='r') as archive:
                self.assertEqual(archive.extractfile('jenkins/jobs/workspace/config.xml').read(), b'job config')
                self.assertIn('jenkins/jobs/build.xml', archive.getnames())
                self.assertNotIn('jenkins/workspace/cache', archive.getnames())

    def test_backup_requires_a_stopped_controller(self):
        with patch.object(runtime, 'active', return_value=True):
            with self.assertRaisesRegex(runtime.Failure, 'stopped'):
                runtime.backup({})


class ReleaseTests(unittest.TestCase):
    def test_plugin_closure_includes_every_required_dependency_and_platform_pin(self):
        lock = json.loads((ROOT / 'infra/jenkins/controller-lock.json').read_text())
        platform = json.loads((ROOT / 'infra/jenkins/platform-plugins.json').read_text())
        self.assertEqual(lock['core']['version'], platform['tested_core'])
        for name, pin in lock['plugins'].items():
            self.assertTrue(set(pin['dependencies']) <= set(lock['plugins']), name)
            self.assertRegex(pin['sha256'], r'^[0-9a-f]{64}$')
            self.assertTrue(pin['url'].startswith('https://updates.jenkins.io/download/plugins/' + name + '/'))
        for name, version in platform['plugins'].items():
            self.assertEqual(lock['plugins'][name]['version'], version)

    def test_jcasc_has_no_destructive_identity_lists(self):
        content = (ROOT / 'infra/ansible/roles/ci_services/templates/jenkins.yaml.j2').read_text()
        lines = '\n'.join(line for line in content.splitlines() if not line.lstrip().startswith('#'))
        for key in ('securityRealm:', 'authorizationStrategy:', 'users:', 'nodes:', 'credentials:', 'jobs:'):
            self.assertNotIn(key, lines)


if __name__ == '__main__':
    unittest.main()
