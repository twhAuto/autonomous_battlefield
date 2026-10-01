#!/usr/bin/env python3
"""Offline evidence tests for the isolated two-release K3s recovery rehearsal."""
import copy
import importlib.util
import os
import subprocess
import sys
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest.mock import Mock

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('delivery_rehearsal', ROOT/'scripts/infra/rehearse-k3s-delivery.py')
rehearsal = importlib.util.module_from_spec(spec)
spec.loader.exec_module(rehearsal)
OLD = {'backend': 'ghcr.io/test/backend@sha256:'+'a'*64,
       'frontend': 'ghcr.io/test/frontend@sha256:'+'b'*64, 'revision': 'c'*40}
NEW = {'backend': 'ghcr.io/test/backend@sha256:'+'d'*64,
       'frontend': 'ghcr.io/test/frontend@sha256:'+'e'*64, 'revision': 'f'*40}


def state(release):
    return {'deployments': {'items': [
        {'metadata': {'name': name, 'generation': 2},
         'spec': {'replicas': 1, 'template': {'spec': {'containers': [{
             'name': name.removeprefix('battlefield-'),
             'image': release['frontend' if name == 'battlefield-frontend' else 'backend']}]}}},
         'status': {'observedGeneration': 2, 'updatedReplicas': 1, 'readyReplicas': 1, 'availableReplicas': 1}}
        for name in (*rehearsal.recovery.BACKENDS, 'battlefield-frontend')]},
        'claims': {'items': [{'metadata': {'name': name, 'uid': 'uid-'+name},
            'spec': {'volumeName': 'pv-'+name, 'storageClassName': 'battlefield-retained'},
            'status': {'phase': 'Bound'}} for name in ('data-battlefield-postgres-0', 'battlefield-elasticsearch-data')]},
        'migrations': [0, 1, 2]}


class RehearsalTests(unittest.TestCase):
    def test_documented_cli_starts_without_import_shadowing(self):
        environment = {k: v for k, v in os.environ.items() if k not in ('PYTHONPATH', 'PYTHONSAFEPATH')}
        result = subprocess.run([sys.executable, str(ROOT/'scripts/infra/rehearse-k3s-delivery.py'), '--help'],
                                env=environment, capture_output=True, text=True, timeout=20)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('--previous-revision', result.stdout)

    def arguments(self, previous=None, **changes):
        values = NEW | {'previous_'+k: (previous or {}).get(k) for k in OLD}
        return SimpleNamespace(**(values | changes))

    def test_release_arguments_reject_partial_mutable_or_same_release_pairs(self):
        self.assertEqual(rehearsal.releases(self.arguments()), (NEW, NEW))
        self.assertEqual(rehearsal.releases(self.arguments(OLD)), (OLD, NEW))
        for changes in ({'previous_backend': None}, {'backend': 'ghcr.io/test/backend:latest'},
                        {'revision': 'HEAD'}, {'previous_revision': NEW['revision']},
                        {'previous_backend': NEW['backend'], 'previous_frontend': NEW['frontend']}):
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                rehearsal.releases(self.arguments(OLD, **changes))
        # A frontend-only release is a valid distinct pair.
        previous = OLD | {'backend': NEW['backend']}
        self.assertEqual(rehearsal.releases(self.arguments(previous)), (previous, NEW))

    def test_evidence_rejects_wrong_images_and_unfinished_rollouts(self):
        for change in ('wrong image', 'stale generation', 'old replicas', 'unready', 'unavailable', 'scaled down'):
            sample = state(OLD)
            item = sample['deployments']['items'][1]
            if change == 'wrong image':
                item['spec']['template']['spec']['containers'][0]['image'] = NEW['backend']
            elif change == 'stale generation': item['status']['observedGeneration'] = 1
            elif change == 'old replicas': item['status']['updatedReplicas'] = 0
            elif change == 'unready': item['status']['readyReplicas'] = 0
            elif change == 'unavailable': item['status']['availableReplicas'] = 0
            else: item['spec']['replicas'] = 0
            with self.subTest(change=change), self.assertRaisesRegex(ValueError, 'converge'):
                rehearsal.checkpoint(sample, OLD)

    def test_evidence_rejects_missing_unbound_or_wrong_storage(self):
        for change in ('missing', 'unbound', 'wrong class', 'missing volume'):
            sample = state(OLD)
            if change == 'missing': sample['claims']['items'].pop()
            elif change == 'unbound': sample['claims']['items'][0]['status']['phase'] = 'Pending'
            elif change == 'wrong class': sample['claims']['items'][0]['spec']['storageClassName'] = 'local-path'
            else: sample['claims']['items'][0]['spec']['volumeName'] = ''
            with self.subTest(change=change), self.assertRaises(ValueError):
                rehearsal.checkpoint(sample, OLD)
        for ledger in ([], None, [2, 1], [0, 1, 1]):
            with self.subTest(ledger=ledger), self.assertRaises(ValueError):
                rehearsal.checkpoint(state(OLD) | {'migrations': ledger}, OLD)

    def execute(self, snapshots, current=NEW, command=None, probe=None):
        report = {'passed': False}
        command = command or Mock()
        probe = probe or Mock()
        rehearsal.exercise_delivery(command, Mock(side_effect=snapshots), probe, OLD, current, report)
        return report, command, probe

    def test_two_release_run_proves_upgrade_and_restoration_without_replacing_record(self):
        report, command, probe = self.execute([state(OLD), state(OLD), state(NEW), state(OLD)])
        self.assertTrue(report['distinct_release_rollback'])
        self.assertFalse(report['passed'])  # Guest cleanup must succeed before overall acceptance.
        self.assertFalse(report['authenticated_public_smoke'])
        self.assertEqual(report['checkpoints']['current']['images']['battlefield-api'], NEW['backend'])
        self.assertEqual(report['checkpoints']['restored']['images']['battlefield-api'], OLD['backend'])
        self.assertEqual(probe.call_count, 4)
        records = [c for c in command.call_args_list if c.args[0].endswith('production-recovery.sh')]
        self.assertEqual(len(records), 1)
        self.assertEqual(records[0].args[-1], OLD['revision'])
        deploys = [c for c in command.call_args_list if c.args[0].endswith('with-ghcr-auth.sh')]
        self.assertEqual(deploys[0].kwargs['BACKEND_IMAGE_REVISION'], OLD['revision'])
        self.assertEqual(deploys[-1].kwargs['FRONTEND_IMAGE_REVISION'], NEW['revision'])
        self.assertEqual(deploys[1].kwargs['BACKEND_IMAGE_REF'], '')
        self.assertEqual(command.call_args_list[-1].args, ('scripts/cd/rollback.sh', OLD['backend'], OLD['frontend']))

    def test_legacy_reapplication_does_not_claim_distinct_release_recovery(self):
        report, command, _ = self.execute([state(OLD), state(OLD), state(OLD)], current=copy.copy(OLD))
        self.assertFalse(report['distinct_release_rollback'])
        self.assertNotIn('current', report['checkpoints'])
        self.assertEqual(len(command.call_args_list), 4)

    def test_changed_migrations_or_replaced_claims_stop_before_rollback(self):
        for change in ('migration', 'claim'):
            upgraded = state(NEW)
            if change == 'migration': upgraded['migrations'].append(3)
            else: upgraded['claims']['items'][0]['metadata']['uid'] = 'replacement'
            command = Mock()
            with self.subTest(change=change), self.assertRaisesRegex(ValueError, 'changed retained claims or migration state'):
                self.execute([state(OLD), state(OLD), upgraded], command=command)
            self.assertFalse(any(c.args[0].endswith('rollback.sh') for c in command.call_args_list))

    def test_noop_rollback_or_failed_probe_cannot_produce_passing_evidence(self):
        with self.assertRaisesRegex(ValueError, 'converge'):
            self.execute([state(OLD), state(OLD), state(NEW), state(NEW)])
        command = Mock()
        with self.assertRaisesRegex(ValueError, 'readiness failed'):
            self.execute([state(OLD)], command=command, probe=Mock(side_effect=ValueError('readiness failed')))
        self.assertEqual(command.call_count, 1)


if __name__ == '__main__':
    unittest.main()
