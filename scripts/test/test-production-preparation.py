"""Offline checks for consolidated production configuration and tunnel rendering."""
import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('prep', ROOT/'scripts/ops/prepare-limited-production.py')
prep = importlib.util.module_from_spec(spec)
spec.loader.exec_module(prep)

class PreparationTests(unittest.TestCase):
    def fixture(self, directory):
        app = directory/'.env'
        profile = directory/'deployment.env'
        credential = directory/'tunnel-credentials.json'
        tunnel = '12345678-1234-1234-1234-123456789012'
        values = dict(POSTGRES_DB='battlefield', POSTGRES_USER='battlefield', POSTGRES_PASSWORD='existing',
            DATABASE_URL='postgres://battlefield:existing@battlefield-postgres/battlefield',
            BATTLEFIELD_AGENT_SECRET='a'*64, BATTLEFIELD_MCP_CAPABILITY_SECRET='b'*64,
            BATTLEFIELD_AUTH_MODE='cloudflare', BATTLEFIELD_ACCESS_ISSUER='https://team.cloudflareaccess.com',
            BATTLEFIELD_ACCESS_AUDIENCE='c'*64, BATTLEFIELD_PUBLIC_ORIGIN='https://play.example.com',
            BATTLEFIELD_ACCESS_ROLES='{"admin":["owner@example.com"]}')
        app.write_text(''.join(f'{k}={v}\n' for k,v in values.items()))
        profile.write_text(f'BF_ENV=production\nBF_TUNNEL_ID={tunnel}\nBF_TUNNEL_IMAGE=cloudflare/cloudflared@sha256:'+ 'd'*64+'\nBF_TUNNEL_CREDENTIALS_FILE=tunnel-credentials.json\n')
        credential.write_text(json.dumps(dict(TunnelID=tunnel, TunnelSecret='private', AccountTag='account')))
        credential.chmod(0o600)
        return app, profile, credential

    def test_renders_without_modifying_configuration_or_creating_auth_file(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            app, profile, credential = self.fixture(directory)
            before = [f.read_bytes() for f in (app,profile,credential)]
            output = directory/'scratch'
            prep.prepare(app,profile,output)
            self.assertEqual(before,[f.read_bytes() for f in (app,profile,credential)])
            self.assertFalse((output/'auth.env').exists())
            rendered = json.loads((output/'tunnel.json').read_text())
            self.assertEqual(len(rendered['items']),2)
            self.assertTrue((output/'network-policies.json').exists())
            self.assertNotIn('private',json.dumps(rendered))

    def test_main_reads_the_resolved_application_file_not_one_beside_the_profile(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            app, profile, _ = self.fixture(directory)
            selected = directory/'bound'/'application.env'
            selected.parent.mkdir()
            app.rename(selected)
            resolved = {'BATTLEFIELD_ENV_FILE': str(profile), 'BATTLEFIELD_SECRET_FILE': str(selected)}
            with mock.patch.dict(os.environ, resolved), mock.patch.object(sys, 'argv', ['prepare']), contextlib.redirect_stdout(io.StringIO()) as output:
                prep.main()
            self.assertIn('validated', output.getvalue())
            self.assertFalse(app.exists())

    def test_routes_the_configured_guest_hostname(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            app, profile, credential = self.fixture(directory)
            output = directory/'scratch'
            def routes():
                prep.prepare(app, profile, output)
                config = json.loads(json.loads((output/'tunnel.json').read_text())['items'][0]['data']['config.yml'])
                return [(r.get('hostname'), r.get('originRequest', {}).get('access', {}).get('audTag')) for r in config['ingress']]
            self.assertEqual({hostname for hostname, _ in routes()}, {'play.example.com', None})
            with app.open('a') as env:
                env.write('BATTLEFIELD_ACCESS_GUEST_AUDIENCE=' + 'e'*64 + '\nBATTLEFIELD_GUEST_ORIGIN=https://guest.example.com\n')
            self.assertIn(('guest.example.com', ['e'*64]), routes())

    def test_rejects_invalid_credentials_and_mode(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory=Path(tmp)
            app,profile,credential=self.fixture(directory)
            credential.chmod(0o644)
            with self.assertRaises(ValueError): prep.prepare(app,profile,directory/'scratch')
            credential.chmod(0o600)
            credential.write_text('{"TunnelID":"wrong"}')
            with self.assertRaises(ValueError): prep.prepare(app,profile,directory/'scratch')
            app,profile,credential=self.fixture(directory)
            app.write_text(app.read_text().replace('cloudflare\n','local-password\n'))
            with self.assertRaises(ValueError): prep.prepare(app,profile,directory/'scratch')

    def test_tunnel_rolls_only_when_config_or_credentials_change(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            app, profile, credential = self.fixture(directory)
            output = directory/'scratch'
            def template():
                prep.prepare(app, profile, output)
                return json.loads((output/'tunnel.json').read_text())['items'][1]['spec']['template']
            original = template()
            self.assertEqual(template(), original)
            data = json.loads(credential.read_text())
            data['TunnelSecret'] = 'rotated-secret'
            credential.write_text(json.dumps(data))
            rotated = template()
            first = original['metadata']['annotations']
            second = rotated['metadata']['annotations']
            self.assertEqual(first['checksum/config'], second['checksum/config'])
            self.assertNotEqual(first['checksum/credentials'], second['checksum/credentials'])
            self.assertNotIn('rotated-secret', json.dumps(rotated))
            app.write_text(app.read_text().replace('play.example.com', 'new.example.com'))
            third = template()['metadata']['annotations']
            self.assertNotEqual(second['checksum/config'], third['checksum/config'])
            self.assertEqual(second['checksum/credentials'], third['checksum/credentials'])

    def test_shared_parser_rejects_duplicate_keys(self):
        with tempfile.TemporaryDirectory() as tmp:
            file=Path(tmp)/'.env'
            file.write_text('KEY=one\nKEY=two\n')
            with self.assertRaises(ValueError): prep.read_env(file)

if __name__ == '__main__': unittest.main()
