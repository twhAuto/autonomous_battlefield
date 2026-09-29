#!/usr/bin/env python3
"""Exercise bootstrap guards and, optionally, real Vault/Ansible input reconciliation."""
import argparse
import base64
from contextlib import redirect_stdout
import io
import importlib.util
import json
import os
from pathlib import Path
import pwd
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('server_bootstrap', ROOT / 'scripts/infra/server.py')
bootstrap = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bootstrap)


class BootstrapTests(unittest.TestCase):
    def inventory(self, **values):
        result = dict(schema=1, host='192.0.2.10', user='operator',
                      identity_file='/tmp/test-key', machine_id='a' * 32)
        result.update(values)
        return result

    def test_inventory_rejects_ssh_template_and_path_injection(self):
        for values in [dict(host='-oProxyCommand=evil'), dict(host='host;evil'),
                       dict(user='root\nrun'), dict(machine_id='{{ lookup("pipe", "evil") }}'),
                       dict(identity_file='/tmp/../key'), dict(identity_file='/tmp/a b'),
                       dict(schema=True), dict(ansible_connection='local'),
                       dict(ca_files=['/tmp/file.key']), dict(ca_files=['/a/ca.crt', '/b/ca.crt'])]:
            with self.subTest(values=values), self.assertRaises(ValueError):
                bootstrap.validate_inventory(self.inventory(**values))
        self.assertEqual(bootstrap.validate_inventory(self.inventory())['user'], 'operator')

    def test_backup_selection_rejects_root_disk_unknown_and_duplicate_uuids(self):
        disks = [{'name': 'nvme0n1', 'type': 'disk', 'children': [
            {'name': 'nvme0n1p1', 'uuid': 'root-uuid', 'fstype': 'ext4', 'mountpoints': ['/']},
            {'name': 'nvme0n1p2', 'uuid': 'same-disk', 'fstype': 'ext4', 'mountpoints': []}]},
            {'name': 'sda', 'type': 'disk', 'children': [
                {'name': 'sda2', 'uuid': 'backup-uuid', 'fstype': 'ext4', 'mountpoints': []}]}]
        report = {'disks': {'rc': 0, 'output': json.dumps({'blockdevices': disks})}}
        candidate = bootstrap.backup_candidate({'backup_uuid': 'backup-uuid'}, report)
        self.assertEqual(candidate['disk'], 'sda')
        self.assertFalse(candidate['contents_verified'])
        for uuid in ['root-uuid', 'same-disk', 'absent']:
            with self.subTest(uuid=uuid), self.assertRaises(ValueError):
                bootstrap.backup_candidate({'backup_uuid': uuid}, report)
        disks.append(disks[-1])
        report['disks']['output'] = json.dumps({'blockdevices': disks})
        with self.assertRaises(ValueError):
            bootstrap.backup_candidate({'backup_uuid': 'backup-uuid'}, report)

    def test_private_files_and_publication_do_not_follow_links_or_overwrite(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'input'
            bootstrap.write_new(path, b'original')
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            with self.assertRaises(FileExistsError):
                bootstrap.write_new(path, b'replacement')
            self.assertEqual(path.read_bytes(), b'original')
            path.chmod(0o644)
            with self.assertRaises(ValueError):
                bootstrap.private_file(path)
            link = Path(directory) / 'link'
            link.symlink_to(path)
            with self.assertRaises(ValueError):
                bootstrap.private_file(link)
            with self.assertRaises(FileExistsError):
                bootstrap.write_new(link, b'replacement')
            self.assertEqual(path.read_bytes(), b'original')
            with self.assertRaises(ValueError):
                bootstrap.write_new(ROOT / 'test-secret', b'secret')

    def test_identity_mismatch_fails_before_tools_or_playbooks(self):
        report = {'machine_id': 'b' * 32, 'user': 'operator', 'os': 'ubuntu', 'version': '26.04'}
        with patch.object(bootstrap, 'remote_python', return_value=json.dumps(report).encode()):
            with self.assertRaisesRegex(ValueError, 'machine-id'):
                bootstrap.inspect_host(self.inventory())
        report['machine_id'] = 'a' * 32
        report['user'] = 'wrong-user'
        with patch.object(bootstrap, 'remote_python', return_value=json.dumps(report).encode()):
            with self.assertRaisesRegex(ValueError, 'operator account'):
                bootstrap.inspect_host(self.inventory(), local=True)

    def test_ssh_preserves_trust_and_never_copies_the_key(self):
        with tempfile.TemporaryDirectory() as directory:
            key = Path(directory) / 'key'
            bootstrap.write_new(key, b'private-key-fixture')
            command = bootstrap.ssh(self.inventory(identity_file=str(key)))
            self.assertIn('StrictHostKeyChecking=yes', command)
            self.assertIn('BatchMode=yes', command)
            self.assertIn('IdentitiesOnly=yes', command)
            self.assertNotIn('private-key-fixture', ' '.join(command))

    def test_secret_host_failure_does_not_include_captured_output(self):
        failure = subprocess.CompletedProcess([], 1, b'SECRET-STDOUT', b'SECRET-STDERR')
        with patch.object(bootstrap.subprocess, 'run', return_value=failure):
            with self.assertRaises(ValueError) as result:
                bootstrap.remote_python({}, 'raise Exception()', local=True)
        self.assertNotIn('SECRET', str(result.exception))

    def test_ansible_environment_cannot_disable_host_key_checks_or_enable_debug(self):
        with patch.dict(os.environ, {'ANSIBLE_HOST_KEY_CHECKING': 'False', 'ANSIBLE_DEBUG': '1',
                                     'ANSIBLE_LOG_PATH': '/tmp/leak', 'PYTHONPATH': '/tmp/plugins'}):
            env = bootstrap.clean_ansible_env()
        self.assertNotIn('ANSIBLE_DEBUG', env)
        self.assertNotIn('ANSIBLE_LOG_PATH', env)
        self.assertNotIn('PYTHONPATH', env)
        self.assertEqual(env['ANSIBLE_CONFIG'], str(ROOT / 'infra/ansible/ansible.cfg'))

    def test_ca_installation_cannot_publish_private_key_material(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'ca.crt'
            certificate = '-----BEGIN CERTIFICATE-----\nQUJD\n-----END CERTIFICATE-----\n'
            path.write_text(certificate)
            self.assertEqual(bootstrap.public_ca_paths({'ca_files': [str(path)]}), [str(path)])
            path.write_text(certificate + '-----BEGIN PRIVATE KEY-----\nSECRET\n-----END PRIVATE KEY-----\n')
            with self.assertRaisesRegex(ValueError, 'never a private key'):
                bootstrap.public_ca_paths({'ca_files': [str(path)]})

    def test_host_lock_excludes_concurrent_apply_and_releases_after_exit(self):
        with tempfile.TemporaryDirectory() as directory:
            # The lock subprocess resolves HOME; isolate it from real platform state.
            with patch.dict(os.environ, {'HOME': directory}):
                with bootstrap.host_lock({}, True):
                    with self.assertRaises(ValueError):
                        with bootstrap.host_lock({}, True):
                            self.fail('concurrent lock acquired')
                with bootstrap.host_lock({}, True):
                    pass


@unittest.skipUnless(os.environ.get('BATTLEFIELD_TEST_ANSIBLE_BIN'), 'run test-server-bootstrap.sh for real Ansible checks')
class AnsibleInputTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='battlefield-input-test-')
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.binary = Path(os.environ['BATTLEFIELD_TEST_ANSIBLE_BIN'])
        self.password = self.directory / 'password'
        bootstrap.write_new(self.password, b'fixture-password-only\n')
        self.vault = self.directory / 'inputs.vault'
        self.secret = 'not-a-real-secret {{ lookup("pipe", "touch /tmp/ab125-must-not-execute") }}\n'
        self.bundle({'application_env': base64.b64encode(self.secret.encode()).decode()})
        account = pwd.getpwuid(os.getuid())
        variables = dict(bootstrap_config_dir=str(self.directory / 'config'),
                         bootstrap_state_dir=str(self.directory / 'state'),
                         bootstrap_file_owner=account.pw_name, bootstrap_file_group=account.pw_name,
                         bootstrap_operator=account.pw_name, bootstrap_machine_id='a' * 32,
                         bootstrap_vault_file=str(self.vault))
        play = [dict(name='Reconcile isolated test inputs', hosts='all', connection='local', gather_facts=False,
                     vars=variables, tasks=[
                         {'name':'Validate inputs', 'ansible.builtin.include_role':{'name':'inputs','tasks_from':'preflight'}},
                         {'name':'Install inputs', 'ansible.builtin.include_role':{'name':'inputs'}}])]
        self.playbook = self.directory / 'test.json'
        self.playbook.write_text(json.dumps(play))

    def bundle(self, values):
        self.vault.unlink(missing_ok=True)
        result = subprocess.run([str(self.binary / 'ansible-vault'), 'encrypt', '--output', str(self.vault),
                                 '--vault-password-file', str(self.password)],
                                input=json.dumps({'bootstrap_inputs': values}), text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.vault.chmod(0o600)

    def run_play(self, *flags):
        env = bootstrap.clean_ansible_env()
        env['ANSIBLE_ROLES_PATH'] = str(ROOT / 'infra/ansible/roles')
        result = subprocess.run([str(self.binary / 'ansible-playbook'), '-i', 'localhost,', str(self.playbook),
                                 '-e', 'ansible_python_interpreter=' + sys.executable,
                                 '--vault-password-file', str(self.password), *flags],
                                capture_output=True, text=True, env=env)
        self.assertNotIn(self.secret.strip(), result.stdout + result.stderr)
        self.assertNotIn(base64.b64encode(self.secret.encode()).decode(), result.stdout + result.stderr)
        return result

    def test_apply_repeat_check_drift_and_changed_secret(self):
        result = self.run_play('--diff')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        credential = self.directory / 'config/inputs/application_env'
        self.assertEqual(credential.read_text(), self.secret)
        self.assertEqual(credential.stat().st_mode & 0o777, 0o600)
        self.assertFalse(Path('/tmp/ab125-must-not-execute').exists())
        result = self.run_play('--diff')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertRegex(result.stdout, r'changed=0\s')
        before = credential.stat().st_mtime_ns
        result = self.run_play('--check', '--diff')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(credential.stat().st_mtime_ns, before)
        credential.chmod(0o644)
        result = self.run_play()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(credential.stat().st_mode & 0o777, 0o600)
        self.bundle({'application_env': base64.b64encode(b'changed-secret').decode()})
        result = self.run_play('--diff')
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn('changed-secret', result.stdout + result.stderr)
        self.assertEqual(credential.read_text(), self.secret)

    def test_seal_imports_controller_and_host_files_and_refuses_overwrite(self):
        local = self.directory / 'local-secret'
        remote = self.directory / 'remote-secret'
        bootstrap.write_new(local, b'controller-secret')
        bootstrap.write_new(remote, b'host-secret')
        manifest = self.directory / 'manifest.json'
        bootstrap.write_new(manifest, json.dumps({
            'controller_input': {'source':'controller','path':str(local)},
            'host_input': {'source':'host','path':str(remote)},
        }).encode())
        output = self.directory / 'imported.vault'
        args = argparse.Namespace(files=str(manifest), vault=str(output), local=True,
                                  vault_password_file=str(self.password), inventory=self.directory / 'inventory.json')
        with patch.object(bootstrap, 'tools_bin', return_value=self.binary), redirect_stdout(io.StringIO()):
            bootstrap.seal(args, {})
            saved = output.read_bytes()
            with self.assertRaisesRegex(ValueError, 'already exists'):
                bootstrap.seal(args, {})
        self.assertTrue(saved.startswith(b'$ANSIBLE_VAULT;'))
        self.assertEqual(output.read_bytes(), saved)
        self.assertNotIn(b'host-secret', saved)
        result = subprocess.run([str(self.binary / 'ansible-vault'), 'view', str(output),
                                 '--vault-password-file', str(self.password)], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0)
        values = json.loads(result.stdout)['bootstrap_inputs']
        self.assertEqual(base64.b64decode(values['controller_input']), b'controller-secret')
        self.assertEqual(base64.b64decode(values['host_input']), b'host-secret')
        self.assertEqual(output.stat().st_mode & 0o777, 0o600)

    def test_fresh_check_does_not_create_inputs(self):
        result = self.run_play('--check', '--diff')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse((self.directory / 'config').exists())
        self.assertFalse((self.directory / 'state').exists())

    def test_symlink_and_missing_credential_fail_without_mutation(self):
        result = self.run_play()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        credential = self.directory / 'config/inputs/application_env'
        original = self.directory / 'original'
        credential.rename(original)
        credential.symlink_to(original)
        result = self.run_play()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(original.read_text(), self.secret)
        credential.unlink()
        original.rename(credential)
        self.bundle({'different_input': base64.b64encode(b'different').decode()})
        result = self.run_play()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((credential.parent / 'different_input').exists())
        self.assertEqual(credential.read_text(), self.secret)


if __name__ == '__main__':
    unittest.main()
