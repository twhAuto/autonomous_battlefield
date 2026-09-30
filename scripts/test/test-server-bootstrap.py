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
import pty
import select
import signal
import time
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
        self.assertTrue(candidate['usable'])
        self.assertIsNone(bootstrap.backup_candidate({}, report))
        self.assertFalse(candidate['contents_verified'])
        for uuid in ['root-uuid', 'same-disk', 'absent']:
            with self.subTest(uuid=uuid), self.assertRaises(ValueError):
                bootstrap.backup_candidate({'backup_uuid': uuid}, report)
        disks.append(disks[-1])
        report['disks']['output'] = json.dumps({'blockdevices': disks})
        with self.assertRaises(ValueError):
            bootstrap.backup_candidate({'backup_uuid': 'backup-uuid'}, report)

    def test_jenkins_temporary_paths_are_supported_without_shell_syntax(self):
        path = '/tmp/build@tmp/secretFiles/credential'
        self.assertEqual(bootstrap.safe_path(path), Path(path))
        for invalid in ['/tmp/build@tmp/../secret', '/tmp/build@tmp/$(id)',
                        '/tmp/build@tmp/`id`', '/tmp/build@tmp/{{secret}}']:
            with self.subTest(path=invalid), self.assertRaises(ValueError):
                bootstrap.safe_path(invalid)

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
        report = {'machine_id': 'b' * 32, 'user': 'operator', 'os': 'ubuntu', 'version': '26.04', 'architecture': 'x86_64'}
        with patch.object(bootstrap, 'remote_python', return_value=json.dumps(report).encode()):
            with self.assertRaisesRegex(ValueError, 'machine-id'):
                bootstrap.inspect_host(self.inventory())
        report['machine_id'] = 'a' * 32
        report['user'] = 'wrong-user'
        with patch.object(bootstrap, 'remote_python', return_value=json.dumps(report).encode()):
            with self.assertRaisesRegex(ValueError, 'operator account'):
                bootstrap.inspect_host(self.inventory(), local=True)

    def test_target_architecture_is_checked_during_registration_and_inspection(self):
        report = {'machine_id': 'a' * 32, 'user': 'operator', 'os': 'ubuntu',
                  'version': '26.04', 'architecture': 'aarch64'}
        with self.assertRaisesRegex(ValueError, 'x86_64'):
            bootstrap.validate_target(self.inventory(), report)
        with patch.object(bootstrap, 'remote_python', return_value=json.dumps(report).encode()), \
                self.assertRaisesRegex(ValueError, 'x86_64'):
            bootstrap.inspect_host(self.inventory())
        report['architecture'] = 'x86_64'
        bootstrap.validate_target(self.inventory(), report)

    def test_unattended_vault_prompt_fails_before_import_or_install(self):
        args = argparse.Namespace(vault_password_file=None, vault='/tmp/unused.vault')
        with patch.object(bootstrap.sys.stdin, 'isatty', return_value=False), \
                patch.object(bootstrap, 'private_file') as private, \
                patch.object(bootstrap, 'tools_bin') as tools:
            with self.assertRaisesRegex(ValueError, 'Vault prompt'):
                bootstrap.seal(args, {})
            with self.assertRaisesRegex(ValueError, 'Vault prompt'):
                bootstrap.playbook(args, {}, {})
        private.assert_not_called()
        tools.assert_not_called()

    def test_missing_backup_disk_is_reported_without_blocking_foundation_inspection(self):
        report = {'machine_id': 'a' * 32, 'user': 'operator', 'os': 'ubuntu', 'version': '26.04', 'architecture': 'x86_64',
                  'disks': {'available': False}}
        with patch.object(bootstrap, 'remote_python', return_value=json.dumps(report).encode()):
            result = bootstrap.inspect_host(self.inventory(backup_uuid='12345678'))
        self.assertFalse(result['backup_candidate']['usable'])
        self.assertIn('lsblk', result['backup_candidate']['error'])

    def test_registration_rejects_unsupported_target_and_rolls_back_partial_publication(self):
        with self.assertRaisesRegex(ValueError, 'Ubuntu'):
            bootstrap.validate_target(self.inventory(), {'user': 'operator', 'os': 'debian', 'version': '13'})
        with tempfile.TemporaryDirectory() as directory:
            inventory = Path(directory) / 'inventory.json'
            manifest = Path(directory) / 'inputs.json'
            original = bootstrap.write_new
            def fail_manifest(path, content):
                if path == manifest:
                    raise PermissionError('fixture failure')
                return original(path, content)
            with patch.object(bootstrap, 'write_new', side_effect=fail_manifest):
                with self.assertRaises(PermissionError):
                    bootstrap.publish_registration(inventory, self.inventory(), manifest, {})
            self.assertFalse(inventory.exists())
            self.assertFalse(manifest.exists())
            # Serialization, path validation and interruption failures must also undo this registration.
            for failure in (ValueError('fixture path rejection'), KeyboardInterrupt()):
                def fail_manifest_again(path, content):
                    if path == manifest:
                        raise failure
                    return original(path, content)
                with self.subTest(failure=type(failure).__name__), \
                        patch.object(bootstrap, 'write_new', side_effect=fail_manifest_again), \
                        self.assertRaises(type(failure)):
                    bootstrap.publish_registration(inventory, self.inventory(), manifest, {})
                self.assertFalse(inventory.exists())
            with self.assertRaises(TypeError):
                bootstrap.publish_registration(inventory, self.inventory(), manifest, {'invalid': object()})
            self.assertFalse(inventory.exists())
            bootstrap.publish_registration(inventory, self.inventory(), manifest, {})
            self.assertTrue(inventory.exists())
            self.assertTrue(manifest.exists())

    def test_tools_does_not_require_a_valid_machine_inventory_path(self):
        with patch.dict(os.environ, {'XDG_CONFIG_HOME': '/tmp/config with spaces'}), \
                patch.object(sys, 'argv', ['server.py', 'tools']), \
                patch.object(bootstrap, 'tools_bin', return_value=Path('/tmp/tools')) as tools, \
                redirect_stdout(io.StringIO()) as output:
            bootstrap.main()
        tools.assert_called_once()
        self.assertEqual(output.getvalue().strip(), '/tmp/tools')
        with patch.dict(os.environ, {'XDG_CONFIG_HOME': '/tmp/config with spaces'}), \
                patch.object(sys, 'argv', ['server.py', 'inspect']), \
                patch.object(bootstrap, 'load_inventory') as load, \
                self.assertRaisesRegex(ValueError, 'Paths must'):
            bootstrap.main()
        load.assert_not_called()

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

    def test_real_virtualization_guard_accepts_standard_and_rejects_hwe_profiles(self):
        guard = ROOT / 'infra/ansible/roles/foundation/tasks/virtualization-preflight.yml'
        profiles = [({'qemu-system-x86': [], 'libvirt-daemon-system': [], 'linux-generic-hwe-24.04': []}, True),
                    ({'qemu-system-x86-hwe': []}, False), ({'libvirt-daemon-system-hwe': []}, False),
                    ({'qemu-system-x86-hwe-26.04': []}, False)]
        for packages, accepted in profiles:
            with self.subTest(packages=packages):
                self.playbook.write_text(json.dumps([{'hosts': 'all', 'gather_facts': False,
                    'vars': {'ansible_facts': {'packages': packages}},
                    'tasks': [{'ansible.builtin.import_tasks': str(guard)}]}]))
                result = self.run_play()
                self.assertEqual(result.returncode == 0, accepted, result.stdout + result.stderr)
                self.assertRegex(result.stdout, r'changed=0\s')
                if not accepted:
                    self.assertIn('Existing HWE virtualization stack detected', result.stdout)

    def test_interruption_callback_holds_real_ansible_until_wrapper_is_interrupted(self):
        guest_spec = importlib.util.spec_from_file_location('guest_fixture', ROOT / 'scripts/test/rehearse-server-guest.py')
        guest = importlib.util.module_from_spec(guest_spec)
        guest_spec.loader.exec_module(guest)
        marker, next_task = self.directory / 'marker', self.directory / 'must-not-exist'
        self.playbook.write_text(json.dumps([{'hosts': 'all', 'connection': 'local', 'gather_facts': False,
            'tasks': [{'name': 'Record machine ownership before creating managed children',
                       'ansible.builtin.copy': {'content': 'owned', 'dest': str(marker)}},
                      {'name': 'Must remain unreachable before interruption',
                       'ansible.builtin.copy': {'content': 'unexpected', 'dest': str(next_task)}}]}]))
        code = """import importlib.util,signal,sys
# Background CI launchers may preserve SIGINT=SIG_IGN across exec.
signal.signal(signal.SIGINT, signal.default_int_handler)
spec=importlib.util.spec_from_file_location('bootstrap',sys.argv[1])
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
class Lock:
    def poll(self): return None
m.run_playbook(sys.argv[2:],Lock())
"""
        # Reproduce an ignored SIGINT inherited from a background Jenkins launcher.
        launcher = ('import os,signal,sys; signal.signal(signal.SIGINT,signal.SIG_IGN); '
                    'os.execv(sys.executable,[sys.executable,*sys.argv[1:]])')
        command = [sys.executable, '-c', launcher, '-c', code, str(ROOT / 'scripts/infra/server.py'),
                   str(self.binary / 'ansible-playbook'), '-i', 'localhost,', str(self.playbook),
                   '-e', 'ansible_python_interpreter=' + sys.executable]
        with guest.interruption_callback(self.directory / 'callback_plugins'), \
                subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, start_new_session=True) as process:
            output = b''
            try:
                deadline = time.monotonic() + 30
                while guest.INTERRUPT_READY.encode() not in output and time.monotonic() < deadline:
                    ready, _, _ = select.select([process.stdout], [], [], 0.2)
                    if ready:
                        block = os.read(process.stdout.fileno(), 65536)
                        if not block:
                            break
                        output += block
                self.assertIn(guest.INTERRUPT_READY.encode(), output, output.decode(errors='replace'))
                # Deliberately delay the signal: the following task must remain blocked.
                time.sleep(0.3)
                self.assertIsNone(process.poll())
                self.assertEqual(marker.read_text(), 'owned')
                self.assertFalse(next_task.exists())
            finally:
                if process.poll() is None:
                    process.send_signal(signal.SIGINT)
                process.communicate(timeout=20)
            self.assertEqual(process.returncode, -signal.SIGINT)
            self.assertFalse(next_task.exists())
        self.assertFalse((self.directory / 'callback_plugins/battlefield_rehearsal_interrupt.py').exists())

    def test_apply_repeat_check_drift_and_changed_secret(self):
        result = self.run_play('--diff')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        credential = self.directory / 'config/inputs/application_env'
        self.assertEqual(credential.read_text(), self.secret)
        recovered = (self.directory / 'state/bootstrap-inputs.vault').read_bytes()
        self.assertEqual(recovered, self.vault.read_bytes())
        self.assertTrue(recovered.startswith(b'$ANSIBLE_VAULT;'))
        self.assertNotIn(self.secret.encode(), recovered)
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
        self.assertIn('Conflicting imported inputs: application_env', result.stdout)
        self.assertEqual(credential.read_text(), self.secret)

    def test_actual_ansible_vault_and_become_prompts_do_not_echo(self):
        playbook = self.directory / 'prompt-test.json'
        playbook.write_text(json.dumps([{'hosts': 'all', 'gather_facts': False, 'tasks': [
            {'name': 'Load synthetic encrypted inputs', 'ansible.builtin.include_vars': {'file': str(self.vault)}, 'no_log': True},
            {'name': 'Complete prompt check', 'ansible.builtin.debug': {'msg': 'PROMPTS_OK'}},
        ]}]))
        command = [str(self.binary / 'ansible-playbook'), '-i', 'localhost,', '-c', 'local',
                   str(playbook), '--ask-vault-pass', '--ask-become-pass']
        child, fd = pty.fork()
        if child == 0:
            class Lock:
                @staticmethod
                def poll(): return None
            try:
                bootstrap.run_playbook(command, Lock())
                os._exit(0)
            except BaseException:
                os._exit(1)
        output = b''
        prompts = [b'BECOME password:', b'Vault password:']
        answered = set()
        deadline = time.monotonic() + 30
        try:
            while time.monotonic() < deadline:
                ready, _, _ = select.select([fd], [], [], 0.2)
                if ready:
                    try:
                        block = os.read(fd, 65536)
                    except OSError:
                        break
                    if not block:
                        break
                    output += block
                    for prompt in prompts:
                        if prompt in output and prompt not in answered:
                            os.write(fd, b'fixture-password-only\n')
                            answered.add(prompt)
                # Drain through EOF even if the child has exited; its final output may still be queued.
            if len(answered) != 2 or b'PROMPTS_OK' not in output:
                self.fail('Actual Ansible prompts did not complete: ' + output.decode(errors='replace'))
            self.assertNotIn(b'fixture-password-only', output)
        finally:
            os.close(fd)
            try:
                finished, _ = os.waitpid(child, os.WNOHANG)
                if not finished:
                    os.kill(child, signal.SIGTERM)
                    os.waitpid(child, 0)
            except ChildProcessError:
                pass

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
        self.assertIn('Missing imported inputs: application_env', result.stdout)
        self.assertFalse((credential.parent / 'different_input').exists())
        self.assertEqual(credential.read_text(), self.secret)


if __name__ == '__main__':
    unittest.main()
