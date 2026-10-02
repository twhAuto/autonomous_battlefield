#!/usr/bin/env python3
"""Offline safety and evidence checks; these tests never access libvirt or sudo."""
import importlib.util
import json
import subprocess
import os
import sys
import tempfile
from types import SimpleNamespace
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

ROOT = Path(__file__).resolve().parents[2]


def module(name, relative):
    spec = importlib.util.spec_from_file_location(name, ROOT / relative)
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


host = module('rehearsal_host', 'scripts/infra/rehearse-server.py')
guest = module('rehearsal_guest', 'scripts/test/rehearse-server-guest.py')


class RehearsalTests(unittest.TestCase):
    def test_documented_rehearsal_clis_start_without_import_shadowing(self):
        environment = {k: v for k, v in os.environ.items() if k not in ('PYTHONPATH', 'PYTHONSAFEPATH')}
        with tempfile.TemporaryDirectory() as directory:
            for name in ('server', 'vm', 'k3s', 'k3s-delivery', 'cli-vm'):
                with self.subTest(entry_point=name):
                    result = subprocess.run(
                        [sys.executable, str(ROOT / f'scripts/infra/rehearse-{name}.py'), '--help'],
                        cwd=directory, env=environment, capture_output=True, text=True, timeout=20)
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertIn('--image', result.stdout)
                    self.assertIn('--evidence-dir', result.stdout)

    def test_image_integrity_and_backing_file_rejection(self):
        with tempfile.TemporaryDirectory() as directory:
            image = Path(directory) / 'image'
            image.write_bytes(b'cloud image')
            with patch.object(host, 'run') as run:
                with self.assertRaisesRegex(ValueError, 'SHA-256'):
                    host.checked_image(image, '0' * 64)
                run.assert_not_called()
                run.return_value = json.dumps({'format': 'qcow2', 'backing-filename': '/production/disk'})
                with self.assertRaisesRegex(ValueError, 'standalone'):
                    host.checked_image(image, host.digest(image))
                run.return_value = json.dumps({'format': 'qcow2'})
                host.checked_image(image, host.digest(image))

    def test_cleanup_rejects_a_replaced_domain_without_mutations(self):
        with tempfile.TemporaryDirectory() as directory:
            instance = host.Rehearsal(Path(directory), Path(directory), 'default')
            instance.domain_defined = True
            with patch.object(host, 'virsh', return_value='<domain><uuid>another-domain</uuid></domain>') as virsh:
                with self.assertRaisesRegex(ValueError, 'identity changed'):
                    instance.cleanup()
                self.assertEqual(virsh.call_args_list[0].args, ('dumpxml', instance.identity))
                self.assertEqual(virsh.call_count, 1)

    def test_cleanup_rejects_a_retargeted_pool(self):
        with tempfile.TemporaryDirectory() as directory:
            instance = host.Rehearsal(Path(directory), Path(directory), 'default')
            instance.pool_defined = True
            xml = f'<pool type="dir"><uuid>{instance.identity}</uuid><target><path>/production</path></target></pool>'
            with patch.object(host, 'virsh', return_value=xml) as virsh:
                with self.assertRaisesRegex(ValueError, 'Pool path changed'):
                    instance.cleanup()
                self.assertEqual(virsh.call_count, 1)

    def test_cleanup_deletes_only_recorded_volumes_and_checks_identity_first(self):
        with tempfile.TemporaryDirectory() as directory:
            instance = host.Rehearsal(Path(directory), Path(directory), 'default')
            instance.pool_defined = instance.pool_started = instance.pool_built = instance.domain_defined = True
            instance.volumes = ['root.qcow2', 'seed.iso']
            def response(*args):
                if args[0] == 'dumpxml':
                    return f'<domain><uuid>{instance.identity}</uuid></domain>'
                if args[0] == 'pool-dumpxml':
                    return f'<pool type="dir"><uuid>{instance.identity}</uuid><target><path>{instance.pool_path}</path></target></pool>'
                return 'running' if args[0] == 'domstate' else ''
            with patch.object(host, 'virsh', side_effect=response) as virsh:
                instance.cleanup()
                actions = [call.args for call in virsh.call_args_list]
                self.assertEqual([item[0] for item in actions], ['dumpxml', 'domstate', 'destroy', 'undefine',
                    'pool-dumpxml', 'vol-delete', 'vol-delete', 'pool-destroy', 'pool-delete', 'pool-undefine'])
                self.assertEqual([item[1] for item in actions if item[0] == 'vol-delete'], instance.volumes)

    def test_network_requires_existing_nat_and_ipv4_dhcp(self):
        for xml in ['<network/>', '<network><forward mode="bridge"/></network>',
                    '<network><forward mode="nat"/><ip address="192.0.2.1" prefix="24"/></network>']:
            with self.subTest(xml=xml), self.assertRaises(ValueError):
                host.network_details(xml)
        self.assertEqual(str(host.network_details('<network><forward mode="nat"/><ip address="192.0.2.1" prefix="24"><dhcp><range start="192.0.2.2" end="192.0.2.254"/></dhcp></ip></network>')), '192.0.2.0/24')

    def test_dual_stack_network_uses_ipv4_and_lease_parser_ignores_other_fields(self):
        ipv4 = '<ip address="192.0.2.1" prefix="24"><dhcp><range start="192.0.2.2" end="192.0.2.254"/></dhcp></ip>'
        ipv6 = '<ip family="ipv6" address="fd00::1" prefix="64"><dhcp><range start="fd00::2" end="fd00::ff"/></dhcp></ip>'
        self.assertEqual(str(host.network_details('<network><forward mode="nat"/>' + ipv6 + ipv4 + '</network>')), '192.0.2.0/24')
        for entries in [ipv6, ipv4 + ipv4]:
            with self.assertRaisesRegex(ValueError, 'one IPv4'):
                host.network_details('<network><forward mode="nat"/>' + entries + '</network>')
        instance = host.Rehearsal(Path('/tmp'), Path('/tmp'), 'default')
        instance.subnet = host.ipaddress.ip_network('192.0.2.0/24')
        with patch.object(host, 'virsh', return_value=instance.mac + ' weird/field fd00::2/64 192.0.2.10/24'):
            self.assertEqual(instance.lease(), '192.0.2.10')

    def test_malformed_xml_is_reported_without_raw_traceback_or_cleanup(self):
        with self.assertRaisesRegex(ValueError, 'Invalid libvirt XML'):
            host.network_details('<network')
        instance = host.Rehearsal(Path('/tmp'), Path('/tmp'), 'default')
        instance.domain_defined = True
        with patch.object(host, 'virsh', return_value='<domain') as virsh:
            with self.assertRaisesRegex(ValueError, 'Invalid libvirt XML'):
                instance.cleanup()
            self.assertEqual(virsh.call_count, 1)

    def test_reboot_waits_past_old_boot_and_expected_ssh_disconnect(self):
        old, new = '11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222'
        instance = host.Rehearsal(Path('/tmp'), Path('/tmp'), 'default')
        lost = subprocess.CalledProcessError(255, 'ssh')
        for reboot_result in ['', lost]:
            with self.subTest(disconnect=bool(reboot_result)), \
                    patch.object(instance, 'ssh', side_effect=[old, reboot_result, old, lost, new]) as ssh, \
                    patch.object(host.time, 'sleep'):
                instance.reboot()
                self.assertEqual(ssh.call_count, 5)
                self.assertEqual(ssh.call_args_list[1].args, ('sudo systemctl --no-block reboot',))

    def test_reboot_rejects_command_failure_or_unchanged_boot(self):
        old = '11111111-1111-4111-8111-111111111111'
        instance = host.Rehearsal(Path('/tmp'), Path('/tmp'), 'default')
        with patch.object(instance, 'ssh', side_effect=[old, subprocess.CalledProcessError(1, 'ssh')]), \
                self.assertRaises(subprocess.CalledProcessError):
            instance.reboot()
        with patch.object(instance, 'ssh', side_effect=[old, '', old]), \
                patch.object(host.time, 'sleep'), patch.object(host.time, 'monotonic', side_effect=[0, 0, 301]), \
                self.assertRaisesRegex(ValueError, 'new boot ID'):
            instance.reboot()

    def test_host_failures_report_operation_without_captured_output(self):
        for error in [subprocess.TimeoutExpired(['qemu-img', 'convert'], 600, output='private-output'),
                      subprocess.CalledProcessError(1, ['qemu-img', 'convert'], stderr='private-output')]:
            with patch.object(host.subprocess, 'run', side_effect=error), self.assertRaisesRegex(ValueError, 'qemu-img convert') as failure:
                host.run('qemu-img', 'convert')
            self.assertNotIn('private-output', str(failure.exception))
        instance = host.Rehearsal(Path('/tmp'), Path('/tmp'), 'default')
        with patch.object(instance, 'ssh', side_effect=subprocess.CalledProcessError(2, 'ssh', output='private-output')), \
                self.assertRaisesRegex(ValueError, 'degraded') as failure:
            instance.wait_cloud_init()
        self.assertNotIn('private-output', str(failure.exception))

    def test_ssh_binary_transfer_preserves_trailing_bytes(self):
        result = subprocess.CompletedProcess([], 0, stdout=b'archive\n\t ', stderr=b'')
        with patch.object(host.subprocess, 'run', return_value=result):
            self.assertEqual(host.run('ssh', text=False), b'archive\n\t ')

    def test_guest_marker_cannot_authorize_a_different_vm(self):
        identity = '11111111-1111-4111-8111-111111111111'
        marker = MagicMock()
        marker.is_symlink.return_value = False
        marker.stat.return_value = SimpleNamespace(st_uid=0, st_mode=0o100444)
        marker.read_text.return_value = identity
        with patch.object(guest, 'Path', return_value=marker), patch.object(guest, 'run', return_value='22222222-2222-4222-8222-222222222222') as run:
            with self.assertRaisesRegex(ValueError, 'hardware identity'):
                guest.require_guest(identity)
            run.assert_called_once_with('sudo', '-n', 'cat', '/sys/class/dmi/id/product_uuid')
        marker.stat.return_value = SimpleNamespace(st_uid=1000, st_mode=0o100444)
        with patch.object(guest, 'Path', return_value=marker), patch.object(guest, 'run') as run:
            with self.assertRaisesRegex(ValueError, 'Unsafe rehearsal marker'):
                guest.require_guest(identity)
            run.assert_not_called()

    def test_evidence_rejects_changes_failures_and_missing_recap(self):
        for text in ['', 'server : ok=10 changed=0 unreachable=1 failed=0',
                     'server : ok=10 changed=1 unreachable=0 failed=0',
                     'server : ok=10 changed=0 unreachable=0 failed=1']:
            with self.subTest(text=text), self.assertRaises(ValueError):
                guest.assert_recap(text)
        self.assertEqual(guest.assert_recap('server : ok=10 changed=0 unreachable=0 failed=0 skipped=5'), 0)

    def test_expected_refusal_requires_failure_message_and_no_changes(self):
        recap = 'server : ok=20 changed=0 unreachable=0 failed=1'
        guest.assert_expected_failure('HWE detected\n' + recap, 1, 'HWE detected')
        for output, code in [(recap, 1), ('HWE detected\n' + recap, 0),
                             ('HWE detected\n' + recap.replace('changed=0', 'changed=1'), 1)]:
            with self.subTest(output=output, code=code), self.assertRaises(ValueError):
                guest.assert_expected_failure(output, code, 'HWE detected')

    def test_state_evidence_checks_credentials_service_restarts_and_real_reboot(self):
        before = {'files': {'credential': 'original'}, 'mmap': 1048576,
                  'services': {'docker': 'first'}, 'boot_id': 'first'}
        guest.same_state(before, dict(before))
        for changed in [{'files': {'credential': 'replacement'}}, {'services': {'docker': 'second'}}, {'mmap': 1}]:
            with self.subTest(changed=changed), self.assertRaises(ValueError):
                guest.same_state(before, before | changed)
        with self.assertRaisesRegex(ValueError, 'actually reboot'):
            guest.same_state(before, before, reboot=True)
        guest.same_state(before, before | {'boot_id': 'second', 'services': {'docker': 'second'}}, reboot=True)

    def test_secret_output_is_rejected_before_archival(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(guest, 'PRIVATE', Path(directory)):
            (Path(directory) / 'secret').write_text('synthetic-secret\n')
            (Path(directory) / 'password').write_text('synthetic-password\n')
            for text in ['synthetic-secret', 'synthetic-password', 'c3ludGhldGljLXNlY3JldAo=']:
                with self.subTest(text=text), self.assertRaisesRegex(ValueError, 'refusing to archive'):
                    guest.reject_secrets(text)
            guest.reject_secrets('censored because no_log is enabled')


if __name__ == '__main__':
    unittest.main()
