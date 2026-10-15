#!/usr/bin/env python3
"""AI evaluation bundle identity covers the committed corpus, independent of checkout path."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class AIManifestTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        bin_dir = self.root / 'bin'
        bin_dir.mkdir()
        self.git = shutil.which('git')
        go = bin_dir / 'go'
        go.write_text('#!/usr/bin/env python3\nprint(' + repr('prompt-v1\t' + 'a' * 64 + '\ttools-v1\t' + 'b' * 64) + ')\n')
        # A fixed revision keeps manifests comparable; every other git command is real.
        git = bin_dir / 'git'
        git.write_text('#!/usr/bin/env python3\nimport os, sys\n'
                       "if sys.argv[-2:] == ['rev-parse', 'HEAD']:\n    print('c' * 40)\n"
                       "else:\n    os.execv(os.environ['REAL_GIT'], ['git', *sys.argv[1:]])\n")
        for stub in (go, git):
            stub.chmod(0o755)
        self.env = {**os.environ, 'REAL_GIT': self.git, 'PATH': str(bin_dir) + os.pathsep + os.environ['PATH']}

    def checkout(self, name):
        checkout = self.root / name
        script = checkout / 'scripts/ci/generate-ai-manifest.sh'
        script.parent.mkdir(parents=True)
        shutil.copy2(ROOT / 'scripts/ci/generate-ai-manifest.sh', script)
        cases = checkout / 'backend/evals/cases'
        cases.mkdir(parents=True)
        (cases / 'first case.yaml').write_text('fixture: one\n')
        (cases / 'second.yaml').write_text('fixture: two\n')
        subprocess.run([self.git, 'init', '-q', str(checkout)], check=True)
        self.track(checkout)
        return checkout

    def track(self, checkout):
        subprocess.run([self.git, '-C', str(checkout), 'add', '-A', 'backend/evals'], check=True)

    def manifest(self, checkout):
        output = checkout / 'manifest.json'
        result = subprocess.run(['bash', str(checkout / 'scripts/ci/generate-ai-manifest.sh')],
                                env={**self.env, 'AI_MANIFEST_FILE': str(output)},
                                capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(output.read_text())

    def test_identical_corpus_has_identical_identity_across_checkouts(self):
        self.assertEqual(self.manifest(self.checkout('workspace one')),
                         self.manifest(self.checkout('another/workspace')))

    def test_content_changes_change_bundle_hash(self):
        checkout = self.checkout('workspace')
        original = self.manifest(checkout)['evaluationBundleSha256']
        (checkout / 'backend/evals/cases/second.yaml').write_text('fixture: changed\n')
        self.assertNotEqual(original, self.manifest(checkout)['evaluationBundleSha256'])

    def test_relative_filename_changes_change_bundle_hash(self):
        checkout = self.checkout('workspace')
        original = self.manifest(checkout)['evaluationBundleSha256']
        case = checkout / 'backend/evals/cases/second.yaml'
        case.rename(case.with_name('renamed.yaml'))
        self.track(checkout)
        self.assertNotEqual(original, self.manifest(checkout)['evaluationBundleSha256'])

    def test_untracked_files_do_not_change_bundle_hash(self):
        checkout = self.checkout('workspace')
        original = self.manifest(checkout)['evaluationBundleSha256']
        (checkout / 'backend/evals/cases/local-notes.yaml').write_text('scratch\n')
        self.assertEqual(original, self.manifest(checkout)['evaluationBundleSha256'])
        self.track(checkout)
        self.assertNotEqual(original, self.manifest(checkout)['evaluationBundleSha256'])

    def test_manifest_declares_schema_v3(self):
        self.assertEqual(self.manifest(self.checkout('workspace'))['schemaVersion'],
                         'battlefield.ai-build-manifest.v3')

    def test_empty_tracked_corpus_is_rejected(self):
        checkout = self.checkout('workspace')
        subprocess.run([self.git, '-C', str(checkout), 'rm', '-q', '--cached', '-r', 'backend/evals'], check=True)
        result = subprocess.run(['bash', str(checkout / 'scripts/ci/generate-ai-manifest.sh')],
                                env={**self.env, 'AI_MANIFEST_FILE': str(checkout / 'manifest.json')},
                                capture_output=True, text=True, timeout=10)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('No tracked evaluation files', result.stderr)


if __name__ == '__main__':
    unittest.main()
