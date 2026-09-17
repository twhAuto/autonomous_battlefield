#!/usr/bin/env python3
"""Require a trusted kubelet certificate bundle in a staged Prometheus overlay."""
from pathlib import Path
import ssl
import sys


def configure(directory, ca_file):
    pem = ca_file.read_text()
    if 'PRIVATE KEY' in pem:
        raise ValueError('Kubelet trust bundle must contain only public certificates')
    # Parse the entire bundle before editing the staged overlay. Never TOFU or skip TLS.
    ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT).load_verify_locations(cadata=pem)
    config = directory / 'prometheus.yml'
    source = config.read_text()
    old = '    tls_config:\n      insecure_skip_verify: true'
    if source.count(old) != 1:
        raise ValueError('Expected exactly one development kubelet TLS configuration')
    source = source.replace(old, '    tls_config:\n      ca_file: /etc/prometheus/kubelet-ca.crt\n      insecure_skip_verify: false')
    source = source.replace('    # kind\'s kubelet serving certificate is node-local rather than signed by\n'
        '    # the Kubernetes API CA mounted in this Pod. This direct scrape is confined\n'
        '    # to the disposable local cluster and still authenticates with the service\n'
        '    # account token.\n', '    # Production trusts the operator-approved kubelet certificate bundle.\n')
    config.write_text(source)
    (directory / 'kubelet-ca.crt').write_text(pem)
    with (directory / 'kustomization.yaml').open('a') as stream:
        stream.write('      - kubelet-ca.crt\n')


if __name__ == '__main__':
    try:
        configure(Path(sys.argv[1]), Path(sys.argv[2]))
    except (OSError, ValueError, ssl.SSLError, IndexError):
        raise SystemExit('Production monitoring requires BF_KUBELET_CA_FILE with trusted public kubelet certificates; no insecure fallback.')
