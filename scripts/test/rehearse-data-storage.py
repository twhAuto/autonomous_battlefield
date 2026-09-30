#!/usr/bin/env python3
"""Destructive tests ONLY inside an owned disposable rehearsal VM with a blank 1 GiB vdb."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import uuid

ROOT = Path(__file__).resolve().parents[2]
STATE = Path.home() / 'storage-test.json'
TARGET = Path('/srv/battlefield-data')
HELPER = ROOT / 'infra/ansible/roles/data_storage/files/storage.py'


def run(*args, **kwargs):
    result = subprocess.run(args, text=True, capture_output=True, timeout=kwargs.pop('timeout', 300), **kwargs)
    if result.returncode:
        raise RuntimeError(f'{args[0]} failed:\n{result.stdout}\n{result.stderr}')
    return result.stdout.strip()


def assert_fails(*args, text):
    result = subprocess.run(args, text=True, capture_output=True, timeout=60)
    assert result.returncode != 0 and text in result.stdout + result.stderr, result


def role(action, data_uuid):
    tools = run(str(ROOT / 'scripts/infra/server.sh'), 'tools')
    play = Path.home() / 'storage-play.yml'
    play.write_text(json.dumps([{'hosts': 'localhost', 'become': True, 'gather_facts': False,
        'vars': {'bootstrap_action': action, 'bootstrap_operator': 'rehearsal',
                 'bootstrap_data_uuid': data_uuid, 'bootstrap_data_mount': str(TARGET)},
        'tasks': [{'ansible.builtin.import_role': {'name': str(ROOT / 'infra/ansible/roles/data_storage'), 'tasks_from': 'preflight'}},
                  {'ansible.builtin.import_role': {'name': str(ROOT / 'infra/ansible/roles/data_storage')}}]}]))
    command = [tools + '/ansible-playbook', '-i', 'localhost,', '-c', 'local', str(play)]
    if action == 'plan':
        command.append('--check')
    env = dict(os.environ, ANSIBLE_CONFIG=str(ROOT / 'infra/ansible/ansible.cfg'))
    output = run(*command, env=env)
    print(output, flush=True)
    return output


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('stage', choices=['before-reboot', 'after-reboot'])
    parser.add_argument('--identity', required=True)
    args = parser.parse_args()
    identity = str(uuid.UUID(args.identity))
    assert Path('/etc/battlefield-rehearsal').read_text().strip() == identity
    assert run('hostname').startswith('bf-rehearsal-')
    assert os.geteuid() != 0 and run('id', '-un') == 'rehearsal'
    if args.stage == 'after-reboot':
        state = json.loads(STATE.read_text())
        assert run('cat', '/proc/sys/kernel/random/boot_id') != state['boot_id']
        assert (TARGET / 'workspace/preserved').read_text() == 'retain me\n'
        assert 'changed=0' in role('verify', state['uuid'])
        assert 'changed=0' in role('apply', state['uuid'])
        print('PASS: fstab mount, data and idempotency survived a real reboot')
        return
    assert not STATE.exists()
    disk = json.loads(run('lsblk', '--json', '--bytes', '-o', 'NAME,SIZE,TYPE,FSTYPE,MOUNTPOINTS', '/dev/vdb'))['blockdevices'][0]
    assert disk['size'] == 1073741824 and not disk.get('children') and not disk['fstype'] and not any(disk['mountpoints'])
    # Only this verified new disposable VM disk is partitioned/formatted.
    run('sudo', 'sfdisk', '/dev/vdb', input='label: gpt\n, , L\n')
    run('sudo', 'udevadm', 'settle')
    run('sudo', 'mkfs.ext4', '-q', '/dev/vdb1')
    data_uuid = run('sudo', 'blkid', '-s', 'UUID', '-o', 'value', '/dev/vdb1')
    original = Path('/etc/fstab').read_bytes()
    role('plan', data_uuid)
    assert Path('/etc/fstab').read_bytes() == original and not TARGET.exists()
    role('apply', data_uuid)
    (TARGET / 'workspace/preserved').write_text('retain me\n')
    assert 'changed=0' in role('apply', data_uuid)
    assert 'changed=0' in role('verify', data_uuid)
    run('sudo', '-u', 'libvirt-qemu', 'touch', str(TARGET / 'libvirt/images/qemu-write-test'))
    run('sudo', 'rm', str(TARGET / 'libvirt/images/qemu-write-test'))
    hook = '/etc/libvirt/hooks/qemu.d/50-battlefield-data-storage'
    assert run('sudo', hook, 'battlefield-k3s', 'prepare', 'begin', '-') == ''
    # A tiny non-production TCG domain proves libvirt actually invokes the hook.
    domain = Path.home() / 'guard-domain.xml'
    domain.write_text("<domain type='qemu'><name>battlefield-k3s</name><memory unit='MiB'>128</memory><vcpu>1</vcpu><os><type arch='x86_64'>hvm</type></os><devices><emulator>/usr/bin/qemu-system-x86_64</emulator></devices></domain>")
    run('sudo', 'virsh', '-c', 'qemu:///system', 'define', str(domain))
    run('sudo', 'virsh', '-c', 'qemu:///system', 'start', 'battlefield-k3s')
    run('sudo', 'virsh', '-c', 'qemu:///system', 'destroy', 'battlefield-k3s')
    run('sudo', 'umount', str(TARGET))
    assert_fails('sudo', 'virsh', '-c', 'qemu:///system', 'start', 'battlefield-k3s', text='root-filesystem fallback')
    assert run('sudo', hook, 'battlefield-agent', 'prepare', 'begin', '-') == ''
    assert run('sudo', 'find', str(TARGET), '-mindepth', '1', '-maxdepth', '1') == ''
    run('sudo', 'mount', '-t', 'tmpfs', 'tmpfs', str(TARGET))
    assert_fails('sudo', hook, 'battlefield-k3s', 'prepare', 'begin', '-', text='expected writable ext4')
    run('sudo', 'umount', str(TARGET))
    run('sudo', 'mount', str(TARGET))
    run('sudo', 'mount', '-o', 'remount,ro', str(TARGET))
    assert_fails('sudo', hook, 'battlefield-k3s', 'restore', 'begin', '-', text='expected writable ext4')
    run('sudo', 'mount', '-o', 'remount,rw', str(TARGET))
    assert (TARGET / 'workspace/preserved').read_text() == 'retain me\n'
    assert 'changed=0' in role('apply', data_uuid)
    # Removing only the private activation stamp reproduces interruption before reload.
    run('sudo', 'rm', '/etc/libvirt/hooks/.battlefield-data-storage-loaded')
    role('apply', data_uuid)
    assert 'changed=0' in role('apply', data_uuid)
    run('sudo', 'virsh', '-c', 'qemu:///system', 'undefine', 'battlefield-k3s')
    STATE.write_text(json.dumps({'uuid': data_uuid, 'boot_id': run('cat', '/proc/sys/kernel/random/boot_id')}))
    print('PASS: plan/apply/reapply, QEMU write access, real libvirt guard, absent/wrong/read-only mount, activation recovery')


if __name__ == '__main__':
    main()
