#!/usr/bin/env python3
"""Verify origin authentication through an operator-only port forward; never print JWTs."""
import json
import os
from pathlib import Path
import sys
import urllib.error
import urllib.request

base = sys.argv[1].rstrip('/')
if not base.startswith(('http://127.0.0.1:', 'http://localhost:')):
    raise SystemExit('use an operator-only localhost port forward')
p = Path(os.environ['BATTLEFIELD_ACCESS_TOKEN_FILE'])
if p.stat().st_mode & 0o077:
    raise SystemExit('Access token file must be private (chmod 600)')
token = p.read_text().strip()
origin = os.environ['BATTLEFIELD_SMOKE_ORIGIN']
client = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def check(path, expected, headers=None, data=None):
    r = urllib.request.Request(base + path, headers=headers or {}, data=data)
    try:
        response = client.open(r, timeout=10)
    except urllib.error.HTTPError as e:
        response = e
    if response.status != expected:
        raise SystemExit(f'{path}: expected {expected}, received {response.status}')
    return response.read()


check('/v1/scenarios', 401)
check('/v1/scenarios', 401, {'Cf-Access-Authenticated-User-Email': 'forged@example.com'})
headers = {'Cf-Access-Jwt-Assertion': token}
session = json.loads(check('/v1/auth/session', 200, headers))
if session.get('mode') != 'cloudflare' or not session.get('authenticated'):
    raise SystemExit('origin is not in Cloudflare mode')
check('/v1/scenarios', 200, headers)
check('/v1/auth/logout', 403, headers, b'')
check('/v1/auth/logout', 403, {**headers, 'Origin': 'https://attacker.invalid'}, b'')
check('/v1/auth/login', 404, {**headers, 'Origin': origin, 'Content-Type': 'application/json'}, b'{}')
print('Origin JWT and mutation-origin checks passed. Public edge allow/deny and logout must also be verified through Access.')
