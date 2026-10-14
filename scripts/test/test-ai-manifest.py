#!/usr/bin/env python3
"""AI evaluation bundle identity must not depend on its checkout directory."""
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
        for name, output in (
            ('go', 'prompt-v1\t' + 'a' * 64 + '\ttools-v1\t' + 'b' * 64),
            ('git', 'c' * 40),
        ):
            stub = bin_dir / name
            stub.write_text('#!/usr/bin/env python3\nprint(' + repr(output) + ')\n')
            stub.chmod(0o755)
        self.env = {**os.environ, 'PATH': str(bin_dir) + os.pathsep + os.environ['PATH']}

    def checkout(self, name):
        checkout = self.root / name
        script = checkout / 'scripts/ci/generate-ai-manifest.sh'
        script.parent.mkdir(parents=True)
        shutil.copy2(ROOT / 'scripts/ci/generate-ai-manifest.sh', script)
        cases = checkout / 'backend/evals/cases'
        cases.mkdir(parents=True)
        (cases / 'first case.yaml').write_text('fixture: one\n')
        (cases / 'second.yaml').write_text('fixture: two\n')
        return checkout

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
        self.assertNotEqual(original, self.manifest(checkout)['evaluationBundleSha256'])


if __name__ == '__main__':
    unittest.main()
