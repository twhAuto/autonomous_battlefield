#!/usr/bin/env python3
"""Destructive fixtures ONLY inside the UUID-marked disposable AB-126 hypervisor."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[2]
HOME_DIR = Path.home()
EVIDENCE = HOME_DIR / 'evidence'
PRIVATE = HOME_DIR / 'fixtures'
STATE = HOME_DIR / '.local/state/battlefield/k3s'


def run(*command, **kwargs):
    result = subprocess.run(command, check=True, capture_output=True, text=kwargs.pop('text', True),
                            timeout=kwargs.pop('timeout', 120), **kwargs)
    return result.stdout.strip() if isinstance(result.stdout, str) else result.stdout


def load_vm():
    spec = importlib.util.spec_from_file_location('vm', ROOT / 'scripts/infra/vm.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def operation(action, label, replace=False):
    directory = PRIVATE / label
    directory.mkdir(mode=0o700, exist_ok=True)
    command = [sys.executable, str(ROOT / 'scripts/infra/vm.py'), action, '--revision', run('git', '-C', str(ROOT), 'rev-parse', 'HEAD'),
               '--inventory', str(PRIVATE / 'inventory.json'), '--config', str(PRIVATE / 'vm.json'),
               '--become-password-file', str(PRIVATE / 'sudo'), '--vault-password-file', str(PRIVATE / 'vault'),
               '--receipt', str(directory / 'receipt.json'), '--report', str(EVIDENCE / (label + '-' + action + '.json'))]
    if replace:
        command.append('--replace-os')
    result = subprocess.run(command, capture_output=True, text=True, timeout=1800)
    if result.returncode:
        (EVIDENCE / 'failure.txt').write_text(result.stdout + result.stderr)
        if (STATE / 'last-error.log').exists():
            shutil.copyfile(STATE / 'last-error.log', EVIDENCE / 'private-fixture-error.log')
        if (STATE / 'last-plan.json').exists():
            value = json.loads((STATE / 'last-plan.json').read_text())
            changed = []
            def paths(before, after, prefix=''):
                if isinstance(before, dict) and isinstance(after, dict):
                    return [p for k in before.keys() | after.keys() for p in paths(before.get(k), after.get(k), prefix + '/' + k)]
                if isinstance(before, list) and isinstance(after, list) and len(before) == len(after):
                    return [p for i, (a, b) in enumerate(zip(before, after)) for p in paths(a, b, prefix + '/' + str(i))]
                return [prefix] if before != after else []
            for resource in value.get('resource_changes', []):
                change = resource['change']
                if change['actions'] != ['no-op']:
                    changed.append({'address': resource['address'], 'actions': change['actions'],
                        'reason': resource.get('action_reason'), 'replace_paths': change.get('replace_paths'),
                        'changed_paths': paths(change.get('before'), change.get('after'))})
            (EVIDENCE / 'plan-diagnostics.json').write_text(json.dumps(changed))
        raise ValueError('VM ' + action + ' failed: ' + result.stderr)
    return json.loads((EVIDENCE / (label + '-' + action + '.json')).read_text())


def guest(command):
    inventory = json.loads((STATE / 'inventory.json').read_text())['all']['children']['k3s']['hosts']['battlefield-k3s']
    return run('ssh', '-i', str(STATE / 'id_ed25519'), '-o', 'IdentitiesOnly=yes', '-o', 'StrictHostKeyChecking=yes',
               '-o', 'UserKnownHostsFile=' + str(STATE / 'known_hosts'), '-o', 'BatchMode=yes', '-o', 'ConnectTimeout=5',
               'battlefield@' + inventory['ansible_host'], command, timeout=120)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('phase', choices=['before-reboot', 'after-reboot'])
    args = parser.parse_args()
    if not Path('/etc/battlefield-rehearsal').is_file() or os.getuid() == 0 or run('hostname').split('-')[:2] != ['bf', 'rehearsal']:
        raise ValueError('Refusing destructive fixtures outside the disposable hypervisor')
    os.umask(0o077)
    if args.phase == 'after-reboot':
        time.sleep(15)
        operation('verify', 'after-reboot')
        guest('sudo mount /dev/disk/by-id/virtio-battlefield-k3s-data /mnt && test "$(cat /mnt/ab126-marker)" = retained')
        (EVIDENCE / 'result.json').write_text(json.dumps({'passed': True, 'checks': [
            'apply', 'repeat-plan', 'repeat-apply', 'data-delete-rejected', 'os-replacement-retains-data',
            'encrypted-state-restore', 'hypervisor-reboot-autostart', 'post-reboot-verify']}))
        return
    EVIDENCE.mkdir(mode=0o700, exist_ok=True)
    PRIVATE.mkdir(mode=0o700, exist_ok=True)
    print('Installing nested libvirt prerequisites', flush=True)
    run('sudo', 'apt-get', 'update', timeout=600)
    run('sudo', 'env', 'DEBIAN_FRONTEND=noninteractive', 'apt-get', 'install', '-y',
        'qemu-system-x86', 'libvirt-daemon-system', 'libvirt-clients', 'cloud-image-utils', 'python3-venv', 'git', timeout=1200)
    run('sudo', 'usermod', '-aG', 'libvirt,kvm', os.environ['USER'])
    # Refresh this disposable process's supplementary groups; a new SSH session
    # would do the same. Existing production users/groups are never modified.
    if not os.environ.get('AB126_GROUPS_READY'):
        run('sudo', '-u', os.environ['USER'], 'env', 'AB126_GROUPS_READY=1', sys.executable, __file__, args.phase, timeout=3600)
        return
    if not Path('/dev/kvm').exists():
        raise ValueError('Nested KVM is unavailable')
    run('sudo', 'sfdisk', '/dev/vdb', input='type=83\n')
    run('sudo', 'udevadm', 'settle')
    run('sudo', 'mkfs.ext4', '-F', '/dev/vdb1')
    uuid = run('sudo', 'blkid', '-s', 'UUID', '-o', 'value', '/dev/vdb1')
    run('sudo', 'python3', str(ROOT / 'infra/ansible/roles/data_storage/files/storage.py'), 'apply', uuid, '/srv/battlefield-data', os.environ['USER'])
    run('sudo', 'mkdir', '-p', '/etc/battlefield-data-storage', '/etc/libvirt/hooks/qemu.d')
    run('sudo', 'install', '-m', '0755', str(ROOT / 'infra/ansible/roles/data_storage/files/storage.py'), '/etc/battlefield-data-storage/guard.py')
    hook = '#!/bin/sh\n[ "$1" = battlefield-k3s ] || exit 0\ncase "$2" in prepare|restore|migrate) exec /usr/bin/python3 /etc/battlefield-data-storage/guard.py guard ' + uuid + ' /srv/battlefield-data ' + os.environ['USER'] + ' ;; esac\n'
    (PRIVATE / 'hook').write_text(hook)
    run('sudo', 'install', '-m', '0755', str(PRIVATE / 'hook'), '/etc/libvirt/hooks/qemu.d/50-battlefield-data-storage')
    run('sudo', 'systemctl', 'restart', 'libvirtd')
    inventory = {'schema': 1, 'host': '127.0.0.1', 'user': os.environ['USER'],
                 'identity_file': str(PRIVATE / 'unused'), 'machine_id': Path('/etc/machine-id').read_text().strip(),
                 'data_uuid': uuid, 'data_mount': '/srv/battlefield-data'}
    (PRIVATE / 'inventory.json').write_text(json.dumps(inventory))
    config = json.loads((ROOT / 'infra/libvirt/vm.json').read_text())
    config.update(cpus=1, memory_mib=2048, os_gib=12, data_gib=4, reserve_gib=2)
    (PRIVATE / 'vm.json').write_text(json.dumps(config))
    (PRIVATE / 'sudo').write_text('synthetic-unused-password')
    (PRIVATE / 'vault').write_text('synthetic-rehearsal-passphrase')
    # A standalone committed fixture lets the unchanged trusted-main guard run
    # against this isolated host; it never changes refs in the real repository.
    (ROOT / '.gitignore').write_text('__pycache__/\n')
    run('git', '-C', str(ROOT), 'init', '-b', 'main')
    run('git', '-C', str(ROOT), 'config', 'user.name', 'Disposable rehearsal')
    run('git', '-C', str(ROOT), 'config', 'user.email', 'rehearsal@example.invalid')
    run('git', '-C', str(ROOT), 'add', '.')
    run('git', '-C', str(ROOT), 'commit', '-m', 'Disposable AB-126 source fixture')
    run('git', '-C', str(ROOT), 'update-ref', 'refs/remotes/origin/main', 'HEAD')
    operation('plan', 'initial')
    operation('apply', 'initial')
    assert not operation('plan', 'repeat')['changes']
    operation('apply', 'repeat')
    guest('sudo mkfs.ext4 -F /dev/disk/by-id/virtio-battlefield-k3s-data && sudo mount /dev/disk/by-id/virtio-battlefield-k3s-data /mnt && echo retained | sudo tee /mnt/ab126-marker >/dev/null && sudo umount /mnt')
    module = load_vm()
    with module.lock.acquire() as fd:
        options = argparse.Namespace(inventory=PRIVATE / 'inventory.json', config=PRIVATE / 'vm.json',
            become_password_file=PRIVATE / 'sudo', vault_password_file=PRIVATE / 'vault', replace_os=False,
            revision=run('git', '-C', str(ROOT), 'rev-parse', 'HEAD'))
        instance = module.VM(options, fd)
        instance.preflight()
        instance.prepare()
        try:
            instance.tf('plan', '-destroy', '-input=false', '-var-file=' + str(instance.variables))
        except ValueError:
            pass
        else:
            raise ValueError('Terraform destroy did not reject retained resources')
    operation('plan', 'replace', replace=True)
    operation('apply', 'replace', replace=True)
    guest('sudo mount /dev/disk/by-id/virtio-battlefield-k3s-data /mnt && test "$(cat /mnt/ab126-marker)" = retained && sudo umount /mnt')
    snapshot = sorted((STATE / 'recovery').glob('*.vault'))[-1]
    (STATE / 'terraform.tfstate').rename(PRIVATE / 'saved-state.json')
    run(sys.executable, str(ROOT / 'scripts/infra/vm-state.py'), 'restore', '--inventory', str(PRIVATE / 'inventory.json'),
        '--bundle', str(snapshot), '--vault-password-file', str(PRIVATE / 'vault'), timeout=180)
    assert json.loads((STATE / 'terraform.tfstate').read_text()) == json.loads((PRIVATE / 'saved-state.json').read_text())
    operation('verify', 'restored')


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        EVIDENCE.mkdir(mode=0o700, exist_ok=True)
        details = str(error)
        if isinstance(error, subprocess.CalledProcessError):
            details += '\n' + str(error.stdout) + '\n' + str(error.stderr)
        (EVIDENCE / 'failure.txt').write_text(details)
        raise
