#!/usr/bin/env python3
"""Capture verified CI reads for an offline ZAP passive scan; never export credentials."""
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import urllib.request

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'lib'))
from access_service import https_origin, load_credentials

PATHS = ('/v1/auth/session', '/v1/scenarios', '/livez')
SAFE_HEADERS = {'content-type', 'content-length', 'cache-control', 'pragma', 'expires',
    'content-security-policy', 'content-security-policy-report-only', 'strict-transport-security',
    'x-content-type-options', 'x-frame-options', 'referrer-policy', 'permissions-policy',
    'cross-origin-opener-policy', 'cross-origin-resource-policy', 'cross-origin-embedder-policy',
    'access-control-allow-origin', 'access-control-allow-credentials', 'server', 'date', 'vary'}


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args):
        return None


def capture(origin, credentials, issuer, client=None):
    origin = https_origin(origin)
    if https_origin(credentials['origin']) != origin:
        raise ValueError('Credential destination mismatch')
    client = client or urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    entries = []
    for path in PATHS:
        headers = {'CF-Access-Client-Id': credentials['client_id'],
                   'CF-Access-Client-Secret': credentials['client_secret']}
        request = urllib.request.Request(origin + path, headers=headers)
        with client.open(request, timeout=15) as response:
            if response.status != 200:
                raise ValueError('Authenticated application content was not reached')
            body = response.read((1 << 20) + 1)
            if len(body) > 1 << 20:
                raise ValueError('Capture response exceeds the size limit')
            text = body.decode('utf-8')
            mime = response.headers.get('Content-Type', '')
            if path.startswith('/v1/'):
                if mime.split(';')[0] != 'application/json':
                    raise ValueError('Expected an application JSON response')
                data = json.loads(text)
                if path.endswith('/session'):
                    actor = data.get('actor', {})
                    if (data.get('authenticated') is not True or data.get('mode') != 'cloudflare'
                            or actor.get('role') != 'ci' or actor.get('id') != issuer + '|service:' + credentials['client_id']
                            or any(data.get('capabilities', {}).values())):
                        raise ValueError('Capture requires the restricted CI identity')
                elif not isinstance(data.get('scenarios'), list):
                    raise ValueError('Application scenario catalog was not reached')
            safe_headers = [{'name': k, 'value': v} for k, v in response.headers.items() if k.lower() in SAFE_HEADERS]
            # Reject reflected credentials instead of emitting or silently sanitizing evidence.
            exported = text + json.dumps(safe_headers)
            if credentials['client_secret'] in exported or re.search(r'eyJ[\w-]{10,}\.[\w-]{10,}\.[\w-]{10,}', exported):
                raise ValueError('Response contains authentication material; refusing to export it')
            entries.append({'startedDateTime': datetime.now(timezone.utc).isoformat(), 'time': 0,
                'request': {'method': 'GET', 'url': origin + path, 'httpVersion': 'HTTP/1.1',
                    'headers': [], 'cookies': [], 'queryString': [], 'headersSize': -1, 'bodySize': 0},
                'response': {'status': 200, 'statusText': 'OK', 'httpVersion': 'HTTP/1.1',
                    'headers': safe_headers, 'cookies': [], 'content': {'size': len(body), 'mimeType': mime, 'text': text},
                    'redirectURL': '', 'headersSize': -1, 'bodySize': len(body)},
                'cache': {}, 'timings': {'send': 0, 'wait': 0, 'receive': 0}})
    return {'log': {'version': '1.2', 'creator': {'name': 'battlefield-ci', 'version': '1'}, 'entries': entries}}


def plan(origin):
    return {'env': {'contexts': [{'name': 'production-ci-read-only', 'urls': [origin]}],
            'parameters': {'failOnError': True, 'failOnWarning': True}},
        'jobs': [
            {'type': 'import', 'parameters': {'type': 'har', 'fileName': '/capture/responses.har'}},
            {'type': 'passiveScan-wait', 'parameters': {'maxDuration': 2}},
            *[{'type': 'report', 'parameters': {'template': template, 'reportDir': '/zap/wrk', 'reportFile': filename}}
              for template, filename in [('traditional-json', 'zap-report.json'), ('traditional-html', 'zap-report.html')]],
            {'type': 'exitStatus', 'parameters': {'errorLevel': 'High', 'errorExitValue': 1, 'warnExitValue': 1}}]}


def main():
    os.umask(0o077)
    origin = https_origin(os.environ['TARGET_URL'])
    helper = Path(__file__).resolve().parents[1] / 'lib/application-env.cjs'
    result = subprocess.run(['node', str(helper), 'json', os.environ['BATTLEFIELD_AUTH_SECRET_FILE']], capture_output=True, check=True)
    app = json.loads(result.stdout)
    if app.get('BATTLEFIELD_AUTH_MODE') != 'cloudflare' or https_origin(app.get('BATTLEFIELD_PUBLIC_ORIGIN')) != origin:
        raise ValueError('Capture target must match the production Access application')
    credentials = load_credentials(os.environ['BATTLEFIELD_ACCESS_SERVICE_TOKEN_FILE'], origin, app.get('BATTLEFIELD_ACCESS_CI_CLIENT_ID', ''))
    har = capture(origin, credentials, app['BATTLEFIELD_ACCESS_ISSUER'].rstrip('/'))
    directory = Path(sys.argv[1])
    (directory / 'responses.har').write_text(json.dumps(har))
    (directory / 'plan.yaml').write_text(json.dumps(plan(origin)))
    print('Verified authenticated response capture: session, scenarios, liveness. Credentials excluded.')


if __name__ == '__main__':
    try:
        main()
    except (ValueError, KeyError, OSError, subprocess.CalledProcessError):
        raise SystemExit('Authenticated ZAP capture failed; check Access, CI identity and application responses. No credentials or response bodies are logged.')
