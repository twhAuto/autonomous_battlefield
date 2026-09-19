#!/usr/bin/env python3
"""Exercise host profile selection and generated configuration without a cluster."""
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
SOURCE = (ROOT / 'scripts/dev/run-k8s-cli-agent.sh').read_text()
FUNCTIONS = '\n'.join(re.findall(r'^\w+\(\) \{\n.*?^\}', SOURCE, re.M | re.S))


class CLIProfiles(unittest.TestCase):
    def run_profile(self, mode, available):
        with tempfile.TemporaryDirectory() as runtime:
            script = FUNCTIONS + '''
use_codex=0; use_gemini=0; use_claude=0
health_port=18083; mcp_port=18084
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
            return subprocess.run(['bash', '-ec', script], capture_output=True, text=True,
                                  env={**os.environ, 'requested_provider': mode,
                                       'available': available, 'runtime_dir': runtime})

    def test_claude_selection_config_and_flags(self):
        result = self.run_profile('claude', 'claude')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('provider: claude-cli', result.stdout)
        self.assertIn('model: sonnet', result.stdout)
        self.assertNotIn('provider: codex-cli', result.stdout)
        self.assertIn('"CLAUDE_CLI_PROFILE_AVAILABLE":"true"', result.stderr)
        self.assertIn('"CLAUDE_CLI_PROFILE_AVAILABLE":"false"', result.stderr)

    def test_auto_both_and_all(self):
        for mode, available, count in [('auto', 'claude', 1), ('auto', 'codex', 1), ('auto', 'gemini', 1), ('auto', 'codex gemini claude', 3),
                                       ('both', 'codex gemini claude', 2), ('all', 'codex gemini claude', 3)]:
            with self.subTest(mode=mode, available=available):
                result = self.run_profile(mode, available)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout.count('provider:'), count)

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
wait_for_endpoint http://127.0.0.1:18084/healthz 123 'MCP port-forward' "$1"
"""
            result = subprocess.run(['bash', '-ec', script, '_', str(log)], capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('address already in use', result.stderr)
            self.assertIn('HOST_MCP_PORT', result.stderr)
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
wait_for_endpoint http://127.0.0.1:18084/healthz 123 'MCP port-forward' "$1"
"""
            result = subprocess.run(['bash', '-ec', script, '_', str(log)], capture_output=True, text=True)
            self.assertEqual(result.returncode, 1)
            self.assertIn('did not become ready', result.stderr)
            self.assertIn('connection refused', result.stderr)

    def test_live_forward_and_healthy_endpoint_succeed(self):
        script = FUNCTIONS + r"""
kill() { return 0; }
curl() { return 0; }
wait_for_endpoint http://127.0.0.1:18084/healthz 123 'MCP port-forward'
"""
        subprocess.run(['bash', '-ec', script], check=True)

    def test_unavailable_and_invalid_rejected(self):
        for mode, available in [('claude', ''), ('all', 'codex gemini'), ('auto', ''), ('unknown', 'claude')]:
            self.assertNotEqual(self.run_profile(mode, available).returncode, 0)


if __name__ == '__main__':
    unittest.main()
