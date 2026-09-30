#!/usr/bin/env python3
"""Filesystem identity, adoption, read-only modes and libvirt guard regressions."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from types import SimpleNamespace
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location('storage', ROOT / 'infra/ansible/roles/data_storage/files/storage.py')
storage = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(storage)
BOOT = importlib.util.spec_from_file_location('bootstrap', ROOT / 'scripts/infra/server.py')
bootstrap = importlib.util.module_from_spec(BOOT)
BOOT.loader.exec_module(bootstrap)
UUID = 'a1111111-b222-c333-d444-e55555555555'
TARGET = Path('/srv/battlefield-data')
DEVICE = {'name': '/dev/vdb1', 'type': 'part', 'fstype': 'ext4', 'uuid': UUID,
          'label': 'Data Disk', 'partuuid': 'part-id', 'mountpoints': []}


class StorageTests(unittest.TestCase):
    def test_optional_inventory_and_ansible_mapping(self):
        base = dict(schema=1, host='host', user='operator', identity_file='/tmp/key', machine_id='a'*32)
        self.assertEqual(bootstrap.validate_inventory(base), base)
        for values in [dict(data_uuid='bad'), dict(data_uuid=True), dict(data_mount='/'),
                       dict(data_mount='/srv/../etc'), dict(data_mount='/srv/data/child'),
                       dict(data_mount='/srv/data;id'), dict(data_uuid=UUID, backup_uuid=UUID),
                       dict(data_uuid=UUID, backup_uuid=UUID.replace('-', ''))]:
            with self.subTest(values=values), self.assertRaises(ValueError):
                bootstrap.validate_inventory(dict(base, **values))
        result = bootstrap.ansible_inventory(dict(base, data_uuid=UUID, data_mount=str(TARGET)), True, '/usr/bin/sudo')
        self.assertEqual(result['all']['children']['bootstrap']['hosts']['server']['bootstrap_data_uuid'], UUID)

    def test_fstab_preserves_other_entries_and_is_idempotent(self):
        original = '# original\nUUID=root / ext4 defaults 0 1\n/swap.img none swap sw 0 0\n'
        updated = storage.fstab_content(original, UUID, TARGET, DEVICE)
        self.assertTrue(updated.startswith(original))
        self.assertEqual(updated, storage.fstab_content(updated, UUID, TARGET, DEVICE))
        for conflict in [f'UUID={UUID} /other ext4 defaults 0 2',
                         f'UUID=other {TARGET} ext4 defaults 0 2',
                         f'UUID={UUID.upper()} /elsewhere ext4 defaults 0 2',
                         'PARTUUID=PART-ID /elsewhere ext4 defaults 0 2',
                         r'LABEL=Data\040Disk /elsewhere ext4 defaults 0 2',
                         'PARTUUID=part-id /other ext4 defaults 0 2',
                         '/dev/vdb1 /elsewhere ext4 defaults 0 2']:
            with self.subTest(conflict=conflict), self.assertRaises(ValueError):
                storage.fstab_content(original + conflict + '\n', UUID, TARGET, DEVICE)
        with self.assertRaisesRegex(ValueError, 'Duplicate'):
            storage.fstab_content(updated + updated, UUID, TARGET, DEVICE)

    def test_wrong_duplicate_root_and_unformatted_devices_are_rejected(self):
        for devices in [[], [DEVICE, DEVICE], [dict(DEVICE, type='disk')],
                        [dict(DEVICE, fstype='ntfs')], [dict(DEVICE, mountpoints=['/'])],
                        [dict(DEVICE, mountpoints=['/other'])]]:
            with self.subTest(devices=devices), patch.object(storage, 'run', return_value=json.dumps({'blockdevices': devices})), self.assertRaises(ValueError):
                storage.selected_device(UUID, TARGET)
        with patch.object(storage, 'run', return_value=json.dumps({'blockdevices': [DEVICE]})):
            self.assertEqual(storage.selected_device(UUID, TARGET)['name'], '/dev/vdb1')

    def test_runtime_mount_identity_and_readonly_fail_closed(self):
        valid = dict(target=str(TARGET), uuid=UUID, fstype='ext4', options='rw,relatime', fsroot='/')
        for changes in [dict(uuid='wrong'), dict(options='ro,relatime'), dict(fsroot='/subdir'),
                        dict(fstype='tmpfs'), dict(target='/')]:
            result = subprocess.CompletedProcess([], 0, json.dumps({'filesystems': [dict(valid, **changes)]}), '')
            with patch.object(storage.subprocess, 'run', return_value=result), self.assertRaises(ValueError):
                storage.mounted(UUID, TARGET)
        result = subprocess.CompletedProcess([], 0, json.dumps({'filesystems': [valid]}), '')
        with patch.object(storage.subprocess, 'run', return_value=result):
            self.assertTrue(storage.mounted(UUID, TARGET))
        with patch.object(storage.subprocess, 'run', return_value=subprocess.CompletedProcess([], 1, '', '')):
            with self.assertRaisesRegex(ValueError, 'fallback'):
                storage.require_mount(UUID, TARGET)

    def test_symlinked_ancestors_and_workspace_are_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'real').mkdir()
            (root / 'link').symlink_to(root / 'real')
            for path in [root / 'link', root / 'link/child']:
                with self.assertRaises(ValueError):
                    storage.safe_directory(path)

    def test_unmounted_populated_target_is_not_hidden(self):
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory)
            (target / 'keep').write_text('keep')
            with patch.object(storage, 'selected_device', return_value=DEVICE), patch.object(storage, 'mounted', return_value=False), self.assertRaisesRegex(ValueError, 'hide'):
                storage.inspect(UUID, target)
            self.assertEqual((target / 'keep').read_text(), 'keep')

    def test_plan_and_verify_never_mutate(self):
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / 'data'
            with patch.object(storage, 'validate'), patch.object(storage, 'inspect_contract', return_value=False), \
                 patch.object(storage, 'inspect', return_value=(False, 'old', 'new', DEVICE)), \
                 patch.object(storage, 'directory_specs', return_value=[(target, 0, 0, 0o711)]), \
                 patch.object(storage, 'mounted', return_value=False), patch.object(storage, 'run') as run, \
                 patch.object(storage, 'write_fstab') as write:
                self.assertTrue(storage.reconcile(UUID, target, 'operator', 'plan')['changed'])
                with self.assertRaisesRegex(ValueError, 'differs'):
                    storage.reconcile(UUID, target, 'operator', 'verify')
                run.assert_not_called()
                write.assert_not_called()
                self.assertFalse(target.exists())

    def test_remap_contract_requires_migration(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'contract'
            expected = storage.contract_content(UUID, TARGET, 'operator')
            with patch.object(storage, 'CONTRACT', path):
                self.assertFalse(storage.inspect_contract(expected))
                path.write_text(json.dumps(dict(expected, mount='/srv/other')))
                path.chmod(0o600)  # Isolate identity checks from the invoking process's umask.
                # Fixture can be non-root in CI; mock only the root ownership check.
                metadata = path.stat()
                info = SimpleNamespace(st_mode=metadata.st_mode, st_uid=0)
                with patch.object(Path, 'stat', return_value=info), self.assertRaisesRegex(ValueError, 'identity changed'):
                    storage.inspect_contract(expected)

    def test_fstab_validation_failure_preserves_original(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'fstab'
            path.write_text('original\n')
            with patch.object(storage, 'FSTAB', path), patch.object(storage, 'run', side_effect=subprocess.CalledProcessError(1, 'findmnt')):
                with self.assertRaises(subprocess.CalledProcessError):
                    storage.write_fstab('original\n', 'bad\n', 'new entry')
            self.assertEqual(path.read_text(), 'original\n')
            self.assertEqual(list(Path(directory).iterdir()), [path])

    def test_fstab_validation_checks_only_new_entry_and_keeps_unrelated_lines(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'fstab'
            original = '/absent-device /mnt/absent ext4 noauto,nofail 0 0\n'
            path.write_text(original)
            entry = storage.fstab_entry(UUID, TARGET)
            checked = []
            def validate(*args):
                if args[0] == 'findmnt':
                    checked.append(Path(args[-1]).read_text())
            with patch.object(storage, 'FSTAB', path), patch.object(storage, 'run', side_effect=validate):
                storage.write_fstab(original, original + entry + '\n', entry)
            self.assertEqual(checked, [entry + '\n'])
            self.assertEqual(path.read_text(), original + entry + '\n')
            backup = next(path.parent.glob('fstab.before-battlefield-data.*'))
            self.assertEqual(backup.read_text(), original)

    def test_fstab_concurrent_write_is_preserved(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'fstab'
            path.write_text('original\n')
            def changed(*args):
                path.write_text('other writer\n')
            with patch.object(storage, 'FSTAB', path), patch.object(storage, 'run', side_effect=changed), self.assertRaisesRegex(ValueError, 'concurrently'):
                storage.write_fstab('original\n', 'ours\n', 'new entry')
            self.assertEqual(path.read_text(), 'other writer\n')

    def test_guard_requires_contract_and_same_filesystem(self):
        with patch.object(storage, 'inspect_contract', return_value=False), self.assertRaisesRegex(ValueError, 'ownership contract'):
            storage.reconcile(UUID, TARGET, 'operator', 'guard')
        with patch.object(storage, 'inspect_contract', return_value=True), patch.object(storage, 'require_mount', side_effect=ValueError('missing mount')), self.assertRaisesRegex(ValueError, 'missing mount'):
            storage.reconcile(UUID, TARGET, 'operator', 'guard')

    def test_hook_scopes_events_and_preserves_xml_filter_output(self):
        template = (ROOT / 'infra/ansible/roles/data_storage/templates/k3s-storage-hook.j2').read_text()
        # Substitute a failing guard, verifying unrelated guests/events don't invoke it.
        lines = [line if 'exec /usr/bin/python3' not in line else '    exit 42' for line in template.splitlines()]
        with tempfile.TemporaryDirectory() as directory:
            script = Path(directory) / 'hook'
            script.write_text('\n'.join(lines) + '\n')
            for domain, operation, expected in [('battlefield-agent', 'prepare', 0),
                    ('battlefield-k3s', 'reconnect', 0), ('battlefield-k3s', 'release', 0),
                    *[('battlefield-k3s', action, 42) for action in ['prepare', 'start', 'restore', 'migrate']]]:
                result = subprocess.run(['sh', str(script), domain, operation, 'begin', '-'], input='<domain/>', text=True, capture_output=True)
                self.assertEqual(result.returncode, expected)
                self.assertEqual(result.stdout, '')


if __name__ == '__main__':
    unittest.main()
