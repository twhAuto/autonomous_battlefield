"""Production boundaries with synthetic credentials and no live cluster mutations."""
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import ssl
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import Mock
import urllib.error
import urllib.request

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[2]


def module(name, file):
    spec = importlib.util.spec_from_file_location(name, ROOT / file)
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


capture = module('capture', 'scripts/security/capture-zap-traffic.py')
recovery = module('recovery', 'scripts/lib/production_recovery.py')
tls = module('tls', 'scripts/ops/configure-production-prometheus.py')
launch = module('launch', 'scripts/ops/render-limited-launch.py')
ORIGIN = 'https://play.example.com'
ISSUER = 'https://team.cloudflareaccess.com'
TOKEN = dict(client_id='a'*32+'.access', client_secret='b'*64, origin=ORIGIN)


class Response(io.BytesIO):
    status = 200

    def __init__(self, body, mime):
        super().__init__(body.encode())
        self.headers = {'Content-Type': mime, 'Set-Cookie': 'secret-cookie', 'CF-Access-Jwt-Assertion': 'private-assertion'}


class CaptureTests(unittest.TestCase):
    def client(self, role='ci', catalog=None):
        session = {'authenticated': True, 'mode': 'cloudflare', 'actor': {'role': role, 'id': ISSUER+'|service:'+TOKEN['client_id']}, 'capabilities': {'admin': False}}
        responses = [Response(json.dumps(session), 'application/json'),
            Response(json.dumps({'scenarios': []} if catalog is None else catalog), 'application/json'), Response('ok', 'text/plain')]
        client = Mock()
        client.open.side_effect = responses
        return client

    def test_captures_only_authenticated_reads_without_credentials(self):
        client = self.client()
        result = capture.capture(ORIGIN, TOKEN, ISSUER, client)
        self.assertEqual([e['request']['url'] for e in result['log']['entries']], [ORIGIN+p for p in capture.PATHS])
        for call in client.open.call_args_list:
            request = call.args[0]
            self.assertEqual(request.get_method(), 'GET')
            self.assertEqual(request.get_header('Cf-access-client-secret'), TOKEN['client_secret'])
        for secret in (TOKEN['client_secret'], 'secret-cookie', 'private-assertion'):
            self.assertNotIn(secret, json.dumps(result))
        self.assertNotIn('spider', [j['type'] for j in capture.plan(ORIGIN)['jobs']])
        self.assertEqual(capture.plan(ORIGIN)['jobs'][0]['parameters']['type'], 'har')

    def test_login_admin_wrong_catalog_reflection_and_redirect_fail(self):
        for client in (self.client(role='admin'), self.client(catalog={'login': 'Cloudflare'}),
                       self.client(catalog={'scenarios': [TOKEN['client_secret']]})):
            with self.assertRaises(ValueError): capture.capture(ORIGIN, TOKEN, ISSUER, client)
        with self.assertRaises(ValueError): capture.capture('https://attacker.invalid', TOKEN, ISSUER, self.client())
        class Redirect(urllib.request.HTTPSHandler):
            def https_open(self, request):
                response = urllib.response.addinfourl(io.BytesIO(b''), {'Location': 'https://attacker.invalid/'}, request.full_url, 302)
                response.msg = 'Found'
                return response
        client = urllib.request.build_opener(urllib.request.ProxyHandler({}), capture.NoRedirect(), Redirect())
        with self.assertRaises(urllib.error.HTTPError): capture.capture(ORIGIN, TOKEN, ISSUER, client)


class RecoveryTests(unittest.TestCase):
    def state(self):
        return {'items': [{'metadata': {'name': name, 'generation': 3}, 'status': {'observedGeneration': 3, 'updatedReplicas': 1, 'readyReplicas': 1},
            'spec': {'replicas': 1, 'template': {'spec': {'containers': [{'name': name.removeprefix('battlefield-'), 'image': 'ghcr.io/test/image@sha256:'+'a'*64}]}}}}
            for name in (*recovery.BACKENDS, 'battlefield-frontend')]}

    def test_only_exact_cluster_and_image_pair_authorizes_recovery(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp)/'recovery.json'
            digest = 'ghcr.io/test/image@sha256:'+'a'*64
            self.assertEqual(recovery.approved(path, 'cluster', digest, digest, [0, 1]), '')
            recovery.record(path, 'cluster', 'b'*40, self.state(), [0, 1])
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            self.assertEqual(recovery.approved(path, 'cluster', digest, digest, [0, 1]), digest)
            self.assertEqual(recovery.approved(path, 'cluster', digest, digest, [0, 1, 2]), '')
            self.assertEqual(recovery.approved(path, 'cluster', digest, digest, [0]), '')
            self.assertEqual(recovery.approved(path, 'other-cluster', digest, digest, [0, 1]), '')
            self.assertEqual(recovery.approved(path, 'cluster', digest, 'other', [0, 1]), '')
            previous = path.read_bytes()
            state = self.state(); state['items'][0]['status']['readyReplicas'] = 0
            with self.assertRaises(ValueError): recovery.record(path, 'cluster', 'b'*40, state, [0, 1])
            self.assertEqual(path.read_bytes(), previous)
            state = self.state(); state['items'][1]['spec']['template']['spec']['containers'][0]['image'] = 'ghcr.io/test/image@sha256:'+'c'*64
            with self.assertRaises(ValueError): recovery.record(path, 'cluster', 'b'*40, state, [0, 1])
            state = self.state(); state['items'][-1]['spec']['replicas'] = 0
            state['items'][-1]['status'].update(updatedReplicas=0, readyReplicas=0)
            with self.assertRaises(ValueError): recovery.record(path, 'cluster', 'b'*40, state, [0, 1])
            alias = Path(tmp)/'alias.json'; alias.symlink_to(path)
            with self.assertRaises(ValueError): recovery.approved(alias, 'cluster', digest, digest, [0, 1])
            path.chmod(0o644)
            with self.assertRaises(ValueError): recovery.approved(path, 'cluster', digest, digest, [0, 1])


class RenderingTests(unittest.TestCase):
    def test_kubelet_export_requires_identity_and_public_unexpired_certificates(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            fixture = directory/'project'
            script = fixture/'scripts/ops/export-kind-kubelet-ca.sh'
            script.parent.mkdir(parents=True)
            shutil.copy(ROOT/'scripts/ops/export-kind-kubelet-ca.sh', script)
            (fixture/'scripts/lib').mkdir()
            (fixture/'scripts/lib/deployment-environment.sh').write_text('''
battlefield_env_preflight() {
  export BATTLEFIELD_ENV_FILE="$FIXTURE/deployment.env" KIND_CLUSTER_NAME=test
}
battlefield_env_verify_docker() { [[ "${WRONG_CLUSTER:-0}" == 0 ]]; }
''')
            bins = directory/'bin'; bins.mkdir()
            (bins/'kind').write_text('#!/bin/sh\n[ "$*" = "get nodes --name test" ] || exit 1\necho test-node\n')
            (bins/'docker').write_text('#!/bin/sh\n[ "$*" = "exec test-node cat /var/lib/kubelet/pki/kubelet.crt" ] || exit 1\ncat "$FIXTURE/source.pem"\n')
            for command in bins.iterdir(): command.chmod(0o755)
            certificate = directory/'source.pem'
            subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '8',
                '-subj', '/CN=test-node', '-keyout', str(directory/'key.pem'), '-out', str(certificate)],
                check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            env = {**os.environ, 'FIXTURE': str(directory), 'PATH': str(bins)+':'+os.environ['PATH']}
            command = ['bash', str(script)]
            subprocess.run(command, env=env, check=True, stdout=subprocess.DEVNULL)
            exported = directory/'kubelet-ca.crt'
            self.assertEqual(exported.read_bytes(), certificate.read_bytes())
            self.assertEqual(exported.stat().st_mode & 0o777, 0o600)
            previous = exported.read_bytes()
            result = subprocess.run(command, env={**env, 'WRONG_CLUSTER': '1'}, capture_output=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(exported.read_bytes(), previous)
            certificate.write_bytes((directory/'key.pem').read_bytes())
            result = subprocess.run(command, env=env, capture_output=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(exported.read_bytes(), previous)
            subprocess.run(['openssl', 'req', '-x509', '-key', str(directory/'key.pem'), '-days', '1',
                '-subj', '/CN=test-node', '-out', str(certificate)], check=True, capture_output=True)
            result = subprocess.run(command, env=env, capture_output=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(exported.read_bytes(), previous)

    def test_production_zap_container_has_no_credentials_or_network_and_fails_closed(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            fixture = directory/'project'
            for relative in ('scripts/security/zap-baseline.sh', 'scripts/ci/tool-versions.env'):
                target = fixture/relative; target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy(ROOT/relative, target)
            (fixture/'scripts/lib').mkdir()
            (fixture/'scripts/lib/deployment-environment.sh').write_text('battlefield_env_preflight() { :; }\n')
            (fixture/'scripts/security/capture-zap-traffic.py').write_text('import sys\nfrom pathlib import Path\n(Path(sys.argv[1])/"plan.yaml").write_text("fixture")\n')
            bins = directory/'bin'; bins.mkdir()
            docker = bins/'docker'
            docker.write_text('''#!/usr/bin/env python3
import json,os,sys
from pathlib import Path
args=sys.argv[1:]
assert args[args.index('--network')+1]=='none'
assert 'BATTLEFIELD_ACCESS_SERVICE_TOKEN_FILE' not in ' '.join(args)
assert 'private-service-file' not in ' '.join(args)
assert args[-4:]==['zap.sh','-cmd','-autorun','/capture/plan.yaml']
mount=next(a for a in args if a.endswith(':/zap/wrk:rw'))
if os.environ.get('FAIL_SCAN'): sys.exit(23)
Path(mount.removesuffix(':/zap/wrk:rw'), 'zap-report.json').write_text(json.dumps({'site':[{'@name':'https://play.example.com'}]}))
''')
            docker.chmod(0o755)
            env = {**os.environ, 'DEPLOY_ENV': 'production', 'TARGET_URL': ORIGIN,
                'BATTLEFIELD_ACCESS_SERVICE_TOKEN_FILE': '/private-service-file', 'PATH': str(bins)+':'+os.environ['PATH']}
            command = ['bash', str(fixture/'scripts/security/zap-baseline.sh')]
            subprocess.run(command, env=env, check=True)
            report = fixture/'artifacts/security/zap/coverage.json'
            self.assertFalse(json.loads(report.read_text())['fullApplicationScan'])
            result = subprocess.run(command, env={**env, 'FAIL_SCAN': '1'})
            self.assertEqual(result.returncode, 23)
            self.assertFalse(report.exists())

    def test_tls_requires_public_trust_and_keeps_narrow_rbac(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)/'prometheus'
            shutil.copytree(ROOT/'infra/deploy/overlays/local/observability/prometheus', directory)
            source = (directory/'prometheus.yml').read_text()
            bad = Path(tmp)/'bad.pem'; bad.write_text('not a certificate')
            with self.assertRaises((ValueError, ssl.SSLError)): tls.configure(directory, bad)
            self.assertEqual((directory/'prometheus.yml').read_text(), source)
            tls.configure(directory, Path(ssl.get_default_verify_paths().cafile))
            result = (directory/'prometheus.yml').read_text()
            self.assertNotIn('insecure_skip_verify: true', result)
            self.assertIn('ca_file: /etc/prometheus/kubelet-ca.crt', result)
            self.assertNotIn('nodes/proxy', (directory/'rbac.yaml').read_text())
            rendered = subprocess.check_output(['kubectl', 'kustomize', str(directory)], text=True)
            self.assertIn('kubelet-ca.crt:', rendered)

    def test_policy_only_reconciliation_never_starts_or_restarts_tunnel(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            fixture = directory/'project'
            for relative in ('scripts/cd/reconcile-network-policies.sh', 'scripts/ops/render-limited-launch.py'):
                target = fixture/relative; target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy(ROOT/relative, target)
            (fixture/'scripts/lib').mkdir()
            (fixture/'scripts/lib/deployment-environment.sh').write_text('battlefield_env_preflight() { export KUBERNETES_CONTEXT=test KUBERNETES_NAMESPACE=battlefield; }\n')
            bins = directory/'bin'; bins.mkdir()
            kubectl = bins/'kubectl'
            kubectl.write_text('#!/usr/bin/env python3\nimport json,sys\nassert sys.argv[5:7]==["apply","-f"]\nitems=json.load(open(sys.argv[7]))["items"]\nassert len(items)==2 and all(i["kind"]=="NetworkPolicy" for i in items)\n')
            kubectl.chmod(0o755)
            subprocess.run(['bash', str(fixture/'scripts/cd/reconcile-network-policies.sh')], check=True,
                env={**os.environ, 'DEPLOY_ENV': 'production', 'PATH': str(bins)+':'+os.environ['PATH']})


if __name__ == '__main__':
    unittest.main()
