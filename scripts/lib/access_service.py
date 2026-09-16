"""Private CI service credentials, destination binding, and expiry checks."""
import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import subprocess
import sys
from urllib.parse import urlsplit


def https_origin(value):
    if not isinstance(value, str):
        raise ValueError('Service token origin must be an HTTPS origin')
    parsed = urlsplit(value)
    if (parsed.scheme != 'https' or not parsed.hostname or parsed.username is not None
            or parsed.password is not None or parsed.port not in (None, 443)
            or parsed.path not in ('', '/') or parsed.query or parsed.fragment):
        raise ValueError('Service token origin must be an HTTPS origin')
    return value.rstrip('/')


def load_credentials(path, origin, client_id=None, now=None):
    path = Path(path)
    if not path.is_file() or path.stat().st_mode & 0o077:
        raise ValueError('Service token file must be private (chmod 600)')
    try:
        credentials = json.loads(path.read_text())
    except (ValueError, UnicodeError):
        raise ValueError('Invalid service token JSON') from None
    if not isinstance(credentials, dict):
        raise ValueError('Invalid service token JSON')
    token_id = credentials.get('client_id', '')
    secret = credentials.get('client_secret', '')
    if not isinstance(token_id, str) or not re.fullmatch(r'[a-f0-9]{32}\.access', token_id):
        raise ValueError('Invalid service token client ID')
    # Accept both legacy hex and Cloudflare's prefixed secret format.
    if not isinstance(secret, str) or not re.fullmatch(r'[A-Za-z0-9_-]{32,256}', secret):
        raise ValueError('Invalid service token client secret')
    if client_id is not None and token_id != client_id:
        raise ValueError('Service token client ID does not match application configuration')
    if https_origin(credentials.get('origin')) != https_origin(origin):
        raise ValueError('Service token origin does not match the target; refusing to send credentials')
    expiry = credentials.get('expires_at', '')
    if not isinstance(expiry, str) or not re.fullmatch(r'\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z', expiry):
        raise ValueError('Service token expires_at must be the Cloudflare expiry in UTC (YYYY-MM-DDTHH:MM:SSZ)')
    try:
        expires_at = datetime.strptime(expiry, '%Y-%m-%dT%H:%M:%SZ').replace(tzinfo=timezone.utc)
    except ValueError:
        raise ValueError('Invalid service token expiry date') from None
    remaining = expires_at - (now or datetime.now(timezone.utc))
    if remaining.total_seconds() < 3600:
        raise ValueError('Service token expires within one hour or has expired; rotate the Jenkins credential')
    if remaining.days <= 30:
        print(f'WARNING: CI service token expires in {remaining.days} days; rotate it before expiry.', file=sys.stderr)
    return credentials


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--origin', required=True)
    parser.add_argument('--application', type=Path)
    parser.add_argument('--client-id')
    args = parser.parse_args()
    client_id = args.client_id
    if args.application:
        result = subprocess.run(['node', str(Path(__file__).with_name('application-env.cjs')), 'json', str(args.application)],
                                capture_output=True, text=True, check=True)
        app = json.loads(result.stdout)
        client_id = app.get('BATTLEFIELD_ACCESS_CI_CLIENT_ID', '')
        if app.get('BATTLEFIELD_AUTH_MODE') != 'cloudflare' or https_origin(app.get('BATTLEFIELD_PUBLIC_ORIGIN')) != https_origin(args.origin):
            raise ValueError('CI target must match the registered Cloudflare application origin')
    load_credentials(os.environ['BATTLEFIELD_ACCESS_SERVICE_TOKEN_FILE'], args.origin, client_id)
    print('CI service credential format, destination, identity and expiry checks passed.')


if __name__ == '__main__':
    try:
        main()
    except (ValueError, KeyError, OSError, subprocess.CalledProcessError) as error:
        # Never include raw file contents, subprocess output, or HTTP headers.
        raise SystemExit(str(error) if isinstance(error, ValueError) else 'Cannot validate CI service credentials')
