#!/usr/bin/env python3
"""CLI VM identity, retained-layout, approval and Ansible handoff regressions."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import types
import unittest
from unittest.mock import patch
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[2]


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, ROOT / path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


policy = load('cli_vm_policy', 'infra/ansible/module_utils/battlefield_cli_vm_policy.py')
sys.modules['ansible.module_utils.battlefield_cli_vm_policy'] = policy
engine = load('cli_vm_engine', 'infra/ansible/module_utils/battlefield_cli_vm.py')
wrapper = load('cli_vm_wrapper', 'scripts/infra/cli-vm.py')
cli = wrapper.load('cli-bootstrap')
IDENTITIES = {kind: str(i) * 8 + '-1111-4111-a111-' + str(i) * 12 for i, kind in enumerate(('domain', 'network', 'filter'), 1)}


def xml():
    result = {}
    for kind in IDENTITIES:
        node = ET.fromstring((ROOT / 'deploy/agent-vm' / (kind + '.xml')).read_text())
        ET.SubElement(node, 'uuid').text = IDENTITIES[kind]
        result[kind] = node
    return result


def validate(nodes, identities=None):
    policy.validate_layout(*(ET.tostring(nodes[k], encoding='unicode') for k in ('domain', 'network', 'filter')),
                           (ROOT / 'deploy/agent-vm/filter.xml').read_text(), identities or IDENTITIES)


class LayoutTests(unittest.TestCase):
    def test_static_and_libvirt_runtime_normalization(self):
        nodes = xml(); validate(nodes)
        nodes['domain'].find('memory').set('unit', 'KiB')
        nodes['domain'].find('memory').text = '4194304'
        for disk in nodes['domain'].findall('devices/disk'):
            disk.find('source').set('index', '2'); ET.SubElement(disk, 'backingStore')
        nic = nodes['domain'].find('devices/interface/source')
        nic.set('bridge', 'bf-agent0'); nic.set('portid', IDENTITIES['domain'])
        terminal = nodes['domain'].find('devices/serial')
        ET.SubElement(terminal, 'source', path='/dev/pts/0')
        validate(nodes)

    def test_wrong_uuid_rejected_for_each_resource(self):
        for kind in IDENTITIES:
            identities = dict(IDENTITIES); identities[kind] = 'f' * 8 + '-1111-4111-a111-' + 'f' * 12
            with self.subTest(kind=kind), self.assertRaisesRegex(ValueError, 'UUID differs'):
                validate(xml(), identities)

    def test_disk_replacement_extra_disk_backing_and_host_device_rejected(self):
        for mutation in ('path', 'extra', 'backing', 'hostdev', 'filesystem', 'qemu-command'):
            nodes = xml(); domain = nodes['domain']; devices = domain.find('devices')
            if mutation == 'path': devices.find('disk/source').set('file', '/var/lib/foreign.qcow2')
            elif mutation == 'extra': devices.append(copy.deepcopy(devices.find('disk')))
            elif mutation == 'backing': ET.SubElement(devices.find('disk'), 'backingStore', type='file')
            elif mutation == 'qemu-command': ET.SubElement(domain, '{http://libvirt.org/schemas/domain/qemu/1.0}commandline')
            else: ET.SubElement(devices, mutation)
            with self.subTest(mutation=mutation), self.assertRaises(ValueError): validate(nodes)

    def test_network_escapes_rejected(self):
        for mutation in ('nat', 'dhcp', 'dns', 'extra-ip', 'extra-nic', 'filter-rule', 'nic-filter', 'mac', 'bridge', 'host-input'):
            nodes = xml(); network = nodes['network']; nic = nodes['domain'].find('devices/interface')
            if mutation == 'nat': ET.SubElement(network, 'forward', mode='nat')
            elif mutation == 'dhcp': ET.SubElement(network.find('ip'), 'dhcp')
            elif mutation == 'dns': network.find('dns').set('enable', 'yes')
            elif mutation == 'extra-ip': network.append(copy.deepcopy(network.find('ip')))
            elif mutation == 'extra-nic': nodes['domain'].find('devices').append(copy.deepcopy(nic))
            elif mutation == 'filter-rule': nodes['filter'].find('rule').set('action', 'drop')
            elif mutation == 'nic-filter': nic.find('filterref').set('filter', 'clean-traffic')
            elif mutation == 'bridge': nic.find('source').set('bridge', 'virbr0')
            elif mutation == 'mac': nic.find('mac').set('address', '52:54:00:00:00:01')
            else: ET.SubElement(nodes['domain'].find('devices'), 'input', type='evdev', bus='none')
            with self.subTest(mutation=mutation), self.assertRaises(ValueError): validate(nodes)

    def test_overlapping_routes_rejected(self):
        policy.no_overlap(['192.168.233.0/24', '10.42.0.0/16', '2001:db8::/64'])
        for subnet in ['192.168.234.2/32', '192.168.0.0/16']:
            with self.assertRaises(ValueError): policy.no_overlap([subnet])


class PermissionTests(unittest.TestCase):
    def test_private_operator_umask_does_not_remove_qemu_directory_access(self):
        with tempfile.TemporaryDirectory() as temporary, patch.object(engine.os, 'chown') as chown:
            directory = Path(temporary) / 'disk'
            previous = os.umask(0o077)
            try:
                engine.disk_directory(directory, 991)
            finally:
                os.umask(previous)
            self.assertEqual(directory.stat().st_mode & 0o777, 0o750)
            chown.assert_called_once_with(directory, 0, 991)
            with self.assertRaises(FileExistsError):
                engine.disk_directory(directory, 991)


class ReceiptTests(unittest.TestCase):
    def test_stale_replayed_and_changed_inputs_rejected(self):
        request, observed = {'revision': 'a' * 40}, {'exists': True, 'disk': {'inode': 1}}
        receipt = {'request': request, 'observation': observed, 'review_id': 'b' * 64, 'created': 100}
        policy.check_receipt(receipt, request, observed, 'b' * 64, now=200)
        cases = [(dict(request, revision='c' * 40), observed, 'b' * 64, 200),
                 (request, dict(observed, disk={'inode': 2}), 'b' * 64, 200),
                 (request, observed, 'c' * 64, 200), (request, observed, 'b' * 64, 1901),
                 (request, observed, 'b' * 64, 99)]
        for req, obs, approval, now in cases:
            with self.subTest(now=now, request=req), self.assertRaises(ValueError):
                policy.check_receipt(receipt, req, obs, approval, now=now)

    def test_changed_plan_never_reaches_create_or_consumes_receipt(self):
        instance = object.__new__(engine.Engine)
        instance.request = {'revision': 'a' * 40}; instance.owner = None
        instance.receipt_path = types.SimpleNamespace(unlink=lambda: self.fail('Consumed invalid receipt'))
        instance.inspect = lambda: {'exists': False, 'changed': True}
        instance.create = lambda: self.fail('Created without a reviewed plan')
        with patch.object(engine, 'read', return_value={'created': 100, 'review_id': 'b' * 64, 'request': instance.request, 'observation': {'exists': False}}), patch.object(policy.time, 'time', return_value=200):
            with self.assertRaisesRegex(ValueError, 'changed'): instance.apply('b' * 64)

    def test_owned_repeat_apply_never_redefines_resources(self):
        instance = object.__new__(engine.Engine)
        instance.request = {}; instance.owner = {'identities': IDENTITIES}
        instance.inspect = lambda: {'exists': True}; instance.connection = lambda: {'test': True}
        events = []; instance.receipt_path = types.SimpleNamespace(unlink=lambda: events.append('consumed'))
        instance.create = lambda: self.fail('Recreated owned VM')
        with patch.object(engine, 'read', return_value={}), patch.object(engine, 'check_receipt'), patch.object(engine, 'virsh', side_effect=AssertionError('Mutated VM')):
            result = instance.apply('review')
        self.assertFalse(result['changed']); self.assertEqual(events, ['consumed'])


class HandoffTests(unittest.TestCase):
    def setUp(self):
        self.value = {'schema': 1, 'host_machine_id': 'a' * 32, 'machine_id': 'b' * 32,
                      'domain_uuid': IDENTITIES['domain'], 'network_uuid': IDENTITIES['network'],
                      'identity_file': '/private/key', 'known_hosts_file': '/private/known_hosts',
                      'ansible_host': policy.IP, 'ansible_user': 'vm-admin'}
        self.config = {'host': {'domain_uuid': IDENTITIES['domain'], 'network_uuid': IDENTITIES['network']},
                       'guest': {'machine_id': '', 'identity_file': ''}}
        self.server = types.SimpleNamespace(outside_repo=lambda p: None,
            private_file=lambda p: types.SimpleNamespace(read_text=lambda: json.dumps(self.value)))
        self.args = types.SimpleNamespace(local=True, cli_vm_handoff=Path('/private/handoff.json'))

    def test_handoff_populates_connection_without_retargeting_worker(self):
        result = cli.managed_handoff(self.args, {'machine_id': 'a' * 32}, self.config, self.server)
        self.assertEqual(self.config['guest']['identity_file'], '/private/key')
        self.assertEqual(self.config['guest']['machine_id'], 'b' * 32)
        self.assertEqual(result['domain_uuid'], IDENTITIES['domain'])
        self.config['guest']['machine_id'] = 'c' * 32
        with self.assertRaises(ValueError): cli.managed_handoff(self.args, {'machine_id': 'a' * 32}, self.config, self.server)

    def test_managed_connection_remains_valid_in_full_service_registration(self):
        config = json.loads((ROOT / 'infra/ansible/cli.example.json').read_text())
        config['host'].update(machine_id='a' * 32, domain_uuid=IDENTITIES['domain'], network_uuid=IDENTITIES['network'])
        config['guest'].update(machine_id='', identity_file='')
        cli.managed_handoff(self.args, {'machine_id': 'a' * 32}, config, self.server)
        cli.validate(config)
        self.assertEqual(config['guest']['known_hosts_file'], '/private/known_hosts')

    def test_foreign_handoff_or_remote_controller_rejected(self):
        self.args.local = False
        with self.assertRaises(ValueError): cli.managed_handoff(self.args, {'machine_id': 'a' * 32}, self.config, self.server)
        self.args.local = True
        for key, value in [('host_machine_id', 'c' * 32), ('domain_uuid', IDENTITIES['network']), ('ansible_host', '192.168.1.1')]:
            previous = self.value[key]; self.value[key] = value
            with self.subTest(key=key), self.assertRaises(ValueError):
                cli.managed_handoff(self.args, {'machine_id': 'a' * 32}, self.config, self.server)
            self.value[key] = previous

    def test_entrypoint_starts_outside_checkout_without_pythonpath(self):
        env = {k: v for k, v in os.environ.items() if not k.startswith('PYTHON')}
        with tempfile.TemporaryDirectory() as directory:
            result = subprocess.run([sys.executable, str(ROOT / 'scripts/infra/cli-vm.py'), '--help'], cwd=directory, env=env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('--review', result.stdout)


if __name__ == '__main__':
    unittest.main()
