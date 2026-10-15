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
              'boot_start_enabled': True, 'bridge_watchdog_active': True, 'stale_host_files': [],
              'bridge_readyz': 200, 'gateway_accepts_worker_secret': True, 'automation_key': True,
              'guest': {'reachable': True, 'identity_matches': True, 'secret_matches': True, 'marker_matches': True,
                        'worker_active': True, 'worker_enabled': True, 'worker_health': 204}}
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
            (healthy(bridge_readyz=0), ['restart-bridge']),
            (healthy(secret_matches=False), ['sync-worker-secret']),
            (healthy(marker_matches=False), ['sync-worker-secret']),
            (healthy(worker_health=0), ['restart-worker']),
            (healthy(boot_start_enabled=False), ['install-host-units']),
            (healthy(stale_host_files=['/etc/systemd/system/battlefield-cli-vm-start.service']), ['install-host-units']),
            (healthy(guest={'reachable': False}), ['reach-guest']),
        ]
        for checks, expected in cases:
            with self.subTest(expected=expected):
                self.assertEqual(worker.Worker.repairs(checks), expected)

    def test_problems_describe_todays_outage(self):
        checks = healthy(domain='shut off', guest=None, bridge_readyz=0, boot_start_enabled=False)
        problems = worker.Worker.problems(checks)
        self.assertIn('CLI VM is shut off', problems)
        self.assertIn('MCP bridge does not reach the AI gateway', problems)
        self.assertIn('boot start or bridge watchdog is not installed', problems)
        mismatch = worker.Worker.problems(healthy(secret_matches=False, gateway_accepts_worker_secret=False))
        self.assertIn('CLI worker secret differs from production', mismatch)
        self.assertIn('AI gateway rejects the production worker secret', mismatch)

    def test_host_files_render_the_bridge_port_and_boot_retry(self):
        files = worker.host_files(28084)
        self.assertIn('mcp-bridge-watchdog 28084', files['/etc/systemd/system/battlefield-mcp-bridge-watchdog.service'][1])
        self.assertIn('virsh -c qemu:///system start battlefield-agent', files[worker.LIBEXEC + '/start-cli-vm'][1])
        self.assertIn('battlefield-agent-firewall.service', files['/etc/systemd/system/battlefield-cli-vm-start.service'][1])
        self.assertEqual({mode for mode, _ in files.values()}, {'0755', '0644'})


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
