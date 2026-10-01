#!/usr/bin/env python3
"""Read-only checks for the AB-128 platform consumed by application delivery."""
import json
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]


def kube(context, *args):
    return json.loads(subprocess.check_output(
        ['kubectl', '--context', context, '--request-timeout=15s', *args, '-o', 'json'],
        stderr=subprocess.DEVNULL, text=True, timeout=20))


def verify(context, node_name):
    pins = json.loads((ROOT / 'infra/k3s/pins.json').read_text())
    nodes = kube(context, 'get', 'nodes')['items']
    if len(nodes) != 1 or nodes[0]['metadata']['name'] != node_name:
        raise ValueError('Expected the registered single-node K3s platform')
    node = nodes[0]
    if node['status']['nodeInfo']['kubeletVersion'] != pins['k3s']['version']:
        raise ValueError('K3s version differs from the managed platform pin')
    if not any(c['type'] == 'Ready' and c['status'] == 'True' for c in node['status']['conditions']):
        raise ValueError('K3s node is not Ready')
    storage = kube(context, 'get', 'storageclass', 'battlefield-retained')
    expected = {'provisioner': 'rancher.io/local-path', 'reclaimPolicy': 'Retain',
                'volumeBindingMode': 'WaitForFirstConsumer', 'parameters': {'nodePath': '/srv/battlefield/storage'}}
    if any(storage.get(key) != value for key, value in expected.items()):
        raise ValueError('Retained storage class does not match the platform contract')
    claims = kube(context, '-n', 'battlefield', 'get', 'pvc')['items']
    for claim in claims:
        if claim['metadata']['name'] not in ('data-battlefield-postgres-0', 'battlefield-elasticsearch-data'):
            continue
        if claim['spec'].get('storageClassName') != 'battlefield-retained':
            raise ValueError('Existing application claim is not on retained storage')
        volume_name = claim['spec'].get('volumeName')
        if volume_name:
            volume = kube(context, 'get', 'pv', volume_name)
            path = volume['spec'].get('hostPath', {}).get('path', '')
            if not Path(path).is_relative_to('/srv/battlefield/storage') or '..' in Path(path).parts:
                raise ValueError('Existing application volume is outside retained storage')
    for namespace, name, image in (
        ('envoy-gateway-system', 'envoy-gateway', 'docker.io/envoyproxy/gateway:' + pins['envoy']['version']),
        ('kyverno', 'kyverno-admission-controller', 'reg.kyverno.io/kyverno/kyverno:' + pins['kyverno']['version'])):
        deployment = kube(context, '-n', namespace, 'get', 'deployment', name)
        containers = deployment['spec']['template']['spec']['containers']
        # Some official manifests omit Docker Hub's default registry prefix.
        if not any(c['image'].removeprefix('docker.io/') == image.removeprefix('docker.io/') for c in containers):
            raise ValueError('Managed controller version differs: ' + name)
        status, desired = deployment.get('status', {}), deployment['spec'].get('replicas', 1)
        if (desired < 1 or status.get('observedGeneration', 0) < deployment['metadata']['generation'] or
                any(status.get(k, 0) != desired for k in ('updatedReplicas', 'readyReplicas', 'availableReplicas'))):
            raise ValueError('Managed controller is not ready: ' + name)


if __name__ == '__main__':
    try:
        action, context, node_name = sys.argv[1:]
        if action != 'verify':
            raise ValueError('Unknown K3s delivery action')
        verify(context, node_name)
    except (ValueError, KeyError, OSError, subprocess.SubprocessError) as error:
        raise SystemExit('K3s delivery: ' + (str(error) if isinstance(error, ValueError) else 'platform inspection failed')) from None
