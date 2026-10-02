#!/usr/bin/env python3
"""AB-140 destructive fixtures exclusively inside a UUID-marked disposable hypervisor."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import time
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[2]
HOME_DIR = Path.home()
PRIVATE = HOME_DIR / 'fixtures'
EVIDENCE = HOME_DIR / 'evidence'
STATE = Path('/var/lib/battlefield-cli-vm')
CONNECTION = HOME_DIR / '.local/state/battlefield/cli-vm'
NAME = 'battlefield-agent'


def run(*command, **kwargs):
    result = subprocess.run([str(c) for c in command], check=True, capture_output=True,
                            text=True, timeout=kwargs.pop('timeout', 120), **kwargs)
    return result.stdout.strip()


def virsh(*args):
    return run('sudo', 'virsh', '-c', 'qemu:///system', *args)


def operation(action, label, mode='create', review=None, failure=False, reason=None):
    output = EVIDENCE / (label + '-' + action + '.json')
    command = ['python3', str(ROOT / 'scripts/infra/cli-vm.py'), action, '--mode', mode,
               '--revision', run('git', '-C', ROOT, 'rev-parse', 'HEAD'),
               '--inventory', str(PRIVATE / 'inventory.json'), '--cli-config', str(PRIVATE / 'cli.json'),
               '--report', str(output)]
    if review:
        command += ['--review', str(EVIDENCE / (review + '-plan.json'))]
    result = subprocess.run(command, capture_output=True, text=True, timeout=1200)
    (EVIDENCE / (label + '-' + action + '.log')).write_text(result.stdout + result.stderr)
    if failure:
        if result.returncode == 0:
            raise ValueError('Negative CLI VM fixture unexpectedly passed: ' + label)
        if reason and reason not in result.stdout + result.stderr:
            raise ValueError('Negative CLI VM fixture failed for the wrong reason: ' + label)
        return None
    if result.returncode:
        diagnostics = subprocess.run(['sudo', 'cat', str(STATE / 'last-error.log')], capture_output=True, text=True)
        (EVIDENCE / 'private-command-error.log').write_text(diagnostics.stdout)
        raise ValueError('CLI VM fixture failed: ' + label + '; inspect its private log')
    return json.loads(output.read_text())


def guest(command):
    value = json.loads((CONNECTION / 'handoff.json').read_text())
    return run('ssh', '-i', value['identity_file'], '-o', 'BatchMode=yes', '-o', 'IdentitiesOnly=yes',
               '-o', 'StrictHostKeyChecking=yes', '-o', 'UserKnownHostsFile=' + value['known_hosts_file'],
               '-o', 'ConnectTimeout=10', 'vm-admin@192.168.234.2', command, timeout=120)


def baseline():
    return {'domain': virsh('dumpxml', NAME, '--inactive'), 'network': virsh('net-dumpxml', NAME, '--inactive'),
            'filter': virsh('nwfilter-dumpxml', NAME),
            'disk': run('sudo', 'stat', '-c', '%d:%i', '/var/lib/libvirt/images/battlefield-agent/root.qcow2'),
            'homes': guest('sudo sha256sum /var/lib/battlefield-worker/.fixture-login /var/lib/battlefield-worker/.fixture-data')}


def save_root(path, content):
    source = PRIVATE / 'root-input'
    source.write_text(content)
    run('sudo', 'install', '-m', '0600', source, path)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('phase', choices=['before-reboot', 'after-reboot'])
    parser.add_argument('--identity', required=True)
    args = parser.parse_args()
    marker = Path('/etc/battlefield-rehearsal')
    if (not marker.is_file() or marker.read_text().strip() != args.identity or os.getuid() == 0 or
            run('sudo', 'cat', '/sys/class/dmi/id/product_uuid').lower() != args.identity or
            not run('hostname').startswith('bf-rehearsal-')):
        raise ValueError('Refusing CLI VM fixtures outside their disposable UUID-bound hypervisor')
    os.umask(0o077)
    if args.phase == 'after-reboot':
        time.sleep(15)
        operation('verify', 'after-reboot', mode='adopt')
        if baseline() != json.loads((EVIDENCE / 'baseline.json').read_text()):
            raise ValueError('VM identity, disk inode, XML or provider home bytes changed across reboot')
        (EVIDENCE / 'result.json').write_text(json.dumps({'passed': True, 'checks': [
            'pinned-clean-create', 'strict-ssh-handoff', 'repeat-plan-no-change', 'repeat-apply-no-recreation',
            'wrong-uuid-refused', 'missing-disk-refused', 'network-drift-refused', 'stale-plan-refused',
            'consumed-plan-refused', 'existing-layout-adoption', 'provider-home-bytes-preserved',
            'hypervisor-reboot-autostart', 'post-reboot-no-change']}))
        return
    PRIVATE.mkdir(mode=0o700); EVIDENCE.mkdir(mode=0o700)
    run('sudo', 'apt-get', 'update', timeout=600)
    run('sudo', 'env', 'DEBIAN_FRONTEND=noninteractive', 'apt-get', 'install', '-y',
        'qemu-system-x86', 'libvirt-daemon-system', 'libvirt-clients', 'cloud-image-utils',
        'python3-venv', 'git', timeout=1200)
    inventory = {'schema': 1, 'host': '127.0.0.1', 'user': os.environ['USER'],
                 'identity_file': str(PRIVATE / 'unused'), 'machine_id': Path('/etc/machine-id').read_text().strip()}
    (PRIVATE / 'inventory.json').write_text(json.dumps(inventory))
    # Commit this synthetic fixture only; production refs and trusted-main checks are unchanged.
    (ROOT / '.gitignore').write_text('__pycache__/\n')
    for command in [('init', '-b', 'main'), ('config', 'user.name', 'Disposable rehearsal'),
                    ('config', 'user.email', 'rehearsal@example.invalid'), ('add', '.'),
                    ('commit', '-m', 'Disposable AB-140 source fixture'),
                    ('update-ref', 'refs/remotes/origin/main', 'HEAD')]:
        run('git', '-C', ROOT, *command)
    operation('plan', 'create')
    created = operation('apply', 'create', review='create')
    if not created['changed'] or not created['applied_changes']:
        raise ValueError('Fresh creation report lost its applied-change evidence')
    guest('sudo -u battlefield-worker sh -c "umask 077; echo synthetic-login > /var/lib/battlefield-worker/.fixture-login; echo retained > /var/lib/battlefield-worker/.fixture-data"')
    before = baseline()
    (EVIDENCE / 'baseline.json').write_text(json.dumps(before))
    if operation('plan', 'repeat')['changes']:
        raise ValueError('Repeated create plan did not converge')
    repeated = operation('apply', 'repeat', review='repeat')
    if repeated['changed'] or repeated['applied_changes']:
        raise ValueError('No-op apply report incorrectly claims infrastructure changes')
    operation('apply', 'replayed', review='repeat', failure=True)
    operation('plan', 'stale')
    receipt = json.loads(run('sudo', 'cat', STATE / 'review.json')); receipt['created'] -= 2000
    save_root(STATE / 'review.json', json.dumps(receipt))
    operation('apply', 'stale', review='stale', failure=True, reason='plan expired')
    disk = '/var/lib/libvirt/images/battlefield-agent/root.qcow2'
    run('sudo', 'mv', disk, disk + '.retained')
    try:
        operation('plan', 'missing-disk', failure=True)
    finally:
        run('sudo', 'mv', disk + '.retained', disk)
    network = ET.fromstring(before['network']); ET.SubElement(network, 'forward', mode='nat')
    (PRIVATE / 'drift.xml').write_text(ET.tostring(network, encoding='unicode'))
    virsh('net-define', PRIVATE / 'drift.xml')
    try:
        operation('plan', 'network-drift', failure=True, reason='no forwarding')
    finally:
        (PRIVATE / 'original.xml').write_text(before['network']); virsh('net-define', PRIVATE / 'original.xml')
    # Simulate a legacy installation with no AB-140 marker, keeping all live resources and login homes.
    handoff = json.loads((CONNECTION / 'handoff.json').read_text())
    config = {'schema': 1, 'host': {'machine_id': inventory['machine_id'], 'operator': inventory['user'],
        'home': str(HOME_DIR), 'domain_uuid': handoff['domain_uuid'], 'network_uuid': handoff['network_uuid'],
        'kube_system_uid': args.identity, 'kubeconfig': str(PRIVATE / 'unused-kube'), 'context': 'fixture', 'bridge_port': 28084},
        'bundle': str(PRIVATE / 'unused-bundle'), 'guest': {
        'machine_id': handoff['machine_id'], 'identity_file': handoff['identity_file'],
        'known_hosts_file': handoff['known_hosts_file'], 'adopt_existing': True,
        'config': str(PRIVATE / 'unused-config'), 'service': str(PRIVATE / 'unused-service'),
        'artifacts': {name: {'path': str(PRIVATE / name), 'sha256': '0' * 64} for name in ('battlefield-agent', 'codex', 'codex-code-mode-host')}}}
    (PRIVATE / 'cli.json').write_text(json.dumps(config))
    run('sudo', 'mv', STATE / 'owner.json', PRIVATE / 'created-owner.json')
    original_uuid = config['host']['domain_uuid']; config['host']['domain_uuid'] = args.identity
    (PRIVATE / 'cli.json').write_text(json.dumps(config))
    operation('plan', 'wrong-uuid', mode='adopt', failure=True, reason='UUID differs')
    config['host']['domain_uuid'] = original_uuid; (PRIVATE / 'cli.json').write_text(json.dumps(config))
    operation('plan', 'adopt', mode='adopt')
    adopted = operation('apply', 'adopt', mode='adopt', review='adopt')
    if not adopted['changed'] or adopted['applied_changes'] != ['record existing VM ownership', 'export trusted guest connection']:
        raise ValueError('Adoption report does not describe its metadata-only changes')
    if operation('plan', 'adopt-repeat', mode='adopt')['changes']:
        raise ValueError('Adoption repeat plan did not converge')
    operation('apply', 'adopt-repeat', mode='adopt', review='adopt-repeat')
    if baseline() != before:
        raise ValueError('CLI adoption changed VM/disk identity, XML or provider-home bytes')


if __name__ == '__main__':
    main()
