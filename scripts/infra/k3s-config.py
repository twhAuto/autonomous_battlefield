#!/usr/bin/env python3
"""Declared, private K3s platform configuration; application delivery remains separate."""
import base64
import hashlib
import ipaddress
import json
import re

NS = 'battlefield-platform-checks'
BUSYBOX = 'docker.io/library/busybox@sha256:dc88b80842580654294472735332ec7c7789a4010ec9a68b69f9b88c19abede6'


def validate(settings, networks):
    if set(settings) != {'schema', 'pod_cidr', 'service_cidr', 'cluster_dns', 'datastore', 'observability', 'langfuse'}:
        raise ValueError('Unexpected K3s settings')
    if settings['schema'] != 1 or settings['datastore'] != 'sqlite':
        raise ValueError('Only the single-server SQLite profile is supported')
    if type(settings['observability']) is not bool or settings['langfuse'] is not False:
        raise ValueError('Langfuse remains disabled until its AB-129 delivery profile is available')
    pod, service = [ipaddress.IPv4Network(settings[key]) for key in ('pod_cidr', 'service_cidr')]
    if not pod.is_private or not service.is_private or not 16 <= pod.prefixlen <= 20 or not 16 <= service.prefixlen <= 24:
        raise ValueError('K3s needs private IPv4 pod /16-/20 and service /16-/24 ranges')
    if pod.overlaps(service) or any(n.overlaps(ipaddress.ip_network(other, strict=False)) for n in (pod, service) for other in networks):
        raise ValueError('K3s networks overlap an existing host, VM or container route')
    dns = ipaddress.IPv4Address(settings['cluster_dns'])
    if dns not in service or int(dns) - int(service.network_address) < 2 or dns == service.broadcast_address:
        raise ValueError('Cluster DNS must be an available address in the service range')
    return settings


def registry(value):
    if not isinstance(value, dict) or set(value) != {'username', 'password', 'probe_image'}:
        raise ValueError('Expected private registry username, password and digest-pinned probe image')
    if any(not isinstance(value[k], str) or not value[k] or any(c in value[k] for c in '\r\n\0') for k in value):
        raise ValueError('Invalid registry inputs')
    if not re.fullmatch(r'ghcr.io/twh09/autonomous-battlefield-backend@sha256:[0-9a-f]{64}', value['probe_image']):
        raise ValueError('The private pull probe requires a verified backend image digest')
    return value


def contract(host, connection, size, settings, pins):
    storage_id = hashlib.sha256(json.dumps([host['machine_id'], host['data_uuid'], connection['data_volume']], separators=(',', ':')).encode()).hexdigest()
    return dict(settings, storage_id=storage_id, domain_uuid=connection['domain_uuid'], data_bytes=size,
                guest_ip=connection['address'], host_ip=host['gateway'],
                versions={name: value['version'] for name, value in pins.items() if name != 'schema'})


def server_config(c):
    return {'node-name': 'battlefield-k3s', 'node-ip': c['guest_ip'], 'advertise-address': c['guest_ip'],
            'tls-san': [c['guest_ip'], 'battlefield-k3s'], 'data-dir': '/srv/battlefield/k3s',
            'default-local-storage-path': '/srv/battlefield/storage', 'write-kubeconfig-mode': '0600',
            'disable': ['traefik', 'servicelb'], 'cluster-cidr': c['pod_cidr'], 'service-cidr': c['service_cidr'],
            'cluster-dns': c['cluster_dns'], 'flannel-backend': 'vxlan', 'prefer-bundled-bin': True,
            'kubelet-arg': ['root-dir=/srv/battlefield/kubelet'],
            'kube-proxy-arg': ['nodeport-addresses=' + c['guest_ip'] + '/32']}


def resource(api, kind, name, namespace=None, **fields):
    meta = {'name': name, 'labels': {'app.kubernetes.io/managed-by': 'battlefield-platform'}}
    if namespace:
        meta['namespace'] = namespace
    return dict(apiVersion=api, kind=kind, metadata=meta, **fields)


def items(values):
    return {'apiVersion': 'v1', 'kind': 'List', 'items': values}


def namespaces():
    return items([resource('v1', 'Namespace', name) for name in (NS, 'battlefield')])


def secrets(credentials):
    auth = base64.b64encode((credentials['username'] + ':' + credentials['password']).encode()).decode()
    payload = base64.b64encode(json.dumps({'auths': {credentials['probe_image'].split('/')[0]: {'auth': auth}}}).encode()).decode()
    return items([resource('v1', 'Secret', 'battlefield-ghcr', namespace, type='kubernetes.io/dockerconfigjson',
                           data={'.dockerconfigjson': payload}) for namespace in (NS, 'battlefield')])


def deployment(name, container, **pod):
    return resource('apps/v1', 'Deployment', name, NS, spec={
        'replicas': 1, 'strategy': {'type': 'Recreate'}, 'selector': {'matchLabels': {'app': name}},
        'template': {'metadata': {'labels': {'app': name}}, 'spec': {
            'automountServiceAccountToken': False, 'containers': [dict(container,
                resources={'requests': {'cpu': '10m', 'memory': '16Mi'}, 'limits': {'memory': '64Mi'}})], **pod}}})


def platform(c, probe_image):
    values = [resource('storage.k8s.io/v1', 'StorageClass', 'battlefield-retained',
              provisioner='rancher.io/local-path', reclaimPolicy='Retain', volumeBindingMode='WaitForFirstConsumer',
              parameters={'nodePath': '/srv/battlefield/storage'}),
        resource('v1', 'PersistentVolumeClaim', 'persistence', NS, spec={
            'accessModes': ['ReadWriteOnce'], 'storageClassName': 'battlefield-retained',
            'resources': {'requests': {'storage': '64Mi'}}}),
        deployment('storage-probe', {'name': 'web', 'image': BUSYBOX,
            'command': ['sh', '-ec', 'test -f /data/identity || cat /proc/sys/kernel/random/uuid > /data/identity; exec httpd -f -p 8080 -h /data'],
            'ports': [{'containerPort': 8080}], 'volumeMounts': [{'name': 'data', 'mountPath': '/data'}],
            'readinessProbe': {'httpGet': {'path': '/identity', 'port': 8080}}},
            volumes=[{'name': 'data', 'persistentVolumeClaim': {'claimName': 'persistence'}}]),
        resource('v1', 'Service', 'storage-probe', NS, spec={'selector': {'app': 'storage-probe'},
                 'ports': [{'port': 8080, 'targetPort': 8080}]}),
        deployment('allowed-client', {'name': 'client', 'image': BUSYBOX, 'command': ['sleep', 'infinity']}),
        deployment('denied-client', {'name': 'client', 'image': BUSYBOX, 'command': ['sleep', 'infinity']}),
        deployment('registry-probe', {'name': 'client', 'image': probe_image, 'imagePullPolicy': 'Always',
                    'command': ['/probe/busybox', 'sleep', 'infinity'], 'volumeMounts': [{'name': 'probe', 'mountPath': '/probe'}]},
                   imagePullSecrets=[{'name': 'battlefield-ghcr'}], volumes=[{'name': 'probe', 'emptyDir': {}}],
                   initContainers=[{'name': 'copy-probe', 'image': BUSYBOX, 'command': ['cp', '/bin/busybox', '/probe/busybox'],
                                    'volumeMounts': [{'name': 'probe', 'mountPath': '/probe'}]}]),
        resource('networking.k8s.io/v1', 'NetworkPolicy', 'default-deny', NS, spec={
            'podSelector': {}, 'policyTypes': ['Ingress', 'Egress']}),
        resource('networking.k8s.io/v1', 'NetworkPolicy', 'dns', NS, spec={
            'podSelector': {}, 'policyTypes': ['Egress'], 'egress': [{'to': [{'namespaceSelector': {'matchLabels': {
                'kubernetes.io/metadata.name': 'kube-system'}}, 'podSelector': {'matchLabels': {'k8s-app': 'kube-dns'}}}],
                'ports': [{'protocol': proto, 'port': 53} for proto in ('TCP', 'UDP')]}]}),
        resource('networking.k8s.io/v1', 'NetworkPolicy', 'allow-probe', NS, spec={
            'podSelector': {'matchLabels': {'app': 'storage-probe'}}, 'policyTypes': ['Ingress'],
            'ingress': [{'from': [{'podSelector': {'matchLabels': {'app': 'allowed-client'}}},
                                 {'namespaceSelector': {'matchLabels': {'kubernetes.io/metadata.name': 'envoy-gateway-system'}}}],
                         'ports': [{'protocol': 'TCP', 'port': 8080}]}]}),
        resource('networking.k8s.io/v1', 'NetworkPolicy', 'allow-client', NS, spec={
            'podSelector': {'matchLabels': {'app': 'allowed-client'}}, 'policyTypes': ['Egress'],
            'egress': [{'to': [{'podSelector': {'matchLabels': {'app': 'storage-probe'}}}],
                        'ports': [{'protocol': 'TCP', 'port': 8080}]}]}),
        resource('gateway.envoyproxy.io/v1alpha1', 'EnvoyProxy', 'platform-private', 'envoy-gateway-system',
            spec={'provider': {'type': 'Kubernetes', 'kubernetes': {'envoyService': {'type': 'NodePort'},
                'envoyDeployment': {'replicas': 1}}}}),
        resource('gateway.networking.k8s.io/v1', 'GatewayClass', 'battlefield-platform', spec={
            'controllerName': 'gateway.envoyproxy.io/gatewayclass-controller',
            'parametersRef': {'group': 'gateway.envoyproxy.io', 'kind': 'EnvoyProxy', 'name': 'platform-private', 'namespace': 'envoy-gateway-system'}}),
        resource('gateway.networking.k8s.io/v1', 'Gateway', 'platform', NS, spec={
            'gatewayClassName': 'battlefield-platform', 'listeners': [{'name': 'http', 'protocol': 'HTTP', 'port': 8080,
            'allowedRoutes': {'namespaces': {'from': 'Same'}}}]}),
        resource('gateway.networking.k8s.io/v1', 'HTTPRoute', 'persistence', NS, spec={
            'parentRefs': [{'name': 'platform'}], 'rules': [{'backendRefs': [{'name': 'storage-probe', 'port': 8080}]}]})]
    return items(values)
