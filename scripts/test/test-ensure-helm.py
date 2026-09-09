#!/usr/bin/env python3
"""Verify Helm bootstrap reuse, extraction and rejection without network access."""
import hashlib
import importlib.util
import io
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch
import sys

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("ensure_helm", Path(__file__).resolve().parents[1] / "lib/ensure-helm.py")
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class HelmBootstrapTest(unittest.TestCase):
    def test_existing_binary_never_downloads(self):
        with patch.object(module.shutil, "which", return_value="/installed/helm"), patch.object(module.urllib.request, "urlopen") as download:
            self.assertEqual(module.ensure_helm("unused"), "/installed/helm")
            download.assert_not_called()

    def test_verified_archive_extracts_only_binary(self):
        archive = io.BytesIO()
        with tarfile.open(fileobj=archive, mode="w:gz") as package:
            for name in ("linux-amd64/helm", "../../unwanted"):
                member = tarfile.TarInfo(name)
                member.size = 4
                package.addfile(member, io.BytesIO(b"helm"))
        payload = archive.getvalue()
        with tempfile.TemporaryDirectory() as directory, patch.object(module.shutil, "which", return_value=None), patch.object(module.platform, "system", return_value="Linux"), patch.object(module.platform, "machine", return_value="x86_64"), patch.object(module, "HELM_SHA256", hashlib.sha256(payload).hexdigest()), patch.object(module.urllib.request, "urlopen", return_value=io.BytesIO(payload)):
            binary = Path(module.ensure_helm(directory))
            self.assertEqual(binary.read_bytes(), b"helm")
            self.assertEqual(binary.stat().st_mode & 0o777, 0o700)
            self.assertEqual(list(Path(directory).iterdir()), [binary])

    def test_checksum_failure_writes_no_binary(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(module.shutil, "which", return_value=None), patch.object(module.platform, "system", return_value="Linux"), patch.object(module.platform, "machine", return_value="x86_64"), patch.object(module.urllib.request, "urlopen", return_value=io.BytesIO(b"corrupted")):
            with self.assertRaisesRegex(RuntimeError, "checksum mismatch"):
                module.ensure_helm(directory)
            self.assertFalse(list(Path(directory).iterdir()))

    def test_unsupported_platform_does_not_download(self):
        with patch.object(module.shutil, "which", return_value=None), patch.object(module.platform, "system", return_value="Darwin"), patch.object(module.urllib.request, "urlopen") as download:
            with self.assertRaisesRegex(RuntimeError, "Install Helm"):
                module.ensure_helm("unused")
            download.assert_not_called()


if __name__ == "__main__":
    unittest.main()
