#!/usr/bin/env python3
"""CLI worker job: secret sourcing, repair planning, receipts, guest scripts and redaction."""
import contextlib
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[2]
SECRET = 'a' * 64


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


worker = load('cli_worker', ROOT / 'scripts/infra/cli-worker.py')


def healthy(**overrides):
    checks = {'domain': 'running', 'host_units': {u: 'active' for u in worker.HOST_UNITS},
              'boot_start_enabled': True, 'bridge_watchdog_active': True, 'watchdog_enabled': True, 'stale_host_files': [],
              'bridge_reachable': True, 'gateway_accepts_worker_secret': True, 'automation_key': True,
              'guest': {'reachable': True, 'identity_matches': True, 'secret_matches': True, 'marker_matches': True,
                        'worker_active': True, 'worker_enabled': True, 'worker_health': 204,
                        'config_registered': True, 'config_current': True, 'config_pending': False}}
    if 'guest' in overrides:
        checks['guest'] = overrides.pop('guest')
    for key, value in overrides.items():
        if checks['guest'] is not None and key in checks['guest'] and key not in checks:
            checks['guest'][key] = value
        else:
            checks[key] = value
    return checks


class SecretTests(unittest.TestCase):
    def env(self, content):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        path = Path(directory.name) / '.env'
        path.write_text(content)
        return path

    def test_reads_a_valid_production_secret(self):
        self.assertEqual(worker.production_worker_secret(self.env('X=1\nBATTLEFIELD_AGENT_SECRET=' + SECRET + '\n')), SECRET)

    def test_rejects_missing_short_placeholder_retired_and_duplicate_values(self):
        for content in ('X=1\n', 'BATTLEFIELD_AGENT_SECRET=short\n',
                        'BATTLEFIELD_AGENT_SECRET=replace-with-at-least-32-random-characters\n',
                        'BATTLEFIELD_AGENT_SECRET=local-development-capability-secret-change-me\n',
                        f'BATTLEFIELD_AGENT_SECRET={SECRET}\nBATTLEFIELD_AGENT_SECRET={SECRET}\n'):
            with self.subTest(content=content[:30]), self.assertRaises(ValueError):
                worker.production_worker_secret(self.env(content))

    def test_secret_substitution_matches_the_bootstrap_filter(self):
        environment = 'BATTLEFIELD_AGENT_SECRET=old-old-old-old-old-old-old-old-old\nBATTLEFIELD_ENV=production\nBATTLEFIELD_CLI_ISOLATION_VERIFIED=false\n'
        updated = worker.with_worker_secret(environment, SECRET)
        self.assertEqual(updated, environment.replace('old-old-old-old-old-old-old-old-old', SECRET))
        with self.assertRaises(ValueError):
            worker.with_worker_secret('BATTLEFIELD_ENV=production\n', SECRET)
        try:
            plugin = load('cli_filter', ROOT / 'infra/ansible/filter_plugins/cli_bootstrap.py')
        except ModuleNotFoundError:
            self.skipTest('Ansible is not installed; the bootstrap filter is covered by test-server-bootstrap.sh')
        self.assertEqual(plugin.with_worker_secret(environment, SECRET), updated)


class PlanningTests(unittest.TestCase):
    def test_healthy_worker_needs_no_repairs_or_reports_no_problems(self):
        self.assertEqual(worker.Worker.repairs(healthy()), [])
        self.assertEqual(worker.Worker.problems(healthy()), [])

    def test_each_failure_maps_to_its_repair(self):
        cases = [
            (healthy(domain='shut off', guest=None), ['start-vm']),
            (healthy(bridge_reachable=False), ['restart-bridge']),
            (healthy(secret_matches=False), ['sync-worker-secret']),
            (healthy(worker_health=0), ['restart-worker']),
            (healthy(worker_enabled=False), ['enable-worker']),
            (healthy(boot_start_enabled=False), ['install-host-units']),
            (healthy(watchdog_enabled=False), ['install-host-units']),
            # Drift beyond the secret, or another machine, is reported but never repaired.
            (healthy(marker_matches=False, secret_matches=False), []),
            (healthy(identity_matches=False, secret_matches=False), []),
            (healthy(config_current=False), ['sync-worker-config']),
            (healthy(config_registered=False, config_current=False), []),
            # A drifted worker environment blocks every guest repair, including config.
            (healthy(marker_matches=False, config_current=False, worker_health=0), []),
            # An interrupted config sync is planned again so it can finish.
            (healthy(config_registered=False, config_pending=True, config_current=False), ['sync-worker-config']),
            (healthy(stale_host_files=['/etc/systemd/system/battlefield-cli-vm-start.service']), ['install-host-units']),
            (healthy(guest={'reachable': False}), ['reach-guest']),
        ]
        for checks, expected in cases:
            with self.subTest(expected=expected):
                self.assertEqual(worker.Worker.repairs(checks), expected)

    def test_problems_describe_todays_outage(self):
        checks = healthy(domain='shut off', guest=None, bridge_reachable=False, boot_start_enabled=False)
        problems = worker.Worker.problems(checks)
        self.assertIn('CLI VM is shut off', problems)
        self.assertIn('MCP bridge does not reach the AI gateway', problems)
        self.assertIn('boot start or bridge watchdog is not installed', problems)
        mismatch = worker.Worker.problems(healthy(secret_matches=False, gateway_accepts_worker_secret=False))
        self.assertIn('CLI worker secret differs from production', mismatch)
        self.assertIn('AI gateway rejects the production worker secret', mismatch)
        drift = worker.Worker.problems(healthy(marker_matches=False, worker_enabled=False))
        self.assertIn('CLI worker environment differs from its registered content; inspect it, it is not repaired automatically', drift)
        self.assertIn('CLI worker service is not enabled for reboot', drift)
        self.assertIn('boot start or bridge watchdog is not installed', worker.Worker.problems(healthy(watchdog_enabled=False)))

    def test_inspect_treats_any_2xx_gateway_readiness_as_reachable(self):
        instance = object.__new__(worker.Worker)
        instance.port, instance.config = 28084, {'guest': {}, 'host': {}}
        instance.domain_state = lambda: 'shut off'
        instance.unit_state = lambda unit, query='is-active': 'enabled' if query == 'is-enabled' else 'active'
        instance.host_files_current = lambda: []
        for status, reachable in ((204, True), (200, True), (503, False), (0, False)):
            instance.gateway = lambda path, secret=None, status=status: status if path == '/readyz' else 401
            with self.subTest(status=status), mock.patch.object(worker, 'AUTOMATION_KEY', Path('/nonexistent')):
                self.assertIs(instance.inspect(SECRET)['bridge_reachable'], reachable)

    def test_host_files_render_the_bridge_port_and_boot_retry(self):
        files = worker.host_files(28084)
        self.assertIn('mcp-bridge-watchdog 28084', files['/etc/systemd/system/battlefield-mcp-bridge-watchdog.service'][1])
        self.assertIn('virsh -c qemu:///system start battlefield-agent', files[worker.LIBEXEC + '/start-cli-vm'][1])
        self.assertIn('battlefield-agent-firewall.service', files['/etc/systemd/system/battlefield-cli-vm-start.service'][1])
        self.assertEqual({mode for mode, _ in files.values()}, {'0755', '0644'})


CONFIG = """ai:
  environment: local
  agentHealthAddress: 127.0.0.1:8083
  mcpUrl: http://192.168.234.1:18084/mcp
  profiles:
    - id: claude-subscription-local
      provider: claude-cli
      runtime: cli
      model: sonnet
      enabled: true
    - id: gemini-subscription-local
      provider: antigravity-cli
      runtime: cli
      model: gemini-3.7-flash-medium
      enabled: true
"""
SETTINGS = {'agentConcurrency': 4, 'models': {'claude-subscription-local': 'sonnet', 'gemini-subscription-local': 'gemini-3.8-flash-medium'}}


class ConfigTests(unittest.TestCase):
    def test_managed_fields_change_and_nothing_else(self):
        import yaml
        updated = yaml.safe_load(worker.desired_config(CONFIG, SETTINGS))
        original = yaml.safe_load(CONFIG)
        self.assertEqual(updated['ai'].pop('agentConcurrency'), 4)
        self.assertEqual(updated['ai']['profiles'][1].pop('model'), 'gemini-3.8-flash-medium')
        original['ai']['profiles'][1].pop('model')
        self.assertEqual(updated, original)

    def test_matching_settings_leave_the_file_byte_identical(self):
        current = worker.desired_config(CONFIG, SETTINGS)
        self.assertIs(worker.desired_config(current, SETTINGS), current)

    def test_null_concurrency_removes_the_setting_for_older_workers(self):
        import yaml
        older = dict(SETTINGS, agentConcurrency=None)
        with_concurrency = worker.desired_config(CONFIG, SETTINGS)
        removed = worker.desired_config(with_concurrency, older)
        self.assertNotIn('agentConcurrency', yaml.safe_load(removed)['ai'])
        self.assertIs(worker.desired_config(removed, older), removed)
        self.assertNotIn('agentConcurrency', yaml.safe_load(worker.desired_config(CONFIG, older))['ai'])

    def test_unknown_profiles_and_invalid_settings_are_rejected(self):
        with self.assertRaisesRegex(ValueError, 'does not have'):
            worker.desired_config(CONFIG, {'agentConcurrency': 2, 'models': {'codex-subscription-local': 'gpt'}})
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        for bad in ({'agentConcurrency': 5, 'models': {'a': 'b'}}, {'agentConcurrency': 2, 'models': {}},
                    {'agentConcurrency': True, 'models': {'a': 'b'}}, {'agentConcurrency': 2, 'models': {'a': 'bad model'}},
                    {'agentConcurrency': 0, 'models': {'a': 'b'}}, {'models': {'a': 'b'}}):
            path = Path(directory.name) / 'settings.json'
            path.write_text(json.dumps(bad))
            with self.subTest(bad=bad), self.assertRaises(ValueError):
                worker.managed_settings(path)

    def test_managed_settings_match_the_application_profile_catalog(self):
        import yaml
        settings = worker.managed_settings()
        catalog = {}
        for document in yaml.safe_load_all((ROOT / 'infra/deploy/base/infrastructure/configmap.yaml').read_text()):
            if document and document.get('kind') == 'ConfigMap':
                for value in document['data'].values():
                    if 'profiles' in value:
                        catalog = {p['id']: p['model'] for p in yaml.safe_load(value)['ai']['profiles'] if p['runtime'] == 'cli'}
        self.assertEqual(settings['models'], catalog)
        template = yaml.safe_load((ROOT / 'infra/deploy/agent-vm/config.yaml').read_text())['ai']
        self.assertEqual({p['id']: p['model'] for p in template['profiles']}, settings['models'])
        if settings['agentConcurrency'] is not None:
            self.assertEqual(template['agentConcurrency'], settings['agentConcurrency'])

    def test_sync_config_updates_guest_then_the_registered_copy(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        registered = Path(directory.name) / 'config.yaml'
        registered.write_text(CONFIG)
        registered.chmod(0o600)
        instance = object.__new__(worker.Worker)
        instance.registered_config = registered
        sent = []
        instance.guest_python = lambda code, payload: sent.append(payload) or {'changed': True}
        with mock.patch.object(worker, 'managed_settings', lambda: SETTINGS):
            instance.sync_config()
        self.assertEqual(sent[0]['expected_sha256'], hashlib.sha256(CONFIG.encode()).hexdigest())
        self.assertEqual(registered.read_text(), sent[0]['content'])
        self.assertIn('gemini-3.8-flash-medium', registered.read_text())
        self.assertEqual(registered.stat().st_mode & 0o777, 0o600)


class ApplyTests(unittest.TestCase):
    def worker_with(self, guest):
        instance = object.__new__(worker.Worker)
        calls = []
        instance.sudo = lambda command: calls.append(('sudo', command[-1]))
        instance.gateway = lambda path, secret=None: 204
        instance.wait_for = lambda condition, seconds, message: None
        instance.inspect = lambda secret: {'guest': guest}
        instance.guest_python = lambda code, payload: calls.append(('guest', 'config' if code is worker.GUEST_CONFIG_SYNC else 'secret')) or {}
        instance.ssh = lambda key, remote, data=None, timeout=120: calls.append(('ssh', remote)) or mock.Mock(returncode=0)
        instance.sync_config = lambda: calls.append(('guest', 'config'))
        instance.private = lambda path: Path(path)
        return instance, calls

    def test_environment_drift_keeps_host_repairs_away_from_the_guest(self):
        guest = healthy(marker_matches=False, worker_health=0)['guest']
        instance, calls = self.worker_with(guest)
        instance.apply(['restart-bridge'], SECRET)
        self.assertEqual(calls, [('sudo', 'battlefield-mcp-bridge.service')])
        with self.assertRaisesRegex(ValueError, 'differs from its registered content'):
            instance.apply(['restart-bridge', 'sync-worker-config'], SECRET)

    def test_apply_runs_exactly_the_planned_guest_repairs(self):
        guest = healthy(worker_health=0, worker_enabled=False, config_current=False)['guest']
        instance, calls = self.worker_with(guest)
        instance.apply(['enable-worker'], SECRET)
        self.assertEqual(calls, [('ssh', 'sudo -n systemctl enable battlefield-worker')])
        calls.clear()
        instance.apply(['sync-worker-config'], SECRET)
        self.assertEqual(calls, [('guest', 'config'), ('ssh', 'sudo -n systemctl restart battlefield-worker')])


class ExecuteTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.directory = Path(directory.name)
        env = self.directory / '.env'
        env.write_text('BATTLEFIELD_AGENT_SECRET=' + SECRET + '\n')
        patches = [mock.patch.object(worker, 'PRODUCTION_ENV', env),
                   mock.patch.object(worker, 'production_worker_secret', lambda path=env: SECRET),
                   mock.patch.object(worker, 'trusted_revision', lambda revision: None)]
        for patch in patches:
            patch.start()
            self.addCleanup(patch.stop)
        self.checks = healthy()
        self.applied = []
        test = self

        class Fake:
            def __init__(self, args):
                self.args = args

            def inspect(self, secret):
                return test.checks

            repairs = staticmethod(worker.Worker.repairs)
            problems = staticmethod(worker.Worker.problems)
            private = staticmethod(lambda path: Path(path))

            def apply(self, repairs, secret):
                test.applied.append(repairs)
                test.checks = healthy()

        patch = mock.patch.object(worker, 'Worker', Fake)
        patch.start()
        self.addCleanup(patch.stop)
        lock = mock.MagicMock()
        lock.acquire.return_value = contextlib.nullcontext(3)
        original = worker.load
        patch = mock.patch.object(worker, 'load', lambda name, path: lock if name == 'operation_lock' else original(name, path))
        patch.start()
        self.addCleanup(patch.stop)

    def args(self, action, **extra):
        values = dict(action=action, report=str(self.directory / (action + '.json')), revision='f' * 40,
                      receipt=str(self.directory / 'receipt.json'), become_password_file=None)
        values.update(extra)
        return mock.Mock(**values)

    def test_verify_reports_problems_with_exit_3_and_never_the_secret(self):
        self.checks = healthy(domain='shut off', guest=None)
        with contextlib.redirect_stderr(open(os.devnull, 'w')):
            self.assertEqual(worker.execute(self.args('verify')), 3)
        report = (self.directory / 'verify.json').read_text()
        self.assertIn('CLI VM is shut off', report)
        self.assertNotIn(SECRET, report)
        self.checks = healthy()
        self.assertEqual(worker.execute(self.args('verify')), 0)

    def test_apply_consumes_a_matching_receipt_then_verifies(self):
        self.checks = healthy(secret_matches=False)
        worker.execute(self.args('plan'))
        receipt = json.loads((self.directory / 'receipt.json').read_text())
        self.assertEqual(receipt['repairs'], ['sync-worker-secret'])
        worker.execute(self.args('apply'))
        self.assertEqual(self.applied, [['sync-worker-secret']])
        self.assertFalse((self.directory / 'receipt.json').exists())
        self.assertEqual(json.loads((self.directory / 'apply.json').read_text())['problems'], [])

    def test_apply_refuses_drift_expiry_and_another_revision(self):
        self.checks = healthy(secret_matches=False)
        worker.execute(self.args('plan'))
        self.checks = healthy(domain='shut off', guest=None)
        with self.assertRaisesRegex(ValueError, 'changed since the reviewed plan'):
            worker.execute(self.args('apply'))
        self.checks = healthy(secret_matches=False)
        worker.execute(self.args('plan'))
        with self.assertRaisesRegex(ValueError, 'revision changed or the plan expired'):
            worker.execute(self.args('apply', revision='e' * 40))
        receipt = json.loads((self.directory / 'receipt.json').read_text())
        receipt['created'] = time.time() - worker.RECEIPT_SECONDS - 5
        (self.directory / 'receipt.json').write_text(json.dumps(receipt))
        with self.assertRaisesRegex(ValueError, 'expired'):
            worker.execute(self.args('apply'))
        self.assertEqual(self.applied, [])

    def test_failed_apply_still_writes_its_report(self):
        self.checks = healthy(secret_matches=False)
        worker.execute(self.args('plan'))
        def fail(repairs, secret):
            raise ValueError('The CLI worker did not report healthy after the repair')
        worker.Worker.apply = lambda self, repairs, secret: fail(repairs, secret)
        with self.assertRaisesRegex(ValueError, 'did not report healthy'):
            worker.execute(self.args('apply'))
        report = json.loads((self.directory / 'apply.json').read_text())
        self.assertEqual(report['error'], 'The CLI worker did not report healthy after the repair')
        self.assertNotIn(SECRET, json.dumps(report))


class HostTests(unittest.TestCase):
    def test_sudo_password_goes_to_stdin_not_argv(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        password = Path(directory.name) / 'sudo'
        password.write_text('host-sudo-password\n')
        password.chmod(0o600)
        instance = object.__new__(worker.Worker)
        instance.args = mock.Mock(become_password_file=str(password))
        calls = []
        instance.run = lambda command, data=None, timeout=60, check=False: calls.append((command, data)) or mock.Mock(returncode=0)
        instance.sudo(['systemctl', 'daemon-reload'])
        command, data = calls[0]
        self.assertEqual(command, ['sudo', '-S', '-p', '', 'systemctl', 'daemon-reload'])
        self.assertEqual(data, b'host-sudo-password\n')
        self.assertNotIn('host-sudo-password', ' '.join(command))


class GuestScriptTests(unittest.TestCase):
    """Run the real guest scripts against temporary files instead of /etc."""

    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        self.env = self.root / 'worker.env'
        self.marker = self.root / 'guest.json'
        self.env.write_text('BATTLEFIELD_AGENT_SECRET=old-old-old-old-old-old-old-old-old\nBATTLEFIELD_ENV=production\nBATTLEFIELD_CLI_ISOLATION_VERIFIED=true\n')
        self.marker.write_text(json.dumps({'schema': 1, 'identity': {}, 'files': {
            '/etc/battlefield-agent/worker.env': hashlib.sha256(self.env.read_bytes()).hexdigest()}}))
        (self.root / 'machine-id').write_text('0' * 32 + '\n')
        (self.root / 'product_uuid').write_text('ABCDEF\n')
        (self.root / 'config.yaml').write_text(CONFIG)
        bin_dir = self.root / 'bin'
        bin_dir.mkdir()
        systemctl = bin_dir / 'systemctl'
        systemctl.write_text('#!/bin/sh\necho "$@" >> "$CALLS"\n[ "$1" = is-active ] && echo active\n[ "$1" = is-enabled ] && echo enabled\nexit 0\n')
        systemctl.chmod(0o755)
        self.calls = self.root / 'calls'
        self.process_env = dict(os.environ, PATH=str(bin_dir) + os.pathsep + os.environ['PATH'], CALLS=str(self.calls))

    def script(self, code):
        code = (code.replace("pathlib.Path('/etc/battlefield-agent/worker.env')", f"pathlib.Path({str(self.env)!r})")
                .replace("pathlib.Path('/etc/battlefield-cli-bootstrap/guest.json')", f"pathlib.Path({str(self.marker)!r})")
                .replace("pathlib.Path('/etc/machine-id')", f"pathlib.Path({str(self.root / 'machine-id')!r})")
                .replace("pathlib.Path('/sys/class/dmi/id/product_uuid')", f"pathlib.Path({str(self.root / 'product_uuid')!r})")
                .replace("pathlib.Path('/etc/battlefield-agent/config.yaml')", f"pathlib.Path({str(self.root / 'config.yaml')!r})")
                .replace('str(path)', "'/etc/battlefield-agent/worker.env'"))
        return code

    def run_script(self, code, payload):
        result = subprocess.run([sys.executable, '-c', self.script(code)], input=json.dumps(payload).encode(),
                                capture_output=True, env=self.process_env, timeout=30)
        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(result.stdout)

    def test_sync_replaces_only_the_secret_updates_marker_and_restarts(self):
        first = self.run_script(worker.GUEST_SYNC, {'secret': SECRET})
        self.assertTrue(first['changed'])
        content = self.env.read_text()
        self.assertEqual(content, f'BATTLEFIELD_AGENT_SECRET={SECRET}\nBATTLEFIELD_ENV=production\nBATTLEFIELD_CLI_ISOLATION_VERIFIED=true\n')
        self.assertEqual(self.env.stat().st_mode & 0o777, 0o400)
        files = json.loads(self.marker.read_text())['files']
        self.assertEqual(files['/etc/battlefield-agent/worker.env'], hashlib.sha256(content.encode()).hexdigest())
        self.assertIn('restart battlefield-worker', self.calls.read_text())
        self.assertFalse(self.run_script(worker.GUEST_SYNC, {'secret': SECRET})['changed'])

    def run_failing(self, code, payload):
        result = subprocess.run([sys.executable, '-c', self.script(code)], input=json.dumps(payload).encode(),
                                capture_output=True, env=self.process_env, timeout=30)
        self.assertNotEqual(result.returncode, 0)
        return result.stderr.decode()

    def test_sync_refuses_drift_from_the_registered_file(self):
        original = self.env.read_text()
        self.env.write_text(original.replace('ISOLATION_VERIFIED=true', 'ISOLATION_VERIFIED=false'))
        self.assertIn('differs from its registered content', self.run_failing(worker.GUEST_SYNC, {'secret': SECRET}))
        self.assertIn('ISOLATION_VERIFIED=false', self.env.read_text())
        self.assertFalse(self.calls.exists())

    def test_sync_refuses_unexpected_settings_even_when_registered(self):
        content = self.env.read_text() + 'HTTPS_PROXY=http://attacker.invalid\n'
        self.env.write_text(content)
        self.marker.write_text(json.dumps({'files': {'/etc/battlefield-agent/worker.env': hashlib.sha256(content.encode()).hexdigest()}}))
        self.assertIn('unexpected settings', self.run_failing(worker.GUEST_SYNC, {'secret': SECRET}))
        self.assertEqual(self.env.read_text(), content)

    def test_config_sync_refuses_drift_and_updates_the_marker(self):
        config = self.root / 'config.yaml'
        config.write_text(CONFIG)
        code = worker.GUEST_CONFIG_SYNC.replace("pathlib.Path('/etc/battlefield-agent/config.yaml')", f"pathlib.Path({str(config)!r})")
        updated = worker.desired_config(CONFIG, SETTINGS)
        self.assertIn('differs from its registered content', self.run_failing(code, {'content': updated, 'expected_sha256': '0' * 64}))
        self.assertEqual(config.read_text(), CONFIG)
        self.run_script(code, {'content': updated, 'expected_sha256': hashlib.sha256(CONFIG.encode()).hexdigest()})
        self.assertEqual(config.read_text(), updated)
        self.assertEqual(config.stat().st_mode & 0o777, 0o644)
        self.assertEqual(json.loads(self.marker.read_text())['files']['/etc/battlefield-agent/config.yaml'],
                         hashlib.sha256(updated.encode()).hexdigest())

    def test_config_sync_resumes_after_the_guest_was_already_updated(self):
        config = self.root / 'config.yaml'
        updated = worker.desired_config(CONFIG, SETTINGS)
        config.write_text(updated)
        code = worker.GUEST_CONFIG_SYNC.replace("pathlib.Path('/etc/battlefield-agent/config.yaml')", f"pathlib.Path({str(config)!r})")
        before = config.stat().st_mtime_ns
        result = self.run_script(code, {'content': updated, 'expected_sha256': hashlib.sha256(CONFIG.encode()).hexdigest()})
        self.assertFalse(result['changed'])
        self.assertEqual(config.stat().st_mtime_ns, before)
        self.assertEqual(json.loads(self.marker.read_text())['files']['/etc/battlefield-agent/config.yaml'],
                         hashlib.sha256(updated.encode()).hexdigest())

    def test_inspect_compares_digests_without_returning_the_secret(self):
        result = self.run_script(worker.GUEST_INSPECT, {'secret_sha256': hashlib.sha256(b'old-old-old-old-old-old-old-old-old').hexdigest()})
        self.assertTrue(result['secret_matches'])
        self.assertTrue(result['marker_matches'])
        self.assertEqual(result['domain_uuid'], 'abcdef')
        self.assertTrue(result['worker_active'])
        self.assertEqual(result['worker_health'], 0)
        mismatch = self.run_script(worker.GUEST_INSPECT, {'secret_sha256': hashlib.sha256(SECRET.encode()).hexdigest()})
        self.assertFalse(mismatch['secret_matches'])
        self.assertNotIn('old-old', json.dumps(result))


if __name__ == '__main__':
    unittest.main()
