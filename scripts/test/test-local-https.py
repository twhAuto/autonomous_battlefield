#!/usr/bin/env python3
"""Native HTTPS capture, CA, ownership and reviewed-operation regressions."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
from unittest.mock import MagicMock

ROOT = Path(__file__).resolve().parents[2]


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, ROOT / path)
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
    return module


runtime = load('https_runtime_tests', 'scripts/infra/https-runtime.py')
wrapper = load('https_wrapper_tests', 'scripts/infra/https.py')


def caddyfile():
    return '\n'.join(host + ' {\n tls internal\n reverse_proxy ' + upstream +
                     (' {\n header_up Host ' + header + '\n }' if header else '') + '\n}\n'
                     for host, (upstream, header) in runtime.REQUIRED.items())


def routing():
    return {'gateway': '192.168.235.10:30080', 'public_origin': 'https://play.example.com',
            'cluster_uid': 'a'*8+'-'+ 'b'*4+'-'+ 'c'*4+'-'+ 'd'*4+'-'+ 'e'*12,
            'node_uid': 'f'*8+'-'+ 'a'*4+'-'+ 'b'*4+'-'+ 'c'*4+'-'+ 'd'*12}


class PolicyTests(unittest.TestCase):
    def test_migration_preserves_extra_routes_and_sonarqube_and_redirects_only_application(self):
        extra = '\n# retain this comment\nnotes.home.arpa { tls internal reverse_proxy 127.0.0.1:9999 }\n'
        original = caddyfile() + extra
        config = runtime.desired_configuration({'caddyfile': original}, routing())
        parsed = runtime.routes(config['caddyfile'])
        self.assertEqual(parsed['battlefield.home.arpa'], ['https://play.example.com', 'redirect'])
        self.assertEqual(parsed['grafana.home.arpa'], ['192.168.235.10:30080', 'grafana.localhost'])
        self.assertEqual(parsed['sonarqube.home.arpa'], ['127.0.0.1:9000', None])
        self.assertTrue(config['caddyfile'].endswith(extra))
        self.assertEqual(runtime.desired_configuration(config, routing()), config)
        for replacement in ({'gateway':'8.8.8.8:30080'}, {'public_origin':'https://evil.invalid/path'}, {'node_uid':'wrong'}):
            with self.assertRaises(ValueError):runtime.desired_configuration({'caddyfile':original},dict(routing(),**replacement))

    def test_capture_records_503_but_normal_tls_acceptance_rejects_it(self):
        # All four responses share a verified TLS socket; one retained upstream is down.
        direct = MagicMock(); direct.getresponse.return_value.status = 503
        response = MagicMock(); response.status = 503
        with patch.object(runtime.ssl,'create_default_context'), patch.object(runtime.socket,'create_connection'), \
             patch.object(runtime.http.client,'HTTPResponse',return_value=response), \
             patch.object(runtime.http.client,'HTTPConnection',return_value=direct):
            config={'root_certificate':'fixture CA', 'caddyfile':caddyfile()}
            observations=runtime.tls_checks(config,require_healthy=False)
            self.assertTrue(all(x['verified_tls'] and not x['healthy'] for x in observations.values()))
            with self.assertRaisesRegex(ValueError,'health check failed'):runtime.tls_checks(config)

    def test_current_routes_and_unrelated_local_route_preserved(self):
        config = caddyfile() + '\nnotes.home.arpa { tls internal reverse_proxy 127.0.0.1:9999 }'
        self.assertEqual(runtime.routes(config)['notes.home.arpa'], ['127.0.0.1:9999', None])
        self.assertEqual(len(runtime.routes(config)), 5)

    def test_unsafe_or_unknown_directives_never_accepted(self):
        for text in (caddyfile() + '\nimport /private/*', caddyfile().replace('tls internal', 'tls /private/cert /private/key'),
                     caddyfile().replace('127.0.0.1', '192.168.1.126'), caddyfile() + caddyfile(),
                     caddyfile().replace('battlefield.localhost', 'foreign.localhost'),
                     caddyfile() + "{{ lookup('pipe', 'touch /tmp/never') }}"):
            with self.subTest(text=text[:30]), self.assertRaises(ValueError): runtime.routes(text)

    def test_file_guards_reject_redirects_hardlinks_and_broad_secrets(self):
        with tempfile.TemporaryDirectory() as directory:
            p = Path(directory)/'input'; p.write_text('private'); p.chmod(0o600)
            self.assertEqual(runtime.read(p, private=True), b'private')
            link = p.with_name('link'); link.symlink_to(p)
            with self.assertRaises(ValueError): runtime.read(link)
            link.unlink(); os.link(p, link)
            with self.assertRaises(ValueError): runtime.read(p)
            link.unlink(); p.chmod(0o644)
            with self.assertRaises(ValueError): runtime.read(p, private=True)

    def test_cli_help_works_without_pythonpath_from_outside_checkout(self):
        env = {k:v for k,v in os.environ.items() if k not in ('PYTHONPATH','PYTHONSAFEPATH')}
        r = subprocess.run([sys.executable, str(ROOT/'scripts/infra/https.py'), '--help'], cwd='/tmp', env=env, capture_output=True)
        self.assertEqual(r.returncode, 0, r.stderr.decode())


class CertificateTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory()
        cls.path = Path(cls.temp.name)
        for name in ('one', 'two'):
            subprocess.run(['openssl','req','-x509','-newkey','ec','-pkeyopt','ec_paramgen_curve:P-256',
                            '-nodes','-days','2','-subj','/CN=HTTPS disposable test root',
                            '-addext','basicConstraints=critical,CA:TRUE','-keyout',str(cls.path/(name+'.key')),
                            '-out',str(cls.path/(name+'.crt'))],capture_output=True,check=True)

    @classmethod
    def tearDownClass(cls): cls.temp.cleanup()

    def test_real_ca_pair_validated_and_wrong_key_rejected(self):
        cert = (self.path/'one.crt').read_text(); key = (self.path/'one.key').read_text()
        self.assertRegex(runtime.certificate(cert, key), r'^[0-9a-f]{64}$')
        with self.assertRaisesRegex(ValueError, 'do not match'):
            runtime.certificate(cert, (self.path/'two.key').read_text())

    def test_missing_or_corrupt_restore_material_rejected(self):
        for cert, key in [(None, None), ('not a certificate', 'not a key'), ((self.path/'one.crt').read_text(), '')]:
            with self.subTest(cert=bool(cert)), self.assertRaises(ValueError): runtime.certificate(cert, key)

    def test_validated_capture_requires_exact_schema(self):
        config = {'schema':1,'caddy_version':'2.6.2','unit_sha256':'a'*64,'caddyfile':caddyfile(),
                  'root_certificate':(self.path/'one.crt').read_text(),'root_private_key':(self.path/'one.key').read_text()}
        runtime.validate(config)
        for field in ('root_private_key','unit_sha256','caddyfile'):
            value = copy.deepcopy(config); del value[field]
            with self.assertRaises(ValueError): runtime.validate(value)


class ReviewTests(unittest.TestCase):
    def instance(self):
        engine = object.__new__(runtime.Engine)
        engine.request = {'revision':'a'*40,'mode':'adopt'}
        engine.config = {'caddyfile':caddyfile()}; engine.ca_fingerprint = 'b'*64
        engine.desired = engine.config
        engine.binding = {'inputs':'test'}; engine.owner = None
        engine.inspect = lambda: {'configuration':'unchanged','tls':{}}
        engine.receipt = Path('/unreachable/review.json'); engine.owner_path = Path('/unreachable/owner.json')
        return engine

    def receipt(self, engine):
        return {'request':runtime.sha(runtime.encoded(engine.request)), 'observation':runtime.sha(runtime.encoded(engine.inspect())),
                'created':100,'review_id':'expected'}

    def test_wrong_stale_changed_observation_and_revision_refused_before_consumption(self):
        for kind in ('wrong-review','expired','future','drift','source'):
            engine = self.instance(); receipt = self.receipt(engine); now=200; review='expected'
            if kind=='wrong-review':review='wrong'
            if kind=='expired':now=1901
            if kind=='future':now=99
            if kind=='drift':receipt['observation']='different'
            if kind=='source':engine.request['revision']='c'*40
            with patch.object(runtime,'read',return_value=runtime.encoded(receipt)), patch.object(runtime.time,'time',return_value=now), \
                 patch.object(Path,'unlink',side_effect=AssertionError('Consumed invalid receipt')), patch.object(runtime,'write',side_effect=AssertionError('Mutated on invalid receipt')):
                with self.subTest(kind=kind), self.assertRaisesRegex(ValueError,'review expired'):engine.apply(review)

    def test_adoption_writes_only_owner_and_repeat_apply_has_no_changes(self):
        engine=self.instance(); writes=[]
        for expected in (True,False):
            receipt=self.receipt(engine)
            with patch.object(runtime,'read',return_value=runtime.encoded(receipt)), patch.object(runtime.time,'time',return_value=200), \
                 patch.object(Path,'unlink'), patch.object(runtime,'write',side_effect=lambda p,v,**kw:writes.append(p)), \
                 patch.object(runtime,'command',side_effect=AssertionError('Adoption changed service')):
                result=engine.apply('expected')
            self.assertEqual(result['changed'],expected); self.assertEqual(result['changes'],[])
        self.assertEqual(writes,[engine.owner_path])

    def test_verify_without_ownership_refused(self):
        with self.assertRaisesRegex(ValueError,'not been adopted'):self.instance().verify()

    def test_failed_migration_restores_exact_config_and_reloads_original(self):
        engine=self.instance(); engine.desired=runtime.desired_configuration(engine.config,routing())
        writes=[]; calls=[]
        def command(*args):
            calls.append(args)
            if len(calls)==1:raise ValueError('Fixture reload failed')
        with patch.object(runtime,'read',return_value=b'original bytes'), \
             patch.object(runtime,'write',side_effect=lambda p,v,**kw:writes.append((p,v))), \
             patch.object(runtime,'command',side_effect=command), patch.object(runtime,'active_configuration') as active, \
             patch.object(Path,'unlink'):
            with self.assertRaisesRegex(ValueError,'Fixture reload failed'):engine.migrate()
        self.assertEqual(writes[-1],(runtime.CONFIG,b'original bytes'))
        self.assertEqual(len(calls),2);active.assert_called_once()

    def test_tls_startup_retry_is_bounded(self):
        with patch.object(runtime,'active_configuration'), patch.object(runtime,'tls_checks',side_effect=[OSError(),{'passed':True}]) as check, patch.object(runtime.time,'sleep'):
            self.assertEqual(runtime.wait_for_tls({}),{'passed':True}); self.assertEqual(check.call_count,2)
        with patch.object(runtime,'active_configuration'), patch.object(runtime,'tls_checks',side_effect=OSError()) as check, patch.object(runtime.time,'sleep'):
            with self.assertRaises(OSError):runtime.wait_for_tls({})
            self.assertEqual(check.call_count,6)


class AnsibleTests(unittest.TestCase):
    @unittest.skipUnless(os.environ.get('BATTLEFIELD_TEST_ANSIBLE_BIN'), 'Pinned Ansible supplied by full deployment harness')
    def test_private_input_values_are_never_ansible_templates(self):
        with tempfile.TemporaryDirectory() as directory:
            path=Path(directory); payload=path/'inputs.json'; marker=path/'unexpected-execution'
            canary="{{ lookup('pipe', 'touch " + str(marker) + "') }}"
            payload.write_text(json.dumps({'caddyfile':canary,'root_private_key':'synthetic-sensitive-content'})); payload.chmod(0o600)
            script=path/'runtime.py'
            script.write_text('import json,sys\np=json.load(open(sys.argv[1])); assert p["caddyfile"].startswith("{{ lookup"); print(json.dumps({"changed":False}))\n')
            inventory=path/'inventory.json';inventory.write_text(json.dumps({'all':{'children':{'bootstrap':{'hosts':{'server':{'ansible_connection':'local','ansible_python_interpreter':sys.executable,'ansible_become':False}}}}}}))
            result=subprocess.run([str(Path(os.environ['BATTLEFIELD_TEST_ANSIBLE_BIN'])/'ansible-playbook'),'-i',str(inventory),str(ROOT/'infra/ansible/https.yml'),'-e',json.dumps({'https_runtime':str(script),'https_inputs_file':str(payload)})],env=dict(os.environ,ANSIBLE_CONFIG=str(ROOT/'infra/ansible/ansible.cfg')),capture_output=True,text=True)
            self.assertEqual(result.returncode,0,result.stdout+result.stderr)
            self.assertFalse(marker.exists()); self.assertNotIn('synthetic-sensitive-content',result.stdout+result.stderr)


if __name__=='__main__':unittest.main()
