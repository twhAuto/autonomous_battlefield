"""CI credential lifecycle and HTTP boundary regressions; no live credentials."""
from contextlib import redirect_stderr
from datetime import datetime, timedelta, timezone
import importlib.util
import io
import json
import os
import subprocess
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'scripts/lib'))
import access_service
spec = importlib.util.spec_from_file_location('verify', ROOT / 'scripts/ops/verify-access-service.py')
verify = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verify)
ORIGIN = 'https://play.example.com'
ISSUER = 'https://team.cloudflareaccess.com'
CLIENT_ID = 'a' * 32 + '.access'
NOW = datetime(2030, 1, 1, tzinfo=timezone.utc)


def credentials():
    return dict(client_id=CLIENT_ID, client_secret='b' * 64, origin=ORIGIN, expires_at='2031-01-01T00:00:00Z')


class CredentialsTests(unittest.TestCase):
    def test_credentials_and_expiry(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'token'
            data = credentials()
            path.write_text(json.dumps(data))
            path.chmod(0o600)
            self.assertEqual(access_service.load_credentials(path, ORIGIN, CLIENT_ID, NOW), data)
            for key, value in [('client_id', 'unknown'), ('client_secret', 'secret\nheader'),
                               ('expires_at', 'yesterday'), ('expires_at', '2030-02-30T00:00:00Z'),
                               ('expires_at', '2030-01-01T00:00:00Z'), ('origin', 'http://play.example.com'),
                               ('origin', ORIGIN + '/path'), ('origin', 'https://attacker.invalid'), ('origin', ORIGIN+'?token=bad')]:
                with self.subTest(key=key, value=value):
                    path.write_text(json.dumps({**data, key: value}))
                    with self.assertRaises(ValueError):
                        access_service.load_credentials(path, ORIGIN, CLIENT_ID, NOW)
            path.write_text(json.dumps(data))
            with self.assertRaisesRegex(ValueError, 'client ID does not match'):
                access_service.load_credentials(path, ORIGIN, '', NOW)
            path.chmod(0o644)
            with self.assertRaisesRegex(ValueError, 'private'):
                access_service.load_credentials(path, ORIGIN, CLIENT_ID, NOW)
            path.chmod(0o600)
            with redirect_stderr(io.StringIO()) as output:
                access_service.load_credentials(path, ORIGIN, CLIENT_ID, datetime(2030,12,15,tzinfo=timezone.utc))
            self.assertIn('WARNING', output.getvalue())
            self.assertNotIn(data['client_secret'], output.getvalue())
            with self.assertRaisesRegex(ValueError, 'one hour'):
                access_service.load_credentials(path, ORIGIN, CLIENT_ID, datetime(2030,12,31,23,30,tzinfo=timezone.utc))
            path.write_text('{bad JSON secret}')
            with self.assertRaisesRegex(ValueError, '^Invalid service token JSON$'):
                access_service.load_credentials(path, ORIGIN, CLIENT_ID, NOW)

    def test_predeployment_configuration_match(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'token'
            app = Path(directory) / '.env'
            data = credentials()
            data['expires_at'] = (datetime.now(timezone.utc)+timedelta(days=90)).strftime('%Y-%m-%dT%H:%M:%SZ')
            path.write_text(json.dumps(data))
            path.chmod(0o600)
            base = f'BATTLEFIELD_AUTH_MODE=cloudflare\nBATTLEFIELD_PUBLIC_ORIGIN={ORIGIN}\n'
            command = [sys.executable, str(ROOT/'scripts/lib/access_service.py'), '--origin', ORIGIN, '--application', str(app)]
            for client_id, success in [(CLIENT_ID, True), ('', False), ('c'*32+'.access', False)]:
                app.write_text(base + f'BATTLEFIELD_ACCESS_CI_CLIENT_ID={client_id}\n')
                result = subprocess.run(command, env={**os.environ, 'BATTLEFIELD_ACCESS_SERVICE_TOKEN_FILE':str(path)}, capture_output=True, text=True)
                self.assertEqual(result.returncode == 0, success)
                self.assertNotIn(data['client_secret'], result.stdout + result.stderr)


class Response(io.BytesIO):
    def __init__(self, status, body=b''):
        super().__init__(body)
        self.status = status


class VerificationTests(unittest.TestCase):
    def client(self, session_role='ci', rejected=False):
        requests = []
        def send(request, timeout):
            requests.append(request)
            if request.full_url.startswith('http://127.0.0.1:'):
                self.assertIsNone(request.get_header('Cf-access-client-secret'))
                return Response(401)
            self.assertTrue(request.full_url.startswith(ORIGIN+'/'))
            if not request.get_header('Cf-access-client-secret'):
                return Response(302)
            self.assertEqual(request.get_header('Cf-access-client-secret'), credentials()['client_secret'])
            if rejected:
                return Response(401)
            if request.selector == '/v1/auth/session':
                return Response(200, json.dumps(dict(mode='cloudflare', authenticated=True,
                    actor=dict(id=ISSUER+'|service:'+CLIENT_ID,role=session_role),capabilities=dict(admin=False))).encode())
            if request.selector in ('/v1/scenarios', '/livez'):
                return Response(200,b'[]')
            return Response(403)
        client = unittest.mock.Mock()
        client.open.side_effect = send
        return client, requests

    def test_public_identity_and_private_denial(self):
        client, requests = self.client()
        with patch.object(verify.urllib.request, 'build_opener', return_value=client):
            verify.verify('http://127.0.0.1:18081', ORIGIN, credentials(), ISSUER)
        self.assertEqual(len(requests), 13)
        self.assertTrue(any(r.selector == '/v1/admin/model-access' for r in requests))
        self.assertTrue(any(r.get_header('Origin') == ORIGIN for r in requests))

    def test_fail_closed(self):
        for role, rejected in [('admin', False), ('ci', True)]:
            client, _ = self.client(role, rejected)
            with patch.object(verify.urllib.request, 'build_opener', return_value=client):
                with self.assertRaises(ValueError):
                    verify.verify('http://127.0.0.1:18081', ORIGIN, credentials(), ISSUER)
        with self.assertRaisesRegex(ValueError, 'localhost'):
            verify.verify('http://attacker.invalid:8080', ORIGIN, credentials(), ISSUER)

    def test_connection_failures_retry_briefly_but_statuses_do_not(self):
        client, requests = self.client()
        send = client.open.side_effect
        failures = {'/livez': 2}
        def flaky(request, timeout):
            if failures.get(request.selector, 0):
                failures[request.selector] -= 1
                raise urllib.error.URLError('connection reset')
            return send(request, timeout)
        client.open.side_effect = flaky
        with patch.object(verify.urllib.request, 'build_opener', return_value=client), \
                patch.object(verify.time, 'sleep') as sleep:
            verify.verify('http://127.0.0.1:18081', ORIGIN, credentials(), ISSUER)
        self.assertEqual(sleep.call_count, 2)
        client.open.side_effect = unittest.mock.Mock(side_effect=urllib.error.URLError('down'))
        with patch.object(verify.time, 'sleep') as sleep, \
                self.assertRaisesRegex(ValueError, '/livez: connection or TLS verification failed'):
            verify.check(client, ORIGIN, '/livez', (200,))
        self.assertEqual(sleep.call_count, verify.CONNECT_ATTEMPTS - 1)
        client.open.side_effect = lambda request, timeout: Response(503)
        with patch.object(verify.time, 'sleep') as sleep, self.assertRaisesRegex(ValueError, 'received 503'):
            verify.check(client, ORIGIN, '/livez', (200,))
        sleep.assert_not_called()

    def test_redirect_is_not_followed(self):
        # Exercise urllib's actual redirect machinery without network I/O.
        class Redirect(urllib.request.HTTPSHandler):
            def https_open(self, request):
                response = urllib.response.addinfourl(io.BytesIO(b''),
                    {'Location': 'https://attacker.invalid/'}, request.full_url, 302)
                response.msg = 'Found'
                return response
        client = urllib.request.build_opener(urllib.request.ProxyHandler({}), verify.NoRedirect(), Redirect())
        with self.assertRaisesRegex(ValueError, 'received 302'):
            verify.check(client, ORIGIN, '/v1/auth/session', (200,), {'CF-Access-Client-Secret': 'test'})


if __name__ == '__main__':
    unittest.main()
