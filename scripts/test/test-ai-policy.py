#!/usr/bin/env python3
"""The AI policy script sends only validated statements to the named PostgreSQL pod."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / 'scripts/ops/ai-policy.sh'


class AIPolicyTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.log = Path(self.temp.name) / 'kubectl.log'
        stub = Path(self.temp.name) / 'kubectl'
        # The identity probe answers with TEST_UID; every other call is logged with its input.
        stub.write_text('#!/bin/sh\ncase "$*" in *"get namespace kube-system"*) printf "%s" "${TEST_UID:-cluster-dev}"; exit 0;; esac\n'
                        'printf "%s\\n" "$*" "KUBECONFIG=${KUBECONFIG:-}" >> "$KUBECTL_LOG"\ncat >> "$KUBECTL_LOG"\n')
        stub.chmod(0o755)
        clean = {key: value for key, value in os.environ.items()
                 if not key.startswith(('BF_', 'BATTLEFIELD_', 'DEPLOY_', 'KUBERNETES_', 'KIND_', 'DOCKER_')) and key != 'KUBECONFIG'}
        self.env = {**clean, 'PATH': f'{self.temp.name}:{os.environ["PATH"]}', 'KUBECTL_LOG': str(self.log),
                    'BATTLEFIELD_POSTGRES_POD': 'battlefield-postgres-0'}

    def run_policy(self, *args, env=None):
        return subprocess.run(['bash', str(SCRIPT), *args], capture_output=True, text=True, env=env or self.env,
                              stdin=subprocess.DEVNULL)

    def registered(self, name='dev', **overrides):
        kubeconfig = Path(self.temp.name) / 'kubeconfig'
        kubeconfig.touch()
        driver = (f'BF_DRIVER=k3s\nBF_KUBELET_CA_FILE={kubeconfig}\n' if name == 'production'
                  else 'BF_DOCKER_HOST=unix:///var/run/docker.sock\n')
        profile = Path(self.temp.name) / 'deployment.env'
        profile.write_text(f'BF_ENV={name}\nBF_CONTEXT=battlefield-{name}\nBF_CLUSTER=battlefield\nBF_UID=cluster-dev\n'
                           f'{driver}BF_URL=http://battlefield.localhost:8888\nBF_KUBECONFIG={kubeconfig}\nBF_DISPOSABLE=0\n')
        return {**self.env, 'DEPLOY_ENV': name, 'BATTLEFIELD_ENV_FILE': str(profile), **overrides}, kubeconfig

    def test_cli_slots_sets_a_value_from_one_to_eight(self):
        for slots in ('1', '8'):
            with self.subTest(slots=slots):
                self.log.unlink(missing_ok=True)
                result = self.run_policy('cli-slots', slots)
                self.assertEqual(result.returncode, 0, result.stderr)
                sent = self.log.read_text()
                self.assertIn('exec -i --namespace battlefield battlefield-postgres-0', sent)
                self.assertIn(f'UPDATE runtime_policy SET cli_concurrency={slots} WHERE id;', sent)

    def test_cli_slots_rejects_other_values_before_contacting_the_cluster(self):
        for value in ('0', '9', '10', '4;DROP', ''):
            with self.subTest(value=value):
                result = self.run_policy('cli-slots', value)
                self.assertEqual(result.returncode, 2)
                self.assertIn('from 1 to 8', result.stderr)
                self.assertFalse(self.log.exists())

    def test_named_environment_uses_its_registered_kubeconfig_and_context(self):
        env, kubeconfig = self.registered()
        result = self.run_policy('cli-slots', '8', env=env)
        self.assertEqual(result.returncode, 0, result.stderr)
        sent = self.log.read_text()
        self.assertIn('--context battlefield-dev exec -i --namespace battlefield battlefield-postgres-0', sent)
        self.assertIn(f'KUBECONFIG={kubeconfig}', sent)

    def test_named_environment_refuses_another_cluster(self):
        env, _ = self.registered(TEST_UID='another-cluster')
        result = self.run_policy('cli-slots', '8', env=env)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('identity mismatch', result.stderr)
        self.assertFalse(self.log.exists())

    def test_stop_works_while_a_k3s_platform_controller_is_unhealthy(self):
        # The K3s helper fails its full readiness check but passes the recovery check,
        # which keeps identity, node and storage verification.
        helper = Path(self.temp.name) / 'python3'
        helper.write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> "$HELPER_LOG"\ncase "$2" in verify-recovery) exit 0;; esac\nexit 1\n')
        helper.chmod(0o755)
        helper_log = Path(self.temp.name) / 'helper.log'
        env, _ = self.registered('production', HELPER_LOG=str(helper_log))
        result = self.run_policy('stop', env=env)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('verify-recovery battlefield-production battlefield', helper_log.read_text())
        self.assertIn('UPDATE runtime_policy SET ai_enabled=false WHERE id;', self.log.read_text())

    def test_requires_an_explicit_pod(self):
        env = {key: value for key, value in self.env.items() if key != 'BATTLEFIELD_POSTGRES_POD'}
        result = self.run_policy('status', env=env)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.log.exists())


if __name__ == '__main__':
    unittest.main()
