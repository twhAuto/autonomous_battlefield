#!/usr/bin/env python3
"""Validation and review policy for the single retained-data K3s guest."""
import hashlib
import ipaddress
import json
import re
import time

RESOURCES = {'libvirt_pool.k3s', 'libvirt_network.k3s', 'libvirt_volume.base',
             'libvirt_volume.os', 'libvirt_volume.data', 'libvirt_volume.seed', 'libvirt_domain.k3s'}
REPLACEABLE = {'libvirt_volume.os', 'libvirt_volume.seed', 'libvirt_domain.k3s'}
RETAINED = RESOURCES - REPLACEABLE


def config(value):
    required = {'schema', 'cpus', 'memory_mib', 'os_gib', 'data_gib', 'reserve_gib', 'network_cidr', 'guest_ip', 'mac'}
    if not isinstance(value, dict) or set(value) != required or type(value['schema']) is not int or value['schema'] != 1:
        raise ValueError('Invalid VM configuration schema or unknown fields')
    for key, lower, upper in [('cpus', 1, 32), ('memory_mib', 2048, 131072), ('os_gib', 12, 256),
                              ('data_gib', 4, 4096), ('reserve_gib', 2, 1024)]:
        if type(value[key]) is not int or not lower <= value[key] <= upper:
            raise ValueError('Invalid VM setting: ' + key)
    network = ipaddress.ip_network(value['network_cidr'], strict=True)
    address = ipaddress.ip_address(value['guest_ip'])
    if network.version != 4 or network.prefixlen != 24 or not network.is_private:
        raise ValueError('VM network must be a private IPv4 /24')
    if address not in network or not 10 <= int(address) - int(network.network_address) <= 200:
        raise ValueError('VM address must be in the reserved DHCP range .10–.200')
    if not isinstance(value['mac'], str) or not re.fullmatch(r'52:54:00(?::[0-9a-f]{2}){3}', value['mac']):
        raise ValueError('Invalid QEMU guest MAC address')
    return value


def no_overlap(cidr, networks):
    selected = ipaddress.ip_network(cidr)
    for other in networks:
        candidate = ipaddress.ip_network(other, strict=False)
        if candidate.version == 4 and selected.overlaps(candidate):
            raise ValueError('VM subnet overlaps an existing network: ' + str(candidate))


def plan_changes(plan, existing, replace_os=False):
    if not isinstance(plan, dict) or not str(plan.get('format_version', '')).startswith('1.'):
        raise ValueError('Unsupported Terraform plan format')
    changes = plan.get('resource_changes', [])
    if not isinstance(changes, list) or plan.get('errored') or plan.get('complete') is False:
        raise ValueError('Incomplete Terraform plan')
    result = []
    for resource in changes:
        address = resource.get('address')
        if address not in RESOURCES or resource.get('mode') != 'managed':
            raise ValueError('Plan contains an unexpected resource')
        change = resource['change']
        actions = change['actions']
        if actions not in (['no-op'], ['create'], ['update'], ['delete', 'create'], ['create', 'delete'], ['delete']):
            raise ValueError('Unsupported Terraform action')
        if 'delete' in actions and (not replace_os or address not in REPLACEABLE or 'create' not in actions):
            raise ValueError('Plan would destroy a protected resource: ' + address)
        if address in RETAINED and address in existing and 'create' in actions:
            raise ValueError('Retained resource is missing; recover it instead of recreating: ' + address)
        if actions != ['no-op']:
            result.append({'address': address, 'actions': actions})
    return result


def plan_digest(plan):
    # Exclude evaluation timestamps and harmless computed-only refresh of pool
    # capacity; include the full before/after of every actual managed change.
    value = {'resources': [r for r in plan.get('resource_changes', []) if r['change']['actions'] != ['no-op']],
             'outputs': {k: v for k, v in plan.get('output_changes', {}).items() if v['actions'] != ['no-op']}}
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


def receipt(value, revision, identity, state_digest, replace_os):
    if (value.get('schema') != 1 or value.get('revision') != revision or value.get('identity') != identity
            or value.get('state_digest') != state_digest or value.get('replace_os') is not replace_os):
        raise ValueError('VM plan inputs, state or revision changed; review a new plan')
    age = time.time() - value.get('created', 0)
    if age < 0 or age > 1800:
        raise ValueError('VM plan expired; review a new plan')


def inventory(connection, key, known_hosts):
    if set(connection) != {'domain_uuid', 'hostname', 'address', 'user', 'data_device', 'data_volume'}:
        raise ValueError('Unexpected Terraform connection output')
    if any(not isinstance(value, str) for value in connection.values()):
        raise ValueError('Terraform connection facts must be strings')
    if (connection['hostname'] != 'battlefield-k3s' or connection['user'] != 'battlefield'
            or not re.fullmatch(r'[0-9a-f-]{36}', connection['domain_uuid'])
            or ipaddress.ip_address(connection['address']).version != 4):
        raise ValueError('Invalid Terraform connection identity')
    return {'all': {'children': {'k3s': {'hosts': {'battlefield-k3s': {
        'ansible_host': connection['address'], 'ansible_user': connection['user'],
        'ansible_ssh_private_key_file': str(key),
        'ansible_ssh_common_args': '-o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=' + str(known_hosts),
        'battlefield_data_device': connection['data_device'],
        'battlefield_domain_uuid': connection['domain_uuid'],
    }}}}}}
