#!/usr/bin/env python3
"""Exercise the actual AB-128 roles in an owned disposable guest, using synthetic TLS registry credentials."""
import argparse
import hashlib
import importlib.util
import ipaddress
import json
import os
import shutil
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, ROOT / path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


k3s = load('k3s', 'scripts/infra/k3s.py')
base = load('base', 'scripts/infra/rehearse-server.py')
fixture = load('registry_fixture', 'scripts/test/k3s-registry-fixture.py')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', type=Path, required=True)
    parser.add_argument('--evidence-dir', type=Path, required=True)
    args = parser.parse_args()
    os.umask(0o077)
    if args.evidence_dir.resolve().is_relative_to(ROOT):
        raise ValueError('Rehearsal evidence must be outside the checkout')
    pins = json.loads((ROOT / 'infra/ansible/rehearsal-image.json').read_text())['24.04']
    base.checked_image(args.image, pins['sha256'])
    available = int(dict(line.split(':', 1) for line in Path('/proc/meminfo').read_text().splitlines())['MemAvailable'].split()[0])
    if available < 10 * 1024**2:
        raise ValueError('Need 10 GiB free memory before allocating the 8 GiB disposable guest')
    if shutil.disk_usage('/var/lib/libvirt/images').free < 44 * 1024**3:
        raise ValueError('Need 44 GiB free disk space for the disposable guest and headroom')
    args.evidence_dir.mkdir(mode=0o700)
    normal_env = k3s.bootstrap.clean_ansible_env
    def fixture_env(report=None):
        env = normal_env(report)
        if report:
            env['ANSIBLE_STDOUT_CALLBACK'] = 'rehearsal_report'
            env['ANSIBLE_CALLBACK_PLUGINS'] += ':' + str(ROOT / 'scripts/test/k3s_callback_plugins')
            env['BATTLEFIELD_REHEARSAL_DIAGNOSTIC'] = str(args.evidence_dir / 'failure-private.jsonl')
        return env
    k3s.bootstrap.clean_ansible_env = fixture_env
    before = {name: base.virsh('domuuid', name) for name in base.virsh('list', '--all', '--name').splitlines() if name}
    network_before = {name: base.virsh('net-uuid', name) for name in base.virsh('net-list', '--all', '--name').splitlines() if name}
    result = {'passed': False, 'cleaned_up': False, 'source_sha256': {}}
    for directory in ('infra/ansible', 'infra/k3s', 'scripts/infra'):
        for path in (ROOT / directory).rglob('*'):
            if path.is_file() and '__pycache__' not in path.parts:
                result['source_sha256'][str(path.relative_to(ROOT))] = base.digest(path)
    with tempfile.TemporaryDirectory(prefix='bf-k3s-rehearsal-') as tmp:
        work = Path(tmp)
        guest = base.Rehearsal(work, args.evidence_dir, 'default', memory_mib=8192, root_gib=32)
        registry = None
        try:
            print('Preparing disposable K3s guest ' + guest.name, flush=True)
            guest.prepare(args.image)
            base.virsh('vol-create-as', guest.identity, 'data.qcow2', '8G', '--format', 'qcow2', '--allocation', '0')
            guest.volumes.append('data.qcow2')
            base.virsh('attach-disk', guest.identity, guest.pool_path + '/data.qcow2', 'vdb', '--serial', 'battlefield-k3s-data',
                       '--subdriver', 'qcow2', '--targetbus', 'virtio', '--live', '--config')
            host_ip = str(guest.subnet.network_address + 1)
            registry = fixture.Registry(work, host_ip)
            guest.ssh('sudo tee /usr/local/share/ca-certificates/battlefield-rehearsal.crt >/dev/null', input=registry.cert.read_text())
            guest.ssh('sudo update-ca-certificates >/dev/null')
            credentials = {'username': registry.username, 'password': registry.password, 'probe_image': registry.image}
            settings = k3s.config.validate(json.loads((ROOT / 'infra/k3s/settings.json').read_text()), [str(guest.subnet)])
            cache = k3s.tools.private_directory(Path.home() / '.cache/autonomous-battlefield/k3s-rehearsal')
            pins, assets = k3s.pins_and_assets(cache)
            contract = k3s.config.contract({'machine_id': guest.identity, 'data_uuid': guest.identity, 'gateway': host_ip},
                {'domain_uuid': guest.identity, 'data_volume': guest.pool_path + '/data.qcow2', 'address': guest.ip}, 8 * 1024**3, settings, pins)
            inventory = {'all': {'children': {'k3s': {'hosts': {'battlefield-k3s': {'ansible_host': guest.ip,
                'ansible_user': 'rehearsal', 'ansible_ssh_private_key_file': str(work / 'client'),
                'ansible_ssh_common_args': '-o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=' + str(work / 'known_hosts')}}}}}}
            k3s.tools.atomic(work / 'inventory.json', k3s.vm.json_bytes(inventory))
            values = k3s.variables(contract, pins, assets, credentials)
            # The fixture calls the same roles with its own generated identity. The
            # production CLI retains its clean origin/main and Terraform guards.
            with k3s.lock.acquire() as fd:
                def action(name):
                    report = k3s.run_action(name, work / 'inventory.json', values, args.evidence_dir, fd)
                    print(name + ': ' + str(sum(t['changed'] for t in report['tasks'])) + ' changed tasks', flush=True)
                    return report
                plan = action('plan')
                if plan['change_digest'] != action('plan')['change_digest']:
                    raise ValueError('Fresh installation plans are not stable')
                print('Rejecting nonzero unformatted data before initialization', flush=True)
                guest.ssh('printf x | sudo dd of=/dev/disk/by-id/virtio-battlefield-k3s-data bs=1 count=1 conv=notrunc status=none')
                try:
                    action('apply')
                except ValueError as error:
                    if 'Prepare only the registered empty or previously owned retained disk' not in str(error):
                        raise
                else:
                    raise ValueError('Nonzero data was formatted')
                guest.ssh('sudo blkid -p /dev/disk/by-id/virtio-battlefield-k3s-data >/dev/null; test "$?" = 2')
                guest.ssh('sudo dd if=/dev/zero of=/dev/disk/by-id/virtio-battlefield-k3s-data bs=1 count=1 conv=notrunc status=none')
                action('apply')
                action('verify')
                print('Detecting and repairing live firewall drift', flush=True)
                firewall_pid = guest.ssh('sudo systemctl show k3s -p ExecMainStartTimestampMonotonic --value')
                guest.ssh('sudo nft flush table inet battlefield_k3s')
                try:
                    action('verify')
                except ValueError:
                    diagnostic = json.loads((args.evidence_dir / 'failure-private.jsonl').read_text().splitlines()[-1])
                    if 'Managed firewall has runtime drift' not in diagnostic['result'].get('stdout', ''):
                        raise
                else:
                    raise ValueError('Removed firewall rules were not detected')
                drift_plan = action('plan')
                if not any(t['changed'] and 'firewall' in t['task'] for t in drift_plan['tasks']):
                    raise ValueError('Plan did not declare firewall restoration')
                action('apply')
                action('verify')
                if firewall_pid != guest.ssh('sudo systemctl show k3s -p ExecMainStartTimestampMonotonic --value'):
                    raise ValueError('Firewall repair unnecessarily restarted K3s')
                print('Rejecting revoked registry access even while the cached probe is running', flush=True)
                registry.reject_auth = True
                try:
                    action('verify')
                except ValueError:
                    diagnostic = json.loads((args.evidence_dir / 'failure-private.jsonl').read_text().splitlines()[-1])
                    if 'Authenticated registry manifest retrieval failed' not in diagnostic['result'].get('stdout', ''):
                        raise
                else:
                    raise ValueError('Revoked registry authentication was not detected')
                finally:
                    registry.reject_auth = False
                k3s.unchanged(action('plan'))
                def registry_identity():
                    data = json.loads(guest.ssh('sudo /usr/local/bin/k3s kubectl get secrets -A --field-selector metadata.name=battlefield-ghcr -o json'))
                    selected = sorted([{'namespace': x['metadata']['namespace'], 'uid': x['metadata']['uid'], 'data': x['data']}
                                       for x in data['items'] if x['metadata']['name'] == 'battlefield-ghcr'], key=lambda x: x['namespace'])
                    if len(selected) != 3:
                        raise ValueError('Expected three private pull secrets')
                    return hashlib.sha256(json.dumps(selected, sort_keys=True).encode()).hexdigest()
                original_secrets = registry_identity()
                before_pid = guest.ssh('sudo systemctl show k3s -p ExecMainStartTimestampMonotonic --value')
                action('apply')
                if before_pid != guest.ssh('sudo systemctl show k3s -p ExecMainStartTimestampMonotonic --value'):
                    raise ValueError('Repeat apply unnecessarily restarted K3s')
                if registry_identity() != original_secrets:
                    raise ValueError('Repeat apply changed registry secret identity or data')
                print('Rebooting disposable guest and checking retained cluster/PVC identity', flush=True)
                guest.reboot()
                action('verify')
                k3s.unchanged(action('plan'))
                print('Restoring an intentionally stopped managed service through a new plan', flush=True)
                guest.ssh('sudo systemctl stop k3s')
                action('plan')
                action('apply')
                action('verify')
                print('Restoring missing OS node-registration link without replacing its retained password', flush=True)
                guest.ssh('sudo systemctl stop k3s && sudo test -L /etc/rancher/node && sudo unlink /etc/rancher/node && sudo rm /usr/local/bin/k3s')
                action('plan')
                action('apply')
                action('verify')
                print('Testing startup refusal with retained mount absent', flush=True)
                guest.ssh("sudo unshare --mount --propagation private sh -c 'umount -R -l /srv/battlefield; python3 /usr/local/lib/battlefield-k3s/storage.py guard /etc/battlefield-k3s/contract.json; test \"$?\" -ne 0'")
                action('verify')
                print('Testing startup refusal with a different mounted filesystem', flush=True)
                guest.ssh("sudo unshare --mount --propagation private sh -c 'mount -t tmpfs none /srv/battlefield; python3 /usr/local/lib/battlefield-k3s/storage.py guard /etc/battlefield-k3s/contract.json; test \"$?\" -ne 0'")
                action('verify')
                result['passed'] = True
        finally:
            if registry:
                registry.close()
            try:
                if guest.ip:
                    # Private diagnostic archive never becomes a Jenkins/public artifact.
                    guest.ssh('sudo journalctl -u k3s --no-pager -n 100 > /tmp/rehearsal-k3s.log')
                    (args.evidence_dir / 'k3s.log').write_text(guest.ssh('cat /tmp/rehearsal-k3s.log'))
            except (OSError, subprocess.SubprocessError):
                pass
            try:
                guest.cleanup()
                result['cleaned_up'] = True
                after = {name: base.virsh('domuuid', name) for name in base.virsh('list', '--all', '--name').splitlines() if name}
                network_after = {name: base.virsh('net-uuid', name) for name in base.virsh('net-list', '--all', '--name').splitlines() if name}
                result['existing_resources_preserved'] = before == after and network_before == network_after
                if not result['existing_resources_preserved']:
                    raise ValueError('Existing hypervisor inventory changed during rehearsal')
            finally:
                (args.evidence_dir / 'result.json').write_text(json.dumps(result, indent=2) + '\n')
    print('PASS: disposable K3s convergence, reboot, mount refusal and cleanup', flush=True)


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        raise SystemExit('K3s rehearsal: ' + (str(error) if isinstance(error, ValueError) else type(error).__name__))
