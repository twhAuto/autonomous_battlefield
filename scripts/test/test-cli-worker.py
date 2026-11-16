#!/usr/bin/env python3
"""CLI worker job: secret sourcing, repair planning, upgrades, receipts, guest scripts and redaction."""
import contextlib
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import time
import unittest
from unittest import mock
import urllib.error

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
              'release': {'image_digest': 'd' * 64},
              'guest': {'reachable': True, 'identity_matches': True, 'secret_matches': True, 'marker_matches': True,
                        'worker_active': True, 'worker_enabled': True, 'worker_health': 204,
                        'config_registered': True, 'config_current': True, 'config_pending': False,
                        'release_known': True, 'binary_registered': True, 'binary_current': True, 'binary_pending': False,
                        'drain_supported': True, 'draining': False, 'config_needs_upgrade': False,
                        'codex_known': True, 'codex_registered': True, 'codex_current': True}}
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

    def test_bound_application_file_replaces_the_profile_default(self):
        bound = self.env('BATTLEFIELD_AGENT_SECRET=' + SECRET + '\n')
        with mock.patch.dict(os.environ, {'BATTLEFIELD_APPLICATION_FILE': str(bound)}):
            self.assertEqual(load('cli_worker_bound', ROOT / 'scripts/infra/cli-worker.py').production_worker_secret(), SECRET)
        with mock.patch.dict(os.environ, {'BATTLEFIELD_CLI_HOME': '/operator'}):
            os.environ.pop('BATTLEFIELD_APPLICATION_FILE', None)
            self.assertEqual(load('cli_worker_profile', ROOT / 'scripts/infra/cli-worker.py').PRODUCTION_ENV,
                             Path('/operator/.config/autonomous-battlefield/production/.env'))

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
            # A registered binary older than production is upgraded; the upgrade restarts it anyway.
            (healthy(binary_current=False), ['upgrade-worker']),
            (healthy(binary_current=False, worker_health=0), ['upgrade-worker']),
            (healthy(binary_current=False, secret_matches=False), ['sync-worker-secret', 'upgrade-worker']),
            # An interrupted upgrade is planned again so the registration catches up.
            (healthy(binary_registered=False, binary_pending=True), ['upgrade-worker']),
            # An unknown release or an unregistered binary is never replaced.
            (healthy(binary_current=False, release_known=False), []),
            (healthy(binary_current=False, binary_registered=False), []),
            (healthy(draining=True), ['resume-worker']),
            # Concurrency waits for a binary that accepts it, unless this apply upgrades first.
            (healthy(config_current=False, config_needs_upgrade=True, binary_current=False, release_known=False), []),
            (healthy(config_current=False, config_needs_upgrade=True, binary_current=False), ['upgrade-worker', 'sync-worker-config']),
            # Codex follows the pinned release, under the release worker that reports the profiles it serves.
            (healthy(codex_current=False), ['upgrade-codex']),
            (healthy(codex_current=False, binary_current=False), ['upgrade-worker', 'upgrade-codex']),
            (healthy(codex_current=False, binary_current=False, release_known=False), []),
            (healthy(codex_current=False, codex_known=False), []),
            (healthy(codex_current=False, codex_registered=False), []),
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

    def test_binary_drift_and_draining_are_problems_but_an_older_release_is_a_notice(self):
        self.assertIn('CLI worker binary differs from its registered release; inspect it, it is not repaired automatically',
                      worker.Worker.problems(healthy(binary_registered=False, binary_current=False)))
        self.assertIn('CLI worker is draining and claims no new decisions', worker.Worker.problems(healthy(draining=True)))
        self.assertIn('CLI worker settings need the production worker release; apply upgrades it first',
                      worker.Worker.problems(healthy(config_current=False, config_needs_upgrade=True)))
        older = healthy(binary_current=False)
        self.assertEqual(worker.Worker.problems(older), [])
        self.assertEqual(worker.notices(older), ['CLI worker binary differs from the production release; run apply to upgrade it'])
        self.assertEqual(worker.notices(healthy(binary_pending=True, binary_registered=False)),
                         ['CLI worker binary differs from the production release; run apply to upgrade it'])
        self.assertIn('unknown', worker.notices(healthy(release_known=False, binary_current=False))[0])
        self.assertEqual(worker.notices(healthy()), [])
        self.assertEqual(worker.notices(healthy(guest=None)), [])

    def test_codex_drift_is_a_problem_but_an_older_codex_is_a_notice(self):
        drift = healthy(codex_registered=False, codex_current=False)
        self.assertIn('CLI worker Codex differs from its registered release; inspect it, it is not repaired automatically',
                      worker.Worker.problems(drift))
        self.assertEqual(worker.notices(drift), [])
        older = healthy(codex_current=False)
        self.assertEqual(worker.Worker.problems(older), [])
        self.assertEqual(worker.notices(older), ['CLI worker Codex differs from infra/deploy/agent-vm/cli-releases.json; run apply to update it'])
        self.assertEqual(worker.notices(healthy(codex_known=False, codex_current=False)),
                         ['Could not read the pinned Codex release, so the Codex update status is unknown'])
        self.assertEqual(len(worker.notices(healthy(binary_current=False, codex_current=False))), 2)

    def test_codex_checks_compare_registration_pin_and_version_family(self):
        old = {name: name + '-old' for name in worker.CODEX_BINARIES}
        new = {name: name + '-new' for name in worker.CODEX_BINARIES}
        instance = object.__new__(worker.Worker)
        instance.config = {'guest': {'artifacts': {name: {'sha256': old[name]} for name in worker.CODEX_BINARIES}}}
        instance.codex_info = {'version': '0.159.2', 'binaries': {name: {'sha256': new[name]} for name in worker.CODEX_BINARIES}}
        older, prefix = instance.codex_checks(old, CODEX_CONFIG)
        self.assertEqual((older, prefix), ({'codex_known': True, 'codex_registered': True, 'codex_current': False}, 'codex-cli 0.159.'))
        # An interrupted install leaves each executable at either release.
        mixed = dict(old, codex=new['codex'])
        self.assertTrue(instance.codex_checks(mixed, CODEX_CONFIG)[0]['codex_registered'])
        self.assertFalse(instance.codex_checks(dict(old, codex='other'), CODEX_CONFIG)[0]['codex_registered'])
        # Current needs the executables, their registration and the registered config at the pin.
        instance.config['guest']['artifacts'] = {name: {'sha256': new[name]} for name in worker.CODEX_BINARIES}
        self.assertFalse(instance.codex_checks(new, CODEX_CONFIG)[0]['codex_current'])
        target = worker.with_codex_prefix(CODEX_CONFIG, prefix)
        self.assertTrue(instance.codex_checks(new, target)[0]['codex_current'])
        instance.codex_info = None
        self.assertEqual(instance.codex_checks(new, target), ({'codex_known': False, 'codex_registered': True, 'codex_current': False}, None))

    def test_config_states_include_an_interrupted_codex_update(self):
        moved = worker.with_codex_prefix(CODEX_CONFIG, 'codex-cli 0.159.')
        self.assertEqual(worker.config_states(CODEX_CONFIG, 'codex-cli 0.159.'), {sha(CODEX_CONFIG.encode()), sha(moved.encode())})
        self.assertEqual(worker.config_states(moved, 'codex-cli 0.159.'), {sha(moved.encode())})
        self.assertEqual(worker.config_states(CODEX_CONFIG, None), {sha(CODEX_CONFIG.encode())})

    def test_binary_checks_compare_registration_and_release(self):
        instance = object.__new__(worker.Worker)
        instance.config = {'guest': {'artifacts': {'battlefield-agent': {'sha256': 'old'}}}}
        instance.release_info = {'sha256': 'new'}
        with mock.patch.object(worker, 'managed_settings', lambda: SETTINGS):
            older = instance.binary_checks('old', None)
            self.assertEqual((older['binary_registered'], older['binary_current'], older['binary_pending']), (True, False, False))
            self.assertEqual((older['drain_supported'], older['draining'], older['config_needs_upgrade']), (False, False, True))
            pending = instance.binary_checks('new', {'draining': True, 'active': 0, 'concurrency': 1})
            self.assertEqual((pending['binary_registered'], pending['binary_current'], pending['binary_pending']), (False, True, True))
            self.assertEqual((pending['drain_supported'], pending['draining'], pending['config_needs_upgrade']), (True, True, False))
            instance.release_info = None
            unknown = instance.binary_checks('old', None)
            self.assertEqual((unknown['release_known'], unknown['binary_current']), (False, False))

    def test_inspect_treats_any_2xx_gateway_readiness_as_reachable(self):
        instance = object.__new__(worker.Worker)
        instance.port, instance.config = 28084, {'guest': {}, 'host': {}}
        instance.domain_state = lambda: 'shut off'
        instance.unit_state = lambda unit, query='is-active': 'enabled' if query == 'is-enabled' else 'active'
        instance.host_files_current = lambda: []
        instance.release = lambda: None
        instance.codex_release = lambda: None
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
CODEX_CONFIG = CONFIG + """    - id: codex-subscription-local
      provider: codex-cli
      runtime: cli
      model: gpt-5.6-luna
      supportedVersionPrefix: codex-cli 0.155.
      enabled: true
    - id: codex-spare
      provider: codex-cli
      runtime: cli
      model: gpt-5.6-luna
      supportedVersionPrefix: codex-cli 0.155.
      enabled: false
"""


def sha(content):
    return hashlib.sha256(content).hexdigest()


class ConfigTests(unittest.TestCase):
    def test_model_options_sync_preserves_other_fields_and_is_idempotent(self):
        import yaml
        options = {'claude-subscription-local': [{'id': 'sonnet', 'reasoningEfforts': ['low', 'high']}]}
        settings = dict(SETTINGS, agentConcurrency=None, modelOptions=options)
        current = worker.desired_config(CONFIG, settings)
        updated = yaml.safe_load(current)
        expected = yaml.safe_load(worker.desired_config(CONFIG, dict(SETTINGS, agentConcurrency=None)))
        expected['ai']['profiles'][0]['modelOptions'] = options['claude-subscription-local']
        self.assertEqual(updated, expected)
        self.assertIs(worker.desired_config(current, settings), current)
        # Omitting options leaves an existing catalog intact.
        self.assertIs(worker.desired_config(current, dict(SETTINGS, agentConcurrency=None)), current)

    def test_model_options_require_release_upgrade_even_without_concurrency(self):
        instance = object.__new__(worker.Worker)
        instance.config = {'guest': {'artifacts': {'battlefield-agent': {'sha256': 'old'}}}}
        instance.release_info = {'sha256': 'new'}
        settings = dict(SETTINGS, agentConcurrency=None, modelOptions={'claude-subscription-local': [{'id': 'sonnet'}]})
        with mock.patch.object(worker, 'managed_settings', return_value=settings):
            self.assertTrue(instance.binary_checks('old', None)['config_needs_upgrade'])
            self.assertFalse(instance.binary_checks('new', None)['config_needs_upgrade'])

    def test_invalid_model_options_are_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'settings.json'
            for options in ([], {'unknown': [{'id': 'a'}]}, {'a': []}, {'a': ['b']},
                            {'a': [{'id': 'b', 'extra': True}]}, {'a': [{'id': 'bad model'}]},
                            {'a': [{'id': 'b'}, {'id': 'b'}]},
                            {'a': [{'id': 'b', 'reasoningEfforts': ['unknown']}]},
                            {'a': [{'id': 'b', 'reasoningEfforts': ['low', 'low']}]},
                            {'a': [{'id': 'b', 'reasoningEfforts': ['low'], 'defaultReasoningEffort': 'high'}]},
                            {'a': [{'id': 'b', 'aliases': 'c'}]}, {'a': [{'id': 'b', 'aliases': ['bad alias']}]},
                            {'a': [{'id': 'b', 'aliases': ['b']}]}, {'a': [{'id': 'b', 'aliases': ['c', 'c']}]},
                            {'a': [{'id': 'b', 'aliases': ['c']}, {'id': 'c'}]},
                            {'a': [{'id': 'b'}, {'id': 'c', 'aliases': ['b']}]},
                            {'a': [{'id': 'b', 'aliases': ['d']}, {'id': 'c', 'aliases': ['d']}]}):
                path.write_text(json.dumps({'agentConcurrency': None, 'models': {'a': 'b'}, 'modelOptions': options}))
                with self.subTest(options=options), self.assertRaises(ValueError):
                    worker.managed_settings(path)

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
        explicit_null = CONFIG.replace('  agentHealthAddress:', '  agentConcurrency: null\n  agentHealthAddress:').replace('gemini-3.7-flash-medium', 'gemini-3.8-flash-medium')
        self.assertIn('agentConcurrency', yaml.safe_load(explicit_null)['ai'])
        self.assertNotIn('agentConcurrency', yaml.safe_load(worker.desired_config(explicit_null, older))['ai'])

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
        options = {}
        for document in yaml.safe_load_all((ROOT / 'infra/deploy/base/infrastructure/configmap.yaml').read_text()):
            if document and document.get('kind') == 'ConfigMap':
                for value in document['data'].values():
                    if 'profiles' in value:
                        catalog = {p['id']: p['model'] for p in yaml.safe_load(value)['ai']['profiles'] if p['runtime'] == 'cli'}
                        options = {p['id']: p['modelOptions'] for p in yaml.safe_load(value)['ai']['profiles'] if p['runtime'] == 'cli' and 'modelOptions' in p}
        self.assertEqual(settings['models'], catalog)
        self.assertEqual(settings['modelOptions'], options)
        template = yaml.safe_load((ROOT / 'infra/deploy/agent-vm/config.yaml').read_text())['ai']
        self.assertEqual({p['id']: p['model'] for p in template['profiles']}, settings['models'])
        self.assertEqual({p['id']: p['modelOptions'] for p in template['profiles'] if 'modelOptions' in p}, settings['modelOptions'])
        if settings['agentConcurrency'] is not None:
            self.assertEqual(template['agentConcurrency'], settings['agentConcurrency'])
        launcher_tests = load('cli_agent_tests', ROOT / 'scripts/test/test-cli-agent.py')
        generated = launcher_tests.CLIProfiles().run_profile('all', 'codex gemini claude')
        self.assertEqual(generated.returncode, 0, generated.stderr)
        for source in [(ROOT / 'backend/config.yaml').read_text(), generated.stdout]:
            profiles = [p for p in yaml.safe_load(source)['ai']['profiles'] if p['runtime'] == 'cli']
            self.assertEqual({p['id']: p['model'] for p in profiles}, settings['models'])
            self.assertEqual({p['id']: p['modelOptions'] for p in profiles if 'modelOptions' in p}, settings['modelOptions'])

    def test_codex_version_family_changes_only_codex_profiles(self):
        import yaml
        updated = worker.with_codex_prefix(CODEX_CONFIG, 'codex-cli 0.159.')
        profiles = yaml.safe_load(updated)['ai']['profiles']
        self.assertEqual([p.get('supportedVersionPrefix') for p in profiles], [None, None, 'codex-cli 0.159.', 'codex-cli 0.159.'])
        original = yaml.safe_load(CODEX_CONFIG)
        for profile in original['ai']['profiles'][2:]:
            profile['supportedVersionPrefix'] = 'codex-cli 0.159.'
        self.assertEqual(yaml.safe_load(updated), original)
        self.assertIs(worker.with_codex_prefix(updated, 'codex-cli 0.159.'), updated)
        self.assertIs(worker.with_codex_prefix(CONFIG, 'codex-cli 0.159.'), CONFIG)
        self.assertEqual(worker.codex_prefix('0.159.2'), 'codex-cli 0.159.')
        self.assertEqual(worker.codex_profiles(CODEX_CONFIG), ['codex-subscription-local'])

    def test_cli_releases_need_a_version_and_both_archive_digests(self):
        valid = {'codex': {'version': '0.159.2', 'archives': {'codex': 'a' * 64, 'codex-code-mode-host': 'b' * 64}}}
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'cli-releases.json'
            path.write_text(json.dumps(valid))
            self.assertEqual(worker.cli_releases(path), valid)
            codex = valid['codex']
            for bad in ({}, dict(valid, claude={}), {'codex': dict(codex, version='0.159')},
                        {'codex': dict(codex, version='0.159.2-alpha.1')}, {'codex': dict(codex, version='00.159.2')},
                        {'codex': dict(codex, archives={'codex': 'a' * 64})},
                        {'codex': dict(codex, archives=dict(codex['archives'], codex='A' * 64))},
                        {'codex': dict(codex, url='https://example.invalid/codex')}, []):
                path.write_text(json.dumps(bad))
                with self.subTest(bad=bad), self.assertRaises(ValueError):
                    worker.cli_releases(path)

    def test_codex_profiles_accept_the_pinned_release(self):
        import yaml
        prefix = worker.codex_prefix(worker.cli_releases()['codex']['version'])
        sources = [(ROOT / 'backend/config.yaml').read_text(), (ROOT / 'infra/deploy/agent-vm/config.yaml').read_text()]
        for document in yaml.safe_load_all((ROOT / 'infra/deploy/base/infrastructure/configmap.yaml').read_text()):
            if document and document.get('kind') == 'ConfigMap':
                sources += [value for value in document['data'].values() if 'profiles' in value]
        launcher_tests = load('cli_agent_tests', ROOT / 'scripts/test/test-cli-agent.py')
        generated = launcher_tests.CLIProfiles().run_profile('codex', 'codex')
        self.assertEqual(generated.returncode, 0, generated.stderr)
        sources.append(generated.stdout)  # The launcher's minimum local version.
        self.assertEqual(len(sources), 4)
        for source in sources:
            codex = [p for p in yaml.safe_load(source)['ai']['profiles'] if p['provider'] == 'codex-cli']
            self.assertEqual([p['supportedVersionPrefix'] for p in codex], [prefix])

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
        instance.sync_config = lambda check=False: calls.append(('guest', 'config'))
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
        guest = healthy(worker_health=0, worker_enabled=False, config_current=False, drain_supported=False, worker_active=False)['guest']
        instance, calls = self.worker_with(guest)
        instance.apply(['enable-worker'], SECRET)
        self.assertEqual(calls, [('ssh', 'sudo -n systemctl enable battlefield-worker')])
        calls.clear()
        instance.apply(['sync-worker-config'], SECRET)
        self.assertEqual(calls, [('guest', 'config'), ('ssh', 'sudo -n systemctl restart battlefield-worker')])

    def test_a_worker_started_by_the_secret_sync_is_drained_before_the_upgrade(self):
        instance, calls = self.worker_with(healthy(worker_active=False, secret_matches=False, binary_current=False)['guest'])
        instance.upgrade_worker = lambda guest: calls.append(('upgrade', guest['worker_active']))
        instance.apply(['sync-worker-secret', 'upgrade-worker'], SECRET)
        self.assertEqual(calls, [('guest', 'secret'), ('upgrade', True)])

    def test_resume_signals_a_draining_worker(self):
        instance, calls = self.worker_with(healthy(draining=True)['guest'])
        instance.apply(['resume-worker'], SECRET)
        self.assertEqual(calls, [('ssh', 'sudo -n systemctl kill --kill-whom=main --signal=SIGUSR2 battlefield-worker')])

    def test_config_sync_drains_first_and_resumes_if_the_sync_fails(self):
        instance, calls = self.worker_with(healthy(config_current=False)['guest'])
        instance.drain = lambda guest: calls.append(('drain', guest['drain_supported']))
        instance.apply(['sync-worker-config'], SECRET)
        self.assertEqual(calls, [('drain', True), ('guest', 'config'), ('ssh', 'sudo -n systemctl restart battlefield-worker')])
        calls.clear()
        def failing(check=False):
            raise ValueError('the worker rejects the managed settings; not changed')
        instance.sync_config = failing
        with self.assertRaisesRegex(ValueError, 'rejects the managed settings'):
            instance.apply(['sync-worker-config'], SECRET)
        self.assertEqual(calls, [('drain', True), ('ssh', 'sudo -n systemctl kill --kill-whom=main --signal=SIGUSR2 battlefield-worker')])

    def test_codex_updates_after_the_worker_release_and_before_the_config_sync(self):
        instance, calls = self.worker_with(healthy(binary_current=False, codex_current=False, config_current=False,
                                                   drain_supported=False)['guest'])
        instance.upgrade_worker = lambda guest: calls.append(('upgrade', 'worker'))
        instance.upgrade_codex = lambda guest: calls.append(('upgrade', 'codex', guest['drain_supported']))
        instance.drain = lambda guest: calls.append(('drain', guest['drain_supported']))
        instance.apply(['upgrade-worker', 'upgrade-codex', 'sync-worker-config'], SECRET)
        self.assertEqual(calls, [('upgrade', 'worker'), ('upgrade', 'codex', True), ('drain', True), ('guest', 'config'),
                                 ('ssh', 'sudo -n systemctl restart battlefield-worker')])


class DrainTests(unittest.TestCase):
    def worker_with(self, activities):
        instance = object.__new__(worker.Worker)
        calls = []
        readings = iter(activities)
        instance.private = lambda path: Path(path)
        instance.ssh = lambda key, remote, data=None, timeout=120: calls.append(remote.rsplit('=', 1)[-1].split()[0]) or mock.Mock(returncode=0)
        instance.guest_python = lambda code, payload: next(readings)

        def wait_for(condition, seconds, message):
            for _ in range(len(activities)):
                if condition():
                    return
            raise ValueError(message)
        instance.wait_for = wait_for
        return instance, calls

    def test_a_drainable_worker_stops_claiming_until_idle(self):
        busy, idle = {'status': {'draining': True, 'active': 1}}, {'status': {'draining': True, 'active': 0}}
        instance, calls = self.worker_with([busy, idle])
        instance.drain(healthy()['guest'])
        self.assertEqual(calls, ['SIGUSR1'])

    def test_a_drain_timeout_resumes_claims_and_changes_nothing(self):
        busy = {'status': {'draining': True, 'active': 1}}
        instance, calls = self.worker_with([busy, busy])
        with self.assertRaisesRegex(ValueError, 'resumed claiming and nothing was changed'):
            instance.drain(healthy()['guest'])
        self.assertEqual(calls, ['SIGUSR1', 'SIGUSR2'])

    def test_an_ssh_timeout_while_draining_resumes_claims(self):
        instance, calls = self.worker_with([{}])
        def timeout(code, payload):
            raise subprocess.TimeoutExpired('ssh', 120)
        instance.guest_python = timeout
        with self.assertRaises(subprocess.TimeoutExpired):
            instance.drain(healthy()['guest'])
        self.assertEqual(calls, ['SIGUSR1', 'SIGUSR2'])

    def test_an_older_worker_is_never_signalled_and_must_stay_idle_twice(self):
        # SIGUSR1 would stop a worker without drain support, so only its processes are watched.
        instance, calls = self.worker_with([{'children': 0}, {'children': 1}, {'children': 0}, {'children': 0}])
        instance.drain(healthy(drain_supported=False)['guest'])
        self.assertEqual(calls, [])
        for readings in ([{'children': 1}, {'children': 0}, {'children': 1}], [{'children': None}, {'children': None}]):
            instance, calls = self.worker_with(readings)
            with self.subTest(readings=readings), self.assertRaisesRegex(ValueError, 'nothing was changed'):
                instance.drain(healthy(drain_supported=False)['guest'])

    def test_an_inactive_worker_needs_no_drain(self):
        instance, calls = self.worker_with([])
        instance.drain(healthy(worker_active=False)['guest'])
        self.assertEqual(calls, [])


class UpgradeTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        self.old, self.new = b'old worker', b'new worker'
        self.artifact = self.root / 'battlefield-agent'
        self.artifact.write_bytes(self.old)
        self.release = self.root / 'release'
        self.release.write_bytes(self.new)
        self.registration = self.root / 'cli.json'
        self.registration.write_text(json.dumps({'guest': {'artifacts': {'battlefield-agent': {
            'path': str(self.artifact), 'sha256': hashlib.sha256(self.old).hexdigest()}}}}))
        patch = mock.patch.object(worker, 'CLI_CONFIG', self.registration)
        patch.start()
        self.addCleanup(patch.stop)

    def worker_with(self, healthy_after=True):
        instance = object.__new__(worker.Worker)
        instance.config = json.loads(self.registration.read_text())
        instance.bootstrap = mock.Mock(validate=lambda value: value)
        instance.release_info = {'path': self.release, 'sha256': hashlib.sha256(self.new).hexdigest()}
        instance.private = lambda path: Path(path)
        steps = []
        instance.ssh = lambda key, remote, data=None, timeout=120: steps.append(('stage', hashlib.sha256(data).hexdigest())) or mock.Mock(returncode=0)
        names = {id(worker.GUEST_CHECK_RELEASE): 'check', id(worker.GUEST_INSTALL): 'install'}
        instance.guest_python = lambda code, payload: steps.append((names[id(code)], payload.get('target_sha256', payload.get('sha256')))) or {}
        instance.drain = lambda guest: steps.append(('drain', None))
        instance.release_healthy = lambda: healthy_after

        def wait_for(condition, seconds, message):
            if not condition():
                raise ValueError(message)
        instance.wait_for = wait_for
        return instance, steps

    def test_upgrade_validates_drains_installs_then_registers(self):
        instance, steps = self.worker_with()
        instance.upgrade_worker(healthy(binary_current=False)['guest'])
        new, old = hashlib.sha256(self.new).hexdigest(), hashlib.sha256(self.old).hexdigest()
        self.assertEqual(steps, [('stage', new), ('check', new), ('drain', None), ('install', new)])
        self.assertEqual(self.artifact.read_bytes(), self.new)
        self.assertEqual(json.loads(self.registration.read_text())['guest']['artifacts']['battlefield-agent']['sha256'], new)
        self.assertNotEqual(old, new)

    def test_an_unhealthy_release_restores_the_registered_binary_and_keeps_the_registration(self):
        instance, steps = self.worker_with(healthy_after=False)
        with self.assertRaisesRegex(ValueError, 'registered worker binary was restored'):
            instance.upgrade_worker(healthy(binary_current=False)['guest'])
        new, old = hashlib.sha256(self.new).hexdigest(), hashlib.sha256(self.old).hexdigest()
        self.assertEqual(steps[-2:], [('stage', old), ('install', old)])
        self.assertEqual(self.artifact.read_bytes(), self.old)
        self.assertEqual(json.loads(self.registration.read_text())['guest']['artifacts']['battlefield-agent']['sha256'], old)

    def test_a_failed_install_restores_the_registered_binary(self):
        # The install can replace the binary and then fail to restart the worker.
        instance, steps = self.worker_with()
        signals = []
        instance.signal_worker = signals.append
        installs = []
        def install(code, payload):
            if code is worker.GUEST_INSTALL:
                installs.append(payload['target_sha256'])
                if len(installs) == 1:
                    raise ValueError('The CLI guest command failed')
            return {}
        instance.guest_python = install
        with self.assertRaisesRegex(ValueError, 'registered worker binary was restored'):
            instance.upgrade_worker(healthy(binary_current=False)['guest'])
        self.assertEqual(installs, [hashlib.sha256(self.new).hexdigest(), hashlib.sha256(self.old).hexdigest()])
        self.assertEqual(signals, [])  # The restored worker restarted, so it is not draining.
        self.assertEqual(json.loads(self.registration.read_text())['guest']['artifacts']['battlefield-agent']['sha256'],
                         hashlib.sha256(self.old).hexdigest())

    def test_a_failed_restore_still_resumes_claims(self):
        instance, steps = self.worker_with()
        signals = []
        instance.signal_worker = signals.append
        def install(code, payload):
            if code is worker.GUEST_INSTALL:
                raise ValueError('The CLI guest command failed')
            return {}
        instance.guest_python = install
        with self.assertRaisesRegex(ValueError, 'guest command failed'):
            instance.upgrade_worker(healthy(binary_current=False)['guest'])
        self.assertEqual(signals, ['SIGUSR2'])
        self.assertEqual(self.artifact.read_bytes(), self.old)


class CodexUpgradeTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        self.old = {name: b'old ' + name.encode() for name in worker.CODEX_BINARIES}
        self.new = {name: b'new ' + name.encode() for name in worker.CODEX_BINARIES}
        artifacts = {}
        for name in worker.CODEX_BINARIES:
            (self.root / name).write_bytes(self.old[name])
            artifacts[name] = {'path': str(self.root / name), 'sha256': sha(self.old[name])}
        self.config = self.root / 'config.yaml'
        self.config.write_text(CODEX_CONFIG)
        self.updated = worker.with_codex_prefix(CODEX_CONFIG, 'codex-cli 0.159.')
        self.registration = self.root / 'cli.json'
        self.registration.write_text(json.dumps({'guest': {'artifacts': artifacts}}))
        patch = mock.patch.object(worker, 'CLI_CONFIG', self.registration)
        patch.start()
        self.addCleanup(patch.stop)

    def worker_with(self, healthy_after=True):
        instance = object.__new__(worker.Worker)
        instance.config = json.loads(self.registration.read_text())
        instance.bootstrap = mock.Mock(validate=lambda value: value)
        instance.registered_config = self.config
        instance.codex_info = {'version': '0.159.2', 'binaries': {
            name: {'content': self.new[name], 'sha256': sha(self.new[name])} for name in worker.CODEX_BINARIES}}
        instance.private = lambda path: Path(path)
        steps = []
        instance.ssh = lambda key, remote, data=None, timeout=120: steps.append(
            ('stage', remote.rsplit('/', 1)[-1].rstrip("'"), sha(data))) or mock.Mock(returncode=0)
        names = {id(worker.GUEST_CHECK_CODEX): 'check', id(worker.GUEST_INSTALL_CODEX): 'install'}
        instance.guest_python = lambda code, payload: steps.append((names[id(code)], payload)) or {}
        instance.drain = lambda guest: steps.append(('drain', None))
        instance.release_healthy = lambda profiles=(): steps.append(('serving', list(profiles))) or healthy_after

        def wait_for(condition, seconds, message):
            if not condition():
                raise ValueError(message)
        instance.wait_for = wait_for
        return instance, steps

    def test_update_checks_drains_swaps_codex_with_its_version_family_then_registers(self):
        instance, steps = self.worker_with()
        instance.upgrade_codex(healthy(codex_current=False)['guest'])
        self.assertEqual([step[0] for step in steps], ['stage', 'stage', 'check', 'drain', 'install', 'serving'])
        self.assertEqual(steps[:2], [('stage', name, sha(self.new[name])) for name in worker.CODEX_BINARIES])
        self.assertEqual(steps[2][1]['version'], '0.159.2')
        install = steps[4][1]
        self.assertEqual({name: item['target_sha256'] for name, item in install['files'].items()},
                         {name: sha(self.new[name]) for name in worker.CODEX_BINARIES})
        self.assertEqual((install['config'], install['config_expected_sha256']), (self.updated, sha(CODEX_CONFIG.encode())))
        self.assertEqual(steps[5], ('serving', ['codex-subscription-local']))
        registration = json.loads(self.registration.read_text())['guest']['artifacts']
        for name in worker.CODEX_BINARIES:
            self.assertEqual((self.root / name).read_bytes(), self.new[name])
            self.assertEqual(registration[name]['sha256'], sha(self.new[name]))
        self.assertEqual(self.config.read_text(), self.updated)

    def test_codex_the_worker_does_not_serve_is_restored_and_keeps_the_registration(self):
        instance, steps = self.worker_with(healthy_after=False)
        guest = healthy(codex_current=False)['guest']
        with self.assertRaisesRegex(ValueError, 'registered Codex release was restored'):
            instance.upgrade_codex(guest)
        self.assertEqual([step[0] for step in steps[-3:]], ['stage', 'stage', 'install'])
        restore = steps[-1][1]
        self.assertEqual({name: (item['expected_sha256'], item['target_sha256']) for name, item in restore['files'].items()},
                         {name: (sha(self.new[name]), sha(self.old[name])) for name in worker.CODEX_BINARIES})
        self.assertEqual((restore['config'], restore['config_expected_sha256']), (CODEX_CONFIG, sha(self.updated.encode())))
        registration = json.loads(self.registration.read_text())['guest']['artifacts']
        for name in worker.CODEX_BINARIES:
            self.assertEqual((self.root / name).read_bytes(), self.old[name])
            self.assertEqual(registration[name]['sha256'], sha(self.old[name]))
        self.assertEqual(self.config.read_text(), CODEX_CONFIG)

    def test_a_failed_restore_still_resumes_claims(self):
        instance, steps = self.worker_with()
        signals = []
        instance.signal_worker = signals.append
        def install(code, payload):
            if code is worker.GUEST_INSTALL_CODEX:
                raise ValueError('The CLI guest command failed')
            return {}
        instance.guest_python = install
        guest = healthy(codex_current=False)['guest']
        with self.assertRaisesRegex(ValueError, 'guest command failed'):
            instance.upgrade_codex(guest)
        self.assertEqual(signals, ['SIGUSR2'])
        self.assertEqual(self.config.read_text(), CODEX_CONFIG)

    def test_update_starts_from_settings_an_interrupted_sync_left_on_the_guest(self):
        # The guest holds the managed settings, but the registered copy was not updated yet.
        pending = worker.desired_config(CODEX_CONFIG, SETTINGS)
        moved = worker.with_codex_prefix(pending, 'codex-cli 0.159.')
        instance, steps = self.worker_with()
        guest = healthy(codex_current=False, config_current=False, config_registered=False, config_pending=True)['guest']
        with mock.patch.object(worker, 'managed_settings', return_value=SETTINGS):
            instance.upgrade_codex(guest)
            install = steps[4][1]
            self.assertEqual((install['config'], install['config_expected_sha256']), (moved, sha(pending.encode())))
            self.assertEqual(self.config.read_text(), moved)
            # The planned settings sync that follows finds nothing left to change.
            sent = []
            instance.guest_python = lambda code, payload: sent.append(payload) or {'changed': False}
            instance.sync_config()
        self.assertEqual(sent[0]['content'], moved)
        self.assertEqual(sent[0]['expected_sha256'], sha(moved.encode()))


class CodexReleaseTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.cache = Path(directory.name) / 'codex'
        self.archives, self.executables = {}, {}
        for name in worker.CODEX_BINARIES:
            self.executables[name] = b'executable ' + name.encode()
            archive = io.BytesIO()
            with tarfile.open(fileobj=archive, mode='w:gz') as tar:
                info = tarfile.TarInfo(name + '-x86_64-unknown-linux-musl')
                info.size = len(self.executables[name])
                tar.addfile(info, io.BytesIO(self.executables[name]))
            self.archives[name] = archive.getvalue()
        self.pin = {'codex': {'version': '0.159.2', 'archives': {name: sha(self.archives[name]) for name in worker.CODEX_BINARIES}}}
        self.urls = []
        patches = [mock.patch.object(worker, 'CODEX_CACHE', self.cache),
                   mock.patch.object(worker, 'cli_releases', return_value=self.pin),
                   mock.patch.object(worker.urllib.request, 'urlopen', self.urlopen)]
        for patch in patches:
            patch.start()
            self.addCleanup(patch.stop)
        self.instance = object.__new__(worker.Worker)
        self.instance.private = worker.Worker.private

    def urlopen(self, url, timeout):
        self.urls.append(url)
        name = url.rsplit('/', 1)[-1].removesuffix('-x86_64-unknown-linux-musl.tar.gz')
        return contextlib.nullcontext(io.BytesIO(self.archives[name]))

    def test_downloads_each_pinned_archive_once_and_extracts_its_executable(self):
        release = self.instance.codex_release()
        self.assertEqual(release['version'], '0.159.2')
        self.assertEqual(self.urls, ['https://github.com/openai/codex/releases/download/rust-v0.159.2/' + name
                                     + '-x86_64-unknown-linux-musl.tar.gz' for name in worker.CODEX_BINARIES])
        for name in worker.CODEX_BINARIES:
            binary = release['binaries'][name]
            self.assertEqual((binary['content'], binary['sha256']), (self.executables[name], sha(self.executables[name])))
            cached = self.cache / (self.pin['codex']['archives'][name] + '-' + name + '.tar.gz')
            self.assertEqual(cached.read_bytes(), self.archives[name])
            self.assertEqual(cached.stat().st_mode & 0o777, 0o600)
        self.assertEqual(self.instance.codex_release(), release)
        self.assertEqual(len(self.urls), 2)

    def test_a_changed_cache_fails_its_digest_and_is_downloaded_again(self):
        self.instance.codex_release()
        cached = self.cache / (self.pin['codex']['archives']['codex-code-mode-host'] + '-codex-code-mode-host.tar.gz')
        cached.write_bytes(b'replaced')
        self.assertIsNone(self.instance.codex_release())
        self.assertFalse(cached.exists())
        self.assertEqual(self.instance.codex_release()['binaries']['codex-code-mode-host']['content'],
                         self.executables['codex-code-mode-host'])
        self.assertEqual(len(self.urls), 3)

    def test_another_pin_replaces_the_cache(self):
        (self.cache).mkdir(mode=0o700)
        stale = self.cache / ('0' * 64 + '-codex.tar.gz')
        stale.write_bytes(b'old release')
        self.instance.codex_release()
        self.assertFalse(stale.exists())
        self.assertEqual(len(list(self.cache.iterdir())), 2)

    def test_a_digest_mismatch_or_unreachable_release_is_unknown(self):
        self.archives['codex'] = b'tampered'
        self.assertIsNone(self.instance.codex_release())
        self.assertFalse((self.cache / (self.pin['codex']['archives']['codex'] + '-codex.tar.gz')).exists())
        def unreachable(url, timeout):
            raise urllib.error.URLError('offline')
        with mock.patch.object(worker.urllib.request, 'urlopen', unreachable):
            self.assertIsNone(self.instance.codex_release())


class ReleaseTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.cache = Path(directory.name) / 'cache'
        patch = mock.patch.object(worker, 'RELEASE_CACHE', self.cache)
        patch.start()
        self.addCleanup(patch.stop)
        archive = io.BytesIO()
        with tarfile.open(fileobj=archive, mode='w') as tar:
            info = tarfile.TarInfo('agent')
            info.size = len(b'worker binary')
            tar.addfile(info, io.BytesIO(b'worker binary'))
        self.archive = archive.getvalue()

    def worker_with(self, image, present=False, cluster='cluster-uid'):
        instance = object.__new__(worker.Worker)
        instance.private = worker.Worker.private
        instance.config = {'host': {'kubeconfig': '/home/operator/kubeconfig', 'context': 'battlefield-production',
                                    'kube_system_uid': 'cluster-uid'}}
        commands = []

        def run(command, data=None, timeout=60, check=False):
            if command[0] == '/usr/bin/kubectl':
                commands.append(['kubectl', command[1], command[2], command[-1]])
                return mock.Mock(returncode=0, stdout=(cluster if 'kube-system' in command else image).encode())
            commands.append(command[:3])
            stdout = {'cp': self.archive, 'create': b'container\n'}
            failed = command[1:3] == ['image', 'inspect'] and not present
            return mock.Mock(returncode=1 if failed else 0, stdout=stdout.get(command[1], b''))
        instance.run = run
        return instance, commands

    def test_extracts_the_worker_from_the_deployed_digest_once(self):
        digest = 'a' * 64
        image = 'ghcr.io/twh09/autonomous-battlefield-backend@sha256:' + digest
        instance, commands = self.worker_with(image)
        release = instance.release()
        self.assertEqual(release['digest'], digest)
        self.assertEqual(release['sha256'], hashlib.sha256(b'worker binary').hexdigest())
        self.assertEqual(release['path'], self.cache / digest)
        self.assertEqual(release['path'].stat().st_mode & 0o777, 0o600)
        self.assertIn(['docker', 'pull', '--quiet'], commands)
        self.assertIn(['docker', 'image', 'rm'], commands)  # Only an image this job pulled is removed.
        commands.clear()
        self.assertEqual(instance.release()['sha256'], release['sha256'])
        registered = ['kubectl', '--kubeconfig=/home/operator/kubeconfig', '--context=battlefield-production']
        self.assertEqual(commands, [registered + ['jsonpath={.metadata.uid}'],
                                    registered + ['jsonpath={.spec.template.spec.containers[0].image}']])

    def test_another_cluster_is_never_treated_as_production(self):
        instance, commands = self.worker_with('ghcr.io/x/backend@sha256:' + 'c' * 64, cluster='other-uid')
        self.assertIsNone(instance.release())
        self.assertEqual(len(commands), 1)  # Stopped after the identity check.

    def test_an_interrupted_cache_write_leaves_no_release_behind(self):
        image = 'ghcr.io/x/backend@sha256:' + 'd' * 64
        instance, commands = self.worker_with(image)
        with mock.patch.object(worker.os, 'replace', side_effect=OSError('disk full')):
            self.assertIsNone(instance.release())
        self.assertEqual(list(self.cache.iterdir()), [])
        self.assertEqual(instance.release()['sha256'], hashlib.sha256(b'worker binary').hexdigest())

    def test_a_cached_image_is_kept(self):
        instance, commands = self.worker_with('ghcr.io/x/backend@sha256:' + 'b' * 64, present=True)
        instance.release()
        self.assertNotIn(['docker', 'pull', '--quiet'], commands)
        self.assertNotIn(['docker', 'image', 'rm'], commands)

    def test_a_tag_or_unreadable_image_is_unknown(self):
        for image in ('ghcr.io/x/backend:latest', ''):
            instance, _ = self.worker_with(image)
            with self.subTest(image=image):
                self.assertIsNone(instance.release())


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

    def test_a_new_release_after_the_plan_needs_a_new_plan(self):
        self.checks = healthy(binary_current=False)
        worker.execute(self.args('plan'))
        self.assertEqual(json.loads((self.directory / 'receipt.json').read_text())['release'], {'image_digest': 'd' * 64})
        self.checks = healthy(binary_current=False, release={'image_digest': 'e' * 64})
        with self.assertRaisesRegex(ValueError, 'changed since the reviewed plan'):
            worker.execute(self.args('apply'))
        self.assertEqual(self.applied, [])

    def test_verify_reports_an_older_release_as_a_notice_not_a_failure(self):
        self.checks = healthy(binary_current=False)
        with contextlib.redirect_stderr(io.StringIO()) as stderr:
            self.assertEqual(worker.execute(self.args('verify')), 0)
        self.assertIn('CLI worker notice: CLI worker binary differs from the production release', stderr.getvalue())
        self.assertEqual(len(json.loads((self.directory / 'verify.json').read_text())['notices']), 1)

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
        # Stands in for the worker: -check-config fails without the secret or for concurrency 9.
        self.binary = self.root / 'battlefield-agent'
        self.binary.write_text('#!/bin/sh\n[ "$1" = -check-config ] || exit 2\n[ -n "$BATTLEFIELD_AGENT_SECRET" ] || exit 1\n'
                               '! grep -q "agentConcurrency: 9" "$3"\n')
        self.binary.chmod(0o755)
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
                .replace("pathlib.Path('/usr/local/bin/battlefield-agent')", f"pathlib.Path({str(self.binary)!r})")
                .replace("pathlib.Path('/usr/local/bin/codex')", f"pathlib.Path({str(self.root / 'codex')!r})")
                .replace("pathlib.Path('/usr/local/bin/codex-code-mode-host')", f"pathlib.Path({str(self.root / 'codex-code-mode-host')!r})")
                .replace("check_config('/usr/local/bin/battlefield-agent'", f"check_config({str(self.binary)!r}")
                .replace("check_config(binary, '/etc/battlefield-agent/config.yaml')", f"check_config(binary, {str(self.root / 'config.yaml')!r})")
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
        self.assertEqual(result['binary_sha256'], hashlib.sha256(self.binary.read_bytes()).hexdigest())
        self.assertIsNone(result['status'])

    def staged(self, content):
        path = self.root / 'staged'
        path.write_bytes(content)
        return path

    def test_install_swaps_the_binary_updates_the_marker_and_restarts(self):
        old, new = self.binary.read_bytes(), self.binary.read_bytes() + b'# release\n'
        old_sha, new_sha = hashlib.sha256(old).hexdigest(), hashlib.sha256(new).hexdigest()
        source = self.staged(new)
        payload = {'source': str(source), 'expected_sha256': old_sha, 'target_sha256': new_sha}
        self.assertTrue(self.run_script(worker.GUEST_INSTALL, payload)['changed'])
        self.assertEqual(self.binary.read_bytes(), new)
        self.assertEqual(self.binary.stat().st_mode & 0o777, 0o755)
        self.assertEqual(json.loads(self.marker.read_text())['files']['/usr/local/bin/battlefield-agent'], new_sha)
        self.assertIn('restart battlefield-worker', self.calls.read_text())
        self.assertFalse(source.exists())
        # Resuming finds the release in place and only restarts.
        self.assertFalse(self.run_script(worker.GUEST_INSTALL, payload)['changed'])

    def test_install_refuses_an_unregistered_binary_or_a_corrupt_stage(self):
        old = self.binary.read_bytes()
        payload = {'source': str(self.staged(b'release')), 'expected_sha256': '0' * 64, 'target_sha256': hashlib.sha256(b'release').hexdigest()}
        self.assertIn('differs from its registered release', self.run_failing(worker.GUEST_INSTALL, payload))
        payload = {'source': str(self.staged(b'corrupt')), 'expected_sha256': hashlib.sha256(old).hexdigest(), 'target_sha256': hashlib.sha256(b'release').hexdigest()}
        self.assertIn('does not match the release', self.run_failing(worker.GUEST_INSTALL, payload))
        self.assertEqual(self.binary.read_bytes(), old)
        self.assertFalse(self.calls.exists())

    def test_release_check_verifies_the_digest_and_the_installed_config(self):
        release = self.staged(self.binary.read_bytes())
        digest = hashlib.sha256(release.read_bytes()).hexdigest()
        self.assertTrue(self.run_script(worker.GUEST_CHECK_RELEASE, {'binary': str(release), 'sha256': digest})['valid'])
        self.assertIn('does not match the release', self.run_failing(worker.GUEST_CHECK_RELEASE, {'binary': str(release), 'sha256': '0' * 64}))
        (self.root / 'config.yaml').write_text(CONFIG.replace('  agentHealthAddress:', '  agentConcurrency: 9\n  agentHealthAddress:'))
        self.assertIn('rejects the installed configuration', self.run_failing(worker.GUEST_CHECK_RELEASE, {'binary': str(release), 'sha256': digest}))

    def test_config_sync_check_refuses_settings_the_worker_rejects(self):
        config = self.root / 'config.yaml'
        rejected = CONFIG.replace('  agentHealthAddress:', '  agentConcurrency: 9\n  agentHealthAddress:')
        request = {'content': rejected, 'check': True, 'expected_sha256': hashlib.sha256(CONFIG.encode()).hexdigest()}
        self.assertIn('rejects the managed settings', self.run_failing(worker.GUEST_CONFIG_SYNC, request))
        self.assertEqual(config.read_text(), CONFIG)
        self.assertEqual([path.name for path in self.root.iterdir() if path.name.startswith('.battlefield')], [])
        accepted = worker.desired_config(CONFIG, SETTINGS)
        self.run_script(worker.GUEST_CONFIG_SYNC, dict(request, content=accepted))
        self.assertEqual(config.read_text(), accepted)

    def codex_install(self, current, target, config, updated):
        """Install `current` Codex and config, stage `target`, and return the request moving between them."""
        for name in worker.CODEX_BINARIES:
            (self.root / name).write_bytes(current[name])
            (self.root / ('staged-' + name)).write_bytes(target[name])
        (self.root / 'config.yaml').write_text(config)
        request = worker.codex_install({n: sha(current[n]) for n in current}, {n: sha(target[n]) for n in target}, config, updated)
        for name, item in request['files'].items():
            item['source'] = str(self.root / ('staged-' + name))
        return request

    def test_codex_install_swaps_both_executables_and_the_version_family_then_restarts(self):
        old = {name: b'old ' + name.encode() for name in worker.CODEX_BINARIES}
        new = {name: b'new ' + name.encode() for name in worker.CODEX_BINARIES}
        updated = worker.with_codex_prefix(CODEX_CONFIG, 'codex-cli 0.159.')
        request = self.codex_install(old, new, CODEX_CONFIG, updated)
        self.assertTrue(self.run_script(worker.GUEST_INSTALL_CODEX, request)['changed'])
        files = json.loads(self.marker.read_text())['files']
        for name in worker.CODEX_BINARIES:
            self.assertEqual((self.root / name).read_bytes(), new[name])
            self.assertEqual((self.root / name).stat().st_mode & 0o777, 0o755)
            self.assertEqual(files['/usr/local/bin/' + name], sha(new[name]))
            self.assertFalse((self.root / ('staged-' + name)).exists())
        self.assertEqual((self.root / 'config.yaml').read_text(), updated)
        self.assertEqual(files['/etc/battlefield-agent/config.yaml'], sha(updated.encode()))
        self.assertIn('restart battlefield-worker', self.calls.read_text())
        # Resuming finds every file in place and only restarts.
        self.assertFalse(self.run_script(worker.GUEST_INSTALL_CODEX, request)['changed'])
        # A restore moves both back, even from a half-finished install.
        (self.root / 'codex-code-mode-host').write_bytes(old['codex-code-mode-host'])
        restore = worker.codex_install({n: sha(new[n]) for n in new}, {n: sha(old[n]) for n in old}, updated, CODEX_CONFIG)
        for name, item in restore['files'].items():
            (self.root / ('staged-' + name)).write_bytes(old[name])
            item['source'] = str(self.root / ('staged-' + name))
        self.assertTrue(self.run_script(worker.GUEST_INSTALL_CODEX, restore)['changed'])
        self.assertEqual([(self.root / name).read_bytes() for name in worker.CODEX_BINARIES], list(old.values()))
        self.assertEqual((self.root / 'config.yaml').read_text(), CODEX_CONFIG)

    def test_codex_install_refuses_drift_or_a_corrupt_stage_and_changes_nothing(self):
        old = {name: b'old ' + name.encode() for name in worker.CODEX_BINARIES}
        new = {name: b'new ' + name.encode() for name in worker.CODEX_BINARIES}
        updated = worker.with_codex_prefix(CODEX_CONFIG, 'codex-cli 0.159.')
        request = self.codex_install(dict(old, codex=b'unregistered'), new, CODEX_CONFIG, updated)
        request['files']['codex']['expected_sha256'] = sha(old['codex'])
        self.assertIn('differs from its registered release', self.run_failing(worker.GUEST_INSTALL_CODEX, request))
        request = self.codex_install(old, new, CODEX_CONFIG, updated)
        request['config_expected_sha256'] = '0' * 64
        self.assertIn('configuration differs', self.run_failing(worker.GUEST_INSTALL_CODEX, request))
        request = self.codex_install(old, dict(new, **{'codex-code-mode-host': b'corrupt'}), CODEX_CONFIG, updated)
        request['files']['codex-code-mode-host']['target_sha256'] = sha(new['codex-code-mode-host'])
        self.assertIn('does not match the release', self.run_failing(worker.GUEST_INSTALL_CODEX, request))
        self.assertEqual([(self.root / name).read_bytes() for name in worker.CODEX_BINARIES], list(old.values()))
        self.assertEqual((self.root / 'config.yaml').read_text(), CODEX_CONFIG)
        self.assertFalse(self.calls.exists())

    def test_codex_check_verifies_the_stage_and_the_reported_version(self):
        staged = {'codex': b'#!/bin/sh\necho "codex-cli 0.159.2"\n', 'codex-code-mode-host': b'helper'}
        files = {}
        for name, content in staged.items():
            (self.root / ('staged-' + name)).write_bytes(content)
            files[name] = {'source': str(self.root / ('staged-' + name)), 'expected_sha256': '0' * 64, 'target_sha256': sha(content)}
        self.assertTrue(self.run_script(worker.GUEST_CHECK_CODEX, {'files': files, 'version': '0.159.2'})['valid'])
        self.assertIn('does not report the pinned version', self.run_failing(worker.GUEST_CHECK_CODEX, {'files': files, 'version': '0.159.3'}))
        files['codex-code-mode-host']['target_sha256'] = '0' * 64
        self.assertIn('does not match the pinned release', self.run_failing(worker.GUEST_CHECK_CODEX, {'files': files, 'version': '0.159.2'}))

    def test_inspect_reports_codex_digests(self):
        payload = {'secret_sha256': '0' * 64}
        self.assertEqual(self.run_script(worker.GUEST_INSPECT, payload)['codex_sha256'], {'codex': None, 'codex-code-mode-host': None})
        for name in worker.CODEX_BINARIES:
            (self.root / name).write_bytes(name.encode())
        self.assertEqual(self.run_script(worker.GUEST_INSPECT, payload)['codex_sha256'],
                         {name: sha(name.encode()) for name in worker.CODEX_BINARIES})


if __name__ == '__main__':
    unittest.main()
