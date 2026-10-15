#!/usr/bin/env python3
"""Check private origin denial and public CI authentication without logging secrets."""
import json
import os
from pathlib import Path
import sys
import time
import urllib.error
import urllib.request
from urllib.parse import urlsplit

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'lib'))
from access_service import https_origin, load_credentials


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


# Right after a rollout the tunnel can still hold connections to replaced pods, so a
# connection failure is retried briefly. An unexpected HTTP status never is.
CONNECT_ATTEMPTS = 4
CONNECT_RETRY_SECONDS = 5


def check(client, base, path, expected, headers=None, data=None):
    for attempt in range(1, CONNECT_ATTEMPTS + 1):
        request = urllib.request.Request(base + path, headers=headers or {}, data=data)
        try:
            response = client.open(request, timeout=15)
            break
        except urllib.error.HTTPError as error:
            response = error
            break
        except OSError:
            if attempt == CONNECT_ATTEMPTS:
                raise ValueError(f'{path}: connection or TLS verification failed') from None
            time.sleep(CONNECT_RETRY_SECONDS)
    with response:
        if response.status not in expected:
            raise ValueError(f'{path}: expected {expected}, received {response.status}; check Access policy, CI identity and credential expiry')
        return response.read(1 << 20)


def verify(private, origin, credentials, issuer):
    parsed = urlsplit(private)
    if parsed.scheme != 'http' or parsed.hostname not in ('127.0.0.1', 'localhost') or parsed.username or parsed.password or parsed.path or parsed.query or parsed.fragment or not parsed.port:
        raise ValueError('use an operator-only localhost port forward')
    origin = https_origin(origin)
    client = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    check(client, private, '/v1/scenarios', (401,))
    check(client, private, '/v1/scenarios', (401,), {'Cf-Access-Authenticated-User-Email': 'forged@example.com'})
    check(client, private, '/v1/scenarios', (401,), {'Cf-Access-Jwt-Assertion': 'forged.payload.signature'})
    # Even a claimed service identity without a signed assertion grants no access.
    check(client, private, '/v1/scenarios', (401,), {'CF-Access-Client-Id': credentials['client_id']})
    check(client, origin, '/v1/scenarios', (302, 401, 403))
    headers = {'CF-Access-Client-Id': credentials['client_id'], 'CF-Access-Client-Secret': credentials['client_secret']}
    session = json.loads(check(client, origin, '/v1/auth/session', (200,), headers))
    actor = session.get('actor', {})
    if (session.get('mode') != 'cloudflare' or session.get('authenticated') is not True
            or actor.get('role') != 'ci' or actor.get('id') != issuer + '|service:' + credentials['client_id']
            or any(session.get('capabilities', {}).values())):
        raise ValueError('Public session must be the enrolled CI identity with no user/admin capabilities')
    check(client, origin, '/livez', (200,), headers)
    json.loads(check(client, origin, '/v1/scenarios', (200,), headers))
    check(client, origin, '/v1/admin/model-access', (403,), headers)
    check(client, origin, '/v1/battles', (403,), headers)
    check(client, origin, '/v1/auth/logout', (403,), headers, b'')
    check(client, origin, '/v1/auth/logout', (403,), {**headers, 'Origin': 'https://attacker.invalid'}, b'')
    check(client, origin, '/v1/auth/logout', (403,), {**headers, 'Origin': origin}, b'')
    print('Private origin denial and public CI identity, read-only access, and mutation denial: OK')


def main():
    origin = os.environ['BATTLEFIELD_SMOKE_ORIGIN']
    credentials = load_credentials(os.environ['BATTLEFIELD_ACCESS_SERVICE_TOKEN_FILE'], origin,
                                   os.environ['BATTLEFIELD_SMOKE_CI_CLIENT_ID'])
    verify(sys.argv[1], origin, credentials, os.environ['BATTLEFIELD_SMOKE_ISSUER'])


if __name__ == '__main__':
    try:
        main()
    except json.JSONDecodeError:
        raise SystemExit('CI Access verification returned invalid JSON')
    except ValueError as error:
        raise SystemExit(str(error))
    except (KeyError, OSError):
        # JSON parse errors may contain response content; only checks emit safe messages.
        raise SystemExit('CI Access verification failed. Check service credentials, Access policy, deployed CI client ID, issuer and audience.')
