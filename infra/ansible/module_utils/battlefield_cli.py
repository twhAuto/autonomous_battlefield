"""Read-only CLI bootstrap identity and file ownership checks (also tested offline)."""
import hashlib
import json
import re
from pathlib import Path
import stat
import xml.etree.ElementTree as ET

HOST_FILES = {
    'firewall.nft': '/etc/battlefield-agent/firewall.nft',
    'mcp-proxy.Caddyfile': '/etc/battlefield-agent/mcp-proxy.Caddyfile',
    'provider-domains.txt': '/etc/battlefield-agent/provider-domains.txt',
    'squid.conf': '/etc/squid/squid.conf',
    'squid-override.conf': '/etc/systemd/system/squid.service.d/battlefield.conf',
    **{name + '.service': '/etc/systemd/system/' + name + '.service' for name in (
        'battlefield-agent-firewall', 'battlefield-mcp-bridge', 'battlefield-mcp-proxy')},
}
LOOPBACK = '/etc/systemd/system/battlefield-mcp-bridge.service.d/20-loopback.conf'
HOST_UNITS = ['battlefield-agent-firewall', 'battlefield-mcp-bridge', 'battlefield-mcp-proxy', 'squid']
GUEST_FILES = {'/etc/battlefield-agent/config.yaml', '/etc/battlefield-agent/worker.env',
               '/etc/systemd/system/battlefield-worker.service', '/usr/local/bin/battlefield-agent',
               '/usr/local/bin/verify-agent-isolation.py', '/usr/local/bin/codex',
               '/usr/local/bin/codex-code-mode-host', '/usr/local/bin/claude', '/usr/local/bin/agy'}


def sha(data):
    return hashlib.sha256(data).hexdigest()


def safe_components(path):
    """Do not let a privileged copy traverse a redirected parent or special file."""
    path = Path(path)
    for item in [*reversed(path.parents), path]:
        try:
            metadata = item.lstat()
        except FileNotFoundError:
            continue
        if stat.S_ISLNK(metadata.st_mode):
            raise ValueError('Redirected managed path: ' + str(item))
        if item != path and not stat.S_ISDIR(metadata.st_mode):
            raise ValueError('Invalid managed parent: ' + str(item))
        if metadata.st_uid != 0 or metadata.st_mode & 0o022:
            raise ValueError('Managed path must be root owned and not writable by other users: ' + str(item))
    return path


def package_default(path, metadata):
    """Recognize only dpkg's unmodified conffile; SHA256 binds the selected bytes."""
    import hashlib
    from pathlib import Path
    path = Path(path)
    content = path.read_bytes()
    digest = hashlib.md5(content, usedforsecurity=False).hexdigest()
    for line in metadata.splitlines():
        fields = line.split()
        if fields == [str(path), digest]:
            return hashlib.sha256(content).hexdigest()
    return None


def inspect_files(files, marker, contract, *, verify=False, initial_defaults=None):
    """Initial adoption is exact; owned drift is repairable; release changes need AB-139."""
    marker = safe_components(marker)
    expected = {'schema': 1, 'identity': contract,
                'files': {item['dest']: item['sha256'] for item in files}}
    owned = marker.exists()
    if owned:
        if not marker.is_file() or json.loads(marker.read_text()) != expected:
            raise ValueError('CLI ownership/release contract differs; use the reviewed update workflow, not bootstrap')
    elif verify:
        raise ValueError('CLI bootstrap ownership has not been registered by apply')
    changes, content_changes = [], []
    for item in files:
        path = safe_components(item['dest'])
        if path.exists():
            metadata = path.stat()
            if not stat.S_ISREG(metadata.st_mode):
                raise ValueError('Managed destination is not a regular file: ' + str(path))
            actual = sha(path.read_bytes())
            differs = actual != item['sha256']
            initial_default = str(path) == '/etc/squid/squid.conf' and (initial_defaults or {}).get(str(path)) == actual
            if differs and ((not owned and not initial_default) or str(path) == '/etc/battlefield-agent/worker.env'):
                raise ValueError('Unowned or conflicting CLI file: ' + str(path))
            if differs:
                content_changes.append(str(path))
            if differs or stat.S_IMODE(metadata.st_mode) != int(item['mode'], 8) or metadata.st_gid != 0:
                changes.append(str(path))
        else:
            changes.append(str(path))
            content_changes.append(str(path))
    if verify and changes:
        raise ValueError('CLI managed files need reconciliation: ' + ', '.join(changes))
    return {'owned': owned, 'changes': changes, 'content_changes': content_changes, 'marker': expected}


def xml_shape(element):
    return (element.tag, tuple(sorted(element.attrib.items())), (element.text or '').strip(),
            tuple(xml_shape(child) for child in element if child.tag != 'uuid'))


def validate_boundary(domain_xml, network_xml, filter_xml, expected_filter, contract):
    domain, network, nic_filter = [ET.fromstring(value) for value in (domain_xml, network_xml, filter_xml)]
    if domain.findtext('name') != 'battlefield-agent' or domain.findtext('uuid') != contract['domain_uuid']:
        raise ValueError('CLI domain identity differs from registration')
    if network.findtext('name') != 'battlefield-agent' or network.findtext('uuid') != contract['network_uuid']:
        raise ValueError('CLI network identity differs from registration')
    if network.find('forward') is not None or network.find('ip/dhcp') is not None:
        raise ValueError('CLI network must not provide forwarding or DHCP')
    if network.find('bridge').get('name') != 'bf-agent0' or network.find('dns').get('enable') != 'no':
        raise ValueError('CLI bridge/DNS isolation differs')
    addresses = network.findall('ip')
    if len(addresses) != 1 or addresses[0].attrib != {'address': '192.168.234.1', 'netmask': '255.255.255.0'}:
        raise ValueError('Unexpected CLI network addresses')
    interfaces = domain.findall('devices/interface')
    if len(interfaces) != 1:
        raise ValueError('CLI VM must have exactly one isolated NIC')
    nic = interfaces[0]
    if (nic.get('type') != 'network' or nic.find('source').get('network') != 'battlefield-agent'
            or nic.find('mac').get('address') != '52:54:00:ba:77:01'
            or nic.find('filterref').get('filter') != 'battlefield-agent'
            or [p.attrib for p in nic.findall('filterref/parameter')] != [{'name': 'IP', 'value': '192.168.234.2'}]):
        raise ValueError('CLI NIC isolation differs from the supported contract')
    if any(domain.find('devices/' + name) is not None for name in ('filesystem', 'hostdev', 'redirdev')):
        raise ValueError('CLI VM has host device or filesystem passthrough')
    if xml_shape(nic_filter) != xml_shape(ET.fromstring(expected_filter)):
        raise ValueError('CLI NIC filter differs from the reviewed isolation rules')


def reject_overrides(units, allowed):
    for prefix in ('/etc/systemd/system', '/run/systemd/system', '/usr/lib/systemd/system'):
        for unit in units:
            directory = safe_components(Path(prefix) / (unit + '.service.d'))
            if directory.exists():
                for path in directory.iterdir():
                    if str(path) not in allowed:
                        raise ValueError('Unknown CLI service override: ' + str(path))


def nft_equivalent(current, desired):
    def tokens(value):
        value = re.sub(r'#.*', '', value)
        value = re.sub(r'(?m)^(?:add|flush) table inet battlefield_agent\s*$', '', value)
        value = re.sub(r'packets \d+ bytes \d+', '', value)
        value = re.sub(r'priority filter\s*-\s*10', 'priority -10', value)
        return re.findall(r'[A-Za-z0-9_./:@=-]+|[^\w\s;]', value)
    return tokens(current) == tokens(desired)


def affected_services(kind, changes):
    if kind == 'guest':
        return ['battlefield-worker'] if changes else []
    ownership = {
        'battlefield-agent-firewall': {'firewall.nft', 'battlefield-agent-firewall.service'},
        'battlefield-mcp-bridge': {'battlefield-mcp-bridge.service', '20-loopback.conf'},
        'battlefield-mcp-proxy': {'mcp-proxy.Caddyfile', 'battlefield-mcp-proxy.service'},
        'squid': {'squid.conf', 'provider-domains.txt', 'battlefield.conf'},
    }
    names = {Path(path).name for path in changes}
    return [unit for unit, files in ownership.items() if names & files]
