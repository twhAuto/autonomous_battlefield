#!/usr/bin/python
"""Fail before any CLI bootstrap mutation when identity or existing ownership differs."""
from pathlib import Path
import re
import pwd
import stat
from ansible.module_utils.basic import AnsibleModule
from ansible.module_utils.battlefield_cli import (
    GUEST_FILES, HOST_FILES, HOST_UNITS, LOOPBACK, inspect_files, reject_overrides, validate_boundary, nft_equivalent, affected_services,
)


def main():
    module = AnsibleModule(argument_spec={
        'kind': {'type': 'str', 'choices': ['host', 'guest'], 'required': True},
        'contract': {'type': 'dict', 'required': True},
        'files': {'type': 'list', 'elements': 'dict', 'required': True},
        'filter_xml': {'type': 'str', 'default': ''},
        'adopt_existing': {'type': 'bool', 'default': False},
        'firewall': {'type': 'str', 'default': ''},
        'verify': {'type': 'bool', 'default': False},
    }, supports_check_mode=True)
    params = module.params
    kind, contract, files = params['kind'], params['contract'], params['files']
    def run(command):
        rc, out, _ = module.run_command(command)
        if rc:
            raise ValueError('CLI identity probe failed: ' + command[0])
        return out.strip()
    try:
        if Path('/etc/machine-id').read_text().strip() != contract['machine_id']:
            raise ValueError('CLI target machine-id differs from registration')
        if kind == 'guest':
            if Path('/sys/class/dmi/id/product_uuid').read_text().strip().lower() != contract['domain_uuid']:
                raise ValueError('CLI guest hardware UUID differs from the registered libvirt domain')
            home = Path('/var/lib/battlefield-worker')
            if home.exists() or home.is_symlink():
                try:
                    account = pwd.getpwnam('battlefield-worker')
                except KeyError:
                    raise ValueError('Existing worker home has no registered account') from None
                metadata = home.lstat()
                if not stat.S_ISDIR(metadata.st_mode) or metadata.st_uid != account.pw_uid:
                    raise ValueError('Existing worker home is redirected or belongs to another user')
        allowed = set(HOST_FILES.values()) | {LOOPBACK} if kind == 'host' else GUEST_FILES
        if not files or any(item['dest'] not in allowed for item in files):
            raise ValueError('Unexpected CLI managed destination')
        units = HOST_UNITS if kind == 'host' else ['battlefield-worker']
        reject_overrides(units, {item['dest'] for item in files})
        if kind == 'host':
            def virsh(*args):
                return run(['virsh', '--connect', 'qemu:///system', *args])
            for persistent in (False, True):
                validate_boundary(virsh('dumpxml', 'battlefield-agent', *(['--inactive'] if persistent else [])),
                                  virsh('net-dumpxml', 'battlefield-agent', *(['--inactive'] if persistent else [])),
                                  virsh('nwfilter-dumpxml', 'battlefield-agent'), params['filter_xml'], contract)
            if virsh('domstate', 'battlefield-agent') != 'running':
                raise ValueError('Start the registered CLI VM before host configuration')
            listeners = run(['ss', '-Hlnpt', 'sport = :' + str(contract['bridge_port'])])
            if listeners:
                pid = run(['systemctl', 'show', 'battlefield-mcp-bridge', '--property=MainPID', '--value'])
                for line in listeners.splitlines():
                    if (set(re.findall(r'pid=(\d+)', line)) != {pid}
                            or line.split()[3] != '127.0.0.1:' + str(contract['bridge_port'])):
                        raise ValueError('CLI loopback port belongs to an unexpected listener')
            uid = run(['runuser', '-u', contract['operator'], '--', '/usr/bin/kubectl',
                       '--kubeconfig=' + contract['kubeconfig'], '--context=' + contract['context'],
                       '--request-timeout=15s', 'get', 'namespace', 'kube-system', '-o', 'jsonpath={.metadata.uid}'])
            if uid != contract['kube_system_uid']:
                raise ValueError('CLI bridge Kubernetes identity differs from registration')
        result = inspect_files(files, '/etc/battlefield-cli-bootstrap/' + kind + '.json', contract,
                               verify=params['verify'])
        if kind == 'guest' and params['adopt_existing'] and not result['owned'] and result['content_changes']:
            raise ValueError('Guest adoption requires a complete matching existing installation; use disabled fresh-install inputs on a new VM')
        for unit in units:
            rc, fragment, _ = module.run_command(['systemctl', 'show', unit, '--property=FragmentPath', '--value'])
            fragment = fragment.strip()
            allowed_fragments = ({'/usr/lib/systemd/system/squid.service', '/lib/systemd/system/squid.service'}
                                 if unit == 'squid' else {'/etc/systemd/system/' + unit + '.service'})
            if fragment and fragment not in allowed_fragments:
                raise ValueError('CLI service is shadowed by an unexpected unit: ' + unit)
        if kind == 'host':
            nft = module.get_bin_path('nft')
            rc, current, _ = module.run_command([nft, 'list', 'table', 'inet', 'battlefield_agent']) if nft else (1, '', '')
            if rc or not nft_equivalent(current, params['firewall']):
                if not rc and not result['owned']:
                    raise ValueError('Existing CLI firewall table differs; inspect it before adoption')
                if params['verify']:
                    raise ValueError('Live CLI firewall table needs reconciliation')
                result['changes'].append('/etc/battlefield-agent/firewall.nft')
                result['content_changes'].append('/etc/battlefield-agent/firewall.nft')
        # A bootstrap is not a release updater. All desired hashes are bound by the marker.
        result['activate'] = affected_services(kind, result['content_changes'])
        module.exit_json(changed=False, **result)
    except (ValueError, OSError, KeyError, AttributeError, IndexError) as error:
        module.fail_json(msg=str(error) if isinstance(error, ValueError) else 'CLI preflight could not validate target metadata')


if __name__ == '__main__':
    main()
