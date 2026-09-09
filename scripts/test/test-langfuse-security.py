#!/usr/bin/env python3
"""Check rendered Langfuse workload security, including init containers.

Usage: helm template ... | python3 scripts/test/test-langfuse-security.py
Requires PyYAML in the test environment.
"""
import sys
import yaml

checked = set()
for document in yaml.safe_load_all(sys.stdin):
    if not document or document['kind'] not in ('Deployment', 'StatefulSet', 'Job'):
        continue
    name = document['metadata']['name']
    pod = document['spec']['template']['spec']
    inherited = pod.get('securityContext', {})
    for container in pod.get('initContainers', []) + pod['containers']:
        security = {**inherited, **container.get('securityContext', {})}
        assert security.get('runAsNonRoot') is True, (name, container['name'], 'non-root')
        assert security.get('runAsUser', 1) != 0, (name, 'root UID')
        assert security.get('allowPrivilegeEscalation') is False, (name, 'privilege escalation')
        assert 'ALL' in security.get('capabilities', {}).get('drop', []), (name, 'capabilities')
        assert security.get('seccompProfile', {}).get('type') == 'RuntimeDefault', (name, 'seccomp')
    checked.add(name)
assert {'langfuse-web', 'langfuse-worker', 'langfuse-s3-all-in-one', 'langfuse-postgresql', 'langfuse-redis', 'langfuse-bucket-hook'} <= checked, checked
print('All five workloads, init containers and bucket hook pass security checks.')
