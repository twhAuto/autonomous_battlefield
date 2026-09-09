#!/usr/bin/env python3
"""Run as battlefield-worker inside the VM before enabling CLI profiles."""
import argparse
import os
from pathlib import Path
import socket
import urllib.error
import urllib.request


def connect(host, port):
    try:
        with socket.create_connection((host, port), timeout=2):
            return True
    except OSError:
        return False


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--blocked-host', action='append', required=True, help='Actual home router/device/admin IP, including IPv6 if configured; repeat as needed')
    parser.add_argument('--worker-pid', type=int, required=True, help='Running Go worker PID, not a CLI child')
    args = parser.parse_args()
    failures = []
    if os.geteuid() == 0:
        parser.error('run as the unprivileged battlefield-worker user')
    for p in ('/var/run/docker.sock', '/etc/battlefield-agent/worker.env', f'/proc/{args.worker_pid}/environ'):
        if os.access(p, os.R_OK):
            failures.append(f'worker can read forbidden resource: {p}')
    if not Path(f'/proc/{args.worker_pid}').exists():
        failures.append('worker PID does not exist')
    mounts = Path('/proc/self/mountinfo').read_text()
    if ' - 9p ' in mounts or ' - virtiofs ' in mounts:
        failures.append('host shared filesystem mounted')
    for port in (3128, 18084):
        if not connect('192.168.234.1', port):
            failures.append(f'required private endpoint unavailable: {port}')
    for host in ['192.168.234.1', '1.1.1.1', *args.blocked_host]:
        for port in (22, 53, 80, 443, 5432, 6443, 8080, 9000):
            if connect(host, port):
                failures.append(f'forbidden direct destination reachable: {host}:{port}')
    direct = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        direct.open('http://192.168.234.1:18084/internal/agent/enabled', timeout=3)
        failures.append('private worker API accepted anonymous request')
    except urllib.error.HTTPError as e:
        if e.code != 401:
            failures.append(f'unexpected worker authentication status: {e.code}')
    except OSError:
        failures.append('private MCP bridge is unavailable')
    # Attempt an SSRF through the allowed proxy itself; it must reject private targets.
    try:
        with socket.create_connection(('192.168.234.1', 3128), timeout=3) as s:
            s.sendall(b'CONNECT 192.168.234.1:443 HTTP/1.1\r\nHost: 192.168.234.1\r\n\r\n')
            if b' 403 ' not in s.recv(1024).split(b'\r\n')[0]:
                failures.append('provider proxy did not deny a private destination')
    except OSError:
        failures.append('provider proxy is unavailable')
    if failures:
        raise SystemExit('\n'.join(failures))
    print('Guest boundary checks passed. Also verify host filter attachment, provider calls, IPv6 firewall, and public origin from a separate network.')


if __name__ == '__main__':
    main()
