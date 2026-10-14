#!/usr/bin/env python3
"""Exercise test database target checks with synthetic Docker and Go commands."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
CONTAINER = 'a' * 64
URL = 'postgres://battlefield_test:' + 'b' * 48 + '@127.0.0.1:54321/battlefield_test?sslmode=disable'


class DatabaseHarnessTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        for name in ('scripts/test/test-go-coverage.sh', 'scripts/test/with-test-postgres.sh',
                     'scripts/perf/run-query-plans.sh', 'scripts/ci/tool-versions.env'):
            target = self.root / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(ROOT / name, target)
        fixtures = self.root / 'backend/test/performance/query-plans'
        fixtures.mkdir(parents=True)
        for name in ('seed.sql', 'explain.sql'):
            (fixtures / name).write_text('SELECT 1;\n')
        bin_dir = self.root / 'bin'
        bin_dir.mkdir()
        for name in ('docker', 'go'):
            stub = bin_dir / name
            stub.write_text('''#!/usr/bin/env python3
import json, os, pathlib, sys
name = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
with open(os.environ['CALL_LOG'], 'a') as log:
    log.write(json.dumps([name, *args]) + '\\n')
if name == 'go':
    if args[0] == 'env': print('go1.26.0')
    elif args[0] == 'tool': print('total: (statements) 90.0%')
else:
    if args[0] == 'run': print('a' * 64)
    elif args[0] == 'ps': print('a' * 64)
    elif args[0] == 'inspect':
        print('/battlefield-ci-postgres-fixture' if '.Name' in args[2] else 'healthy')
    elif args[0] == 'port': print(os.environ.get('DOCKER_PORT', '127.0.0.1:54321'))
''')
            stub.chmod(0o755)
        self.log = self.root / 'calls.jsonl'
        self.env = {**os.environ, 'PATH': str(bin_dir) + os.pathsep + os.environ['PATH'],
                    'CALL_LOG': str(self.log), 'GO_COVERAGE_FILE': str(self.root / 'coverage.out'),
                    'GO_COVERAGE_SUMMARY_FILE': str(self.root / 'coverage-total.txt')}
        for name in ('TEST_DATABASE_URL', 'TEST_POSTGRES_CONTAINER_ID'):
            self.env.pop(name, None)

    def run_script(self, name, *args):
        return subprocess.run(['bash', str(self.root / 'scripts' / name), *args],
                              env=self.env, capture_output=True, text=True, timeout=10)

    def calls(self):
        return [json.loads(line) for line in self.log.read_text().splitlines()] if self.log.exists() else []

    def test_coverage_requires_database_before_running_go(self):
        result = self.run_script('test/test-go-coverage.sh')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.calls(), [])

    def test_coverage_accepts_harness_database(self):
        self.env['TEST_DATABASE_URL'] = URL
        result = self.run_script('test/test-go-coverage.sh')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(['go', 'test', '-covermode=atomic', '-coverprofile=' + str(self.root / 'coverage.out'), './...'], self.calls())

    def test_query_plans_reject_unverified_targets_before_migration(self):
        for url, container, port in (
            (URL, '', '127.0.0.1:54321'),
            (URL.replace('127.0.0.1', 'database.example'), CONTAINER, '127.0.0.1:54321'),
            (URL + '&host=database.example', CONTAINER, '127.0.0.1:54321'),
            (URL, CONTAINER, '127.0.0.1:54322'),
        ):
            with self.subTest(url=url, container=bool(container)):
                self.log.unlink(missing_ok=True)
                self.env.update(TEST_DATABASE_URL=url, TEST_POSTGRES_CONTAINER_ID=container, DOCKER_PORT=port)
                result = self.run_script('perf/run-query-plans.sh')
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(any(call[0] == 'go' or call[:2] == ['docker', 'exec'] for call in self.calls()), self.calls())

    def test_query_plans_use_only_the_verified_container(self):
        self.env.update(TEST_DATABASE_URL=URL, TEST_POSTGRES_CONTAINER_ID=CONTAINER)
        result = self.run_script('perf/run-query-plans.sh')
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.calls()
        self.assertLess(next(i for i, call in enumerate(calls) if call[:2] == ['docker', 'port']),
                        next(i for i, call in enumerate(calls) if call[0] == 'go'))
        self.assertFalse(any(call[:2] == ['docker', 'ps'] for call in calls))
        self.assertTrue(any(call[:3] == ['docker', 'exec', CONTAINER] for call in calls))

    def test_wrapper_exports_its_container_and_cleans_up(self):
        result = self.run_script('test/with-test-postgres.sh', 'python3', '-c',
                                'import os; print(os.environ["TEST_POSTGRES_CONTAINER_ID"])')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), CONTAINER)
        self.assertIn(['docker', 'rm', '--force', CONTAINER], self.calls())


if __name__ == '__main__':
    unittest.main()
