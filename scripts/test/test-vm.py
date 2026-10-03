#!/usr/bin/env python3
"""VM plan policy, approval ordering, retained storage and recovery boundaries."""
import argparse
import base64
from contextlib import redirect_stdout
import importlib.util
import io
import json
import os
from pathlib import Path
import tempfile
import time
import unittest
import zipfile
from unittest.mock import Mock, patch
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[2]


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, ROOT / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


vm = load('vm_test', 'scripts/infra/vm.py')
recovery = load('vm_recovery_test', 'scripts/infra/vm-state.py')
seed = load('vm_seed_test', 'scripts/infra/seed-platform.py')
rehearsal = load('vm_rehearsal_test', 'scripts/infra/rehearse-vm.py')
policy = vm.policy


def resource(address, actions, before=None, after=None):
    return {'address': address, 'mode': 'managed', 'change': {'actions': actions, 'before': before, 'after': after}}


def plan(*resources):
    return {'format_version': '1.2', 'complete': True, 'resource_changes': list(resources)}


class VMTests(unittest.TestCase):
    def test_configuration_rejects_wrong_types_and_public_or_overlapping_networks(self):
        config = json.loads((ROOT / 'infra/libvirt/vm.json').read_text())
        self.assertEqual(policy.config(config), config)
        for changes in ({'cpus': True}, {'memory_mib': 1}, {'network_cidr': '8.8.8.0/24', 'guest_ip': '8.8.8.10'},
                        {'guest_ip': '192.168.234.10'}, {'mac': 'command;exec'}, {'extra': True}):
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                policy.config(dict(config, **changes))
        policy.no_overlap(config['network_cidr'], ['192.168.234.0/24', '172.18.0.0/16'])
        for network in ['192.168.0.0/16', '192.168.235.0/24', '192.168.235.10/32']:
            with self.assertRaises(ValueError):
                policy.no_overlap(config['network_cidr'], [network])

    def test_normal_updates_and_explicit_os_replacement(self):
        self.assertEqual(policy.plan_changes(plan(resource('libvirt_domain.k3s', ['update'])), {}),
                         [{'address': 'libvirt_domain.k3s', 'actions': ['update']}])
        p = plan(resource('libvirt_volume.os', ['delete', 'create']))
        with self.assertRaisesRegex(ValueError, 'protected'):
            policy.plan_changes(p, {}, False)
        self.assertTrue(policy.plan_changes(p, {}, True))

    def test_all_retained_deletes_and_missing_data_recreation_are_rejected(self):
        for address in policy.RETAINED:
            for actions in (['delete'], ['delete', 'create'], ['create', 'delete']):
                with self.subTest(address=address, actions=actions), self.assertRaises(ValueError):
                    policy.plan_changes(plan(resource(address, actions)), {}, True)
            with self.assertRaisesRegex(ValueError, 'missing'):
                policy.plan_changes(plan(resource(address, ['create'])), {address: {}}, True)
        self.assertTrue(policy.plan_changes(plan(resource('libvirt_volume.data', ['create'])), {}))

    def test_unexpected_resource_and_incomplete_plans_are_rejected(self):
        for candidate in (plan(resource('libvirt_volume.other', ['create'])), dict(plan(), complete=False),
                          dict(plan(), errored=True), dict(plan(), format_version='2.0')):
            with self.assertRaises(ValueError):
                policy.plan_changes(candidate, {})

    def test_digest_ignores_only_noop_refresh_and_timestamp(self):
        p = plan(resource('libvirt_domain.k3s', ['update'], {'memory': 1024}, {'memory': 2048}))
        q = dict(p, timestamp='later', resource_changes=p['resource_changes'] + [resource('libvirt_pool.k3s', ['no-op'], {'available': 1}, {'available': 2})])
        self.assertEqual(policy.plan_digest(p), policy.plan_digest(q))
        self.assertNotEqual(policy.plan_digest(p), policy.plan_digest(plan(resource('libvirt_domain.k3s', ['update'], {'memory': 1024}, {'memory': 4096}))))

    def test_receipt_rejects_changed_state_inputs_revision_intent_and_time(self):
        original = {'schema': 1, 'revision': 'a'*40, 'identity': 'inputs', 'state_digest': 'state', 'replace_os': False, 'created': time.time()}
        policy.receipt(original, 'a'*40, 'inputs', 'state', False)
        for change in ({'revision': 'b'*40}, {'identity': 'other'}, {'state_digest': 'changed'}, {'replace_os': True},
                       {'created': time.time()-1801}, {'created': time.time()+60}):
            with self.subTest(change=change), self.assertRaises(ValueError):
                policy.receipt(dict(original, **change), 'a'*40, 'inputs', 'state', False)

    def test_generated_inventory_requires_strict_host_identity(self):
        connection = {'domain_uuid': 'a'*8+'-'+ 'b'*4+'-'+ 'c'*4+'-'+ 'd'*4+'-'+ 'e'*12,
            'hostname': 'battlefield-k3s', 'address': '192.168.235.10', 'user': 'battlefield',
            'data_device': '/dev/disk/by-id/virtio-battlefield-k3s-data', 'data_volume': '/srv/battlefield-data/libvirt/images/battlefield-k3s-data.qcow2'}
        output = policy.inventory(connection, Path('/tmp/key'), Path('/tmp/known'))
        host = output['all']['children']['k3s']['hosts']['battlefield-k3s']
        self.assertEqual(host['ansible_host'], connection['address'])
        self.assertIn('StrictHostKeyChecking=yes', host['ansible_ssh_common_args'])
        for key, value in [('user', 'root'), ('hostname', 'other'), ('domain_uuid', 'invalid'), ('domain_uuid', 3)]:
            with self.assertRaises(ValueError):
                policy.inventory(dict(connection, **{key: value}), Path('/tmp/key'), Path('/tmp/known'))

    def test_private_paths_reject_symlinks_and_shared_permissions(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root/'other').mkdir()
            (root/'alias').symlink_to(root/'other')
            with self.assertRaises(ValueError):
                vm.tools.private_directory(root/'alias'/'child')
            (root/'other').chmod(0o755)
            with self.assertRaises(ValueError):
                vm.tools.private_directory(root/'other')
            (root/'file').symlink_to(root/'preserve')
            with self.assertRaises(ValueError):
                vm.tools.atomic(root/'file', b'no')
            self.assertFalse((root/'preserve').exists())

    def test_terraform_uses_private_short_ipc_and_preserves_state_environment(self):
        instance = object.__new__(vm.VM)
        instance.binary = '/verified/terraform'
        instance.env = {'TMPDIR': '/long/' + 'workspace/' * 20, 'TF_DATA_DIR': '/private/provider'}
        paths = []
        def run(command, *, timeout, env):
            directory = Path(env['TMPDIR'])
            paths.append(directory)
            self.assertTrue(directory.is_dir())
            self.assertLess(len(str(directory)), 60)
            self.assertEqual(directory.stat().st_mode & 0o777, 0o700)
            self.assertEqual(env['TF_DATA_DIR'], '/private/provider')
            return 'validated'
        instance.run = Mock(side_effect=run)
        self.assertEqual(instance.tf('validate'), 'validated')
        self.assertTrue(instance.env['TMPDIR'].startswith('/long/'))
        self.assertFalse(paths[0].exists())
        instance.run.side_effect = ValueError('synthetic')
        with self.assertRaisesRegex(ValueError, 'synthetic'):
            instance.tf('validate')
        self.assertFalse(Path(instance.run.call_args.kwargs['env']['TMPDIR']).exists())

    def test_cached_terraform_repairs_interrupted_executable_permissions(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            archive = root / 'terraform.zip'
            with zipfile.ZipFile(archive, 'w') as bundle:
                bundle.writestr('terraform', b'synthetic-verified-binary')
            with patch.object(vm.tools, 'download', return_value=archive):
                binary = vm.tools.terraform(root)
                binary.chmod(0o600)  # Correct bytes left by interruption before chmod.
                self.assertEqual(vm.tools.terraform(root), binary)
                self.assertEqual(binary.read_bytes(), b'synthetic-verified-binary')
                self.assertEqual(binary.stat().st_mode & 0o777, 0o700)

    def test_guest_agent_errors_are_sanitized_and_valid_key_is_returned(self):
        instance = object.__new__(vm.VM)
        malformed = [
            [{'error': {'desc': 'private-error'}}],
            [{'return': {}}],
            [{'return': {'pid': True}}],
            [{'return': {'pid': 42}}, {'return': []}],
            [{'return': {'pid': 42}}, {'return': {'exited': 'yes'}}],
            [{'return': {'pid': 42}}, {'return': {'exited': True, 'exitcode': 0}}],
            [{'return': {'pid': 42}}, {'return': {'exited': True, 'exitcode': 0, 'out-data': 17}}],
        ]
        for replies in malformed:
            with self.subTest(replies=replies):
                instance.virsh = Mock(side_effect=[json.dumps(value) for value in replies])
                with self.assertRaises(ValueError) as caught:
                    instance.guest_public_key('owned-uuid')
                self.assertNotIn('private-error', str(caught.exception))
        public = 'ssh-ed25519 cHVibGljLWZpeHR1cmU= host-comment'
        instance.virsh = Mock(side_effect=[json.dumps({'return': {'pid': 42}}), json.dumps({'return': {
            'exited': True, 'exitcode': 0, 'out-data': base64.b64encode(public.encode()).decode()}})])
        self.assertEqual(instance.guest_public_key('owned-uuid'), 'ssh-ed25519 cHVibGljLWZpeHR1cmU=')

    def test_rehearsal_rejects_negative_or_malformed_guest_result(self):
        for value in ({'passed': False}, {'passed': 1}, {}, None, []):
            with self.subTest(value=value), self.assertRaisesRegex(ValueError, 'did not report success'):
                rehearsal.require_guest_success(value)
        value = {'passed': True, 'checks': ['fixture']}
        self.assertEqual(rehearsal.require_guest_success(value), value)

    def test_state_rejects_other_modules_and_deposed_resources(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp)/'state'
            value = {'version': 4, 'resources': [{'type': 'libvirt_volume', 'name': 'data', 'mode': 'managed',
                'instances': [{'attributes': {'id': '/data'}}]}]}
            path.write_text(json.dumps(value)); path.chmod(0o600)
            self.assertEqual(vm.state_resources(path)['libvirt_volume.data']['id'], '/data')
            for change in ({'module': 'module.other'}, {'name': 'other'}, {'instances': [{'deposed': 'old', 'attributes': {}}]}):
                path.write_text(json.dumps(dict(value, resources=[dict(value['resources'][0], **change)])))
                with self.assertRaises(ValueError):
                    vm.state_resources(path)

    def approval_fixture(self, root):
        args = argparse.Namespace(action='apply', receipt=root/'receipt', report=root/'report', revision='a'*40, replace_os=False)
        (root/'vm.tfplan').write_bytes(b'saved plan'); (root/'vm.tfplan').chmod(0o600)
        reviewed = plan(resource('libvirt_domain.k3s', ['create']))
        data = {'schema': 1, 'revision': args.revision, 'identity': 'inputs', 'state_digest': '',
                'replace_os': False, 'created': time.time(), 'plan_digest': policy.plan_digest(reviewed),
                'saved_digest': vm.tools.digest(root/'vm.tfplan')}
        args.receipt.write_text(json.dumps(data)); args.receipt.chmod(0o600)
        instance = Mock(directory=root, identity='inputs', config={}, images=root/'images')
        instance.state_digest.return_value = ''
        instance.plan.return_value = (reviewed, [{'address': 'libvirt_domain.k3s', 'actions': ['create']}])
        instance.verify.return_value = {'ssh': True}
        return args, instance

    def test_apply_consumes_receipt_before_mutation_and_snapshots_partial_failure(self):
        for failed in (False, True):
            with self.subTest(failed=failed), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp); args, instance = self.approval_fixture(root)
                def apply(*arguments):
                    self.assertFalse(args.receipt.exists())
                    if failed:
                        raise ValueError('synthetic failure')
                instance.tf.side_effect = apply
                with patch.object(vm.platform, 'trusted_revision'), patch.object(vm, 'VM', return_value=instance), \
                        patch.object(vm.lock.Path, 'home', return_value=root), redirect_stdout(io.StringIO()):
                    if failed:
                        with self.assertRaisesRegex(ValueError, 'synthetic'):
                            vm.execute(args)
                    else:
                        vm.execute(args)
                self.assertEqual(instance.snapshot.call_count, 2)
                self.assertFalse((root/'vm.tfplan').exists())
                self.assertEqual(instance.verify.call_count, 0 if failed else 1)

    def test_changed_plan_and_drift_never_reach_apply(self):
        for changed in ('saved', 'drift', 'state'):
            with self.subTest(changed=changed), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp); args, instance = self.approval_fixture(root)
                if changed == 'saved': (root/'vm.tfplan').write_bytes(b'changed')
                elif changed == 'state': instance.state_digest.return_value = 'changed'
                else: instance.plan.return_value = (plan(resource('libvirt_domain.k3s', ['delete', 'create'])), [])
                with patch.object(vm.platform, 'trusted_revision'), patch.object(vm, 'VM', return_value=instance), \
                        patch.object(vm.lock.Path, 'home', return_value=root), self.assertRaises(ValueError):
                    vm.execute(args)
                instance.tf.assert_not_called()
                instance.snapshot.assert_not_called()

    def test_host_key_change_cannot_be_silently_accepted_by_verify(self):
        with tempfile.TemporaryDirectory() as tmp:
            instance = object.__new__(vm.VM)
            instance.directory = Path(tmp)
            instance.known_hosts = Path(tmp)/'known'
            instance.known_hosts.write_bytes(b'old key'); instance.known_hosts.chmod(0o600)
            instance.guard = Mock()
            instance.connection = Mock(return_value={'domain_uuid': 'uuid', 'address': '192.168.235.10'})
            instance.guest_public_key = Mock(return_value='ssh-ed25519 bmV3')
            instance.run = Mock()
            with self.assertRaisesRegex(ValueError, 'SSH identity changed'):
                instance.verify()
            instance.run.assert_not_called()
            self.assertEqual(instance.known_hosts.read_bytes(), b'old key')

    def test_capacity_counts_only_owned_files_without_parsing_virsh_tables(self):
        instance = object.__new__(vm.VM)
        instance.images = Path('/srv/test/libvirt/images')
        instance.existing = {'libvirt_pool.k3s': {'id': 'pool'}, 'libvirt_volume.data': {'name': 'data.qcow2'}}
        instance.sudo_run = Mock(return_value='["data.qcow2"]')
        instance.virsh = Mock(return_value='<volume><allocation>1234</allocation></volume>')
        self.assertEqual(instance.allocated_bytes(), 1234)
        instance.virsh.assert_called_once_with('vol-dumpxml', 'data.qcow2', '--pool', 'battlefield-k3s')
        instance.sudo_run.return_value = '["data.qcow2 extra"]'
        with self.assertRaisesRegex(ValueError, 'Unowned'):
            instance.allocated_bytes()
        instance.existing = {}
        with self.assertRaisesRegex(ValueError, 'Unowned'):
            instance.allocated_bytes()

    def test_connection_uses_uuid_and_requires_unix_guest_agent(self):
        identity = '12345678-1234-1234-1234-123456789abc'
        for channel in ('pty', 'unix'):
            with self.subTest(channel=channel):
                instance = object.__new__(vm.VM)
                instance.config = {'guest_ip': '192.168.235.10'}
                instance.images = Path('/srv/battlefield-data/libvirt/images')
                instance.key, instance.known_hosts = Path('/tmp/key'), Path('/tmp/known')
                connection = {'domain_uuid': identity, 'hostname': 'battlefield-k3s', 'address': '192.168.235.10',
                    'user': 'battlefield', 'data_device': '/dev/disk/by-id/virtio-battlefield-k3s-data',
                    'data_volume': str(instance.images/'battlefield-k3s-data.qcow2')}
                instance.tf = Mock(return_value=json.dumps({'connection': {'value': connection}}))
                xml = '<domain id="7"><uuid>' + identity + '</uuid><devices><channel type="' + channel + '"><target name="org.qemu.guest_agent.0"/></channel></devices></domain>'
                instance.virsh = Mock(side_effect=[xml, 'running'])
                if channel == 'pty':
                    with self.assertRaisesRegex(ValueError, 'UNIX'):
                        instance.connection()
                else:
                    self.assertEqual(instance.connection()['domain_uuid'], identity)
                    instance.virsh.assert_called_with('domstate', identity)

    def test_recovery_validates_all_live_identities_before_writing(self):
        config = json.loads((ROOT/'infra/libvirt/vm.json').read_text())
        inventory = {'machine_id': 'a'*32, 'user': 'operator', 'data_uuid': 'b'*36, 'data_mount': '/srv/test-data'}
        attributes = {'id': '/srv/test-data/libvirt/images/battlefield-k3s-data.qcow2', 'name': 'battlefield-k3s-data.qcow2'}
        payload = {'schema': 1, 'owner': inventory, 'config': config, 'key': 'synthetic-key',
                   'state': {'version': 4, 'resources': [{'type': 'libvirt_volume', 'name': 'data', 'mode': 'managed',
                              'instances': [{'attributes': attributes}]}]}}
        for wrong in (True, False):
            with self.subTest(wrong=wrong), tempfile.TemporaryDirectory() as tmp:
                directory = Path(tmp)
                path = '/wrong' if wrong else attributes['id']
                xml = '<volume><key>' + path + '</key><target><path>' + path + '</path></target></volume>'
                if wrong:
                    with self.assertRaisesRegex(ValueError, 'volume identity'):
                        recovery.restore(payload, inventory, directory, lambda *args: xml)
                    self.assertEqual(list(directory.iterdir()), [])
                else:
                    recovery.restore(payload, inventory, directory, lambda *args: xml)
                    self.assertEqual(json.loads((directory/'terraform.tfstate').read_text()), payload['state'])
                    self.assertEqual((directory/'id_ed25519').stat().st_mode & 0o777, 0o600)
                    # An identical interrupted restore is resumable, while a
                    # different pre-existing key must survive unchanged.
                    recovery.restore(payload, inventory, directory, lambda *args: xml)
                    (directory/'id_ed25519').write_text('preserve-existing')
                    with self.assertRaisesRegex(ValueError, 'replace existing'):
                        recovery.restore(payload, inventory, directory, lambda *args: xml)
                    self.assertEqual((directory/'id_ed25519').read_text(), 'preserve-existing')

    def test_failed_mount_guard_prevents_tool_preparation(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            args = argparse.Namespace(receipt=root/'receipt', report=root/'report', revision='a'*40)
            instance = Mock()
            instance.preflight.side_effect = ValueError('wrong filesystem')
            with patch.object(vm.platform, 'trusted_revision'), patch.object(vm, 'VM', return_value=instance), \
                    patch.object(vm.lock.Path, 'home', return_value=root), self.assertRaisesRegex(ValueError, 'filesystem'):
                vm.execute(args)
            instance.prepare.assert_not_called()
            instance.tf.assert_not_called()

    def test_jobs_only_preflights_all_owners_and_never_reads_or_writes_credentials(self):
        class Client:
            def __init__(self, foreign_vm=False):
                self.writes = []
                self.definitions = {'job/battlefield-platform/config.xml': seed.folder_xml(),
                    'job/battlefield-platform/job/host/config.xml': seed.job_xml('host'),
                    'job/battlefield-platform/job/k3s/config.xml': None,
                    'job/battlefield-platform/job/vm/config.xml':
                        b'<flow-definition><description>unowned</description></flow-definition>' if foreign_vm else None}
            def request(self, path, data=None, **kwargs):
                if 'credential' in path.lower() or 'build' in path.lower():
                    raise AssertionError('Jobs-only seeding must not access credentials or start builds')
                if data is not None:
                    self.writes.append((path, data))
                    return b''
                return self.definitions[path]
        for foreign in (False, True):
            with self.subTest(foreign=foreign), patch.object(seed, 'check_plugins'), redirect_stdout(io.StringIO()):
                client = Client(foreign)
                if foreign:
                    with self.assertRaisesRegex(ValueError, 'unowned'):
                        seed.seed_jobs(client)
                    self.assertEqual(client.writes, [])
                else:
                    seed.seed_jobs(client)
                    self.assertEqual(client.writes, [('job/battlefield-platform/job/host/config.xml', seed.job_xml('host')),
                        ('job/battlefield-platform/createItem?name=vm', seed.job_xml('vm')),
                        ('job/battlefield-platform/createItem?name=k3s', seed.job_xml('k3s'))])

    def test_vm_job_uses_trusted_main_and_explicit_replacement_default(self):
        job = ET.fromstring(seed.job_xml('vm'))
        self.assertEqual(job.findtext('definition/scriptPath'), 'infra/jenkins/Jenkinsfile.vm')
        self.assertEqual(job.findtext('.//hudson.plugins.git.BranchSpec/name'), '*/main')
        self.assertEqual(job.findtext('.//hudson.model.BooleanParameterDefinition/defaultValue'), 'false')
        pipeline = (ROOT/'infra/jenkins/Jenkinsfile.vm').read_text()
        self.assertIn("lock(resource: 'battlefield-production-platform')", pipeline)
        self.assertIn("submitter: 'whtan'", pipeline)
        self.assertNotIn("credentialsId: 'host-inputs'", pipeline)


if __name__ == '__main__':
    unittest.main()
