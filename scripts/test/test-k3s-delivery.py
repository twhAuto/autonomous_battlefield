"""Exercise actual delivery entry points with isolated external command doubles."""
import importlib.util
import json
import os
import re
import ssl
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
BACKEND = 'ghcr.io/test/backend@sha256:' + 'a' * 64
FRONTEND = 'ghcr.io/test/frontend@sha256:' + 'b' * 64


class DeliveryTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.kubectl = shutil.which('kubectl')
        cls.ca_bundle = ssl.get_default_verify_paths().cafile
        if not cls.kubectl:
            raise RuntimeError('K3s delivery tests require kubectl for real Kustomize rendering')
        if not cls.ca_bundle or not Path(cls.ca_bundle).is_file():
            raise RuntimeError('K3s delivery tests require a system TLS CA bundle')

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='bf-k3s-delivery-')
        self.addCleanup(self.tmp.cleanup)
        self.directory = Path(self.tmp.name)
        self.project = self.directory / 'project'
        for name in ('scripts', 'infra/k3s', 'deploy'):
            shutil.copytree(ROOT / name, self.project / name, ignore=shutil.ignore_patterns('__pycache__'))
        # Use the real interprocess lock, scoped to the disposable controller home.
        lock = self.project / 'scripts/infra/operation-lock.py'
        lock.write_text('import pathlib,runpy\npathlib.Path.home=lambda:pathlib.Path(' + repr(str(self.directory)) + ')\n'
                        'runpy.run_path(' + repr(str(ROOT / 'scripts/infra/operation-lock.py')) + ',run_name="__main__")\n')
        self.profile = self.directory / 'deployment.env'
        self.kubeconfig = self.directory / 'kubeconfig'
        self.kubeconfig.touch()
        self.ca = self.directory / 'ca.pem'
        self.ca.write_bytes(Path(self.ca_bundle).read_bytes())
        self.profile.write_text('BF_ENV=production\nBF_DRIVER=k3s\nBF_CONTEXT=fixture\nBF_CLUSTER=node\nBF_UID=uid\n'
                                'BF_URL=https://play.example.com\nBF_KUBECONFIG=kubeconfig\nBF_KUBELET_CA_FILE=ca.pem\n')
        (self.directory / '.env').write_text('POSTGRES_DB=battlefield\nPOSTGRES_USER=battlefield\nPOSTGRES_PASSWORD=synthetic\n'
            'DATABASE_URL=postgres://battlefield:synthetic@battlefield-postgres:5432/battlefield\nBATTLEFIELD_AUTH_MODE=cloudflare\n'
            'BATTLEFIELD_ACCESS_ISSUER=https://fixture.cloudflareaccess.com\nBATTLEFIELD_ACCESS_AUDIENCE=' + 'c'*64 + '\n'
            'BATTLEFIELD_PUBLIC_ORIGIN=https://play.example.com\nBATTLEFIELD_ACCESS_ROLES={"admin":["owner@example.com"]}\n'
            'BATTLEFIELD_AGENT_SECRET=' + 'd'*32 + '\nBATTLEFIELD_MCP_CAPABILITY_SECRET=' + 'e'*32 + '\n')
        (self.directory / 'config.json').write_text('{"auths":{}}')
        self.log = self.directory / 'commands.jsonl'
        self.log.touch()
        self.bins = self.directory / 'bin'
        self.bins.mkdir()
        self.command('kind', 'raise SystemExit("Kind must never be called for K3s")')
        self.command('git', 'print("f"*40)')
        self.command('docker', '''
if 'verify' in args:
    if os.environ.get('SIGNATURE_FAIL'): sys.exit(5)
    print(json.dumps([{'optional': {'revision': 'f'*40}}]))
elif 'verify-attestation' in args:
    if os.environ.get('ATTESTATION_FAIL'): sys.exit(6)
    print('{}')
elif args[0] == 'login':
    Path(os.environ['DOCKER_CONFIG'], 'config.json').write_text('{"auths":{}}')
    sys.stdin.read()
elif args[0] != 'logout':
    raise SystemExit('K3s requires no Docker cluster operations')
''')
        pins = json.loads((ROOT / 'infra/k3s/pins.json').read_text())
        self.command('kubectl', '''
text=' '.join(args)
if 'get namespace kube-system' in text: print(os.environ.get('LIVE_UID','uid'))
elif 'get nodes' in text:
    if 'jsonpath=' in text: print('node')
    else: print(json.dumps({'items':[{'metadata':{'name':'node'}, 'status':{'nodeInfo':{'kubeletVersion':os.environ.get('K3S_VERSION',PINS['k3s']['version'])}, 'conditions':[{'type':'Ready','status':'True'}]}}]}))
elif 'get storageclass' in text:
    print(json.dumps({'provisioner':'rancher.io/local-path','reclaimPolicy':os.environ.get('RECLAIM','Retain'),'volumeBindingMode':'WaitForFirstConsumer','parameters':{'nodePath':'/srv/battlefield/storage'}}))
elif 'get deployment envoy-gateway' in text or 'get deployment kyverno-admission-controller' in text:
    envoy='envoy-gateway' in text
    image='envoyproxy/gateway:'+PINS['envoy']['version'] if envoy else 'reg.kyverno.io/kyverno/kyverno:'+PINS['kyverno']['version']
    print(json.dumps({'metadata':{'generation':1},'spec':{'replicas':1,'template':{'spec':{'containers':[{'image':image}]}}},'status':{'observedGeneration':1,'updatedReplicas':1,'readyReplicas':int(os.environ.get('CONTROLLER_READY','1')),'availableReplicas':1}}))
elif 'get deployment/' in text and '.image}' in text:
    print(FRONTEND if 'battlefield-frontend' in text else BACKEND)
elif 'get deployments' in text and '-o json' in text:
    names=['api','engine','agent','mcp','frontend']
    print(json.dumps({'items':[{'metadata':{'name':'battlefield-'+n,'generation':1},'spec':{'replicas':1,'template':{'spec':{'containers':[{'name':n,'image':FRONTEND if n=='frontend' else BACKEND}]}}},'status':{'observedGeneration':1,'updatedReplicas':1,'readyReplicas':1}} for n in names]}))
elif ' apply -k ' in ' '+text+' ':
    directory=Path(args[args.index('-k')+1])
    result=subprocess.run([REAL_KUBECTL,'kustomize',str(directory)],capture_output=True,text=True,check=True)
    Path(os.environ['FIXTURE'],directory.name+'.yaml').write_text(result.stdout)
elif 'exec battlefield-postgres-0' in text and 'psql' in text:
    if os.environ.get('DATABASE_UNAVAILABLE'): sys.exit(1)
    print(os.environ.get('MIGRATIONS','[0,1]'))
elif 'pg_dump' in text: print('synthetic dump')
elif 'create secret' in text: print('{}')
elif 'config view' in text: print('synthetic-kubeconfig')
elif 'get pvc' in text: print('{"items":[]}')
elif 'get job battlefield-kibana-setup' in text: print('Complete=True')
elif 'get services' in text: print(os.environ.get('GATEWAY_SERVICES','envoy-battlefield-local'))
elif 'port-forward' in args:
    import time
    time.sleep(60)
elif 'apply' in args and '-f' in args and args[args.index('-f')+1]=='-': sys.stdin.read()
''', {'PINS':pins, 'REAL_KUBECTL':self.kubectl, 'BACKEND':BACKEND, 'FRONTEND':FRONTEND})
        excluded = ('BF_', 'BATTLEFIELD_', 'DEPLOY_', 'KUBERNETES_', 'KIND_', 'DOCKER_', 'SMOKE_', 'BACKEND_', 'FRONTEND_')
        self.env = {k:v for k,v in os.environ.items() if not k.startswith(excluded) and k not in
                    ('KUBECONFIG','CLUSTER_DRIVER','IMAGE_SOURCE','ENFORCE_SUPPLY_CHAIN_POLICY','TARGET_URL','ZAP_TARGET_URL','LANGFUSE_ENABLED')}
        self.env.update(PATH=str(self.bins)+':'+os.environ['PATH'], FIXTURE=str(self.directory), CALLS=str(self.log),
            BATTLEFIELD_ENV_FILE=str(self.profile), DEPLOY_ENV='production', BACKEND_IMAGE_REF=BACKEND,
            FRONTEND_IMAGE_REF=FRONTEND, GHCR_USER='fixture', GHCR_TOKEN='synthetic', DOCKER_CONFIG=str(self.directory),
            COSIGN_PUBLIC_KEY_FILE=str(self.ca), VERIFY_ARTIFACT_DIR=str(self.directory/'verification'),
            BATTLEFIELD_BACKUP_DIR=str(self.directory/'backups'), LANGFUSE_ENABLED='0', ROLLOUT_TIMEOUT='10s')

    def command(self, name, code, values=None):
        p=self.bins/name
        p.write_text('#!/usr/bin/env python3\nimport json,os,sys,subprocess\nfrom pathlib import Path\nargs=sys.argv[1:]\n'
            'with open(os.environ["CALLS"],"a") as stream: stream.write(json.dumps([Path(sys.argv[0]).name,*args])+"\\n")\n'
            + ''.join(k+'='+repr(v)+'\n' for k,v in (values or {}).items())+code)
        p.chmod(0o755)

    def run_script(self, name, *args, ok=True, **settings):
        self.log.write_text('')
        result=subprocess.run(['bash',str(self.project/'scripts/cd'/name),*args], env=self.env|settings,
                              capture_output=True,text=True,timeout=45)
        self.assertEqual(result.returncode==0,ok,(result.stdout,result.stderr))
        self.calls=[json.loads(line) for line in self.log.read_text().splitlines()]
        return result

    def test_k3s_full_delivery_uses_retained_overlays_without_kind_or_controller_install(self):
        self.run_script('deploy.sh','f'*40)
        self.assertFalse(any(c[0]=='kind' for c in self.calls))
        self.assertFalse(any('https://github.com/kyverno' in ' '.join(c) for c in self.calls))
        self.assertFalse(any('terraform' in c or 'ansible-playbook' in c for c in self.calls))
        infrastructure=(self.directory/'infrastructure.yaml').read_text()
        self.assertIn('storageClassName: battlefield-retained',infrastructure)
        self.assertIn('storage: 10Gi',infrastructure)
        self.assertIn('storageClassName: battlefield-retained',(self.directory/'observability.yaml').read_text())
        self.assertIn('insecure_skip_verify: false',(self.directory/'observability.yaml').read_text())
        self.assertNotIn('insecure_skip_verify: true',(self.directory/'observability.yaml').read_text())
        self.assertIn(BACKEND,(self.directory/'migration.yaml').read_text())
        self.assertIn(FRONTEND,(self.directory/'application.yaml').read_text())
        self.assertTrue(any('pg_dump' in ' '.join(c) for c in self.calls))
        self.assertTrue(any('pg_restore' in ' '.join(c) for c in self.calls))

    def test_collector_ports_are_unique_for_kubernetes_validation(self):
        manifest=(ROOT/'deploy/overlays/local/observability/otel-collector/deployment.yaml').read_text()
        ports=re.findall(r'\{name: ([a-z0-9-]+), containerPort: ([0-9]+)\}',manifest)
        self.assertGreaterEqual(len(ports),3)
        self.assertEqual(len(ports),len({name for name,_ in ports}))
        self.assertEqual(len(ports),len({port for _,port in ports}))
        self.assertIn('port: health',manifest)
        self.assertIn(('health','13133'),ports)

    def test_configuration_only_preserves_verified_images_and_component_selection(self):
        self.run_script('deploy.sh','f'*40,DEPLOY_COMPONENTS='application',BACKEND_IMAGE_REF='',FRONTEND_IMAGE_REF='')
        self.assertIn(BACKEND,(self.directory/'application.yaml').read_text())
        self.assertFalse((self.directory/'migration.yaml').exists())
        self.assertFalse((self.directory/'observability.yaml').exists())
        self.assertTrue(any('verify' in c for c in self.calls))

    def test_wrong_identity_driver_platform_and_supply_chain_fail_before_mutation(self):
        for settings in ({'LIVE_UID':'other'},{'CLUSTER_DRIVER':'kind'},{'K3S_VERSION':'wrong'},
                         {'RECLAIM':'Delete'},{'CONTROLLER_READY':'0'},{'IMAGE_SOURCE':'local'},{'SIGNATURE_FAIL':'1'},
                         {'ATTESTATION_FAIL':'1'},{'GHCR_TOKEN':''},{'KIND_CLUSTER_NAME':'old'}):
            with self.subTest(settings=settings):
                self.run_script('deploy.sh','f'*40,ok=False,**settings)
                self.assertFalse(any(any(v in c for v in ['apply','delete','patch','create']) for c in self.calls),self.calls)

    def test_recovery_requires_matching_database_and_keeps_uid_guard(self):
        self.run_script('production-recovery.sh','record','f'*40)
        record=json.loads((self.directory/'recovery.json').read_text())
        self.assertEqual(record['database_migrations'],[0,1])
        self.run_script('rollback.sh',BACKEND,FRONTEND,BATTLEFIELD_ACCESS_COMPATIBLE_BACKEND_DIGEST=BACKEND)
        self.assertEqual(sum('set' in c and 'image' in c for c in self.calls),5)
        self.run_script('rollback.sh',BACKEND,FRONTEND,ok=False,MIGRATIONS='[0,1,2]',BATTLEFIELD_ACCESS_COMPATIBLE_BACKEND_DIGEST=BACKEND)
        self.assertFalse(any('set' in c and 'image' in c for c in self.calls))

    def test_unknown_database_disables_rollback_without_blocking_forward_delivery(self):
        self.run_script('production-recovery.sh','record','f'*40)
        record=self.directory/'recovery.json'
        original=record.read_bytes()
        for settings in ({'DATABASE_UNAVAILABLE':'1'},{'MIGRATIONS':'[]'},{'MIGRATIONS':'invalid-json'}):
            result=self.run_script('production-recovery.sh','read',BACKEND,FRONTEND,**settings)
            self.assertEqual(result.stdout.strip(),'')
            self.run_script('rollback.sh',BACKEND,FRONTEND,ok=False,
                BATTLEFIELD_ACCESS_COMPATIBLE_BACKEND_DIGEST=BACKEND,**settings)
            self.assertFalse(any('set' in c and 'image' in c for c in self.calls))
            self.run_script('production-recovery.sh','record','f'*40,ok=False,**settings)
            self.assertEqual(record.read_bytes(),original)
        self.run_script('production-recovery.sh','invalid',ok=False)
        self.assertEqual(self.calls,[])

    def test_recovery_keeps_identity_and_storage_guards_during_controller_restart(self):
        self.run_script('production-recovery.sh','record','f'*40)
        self.run_script('rollback.sh',BACKEND,FRONTEND,CONTROLLER_READY='0',K3S_VERSION='upgrading',
                        BATTLEFIELD_ACCESS_COMPATIBLE_BACKEND_DIGEST=BACKEND)
        self.assertEqual(sum('set' in c and 'image' in c for c in self.calls),5)
        for settings in ({'LIVE_UID':'other'},{'RECLAIM':'Delete'}):
            self.run_script('rollback.sh',BACKEND,FRONTEND,ok=False,CONTROLLER_READY='0',
                            BATTLEFIELD_ACCESS_COMPATIBLE_BACKEND_DIGEST=BACKEND,**settings)
            self.assertFalse(any('set' in c and 'image' in c for c in self.calls))

    def test_smoke_selects_k3s_gateway_and_rejects_ambiguous_services(self):
        self.command('curl','pass')
        result=self.run_script('smoke-test.sh',ok=False)
        # Stop at the existing production-auth guard, after the private forwards.
        self.assertIn('refuses a non-Cloudflare',result.stderr)
        forwards=[c for c in self.calls if 'port-forward' in c]
        self.assertTrue(any('service/envoy-battlefield-local' in c and '18888:80' in c for c in forwards))
        self.assertFalse(any('8888:80' in c for c in forwards))
        self.assertTrue(any('--context' in c and 'fixture' in c for c in forwards))
        for services in ('','one two'):
            self.run_script('smoke-test.sh',ok=False,GATEWAY_SERVICES=services)
            self.assertFalse(any('port-forward' in c for c in self.calls))

    def test_registration_reuses_platform_without_docker_and_refuses_overwrite(self):
        target=self.directory/'registration'
        settings=dict(BATTLEFIELD_CONFIG_DIR=str(target),BATTLEFIELD_ENV_FILE=str(target/'deployment.env'),
                      KUBELET_CA_FILE=str(self.ca),TARGET_URL='https://play.example.com')
        self.run_script('setup-environment.sh','production','default','k3s',**settings)
        self.assertIn('BF_DRIVER=k3s',(target/'deployment.env').read_text())
        self.assertEqual((target/'kubelet-ca.crt').read_bytes(),self.ca.read_bytes())
        self.assertFalse(any(c[0] in ('docker','kind') for c in self.calls))
        before=(target/'deployment.env').read_bytes()
        self.run_script('setup-environment.sh','production','default','k3s',ok=False,**settings)
        self.assertEqual((target/'deployment.env').read_bytes(),before)


if __name__=='__main__':
    unittest.main()
