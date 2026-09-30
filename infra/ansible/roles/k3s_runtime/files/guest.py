#!/usr/bin/env python3
"""Inspect/register a managed guest without disclosing Kubernetes credentials."""
import argparse
import base64
import hashlib
import json
import os
import re
from pathlib import Path
import subprocess
import time
import urllib.request
import urllib.error
import urllib.parse
import ssl

ROOT = Path('/srv/battlefield')
CONTRACT = Path('/etc/battlefield-k3s/contract.json')
INTENT = ROOT / 'platform/intent.json'
IDENTITY = ROOT / 'platform/identity.json'
NS = 'battlefield-platform-checks'
IMMUTABLE = ('storage_id', 'pod_cidr', 'service_cidr', 'cluster_dns', 'datastore')


def run(*args, allowed=(0,), timeout=60):
    result = subprocess.run(args, capture_output=True, text=True, timeout=timeout, check=False)
    if result.returncode not in allowed:
        raise ValueError('Runtime command failed: ' + ' '.join(args) + ': ' + result.stderr.strip()[-1000:])
    return result.stdout.strip()


def kubectl(*args):
    return run('/usr/local/bin/k3s', 'kubectl', '--kubeconfig=/etc/rancher/k3s/k3s.yaml', '--request-timeout=30s', *args, timeout=330)


def get(*args):
    return json.loads(kubectl('get', *args, '-o', 'json'))


def read(path):
    for parent in (path, *path.parents):
        if parent.is_symlink():
            raise ValueError('Runtime registration must not traverse symlinks')
    if path.exists():
        if not path.is_file() or path.stat().st_uid != 0 or path.stat().st_mode & 0o022:
            raise ValueError('Runtime registration is not a root-owned regular file')
        return json.loads(path.read_text())
    return None


def create(path, value):
    read(path)
    if path.exists():
        raise ValueError('Runtime registration already exists')
    with path.open('x') as stream:
        os.chmod(path, 0o600)
        json.dump(value, stream, sort_keys=True)
        stream.flush()
        os.fsync(stream.fileno())


def immutable(c):
    return {key: c[key] for key in IMMUTABLE}


def inspect(c):
    old = read(CONTRACT)
    intent = read(INTENT)
    installed = Path('/usr/local/bin/k3s').exists()
    state = ROOT / 'k3s/server/db/state.db'
    if old is not None and immutable(old) != immutable(c):
        raise ValueError('Cluster storage/network identity changed; an explicit migration is required')
    if intent is not None and intent != immutable(c):
        raise ValueError('Retained runtime belongs to another cluster configuration')
    if (installed or state.exists()) and intent is None:
        raise ValueError('Unregistered K3s runtime exists; refusing adoption')
    if installed and old is None:
        raise ValueError('Installed K3s has no managed configuration')
    node_config = Path('/etc/rancher/node')
    if installed and (not node_config.is_symlink() or node_config.resolve() != ROOT / 'node'):
        raise ValueError('K3s node registration must use the retained directory')
    for binary in ('kind', 'kubeadm'):
        if any((Path(directory) / binary).exists() for directory in ('/usr/local/bin', '/usr/bin')):
            raise ValueError('Guest already contains an incompatible Kubernetes bootstrap')
    identity = read(IDENTITY)
    if identity:
        token = ROOT / 'k3s/server/token'
        if not state.exists() or not token.exists():
            raise ValueError('Registered SQLite state/token is missing; restore it before starting K3s')
        password = ROOT / 'node/password'
        if not password.is_file() or hashlib.sha256(password.read_bytes()).hexdigest() != identity.get('node_password_sha256'):
            raise ValueError('Retained node registration password changed or is missing')
        if hashlib.sha256(token.read_bytes()).hexdigest() != identity['token_sha256']:
            raise ValueError('Registered server token changed; restore it before starting K3s')
    running = installed and run('systemctl', 'is-active', 'k3s', allowed=(0, 3, 4)) == 'active'
    if identity and running:
        if live_identity() != identity:
            raise ValueError('Live cluster identity changed since registration')
    return {'installed': installed, 'running': running, 'registered': identity is not None}


def live_identity():
    token = ROOT / 'k3s/server/token'
    return {'cluster_uid': get('namespace', 'kube-system')['metadata']['uid'],
            'token_sha256': hashlib.sha256(token.read_bytes()).hexdigest(),
            'node_password_sha256': hashlib.sha256((ROOT / 'node/password').read_bytes()).hexdigest()}


def register(c):
    inspect(c)
    current = live_identity()
    old = read(IDENTITY)
    if old is None:
        create(IDENTITY, current)
    elif old != current:
        raise ValueError('Cluster UID or server token changed; refusing a silent replacement')


def wait(check, description, timeout=300):
    end = time.monotonic() + timeout
    while True:
        try:
            result = check()
            if result:
                return result
        except (ValueError, OSError, subprocess.SubprocessError):
            pass
        if time.monotonic() >= end:
            raise ValueError(description + ' did not become healthy')
        time.sleep(3)


def probe(name, *args):
    return kubectl('exec', '-n', NS, 'deployment/' + name, '--', *args)


def gateway():
    services = get('services', '-n', 'envoy-gateway-system')['items']
    selected = [service for service in services if service['metadata'].get('labels', {}).get('gateway.envoyproxy.io/owning-gateway-name') == 'platform'
                and service['metadata'].get('labels', {}).get('gateway.envoyproxy.io/owning-gateway-namespace') == NS]
    if len(selected) != 1 or selected[0]['spec']['type'] != 'NodePort':
        raise ValueError('Expected exactly one private validation gateway service')
    ports = [port for port in selected[0]['spec']['ports'] if port.get('port') == 8080 and port.get('protocol', 'TCP') == 'TCP']
    if len(ports) != 1 or not ports[0].get('nodePort'):
        raise ValueError('Expected one private HTTP validation listener')
    return ports[0]['nodePort']


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def registry_manifest(image, auths, opener=None):
    match = re.fullmatch(r'([^/]+)/([a-z0-9_.-]+(?:/[a-z0-9_.-]+)*)@sha256:([0-9a-f]{64})', image)
    if not match:
        raise ValueError('Registry probe requires a digest-pinned image')
    host, repository, digest = match.groups()
    if host not in auths or any(part in ('.', '..') for part in repository.split('/')):
        raise ValueError('Registry credentials do not belong to the probe image host')
    auth = auths[host]['auth']
    base64.b64decode(auth, validate=True)
    opener = opener or urllib.request.build_opener(NoRedirect())
    headers = {'Authorization': 'Basic ' + auth,
               'Accept': 'application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json'}
    url = 'https://' + host + '/v2/' + repository + '/manifests/sha256:' + digest
    def fetch(target, request_headers):
        with opener.open(urllib.request.Request(target, headers=request_headers), timeout=30) as response:
            return response.read(8 * 1024 * 1024)
    try:
        try:
            data = fetch(url, headers)
        except urllib.error.HTTPError as error:
            challenge = error.headers.get('WWW-Authenticate', '')
            # The production registry is GHCR. Never forward a PAT to an arbitrary
            # challenge realm or redirect. The disposable fixture uses Basic/TLS.
            fields = dict(re.findall(r'(realm|service|scope)="([^"\n]+)"', challenge))
            if error.code != 401 or not challenge.startswith('Bearer ') or host != 'ghcr.io' or fields.get('realm') != 'https://ghcr.io/token' or fields.get('service') != 'ghcr.io':
                raise ValueError('Registry authentication failed') from None
            query = urllib.parse.urlencode({'service': 'ghcr.io', 'scope': 'repository:' + repository + ':pull'})
            token = json.loads(fetch('https://ghcr.io/token?' + query, {'Authorization': 'Basic ' + auth}))['token']
            data = fetch(url, dict(headers, Authorization='Bearer ' + token))
    except (OSError, KeyError, ValueError):
        raise ValueError('Authenticated registry manifest retrieval failed') from None
    if hashlib.sha256(data).hexdigest() != digest:
        raise ValueError('Registry returned a different image digest')
    return True


def private_registry_access():
    deployment = get('deployment', 'registry-probe', '-n', NS)
    image = deployment['spec']['template']['spec']['containers'][0]['image']
    secret = get('secret', 'battlefield-ghcr', '-n', NS)
    auths = json.loads(base64.b64decode(secret['data']['.dockerconfigjson'], validate=True))['auths']
    return registry_manifest(image, auths)


def deployments_ready(namespace):
    deployments = get('deployments', '-n', namespace)['items']
    if not deployments:
        return False
    for deployment in deployments:
        spec, status = deployment['spec'], deployment.get('status', {})
        replicas = spec.get('replicas', 1)
        if status.get('observedGeneration', 0) < deployment['metadata']['generation'] or any(
                status.get(key, 0) != replicas for key in ('updatedReplicas', 'readyReplicas', 'availableReplicas')):
            return False
    return True


def node_ready():
    if run('systemctl', 'is-active', 'k3s', allowed=(0, 3, 4)) != 'active':
        return False
    if kubectl('get', '--raw', '/readyz') != 'ok':
        return False
    return any(item.get('type') == 'Ready' and item.get('status') == 'True'
               for item in get('node', 'battlefield-k3s').get('status', {}).get('conditions', []))


def wait_ready(target):
    if target == 'node':
        wait(node_ready, 'Private Kubernetes node and API')
    elif target in ('kube-system', 'envoy-gateway-system', 'kyverno', NS):
        wait(lambda: deployments_ready(target), 'Current deployment generation in ' + target)
    else:
        raise ValueError('Unexpected platform readiness target')


def verify(c):
    wait_ready('node')
    status = inspect(c)
    if not status['registered'] or read(IDENTITY) != live_identity():
        raise ValueError('Persistent cluster identity does not match registration')
    if run('systemctl', 'is-enabled', 'k3s') != 'enabled' or run('systemctl', 'is-active', 'k3s') != 'active':
        raise ValueError('K3s is not enabled and active')
    if c['versions']['k3s'] not in run('/usr/local/bin/k3s', '--version').split():
        raise ValueError('Unexpected K3s binary version')
    node = get('node', 'battlefield-k3s')
    if node['status']['nodeInfo']['kubeletVersion'] != c['versions']['k3s']:
        raise ValueError('Unexpected running kubelet version')
    for ns in ('kube-system', 'envoy-gateway-system', 'kyverno', NS):
        wait_ready(ns)
    for name in ('coredns', 'metrics-server'):
        get('deployment', name, '-n', 'kube-system')
    forbidden = {'traefik', 'svclb-traefik'}
    if forbidden.intersection(item['metadata']['name'] for item in get('deployment', '-n', 'kube-system')['items']):
        raise ValueError('An undeclared public ingress controller is installed')
    pvc = get('pvc', 'persistence', '-n', NS)
    if pvc['status']['phase'] != 'Bound' or pvc['spec']['storageClassName'] != 'battlefield-retained':
        raise ValueError('Retained storage claim is not bound')
    volume = get('pv', pvc['spec']['volumeName'])
    path = volume['spec'].get('hostPath', {}).get('path', '')
    if not path.startswith('/srv/battlefield/storage/') or volume['spec']['persistentVolumeReclaimPolicy'] != 'Retain':
        raise ValueError('PVC storage is outside the retained data filesystem')
    marker = wait(lambda: probe('storage-probe', 'cat', '/data/identity'), 'Retained PVC server')
    old_marker = read(ROOT / 'platform/pvc.json')
    if old_marker is None or old_marker != {'marker': marker, 'pv_uid': volume['metadata']['uid']}:
        raise ValueError('Persistent validation claim changed identity')
    probe('allowed-client', 'nslookup', 'kubernetes.default.svc.cluster.local')
    target = 'http://storage-probe:8080/identity'
    if wait(lambda: probe('allowed-client', 'wget', '-qO-', '-T', '3', target), 'Allowed network-policy path') != marker:
        raise ValueError('Allowed network-policy path returned unexpected content')
    probe('denied-client', 'nslookup', 'storage-probe.' + NS + '.svc.cluster.local')
    # Confirm destination health and the denied client's DNS first. A failed
    # negative probe alone is not proof of network-policy enforcement.
    denied = subprocess.run(['/usr/local/bin/k3s', 'kubectl', '--kubeconfig=/etc/rancher/k3s/k3s.yaml',
        '--request-timeout=15s', 'exec', '-n', NS, 'deployment/denied-client', '--', 'sh', '-c',
        'wget -qO- -T 3 http://storage-probe:8080/identity >/dev/null 2>&1; test "$?" = 1'],
        capture_output=True, timeout=30, check=False)
    if denied.returncode:
        raise ValueError('Denied network-policy path was not rejected as expected')
    port = wait(gateway, 'Private gateway')
    def response():
        with urllib.request.urlopen('http://' + c['guest_ip'] + ':' + str(port) + '/identity', timeout=5) as stream:
            return stream.read().decode().strip()
    if wait(response, 'Gateway route') != marker:
        raise ValueError('Private gateway returned unexpected content')
    ca = ROOT / 'k3s/server/tls/server-ca.crt'
    context = ssl.create_default_context(cafile=str(ca))
    context.load_cert_chain(str(ROOT / 'k3s/server/tls/client-kube-apiserver.crt'), str(ROOT / 'k3s/server/tls/client-kube-apiserver.key'))
    with urllib.request.urlopen('https://' + c['guest_ip'] + ':10250/metrics', context=context, timeout=15) as stream:
        if b'kubelet_' not in stream.read():
            raise ValueError('Trusted kubelet metrics did not return expected metrics')
    metrics = json.loads(kubectl('get', '--raw', '/apis/metrics.k8s.io/v1beta1/nodes'))
    if not metrics.get('items'):
        raise ValueError('Metrics API returned no node metrics')
    if c['observability'] and int(Path('/proc/sys/vm/max_map_count').read_text()) < 262144:
        raise ValueError('Observability memory-map prerequisite is missing')
    run('/usr/bin/python3', '/usr/local/lib/battlefield-k3s/firewall.py', 'verify')
    private_registry_access()
    for name in ('battlefield-k3s-firewall',):
        if run('systemctl', 'is-active', name) != 'active':
            raise ValueError('Private gateway firewall is inactive')
    return {'cluster_identity': True, 'retained_pvc': True, 'dns': True, 'network_policy': True,
            'private_registry_pull': True, 'gateway': True, 'gateway_port': port, 'trusted_kubelet_metrics': True,
            'observability_prerequisites': c['observability'], 'langfuse': False, 'public_tunnel': False}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('action', choices=['inspect', 'begin', 'register', 'verify', 'ready'])
    parser.add_argument('contract')
    args = parser.parse_args()
    if os.geteuid() != 0:
        raise ValueError('Managed runtime checks require root')
    if args.action == 'ready':
        wait_ready(args.contract)
        print(json.dumps({'ready': True}))
        return
    c = json.loads(Path(args.contract).read_text()) if args.contract.startswith('/') else json.loads(base64.b64decode(args.contract, validate=True))
    if args.action == 'inspect':
        result = inspect(c)
    elif args.action == 'begin':
        inspect(c)
        if read(INTENT) is None:
            create(INTENT, immutable(c))
        result = {'intent': True}
    elif args.action == 'register':
        register(c)
        wait_ready(NS)
        pvc = get('pvc', 'persistence', '-n', NS)
        marker = {'marker': probe('storage-probe', 'cat', '/data/identity'), 'pv_uid': get('pv', pvc['spec']['volumeName'])['metadata']['uid']}
        if read(ROOT / 'platform/pvc.json') is None:
            create(ROOT / 'platform/pvc.json', marker)
        result = verify(c)
    else:
        result = verify(c)
    print(json.dumps(result))


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        raise SystemExit('Guest runtime: ' + (str(error) if isinstance(error, ValueError) else 'verification failed'))
