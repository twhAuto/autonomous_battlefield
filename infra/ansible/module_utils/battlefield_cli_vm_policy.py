"""Immutable CLI VM layout and reviewed-plan policy; no host mutations."""
import hashlib
import ipaddress
import json
import re
import time
import xml.etree.ElementTree as ET

NAME = 'battlefield-agent'
DIRECTORY = '/var/lib/libvirt/images/' + NAME
IP = '192.168.234.2'
MAC = '52:54:00:ba:77:01'
UUID = r'[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}'


def encoded(value):
    return (json.dumps(value, sort_keys=True, indent=2) + '\n').encode()


def digest(value):
    return hashlib.sha256(encoded(value)).hexdigest()


def shape(node):
    return [node.tag, sorted(node.attrib.items()), (node.text or '').strip(), [shape(child) for child in node]]


def xml_digest(text):
    return digest(shape(ET.fromstring(text)))


def require_identity(value):
    if not isinstance(value, str) or not re.fullmatch(UUID, value):
        raise ValueError('Invalid libvirt UUID')
    return value


def memory(node):
    units = {'KiB': 1024, 'MiB': 1024**2, 'GiB': 1024**3}
    if node is None or node.get('unit', 'KiB') not in units:
        raise ValueError('Unknown CLI VM memory unit')
    return int(node.text) * units[node.get('unit', 'KiB')]


def validate_layout(domain, network, network_filter, expected_filter, identities):
    """Validate both live and persistent XML, including unrecognized escape devices."""
    dom, net, filt = map(ET.fromstring, (domain, network, network_filter))
    for name, node in [('domain', dom), ('network', net), ('filter', filt)]:
        if node.findtext('uuid') != require_identity(identities[name]):
            raise ValueError('CLI ' + name + ' UUID differs from reviewed ownership')
    if dom.tag != 'domain' or dom.get('type') != 'kvm' or dom.findtext('name') != NAME:
        raise ValueError('Unexpected CLI domain')
    allowed = {'name', 'uuid', 'memory', 'currentMemory', 'vcpu', 'os', 'features', 'cpu', 'clock',
               'on_poweroff', 'on_reboot', 'on_crash', 'devices', 'seclabel', 'resource'}
    if any(child.tag not in allowed for child in dom):
        raise ValueError('Unrecognized domain extension; inspect it before adoption')
    if memory(dom.find('memory')) != 4 * 1024**3 or int(dom.findtext('vcpu', '0')) != 2 or dom.find('vcpu').get('current', '2') != '2':
        raise ValueError('CLI layout must remain 2 vCPU / 4 GiB')
    if dom.find('currentMemory') is not None and memory(dom.find('currentMemory')) != 4 * 1024**3:
        raise ValueError('CLI current memory differs from the fixed layout')
    os_type = dom.find('os/type')
    if os_type is None or os_type.text != 'hvm' or os_type.get('arch') != 'x86_64' or any(c.tag not in ('type', 'boot') for c in dom.find('os')):
        raise ValueError('Unexpected CLI boot configuration')
    devices = dom.find('devices')
    allowed_devices = {'emulator', 'disk', 'controller', 'interface', 'serial', 'console', 'input', 'audio', 'memballoon'}
    if devices is None or any(c.tag not in allowed_devices for c in devices):
        raise ValueError('Unexpected CLI device or host passthrough')
    if devices.findtext('emulator', '/usr/bin/qemu-system-x86_64') != '/usr/bin/qemu-system-x86_64':
        raise ValueError('Unexpected CLI emulator')
    for input_device in devices.findall('input'):
        if input_device.get('type') not in ('mouse', 'keyboard') or input_device.get('bus') != 'ps2' or any(c.tag not in ('alias', 'address') for c in input_device):
            raise ValueError('CLI input devices must not pass through host input')
    for audio in devices.findall('audio'):
        if audio.get('type') != 'none':
            raise ValueError('CLI audio must not connect to host services')
    for terminal in [*devices.findall('serial'), *devices.findall('console')]:
        if terminal.get('type') != 'pty' or any(c.tag not in ('target', 'alias', 'address', 'source') for c in terminal):
            raise ValueError('Unexpected CLI console transport')
        source = terminal.find('source')
        if source is not None and not re.fullmatch(r'/dev/pts/[0-9]+', source.get('path', '')):
            raise ValueError('Unexpected CLI console source')
    disks = devices.findall('disk')
    if len(disks) != 2:
        raise ValueError('CLI VM must retain exactly its root disk and seed')
    for disk, device, filename, target, bus, fmt in zip(disks, ('disk', 'cdrom'), ('root.qcow2', 'seed.iso'), ('vda', 'sda'), ('virtio', 'sata'), ('qcow2', 'raw')):
        source, dest, driver = disk.find('source'), disk.find('target'), disk.find('driver')
        if (disk.get('type') != 'file' or disk.get('device') != device or source is None or
                source.get('file') != DIRECTORY + '/' + filename or set(source.attrib) - {'file', 'index'} or dest is None or
                dest.get('dev') != target or dest.get('bus') != bus or driver is None or
                driver.get('name') != 'qemu' or driver.get('type') != fmt or
                any(c.tag not in ('driver', 'source', 'target', 'readonly', 'address', 'alias', 'backingStore') for c in disk)):
            raise ValueError('CLI disk path, transport or layout differs')
        backing = disk.find('backingStore')
        if backing is not None and (backing.attrib or list(backing)):
            raise ValueError('CLI disk must be standalone without a backing store')
    if disks[1].find('readonly') is None:
        raise ValueError('CLI seed must be read-only')
    interfaces = devices.findall('interface')
    if len(interfaces) != 1:
        raise ValueError('CLI VM must have exactly one isolated interface')
    nic = interfaces[0]
    if (nic.get('type') != 'network' or nic.find('source') is None or
            nic.find('source').get('network') != NAME or nic.find('mac') is None or
            nic.find('mac').get('address') != MAC or nic.find('model') is None or nic.find('model').get('type') != 'virtio'):
        raise ValueError('CLI NIC identity or network differs')
    source = nic.find('source')
    if set(source.attrib) - {'network', 'portid', 'bridge'} or list(source) or source.get('bridge', 'bf-agent0') != 'bf-agent0':
        raise ValueError('CLI live NIC is attached to an unexpected bridge or port group')
    if any(c.tag not in ('mac', 'source', 'model', 'filterref', 'target', 'alias', 'address') for c in nic):
        raise ValueError('Unexpected CLI interface extension')
    ref = nic.find('filterref')
    if ref is None or shape(ref) != shape(ET.fromstring(f'<filterref filter="{NAME}"><parameter name="IP" value="{IP}"/></filterref>')):
        raise ValueError('CLI interface filter differs')
    if net.tag != 'network' or net.findtext('name') != NAME or any(c.tag not in ('name', 'uuid', 'bridge', 'mac', 'dns', 'ip') for c in net):
        raise ValueError('CLI network must have no forwarding, routes or extensions')
    bridge, dns, ips = net.find('bridge'), net.find('dns'), net.findall('ip')
    if bridge is None or bridge.attrib != {'name': 'bf-agent0', 'stp': 'on', 'delay': '0'} or dns is None or dns.attrib != {'enable': 'no'}:
        raise ValueError('CLI bridge/DNS boundary differs')
    if len(ips) != 1 or ips[0].attrib != {'address': '192.168.234.1', 'netmask': '255.255.255.0'} or list(ips[0]):
        raise ValueError('CLI network must retain its static subnet without DHCP')
    expected = ET.fromstring(expected_filter)
    for node in (filt, expected):
        for child in node.findall('uuid'):
            node.remove(child)
    if shape(filt) != shape(expected):
        raise ValueError('CLI packet filter differs from the reviewed deny-by-default rules')


def no_overlap(networks):
    wanted = ipaddress.ip_network('192.168.234.0/24')
    for item in networks:
        other = ipaddress.ip_network(item, strict=False)
        if other.version == 4 and wanted.overlaps(other):
            raise ValueError('CLI subnet overlaps another host route or libvirt network')


def check_receipt(receipt, request, observation, review_id, now=None):
    age = (time.time() if now is None else now) - receipt.get('created', 0)
    if age < 0 or age > 1800:
        raise ValueError('CLI VM plan expired; run and review a new plan')
    if receipt.get('review_id') != review_id or not re.fullmatch(r'[0-9a-f]{64}', review_id):
        raise ValueError('CLI VM review ID does not match the saved plan')
    if receipt.get('request') != request or receipt.get('observation') != observation:
        raise ValueError('CLI VM source, inputs or host state changed; review a new plan')
