#!/usr/bin/env python3
"""Retention inventories publish only after every external read succeeds."""
import fcntl
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
RUNNING = 'sha256:' + 'a' * 64
ROLLBACK = 'sha256:' + 'b' * 64


class RetentionReportTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / 'checkout'
        self.script = self.root / 'scripts/ops/ghcr-retention-report.sh'
        self.script.parent.mkdir(parents=True)
        shutil.copy2(ROOT / 'scripts/ops/ghcr-retention-report.sh', self.script)
        library = self.root / 'scripts/lib/deployment-environment.sh'
        library.parent.mkdir()
        # Cluster identity admission is covered by test-deployment-environment.sh.
        library.write_text('battlefield_env_preflight() { :; }\n')
        rollback = self.root / 'artifacts/deployment/rollback-images.env'
        rollback.parent.mkdir(parents=True)
        rollback.write_text('BACKEND_IMAGE_REF=example/backend@' + ROLLBACK + '\n')
        self.cwd = Path(self.temp.name) / 'outside-checkout'
        self.cwd.mkdir()
        bin_dir = Path(self.temp.name) / 'bin'
        bin_dir.mkdir()
        for name in ('kubectl', 'gh'):
            stub = bin_dir / name
            stub.write_text('''#!/usr/bin/env python3
import os, pathlib, sys
name = pathlib.Path(sys.argv[0]).name
if name == 'kubectl':
    if os.environ.get('FAIL_READ') == 'cluster': sys.exit(7)
    if os.environ.get('FAIL_READ') == 'empty': sys.exit(0)
    if os.environ.get('FAIL_READ') == 'tags':
        print('example/backend:latest'); sys.exit(0)
    print('example/backend@sha256:' + 'a' * 64)
elif os.environ.get('FAIL_READ') == 'registry' and 'frontend' in sys.argv[-1]:
    sys.exit(8)
else:
    print('[{"id":1}]')
''')
            stub.chmod(0o755)
        # FAIL_PUBLISH=1 fails the swap of the staged report into place; =both also fails
        # restoring the previous report; =interrupt terminates the script mid-swap.
        # Other moves are real.
        mv = bin_dir / 'mv'
        mv.write_text('#!/usr/bin/env python3\nimport os, sys\n'
                      "mode = os.environ.get('FAIL_PUBLISH')\n"
                      "if mode == 'interrupt' and any('.retention-staging.' in arg for arg in sys.argv):\n"
                      "    import signal; os.kill(os.getppid(), signal.SIGTERM); sys.exit(9)\n"
                      "if mode and any('.retention-staging.' in arg for arg in sys.argv):\n"
                      "    sys.exit(9)\n"
                      "if mode == 'both' and '.retention-retired.' in sys.argv[-2]:\n"
                      "    sys.exit(9)\n"
                      "os.execv(os.environ['REAL_MV'], ['mv', *sys.argv[1:]])\n")
        mv.chmod(0o755)
        self.env = {**os.environ, 'PATH': str(bin_dir) + os.pathsep + os.environ['PATH'],
                    'GH_TOKEN': 'synthetic-test-token', 'REAL_MV': shutil.which('mv')}
        for name in ('REPORT_DIR', 'FAIL_READ', 'FAIL_PUBLISH'):
            self.env.pop(name, None)
        self.report = self.root / 'artifacts/retention'

    def run_report(self):
        return subprocess.run(['bash', str(self.script)], cwd=self.cwd, env=self.env,
                              capture_output=True, text=True, timeout=10)

    def test_reads_and_default_output_are_relative_to_checkout(self):
        result = self.run_report()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.report / 'protected-digests.txt').read_text().splitlines(),
                         [RUNNING, ROLLBACK])
        self.assertTrue((self.report / 'README.txt').is_file())
        self.assertFalse((self.cwd / 'artifacts').exists())

    def test_explicit_report_destination_is_honored(self):
        self.env['REPORT_DIR'] = 'custom-report'
        result = self.run_report()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.cwd / 'custom-report/protected-digests.txt').read_text().splitlines(),
                         [RUNNING, ROLLBACK])

    def test_failed_read_does_not_publish_a_partial_report(self):
        for failure in ('cluster', 'registry', 'empty', 'tags'):
            with self.subTest(failure=failure):
                self.env.update(FAIL_READ=failure, REPORT_DIR=str(self.report))
                result = self.run_report()
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(self.report.exists())

    def test_failed_refresh_preserves_previous_inventory(self):
        self.report.mkdir()
        original = {'README.txt': 'Previous successful inventory\n',
                    'protected-digests.txt': 'previous digest\n',
                    'autonomous-battlefield-backend-versions.json': '[{"id":42}]\n',
                    'autonomous-battlefield-frontend-versions.json': '[{"id":43}]\n'}
        for name, content in original.items():
            (self.report / name).write_text(content)
        for failure in ('cluster', 'registry', 'empty', 'tags'):
            with self.subTest(failure=failure):
                self.env.update(FAIL_READ=failure, REPORT_DIR=str(self.report))
                self.assertNotEqual(self.run_report().returncode, 0)
                self.assertEqual({p.name: p.read_text() for p in self.report.iterdir()}, original)

    def siblings(self):
        return sorted(p.name for p in self.report.parent.iterdir() if p.name != '.retention.lock')

    def previous_report(self):
        self.report.mkdir()
        original = {'README.txt': 'Previous successful inventory\n', 'stale-notes.txt': 'old\n'}
        for name, content in original.items():
            (self.report / name).write_text(content)
        return original

    def test_refresh_replaces_the_whole_report_directory(self):
        self.previous_report()
        result = self.run_report()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(sorted(p.name for p in self.report.iterdir()),
                         ['README.txt', 'autonomous-battlefield-backend-versions.json',
                          'autonomous-battlefield-frontend-versions.json', 'protected-digests.txt'])
        self.assertEqual(self.siblings(), ['deployment', 'retention'])

    def test_failed_swap_restores_previous_report(self):
        original = self.previous_report()
        self.env['FAIL_PUBLISH'] = '1'
        result = self.run_report()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('previous report restored', result.stderr)
        self.assertEqual({p.name: p.read_text() for p in self.report.iterdir()}, original)
        self.assertEqual(self.siblings(), ['deployment', 'retention'])

    def test_failed_restore_keeps_previous_report_beside_destination(self):
        original = self.previous_report()
        self.env['FAIL_PUBLISH'] = 'both'
        result = self.run_report()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.report.exists())
        retired = [p for p in self.report.parent.iterdir() if p.name.startswith('.retention-retired.')]
        self.assertEqual(len(retired), 1)
        self.assertEqual({p.name: p.read_text() for p in retired[0].iterdir()}, original)
        self.assertIn(f'Previous retention report kept at {retired[0]}', result.stderr)

    def test_interrupted_swap_keeps_previous_report(self):
        original = self.previous_report()
        self.env['FAIL_PUBLISH'] = 'interrupt'
        self.assertNotEqual(self.run_report().returncode, 0)
        retired = [p for p in self.report.parent.iterdir() if p.name.startswith('.retention-retired.')]
        self.assertEqual(len(retired), 1)
        self.assertEqual({p.name: p.read_text() for p in retired[0].iterdir()}, original)

    def test_concurrent_run_is_refused(self):
        lock = self.report.parent / '.retention.lock'
        with open(lock, 'a') as handle:
            fcntl.flock(handle, fcntl.LOCK_EX)
            result = self.run_report()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Another retention report run', result.stderr)
        self.assertFalse(self.report.exists())


if __name__ == '__main__':
    unittest.main()
