#!/usr/bin/env python3
"""Exercise host profile selection and generated configuration without a cluster."""
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
SOURCE = (ROOT / 'scripts/dev/run-k8s-cli-agent.sh').read_text()
FUNCTIONS = '\n'.join(re.findall(r'^\w+\(\) \{\n.*?^\}', SOURCE, re.M | re.S))
DEFAULT_CONCURRENCY = re.search(r'^agent_concurrency="\$\{HOST_AGENT_CONCURRENCY:-(\d+)\}"$', SOURCE, re.M).group(1)
BASE_ENV = {key: value for key, value in os.environ.items() if key != 'HOST_CLI_PREFETCH_CONTEXT'}


def configured_prefixes(text):
    """Map each CLI provider to its supportedVersionPrefix in a profile YAML document."""
    return {provider: prefix.strip().strip('"')
            for provider, prefix in re.findall(r'provider: (\S+-cli)\n(?:.*\n)*?\s+supportedVersionPrefix: (.+)', text)}


class CLIProfiles(unittest.TestCase):
    def run_profile(self, mode, available, concurrency=None, overrides=None):
        with tempfile.TemporaryDirectory() as runtime:
            script = FUNCTIONS + '''
use_codex=0; use_gemini=0; use_claude=0
health_port=18083; gateway_port=18084; agent_concurrency="${agent_concurrency:-''' + DEFAULT_CONCURRENCY + '''}"
resolve_version_prefixes
codex_available() { [[ "$available" == *codex* ]]; }
antigravity_available() { [[ "$available" == *gemini* ]]; }
claude_available() { [[ "$available" == *claude* ]]; }
select_profiles
write_agent_config
cat "$runtime_dir/config.yaml"
kube() { printf '%s\\n' "$*" >&2; }
set_profile_flags false false true
set_profile_flags false false false
'''
            return subprocess.run(['bash', '-euo', 'pipefail', '-c', script], capture_output=True, text=True,
                                  env={**BASE_ENV, **(overrides or {}), 'requested_provider': mode,
                                       'available': available, 'runtime_dir': runtime,
                                       **({'agent_concurrency': concurrency} if concurrency else {})})

    def test_claude_selection_config_and_flags(self):
        result = self.run_profile('claude', 'claude')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('provider: claude-cli', result.stdout)
        self.assertIn('model: sonnet', result.stdout)
        self.assertNotIn('provider: codex-cli', result.stdout)
        self.assertIn('"CLAUDE_CLI_PROFILE_AVAILABLE":"true"', result.stderr)
        self.assertIn('"CLAUDE_CLI_PROFILE_AVAILABLE":"false"', result.stderr)

    def test_context_prefetch_option_is_explicit_and_validated(self):
        for value in ['true', 'false']:
            result = self.run_profile('all', 'codex gemini claude', overrides={'HOST_CLI_PREFETCH_CONTEXT': value})
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.count('prefetchContext: ' + value), 2)
        default = self.run_profile('codex', 'codex')
        self.assertIn('prefetchContext: true', default.stdout)
        invalid = self.run_profile('codex', 'codex', overrides={'HOST_CLI_PREFETCH_CONTEXT': 'yes'})
        self.assertEqual(invalid.returncode, 2)
        self.assertIn('must be true or false', invalid.stderr)

    def test_auto_both_and_all(self):
        for mode, available, count in [('auto', 'claude', 1), ('auto', 'codex', 1), ('auto', 'gemini', 1), ('auto', 'codex gemini claude', 3),
                                       ('both', 'codex gemini claude', 2), ('all', 'codex gemini claude', 3)]:
            with self.subTest(mode=mode, available=available):
                result = self.run_profile(mode, available)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout.count('provider:'), count)

    def test_agent_concurrency_default_and_override(self):
        # Eight slots by default so both factions of four concurrent model battles decide at once.
        self.assertIn('agentConcurrency: 8', self.run_profile('claude', 'claude').stdout)
        self.assertIn('agentConcurrency: 4', self.run_profile('claude', 'claude', '4').stdout)

    def test_agent_concurrency_rejects_invalid_values(self):
        for value, valid in [('1', True), ('4', True), ('8', True), ('0', False), ('9', False), ('10', False), ('two', False), ('', False)]:
            with self.subTest(value=value):
                result = subprocess.run(['bash', '-euo', 'pipefail', '-c', FUNCTIONS + '\nvalidate_agent_concurrency'],
                                        capture_output=True, text=True, env={**os.environ, 'agent_concurrency': value})
                self.assertEqual(result.returncode, 0 if valid else 2, result.stderr)
                if not valid:
                    self.assertIn('HOST_AGENT_CONCURRENCY must be a whole number from 1 to 8.', result.stderr)

    def run_functions(self, script, **env):
        return subprocess.run(['bash', '-euo', 'pipefail', '-c', FUNCTIONS + '\n' + script],
                              capture_output=True, text=True, env={**BASE_ENV, **env})

    def test_served_profiles_lists_served_ids_and_reports_dropped_ones(self):
        status = json.dumps({'draining': False, 'active': 0, 'concurrency': 8,
                             'profiles': ['claude-subscription-local', 'codex-subscription-local'],
                             'unavailable': {'gemini-subscription-local': 'agy CLI version is not supported'}})
        result = self.run_functions('served_profiles <<<"$STATUS"', STATUS=status)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.split(), ['claude-subscription-local', 'codex-subscription-local'])
        self.assertIn('cannot serve gemini-subscription-local: agy CLI version is not supported', result.stderr)
        empty = self.run_functions('served_profiles <<<\'{"profiles": []}\'')
        self.assertEqual((empty.returncode, empty.stdout), (0, ''))

    def test_keep_served_profiles_advertises_only_what_the_agent_serves(self):
        script = 'use_codex=1; use_gemini=1; use_claude=1\nset +e; keep_served_profiles "$SERVED"; code=$?; set -e\necho "$code $use_codex $use_gemini $use_claude"'
        dropped = self.run_functions(script, SERVED='codex-subscription-local\nclaude-subscription-local')
        self.assertEqual(dropped.stdout.split(), ['1', '1', '0', '1'], dropped.stderr)
        served = self.run_functions(script, SERVED='codex-subscription-local\ngemini-subscription-local\nclaude-subscription-local')
        self.assertEqual(served.stdout.split(), ['0', '1', '1', '1'], served.stderr)
        # A profile ID must match a whole line, not a prefix of another ID.
        partial = self.run_functions(script, SERVED='codex-subscription-local-old')
        self.assertEqual(partial.stdout.split(), ['1', '0', '0', '0'], partial.stderr)

    def test_default_version_prefixes_match_backend_config(self):
        result = self.run_profile('all', 'codex gemini claude')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn('Warning', result.stderr)
        expected = configured_prefixes((ROOT / 'backend/config.yaml').read_text())
        self.assertEqual(configured_prefixes(result.stdout), expected)
        self.assertEqual(set(expected), {'codex-cli', 'claude-cli', 'antigravity-cli'})

    def test_stable_versions_compare_numerically_and_reject_unknown_banners(self):
        cases = [
            ('codex', 'codex-cli 0.159.0', True),
            ('codex', 'codex-cli 0.159.2', True),
            ('codex', 'codex-cli 0.1000.0', True),
            ('codex', 'codex-cli 1.0.0', True),
            ('codex', 'codex-cli 0.158.99', False),
            ('codex', 'codex-cli 0.155.0', False),
            ('codex', 'codex-cli 0.99.99', False),
            ('codex', 'codex-cli 0.159.2-rc.1', False),
            ('codex', 'codex-cli 0.159.0-beta', False),
            ('codex', 'codex-cli 0.159', False),
            ('codex', 'codex-cli 0.9999999999999999999.0', False),
            ('codex', 'other-cli 0.159.2', False),
            ('agy', '1.2.0', True),
            ('agy', '1.4.5', True),
            ('agy', '1.10.0', True),
            ('agy', '2.0.0', True),
            ('agy', '1.1.99', False),
            ('agy', '1.4.5-dev', False),
            ('agy', '1.4.5\nunexpected output', False),
            ('claude', '2.1.325 (Claude Code)', True),
            ('claude', '2.1.400 (Claude Code)', True),
            ('claude', '2.2.0 (Claude Code)', True),
            ('claude', '3.0.0 (Claude Code)', True),
            ('claude', '2.1.324 (Claude Code)', False),
            ('claude', '2.0.999 (Claude Code)', False),
            ('claude', '2.2.0-beta (Claude Code)', False),
            ('claude', '2.2.0 (Unknown)', False),
        ]
        names = {'codex': 'codex', 'agy': 'antigravity', 'claude': 'claude'}
        with tempfile.TemporaryDirectory() as runtime:
            for binary in names:
                fake = Path(runtime, binary)
                fake.write_text('#!/usr/bin/env bash\nprintf "%s\\n" "$FAKE_VERSION"\nexit "${FAKE_EXIT:-0}"\n')
                fake.chmod(0o755)
            for binary, version, accepted in cases:
                with self.subTest(binary=binary, version=version):
                    name = names[binary]
                    script = FUNCTIONS + f'''
resolve_version_prefixes
accept_cli_version {binary} {name}_version_prefix
printf '%s' "${{{name}_version_prefix}}"
'''
                    env = {**BASE_ENV, 'PATH': runtime + os.pathsep + os.environ['PATH'], 'FAKE_VERSION': version}
                    result = subprocess.run(['bash', '-euo', 'pipefail', '-c', script], capture_output=True, text=True, env=env)
                    self.assertEqual(result.returncode == 0, accepted, result.stderr)
                    if accepted:
                        self.assertEqual(result.stdout, version)
                        self.assertEqual(result.stderr, '')
                        failed = subprocess.run(['bash', '-euo', 'pipefail', '-c', script], capture_output=True, text=True,
                                                env={**env, 'FAKE_EXIT': '1'})
                        self.assertNotEqual(failed.returncode, 0)

    def test_newer_versions_reach_config_and_still_require_authentication(self):
        with tempfile.TemporaryDirectory() as runtime:
            versions = {'codex': 'codex-cli 0.159.2', 'agy': '1.4.5', 'claude': '2.1.400 (Claude Code)'}
            for binary, version in versions.items():
                fake = Path(runtime, binary)
                fake.write_text(f'#!/usr/bin/env bash\necho "{version}"\n')
                fake.chmod(0o755)
            fake_agent = Path(runtime, 'agent')
            fake_agent.write_text('#!/usr/bin/env bash\nexit "${FAKE_AUTH_EXIT:-0}"\n')
            fake_agent.chmod(0o755)
            Path(runtime, '.codex').mkdir()
            auth = Path(runtime, '.codex/auth.json')
            auth.write_text('{}')
            script = FUNCTIONS + '''
use_codex=0; use_gemini=0; use_claude=0
health_port=18083; gateway_port=18084; agent_concurrency=2
resolve_version_prefixes
select_profiles
write_agent_config
cat "$runtime_dir/config.yaml"
'''
            env = {**BASE_ENV, 'PATH': runtime + os.pathsep + os.environ['PATH'],
                   'HOME': runtime, 'runtime_dir': runtime, 'requested_provider': 'auto'}
            result = subprocess.run(['bash', '-euo', 'pipefail', '-c', script], capture_output=True, text=True, env=env)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(configured_prefixes(result.stdout), {
                'codex-cli': versions['codex'], 'antigravity-cli': versions['agy'], 'claude-cli': versions['claude']})
            auth.unlink()
            failed = subprocess.run(['bash', '-euo', 'pipefail', '-c', script], capture_output=True, text=True,
                                    env={**env, 'FAKE_AUTH_EXIT': '1'})
            self.assertNotEqual(failed.returncode, 0)
            self.assertIn('No supported authenticated subscription CLI was found', failed.stderr)

    def test_production_claude_requires_isolation(self):
        script = r"""
const assert = require('node:assert/strict');
const {validate} = require(process.argv[1]);
const values = new Map(Object.entries({
  DATABASE_URL: 'postgres://user:password@localhost/test', POSTGRES_DB: 'test',
  POSTGRES_USER: 'user', POSTGRES_PASSWORD: 'password',
  BATTLEFIELD_AGENT_SECRET: 'a'.repeat(32), BATTLEFIELD_MCP_CAPABILITY_SECRET: 'b'.repeat(32),
  BATTLEFIELD_AUTH_MODE: 'cloudflare', BATTLEFIELD_AUTH_ENABLED: 'true',
  BATTLEFIELD_ACCESS_ISSUER: 'https://test.cloudflareaccess.com',
  BATTLEFIELD_ACCESS_AUDIENCE: 'a'.repeat(64),
  BATTLEFIELD_PUBLIC_ORIGIN: 'https://play.example.com',
  BATTLEFIELD_ACCESS_ROLES: JSON.stringify({admin:['admin@example.com']}),
  CLAUDE_CLI_PROFILE_AVAILABLE: 'true'
}));
assert.throws(() => validate(values, 'production'), /CLI isolation/);
values.set('BATTLEFIELD_CLI_ISOLATION_VERIFIED', 'true');
assert.doesNotThrow(() => validate(values, 'production'));
"""
        subprocess.run(['node', '-e', script, str(ROOT / 'scripts/lib/application-env.cjs')], check=True)

    def test_failed_forward_prints_log_even_if_another_listener_is_healthy(self):
        with tempfile.TemporaryDirectory() as runtime:
            log = Path(runtime) / 'forward.log'
            log.write_text('Unable to listen on port 18084: address already in use\n')
            script = FUNCTIONS + r"""
kill() { return 1; }
curl() { echo 'unexpected health probe'; return 0; }
wait_for_endpoint http://127.0.0.1:18084/healthz 123 'AI gateway port-forward' "$1"
"""
            result = subprocess.run(['bash', '-euo', 'pipefail', '-c', script, '_', str(log)], capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('address already in use', result.stderr)
            self.assertIn('HOST_AI_GATEWAY_PORT', result.stderr)
            self.assertNotIn('unexpected health probe', result.stdout)

    def test_endpoint_timeout_prints_log_and_probes_are_bounded(self):
        with tempfile.TemporaryDirectory() as runtime:
            log = Path(runtime) / 'forward.log'
            log.write_text('error forwarding port: connection refused\n')
            script = FUNCTIONS + r"""
kill() { return 0; }
sleep() { :; }
curl() {
  [[ "$*" == *'--connect-timeout 1 --max-time 1'* ]] || exit 90
  [[ "$*" == *'--noproxy *'* ]] || exit 91
  return 1
}
wait_for_endpoint http://127.0.0.1:18084/healthz 123 'AI gateway port-forward' "$1"
"""
            result = subprocess.run(['bash', '-euo', 'pipefail', '-c', script, '_', str(log)], capture_output=True, text=True)
            self.assertEqual(result.returncode, 1)
            self.assertIn('did not become ready', result.stderr)
            self.assertIn('connection refused', result.stderr)

    def test_live_forward_and_healthy_endpoint_succeed(self):
        script = FUNCTIONS + r"""
kill() { return 0; }
curl() { return 0; }
wait_for_endpoint http://127.0.0.1:18084/healthz 123 'AI gateway port-forward'
"""
        subprocess.run(['bash', '-euo', 'pipefail', '-c', script], check=True)

    def test_forward_pid_is_the_kubectl_process_cleanup_stops(self):
        with tempfile.TemporaryDirectory() as runtime:
            fake = Path(runtime, 'kubectl')
            fake.write_text('#!/usr/bin/env bash\necho "$$ $*" >"$(dirname "$0")/kubectl.pid"\nexec sleep 60\n')
            fake.chmod(0o755)
            script = FUNCTIONS + r"""
kube_context=fixture; namespace=battlefield
start_port_forward "$1/forward.log" service/battlefield-ai-gateway 18084:8084
forward_pid=$!
for _ in {1..100}; do [[ -s "$1/kubectl.pid" ]] && break; sleep 0.05; done
read -r kubectl_pid arguments <"$1/kubectl.pid"
kill "$forward_pid"
wait "$forward_pid" || true
[[ "$kubectl_pid" == "$forward_pid" ]]
[[ "$arguments" == "--context fixture --namespace battlefield port-forward --address 127.0.0.1 service/battlefield-ai-gateway 18084:8084" ]]
"""
            subprocess.run(['bash', '-euo', 'pipefail', '-c', script, '_', runtime], check=True,
                           env={**os.environ, 'PATH': runtime + os.pathsep + os.environ['PATH']})

    def test_host_wait_allows_both_serial_auth_probes(self):
        self.assertIn('"${agent_pid}" "Host agent" "" 300', SOURCE)
        script = FUNCTIONS + r"""
kill() { return 0; }
sleep() { :; }
probes=0
curl() { probes=$((probes+1)); [[ "$probes" -ge 160 ]]; }
wait_for_endpoint http://127.0.0.1:18083/healthz 123 'Host agent' "" 300
[[ "$probes" == 160 ]]
"""
        subprocess.run(['bash', '-euo', 'pipefail', '-c', script], check=True)

    def test_unavailable_and_invalid_rejected(self):
        for mode, available in [('claude', ''), ('all', 'codex gemini'), ('auto', ''), ('unknown', 'claude')]:
            self.assertNotEqual(self.run_profile(mode, available).returncode, 0)


if __name__ == '__main__':
    unittest.main()
