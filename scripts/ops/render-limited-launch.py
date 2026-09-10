#!/usr/bin/env python3
"""Render a fail-closed Tunnel deployment and application auth config; never call Cloudflare."""
import argparse
import json
from pathlib import Path
import re


def render(args):
    if not re.fullmatch(r'[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?', args.hostname) or '.' not in args.hostname:
        raise ValueError('invalid application hostname')
    if not re.fullmatch(r'[a-z0-9][a-z0-9-]*', args.team):
        raise ValueError('invalid Access team name')
    if not re.fullmatch(r'[a-fA-F0-9]{64}', args.audience):
        raise ValueError('Access application audience must be its 64-character AUD tag')
    if not re.fullmatch(r'[a-f0-9-]{36}', args.tunnel_id):
        raise ValueError('invalid tunnel UUID')
    if not re.fullmatch(r'cloudflare/cloudflared@sha256:[a-f0-9]{64}', args.cloudflared_image):
        raise ValueError('provide a verified, digest-pinned cloudflared image')
    roles = {r: [v.strip().lower() for v in getattr(args, r)] for r in ('admin', 'developer', 'player')}
    emails = [e for values in roles.values() for e in values]
    if not roles['admin'] or len(emails) != len(set(emails)) or any(not re.fullmatch(r'[^\s@]+@[^\s@]+\.[^\s@]+', e) for e in emails):
        raise ValueError('assign at least one Admin and each valid email exactly once')
    access = {'access': {'required': True, 'teamName': args.team, 'audTag': [args.audience]}}
    config = {'tunnel': args.tunnel_id, 'credentials-file': '/etc/tunnel/credentials.json',
              'metrics': '0.0.0.0:2000', 'no-autoupdate': True,
              'ingress': [
                  {'hostname': args.hostname, 'path': '^/v1/.*', 'service': 'http://battlefield-api:8080', 'originRequest': access},
                  {'hostname': args.hostname, 'service': 'http://battlefield-frontend:8080', 'originRequest': access},
                  {'service': 'http_status:404'}]}
    cm = {'apiVersion': 'v1', 'kind': 'ConfigMap', 'metadata': {'name': 'battlefield-tunnel'}, 'data': {'config.yml': json.dumps(config)}}
    deployment = {'apiVersion': 'apps/v1', 'kind': 'Deployment', 'metadata': {'name': 'battlefield-tunnel'}, 'spec': {
        'replicas': 2, 'selector': {'matchLabels': {'app': 'battlefield-tunnel'}},
        'template': {'metadata': {'labels': {'app': 'battlefield-tunnel'}}, 'spec': {
            'automountServiceAccountToken': False,
            'securityContext': {'runAsNonRoot': True, 'runAsUser': 65532, 'runAsGroup': 65532, 'fsGroup': 65532, 'seccompProfile': {'type': 'RuntimeDefault'}},
            'containers': [{'name': 'cloudflared', 'image': args.cloudflared_image,
                'args': ['tunnel', '--config', '/etc/cloudflared/config.yml', 'run'],
                'securityContext': {'allowPrivilegeEscalation': False, 'readOnlyRootFilesystem': True, 'capabilities': {'drop': ['ALL']}},
                'resources': {'requests': {'cpu': '100m', 'memory': '128Mi'}, 'limits': {'cpu': '500m', 'memory': '256Mi'}},
                'readinessProbe': {'httpGet': {'path': '/ready', 'port': 2000}},
                'volumeMounts': [{'name': 'config', 'mountPath': '/etc/cloudflared', 'readOnly': True}, {'name': 'credentials', 'mountPath': '/etc/tunnel', 'readOnly': True}]}],
            'volumes': [{'name': 'config', 'configMap': {'name': 'battlefield-tunnel'}},
                        {'name': 'credentials', 'secret': {'secretName': 'battlefield-tunnel', 'defaultMode': 288}}]}}}}
    policies = []
    for component in ('api', 'frontend'):
        peers = [{'podSelector': {'matchLabels': {'app': 'battlefield-tunnel'}}}]
        if component == 'api':
            peers.append({'podSelector': {'matchLabels': {'app.kubernetes.io/name': 'autonomous-battlefield', 'app.kubernetes.io/component': 'frontend'}}})
        policies.append({'apiVersion': 'networking.k8s.io/v1', 'kind': 'NetworkPolicy', 'metadata': {'name': f'battlefield-{component}-tunnel-only'}, 'spec': {
            'podSelector': {'matchLabels': {'app.kubernetes.io/name': 'autonomous-battlefield', 'app.kubernetes.io/component': component}},
            'policyTypes': ['Ingress'], 'ingress': [{'from': peers, 'ports': [{'protocol': 'TCP', 'port': 8080}]}]}})
    out = Path(args.output)
    out.mkdir(parents=True, exist_ok=True)
    (out / 'tunnel.json').write_text(json.dumps({'apiVersion': 'v1', 'kind': 'List', 'items': [cm, deployment, *policies]}, indent=2) + '\n')
    (out / 'tunnel.json').chmod(0o600)
    # No password, signing key, tunnel credentials, or CLI credentials belong in these files.
    return config


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('hostname', 'team', 'audience', 'tunnel-id', 'cloudflared-image', 'output'):
        parser.add_argument('--' + name, required=True)
    for role in ('admin', 'developer', 'player'):
        parser.add_argument('--' + role, action='append', default=[])
    try:
        render(parser.parse_args())
    except ValueError as e:
        parser.error(str(e))
