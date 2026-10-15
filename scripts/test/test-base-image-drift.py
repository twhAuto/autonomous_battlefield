#!/usr/bin/env python3
"""Check the on-demand base-image drift report with a synthetic docker command."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
PINNED = 'sha256:' + 'a' * 64
MOVED = 'sha256:' + 'b' * 64


class BaseImageDriftTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        script = self.root / 'scripts/ops/check-base-image-pins.py'
        script.parent.mkdir(parents=True)
        shutil.copy2(ROOT / 'scripts/ops/check-base-image-pins.py', script)
        self.script = script
        (self.root / 'backend').mkdir()
        (self.root / 'backend/Dockerfile').write_text(
            f'# Base image: golang:1.26-alpine\nFROM golang@{PINNED} AS build\n'
            f'FROM build\n# Base image: alpine:3.23\nFROM alpine@{PINNED}\n')
        bin_dir = self.root / 'bin'
        bin_dir.mkdir()
        docker = bin_dir / 'docker'
        # Prints the digest configured for a reference; an unknown reference fails.
        docker.write_text('#!/usr/bin/env python3\nimport os, sys\n'
                          "digests = dict(item.split('=', 1) for item in os.environ['DIGESTS'].split(','))\n"
                          "reference = sys.argv[-1]\n"
                          "if sys.argv[1:4] != ['buildx', 'imagetools', 'inspect'] or reference not in digests:\n"
                          "    sys.exit(1)\n"
                          "print(f'Name:      {reference}\\nMediaType: application/vnd.oci.image.index.v1+json\\nDigest:    {digests[reference]}')\n")
        docker.chmod(0o755)
        self.env = {**os.environ, 'PATH': str(bin_dir) + os.pathsep + os.environ['PATH']}

    def run_check(self, digests):
        self.env['DIGESTS'] = ','.join(f'{ref}={digest}' for ref, digest in digests.items())
        return subprocess.run([str(self.script)], env=self.env, capture_output=True, text=True, timeout=10)

    def test_current_pins_pass_without_edits(self):
        before = (self.root / 'backend/Dockerfile').read_text()
        result = self.run_check({'golang:1.26-alpine': PINNED, 'alpine:3.23': PINNED})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.count('CURRENT'), 2)
        self.assertEqual((self.root / 'backend/Dockerfile').read_text(), before)

    def test_moved_tag_is_reported_stale(self):
        result = self.run_check({'golang:1.26-alpine': PINNED, 'alpine:3.23': MOVED})
        self.assertEqual(result.returncode, 1)
        self.assertIn(f'STALE      backend/Dockerfile:5 alpine:3.23 pinned {PINNED}, tag now {MOVED}', result.stdout)

    def test_unresolvable_tag_fails(self):
        result = self.run_check({'golang:1.26-alpine': PINNED})
        self.assertEqual(result.returncode, 2)
        self.assertIn('UNRESOLVED backend/Dockerfile:5 alpine:3.23', result.stderr)

    def test_no_pins_fails(self):
        (self.root / 'backend/Dockerfile').write_text('FROM scratch\n')
        self.assertEqual(self.run_check({}).returncode, 2)


if __name__ == '__main__':
    unittest.main()
