#!/usr/bin/env python3
"""The AI policy script sends only validated statements to the named PostgreSQL pod."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / 'scripts/ops/ai-policy.sh'


class AIPolicyTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.log = Path(self.temp.name) / 'kubectl.log'
        stub = Path(self.temp.name) / 'kubectl'
        stub.write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> "$KUBECTL_LOG"\ncat >> "$KUBECTL_LOG"\n')
        stub.chmod(0o755)
        self.env = {**os.environ, 'PATH': f'{self.temp.name}:{os.environ["PATH"]}', 'KUBECTL_LOG': str(self.log),
                    'BATTLEFIELD_POSTGRES_POD': 'battlefield-postgres-0'}

    def run_policy(self, *args, env=None):
        return subprocess.run(['bash', str(SCRIPT), *args], capture_output=True, text=True, env=env or self.env)

    def test_cli_slots_sets_a_value_from_one_to_eight(self):
        for slots in ('1', '8'):
            with self.subTest(slots=slots):
                self.log.unlink(missing_ok=True)
                result = self.run_policy('cli-slots', slots)
                self.assertEqual(result.returncode, 0, result.stderr)
                sent = self.log.read_text()
                self.assertIn('exec -i --namespace battlefield battlefield-postgres-0', sent)
                self.assertIn(f'UPDATE runtime_policy SET cli_concurrency={slots} WHERE id;', sent)

    def test_cli_slots_rejects_other_values_before_contacting_the_cluster(self):
        for value in ('0', '9', '10', '4;DROP', ''):
            with self.subTest(value=value):
                result = self.run_policy('cli-slots', value)
                self.assertEqual(result.returncode, 2)
                self.assertIn('from 1 to 8', result.stderr)
                self.assertFalse(self.log.exists())

    def test_requires_an_explicit_pod(self):
        env = {key: value for key, value in self.env.items() if key != 'BATTLEFIELD_POSTGRES_POD'}
        result = self.run_policy('status', env=env)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.log.exists())


if __name__ == '__main__':
    unittest.main()
