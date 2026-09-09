"""Offline checks for private production preparation and fail-closed rendering."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
def module(name, file):
    spec = importlib.util.spec_from_file_location(name, ROOT / 'scripts/ops' / file)
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result

prep = module('prep', 'prepare-limited-production.py')
renderer = module('renderer', 'render-limited-launch.py')

class PreparationTests(unittest.TestCase):
    def test_rendered_access_and_credentials(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            args = argparse.Namespace(hostname='play.example.com', team='test-team', audience='a'*64,
                tunnel_id='12345678-1234-1234-1234-123456789012', cloudflared_image='cloudflare/cloudflared@sha256:'+'b'*64,
                admin=['owner@example.com'], developer=[], player=[], output=tmp)
            renderer.render(args)
            credential = directory / (args.tunnel_id + '.json')
            credential.write_text(json.dumps({'TunnelID': args.tunnel_id}))
            credential.chmod(0o600)
            self.assertEqual(prep.validate_rendered(directory, directory)['BATTLEFIELD_AUTH_MODE'], 'cloudflare')
            rendered = json.loads((directory / 'tunnel.json').read_text())
            rendered['items'][1]['spec']['template']['spec']['containers'][0]['image'] = 'cloudflare/cloudflared:latest'
            (directory / 'tunnel.json').write_text(json.dumps(rendered))
            with self.assertRaises(ValueError):
                prep.validate_rendered(directory, directory)

    def test_duplicate_env_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp)/'auth.env'
            path.write_text('BATTLEFIELD_AUTH_MODE=cloudflare\nBATTLEFIELD_AUTH_MODE=disabled-development\n')
            with self.assertRaises(ValueError):
                prep.read_env(path)

    def test_existing_credentials_preserved(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp)/'ai.env'
            values = {'BATTLEFIELD_AGENT_SECRET':'a'*64, 'BATTLEFIELD_MCP_CAPABILITY_SECRET':'b'*64}
            values.update({k:'false' for k in ('CODEX_CLI_PROFILE_AVAILABLE','GEMINI_CLI_PROFILE_AVAILABLE','OPENAI_PROFILE_AVAILABLE','GEMINI_PROFILE_AVAILABLE')})
            prep.write_env(path, values)
            original = path.read_bytes()
            with patch.object(prep, 'kube', side_effect=AssertionError('existing file should be reused')):
                prep.prepare_ai(path)
            self.assertEqual(path.read_bytes(), original)
            values['OPENAI_PROFILE_AVAILABLE'] = 'true'
            with self.assertRaises(ValueError): prep.validate_ai(values)
            values['OPENAI_PROFILE_AVAILABLE'] = 'false'
            values['BATTLEFIELD_AGENT_SECRET'] = values['BATTLEFIELD_MCP_CAPABILITY_SECRET']
            with self.assertRaises(ValueError): prep.validate_ai(values)

    def test_missing_file_preserves_cluster_provider_key(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp)/'ai.env'
            from types import SimpleNamespace
            with patch.object(prep, 'kube', return_value=SimpleNamespace(stdout='{"data":{"OPENAI_API_KEY":"cHJpdmF0ZQ=="}}')):
                prep.prepare_ai(path)
            values = prep.read_env(path)
            self.assertEqual(values['OPENAI_API_KEY'], 'private')
            self.assertEqual(values['OPENAI_PROFILE_AVAILABLE'], 'false')
            self.assertNotEqual(values['BATTLEFIELD_AGENT_SECRET'], values['BATTLEFIELD_MCP_CAPABILITY_SECRET'])
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)

if __name__ == '__main__': unittest.main()
