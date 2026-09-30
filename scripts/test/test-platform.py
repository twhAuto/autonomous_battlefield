#!/usr/bin/env python3
"""Exercise host-operation exclusion, reviewed-plan guards and private Jenkins seeding."""
import argparse
import base64
from contextlib import redirect_stdout, nullcontext
import importlib.util
import io
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest
import urllib.error
from unittest.mock import patch, Mock
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[2]

def module(name, path):
    spec = importlib.util.spec_from_file_location(name, ROOT / path)
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result

platform = module('platform_workflow', 'scripts/infra/platform.py')
seed = module('seed_platform', 'scripts/infra/seed-platform.py')
setup = module('setup_platform', 'scripts/infra/setup-platform.py')
lock = platform.lock


class PlatformTests(unittest.TestCase):
    def test_real_lock_contention_and_release(self):
        with tempfile.TemporaryDirectory() as tmp, patch.object(lock.Path, 'home', return_value=Path(tmp)):
            code = '''import importlib.util,pathlib,sys
s=importlib.util.spec_from_file_location('operation',sys.argv[1]); m=importlib.util.module_from_spec(s); s.loader.exec_module(m)
m.Path.home=lambda:pathlib.Path(sys.argv[2])
try:
 with m.acquire(): pass
except ValueError: sys.exit(75)
'''
            command = [sys.executable, '-c', code, str(ROOT/'scripts/infra/operation-lock.py'), tmp]
            with lock.acquire():
                self.assertEqual(subprocess.run(command).returncode, 75)
            self.assertEqual(subprocess.run(command).returncode, 0)

    def test_lock_rejects_symlink_and_forged_inheritance(self):
        with tempfile.TemporaryDirectory() as tmp, patch.object(lock.Path, 'home', return_value=Path(tmp)):
            path = lock.lock_path()
            target = Path(tmp)/'unrelated'
            target.write_text('preserve')
            path.symlink_to(target)
            with self.assertRaises(OSError), lock.acquire():
                pass
            path.unlink()
            with patch.dict(os.environ, {lock.ENV:'999999'}), self.assertRaises(ValueError):
                lock.inherited()
            self.assertEqual(target.read_text(), 'preserve')

    def test_nested_real_lock_inherits_and_keeps_exclusion(self):
        with tempfile.TemporaryDirectory() as tmp, patch.object(lock.Path, 'home', return_value=Path(tmp)):
            with lock.acquire() as fd, patch.dict(os.environ, {lock.ENV:str(fd)}):
                with lock.acquire() as nested:
                    self.assertEqual(nested, fd)
                os.fstat(fd)

    def test_cancelled_child_is_reaped_before_lock_release(self):
        with tempfile.TemporaryDirectory() as tmp:
            marker = Path(tmp)/'started'
            code = '''import importlib.util,pathlib,sys
s=importlib.util.spec_from_file_location('operation',sys.argv[1]); m=importlib.util.module_from_spec(s); s.loader.exec_module(m)
m.Path.home=lambda:pathlib.Path(sys.argv[2])
with m.acquire() as fd:
 m.run([sys.executable,'-c','import pathlib,time,sys; pathlib.Path(sys.argv[1]).write_text("ready"); time.sleep(60)',sys.argv[3]],fd)
'''
            child = subprocess.Popen([sys.executable,'-c',code,str(ROOT/'scripts/infra/operation-lock.py'),tmp,str(marker)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            try:
                for _ in range(100):
                    if marker.exists(): break
                    time.sleep(.02)
                self.assertTrue(marker.exists())
                child.send_signal(signal.SIGTERM)
                child.wait(timeout=5)
                with patch.object(lock.Path, 'home', return_value=Path(tmp)), lock.acquire():
                    pass
            finally:
                if child.poll() is None: child.kill(); child.wait()

    def test_revision_guard_rejects_pr_dirty_and_other_branch(self):
        revision='a'*40
        with patch.dict(os.environ, {'CHANGE_ID':'1'}), self.assertRaisesRegex(ValueError,'PR'):
            platform.trusted_revision(revision)
        with patch.object(platform,'git',side_effect=[revision,'b'*40]), self.assertRaisesRegex(ValueError,'origin/main'):
            platform.trusted_revision(revision)
        with patch.object(platform,'git',side_effect=[revision,revision,' M scripts/infra/server.py']), self.assertRaisesRegex(ValueError,'clean'):
            platform.trusted_revision(revision)
        with patch.object(platform,'git',side_effect=[revision,revision,'']):
            platform.trusted_revision(revision)

    def test_receipt_rejects_stale_inputs_target_and_revision(self):
        args=argparse.Namespace(revision='a'*40)
        receipt={'schema':1,'revision':args.revision,'inputs':'fingerprint','created':time.time()}
        platform.check_receipt(receipt,args,'fingerprint')
        for changed in [{'inputs':'changed'},{'revision':'b'*40},{'created':time.time()-1900},{'created':time.time()+10}]:
            with self.subTest(changed=changed), self.assertRaises(ValueError):
                platform.check_receipt(dict(receipt,**changed),args,'fingerprint')

    def test_apply_rechecks_plan_consumes_receipt_then_verifies(self):
        with tempfile.TemporaryDirectory() as tmp, patch.object(lock.Path,'home',return_value=Path(tmp)):
            args=argparse.Namespace(action='apply',receipt=Path(tmp)/'receipt',report=Path(tmp)/'report',revision='a'*40)
            platform.write_new(args.receipt,{'schema':1,'revision':args.revision,'inputs':'identity','created':time.time(),'change_digest':'plan'})
            calls=[]
            def action(args, name, directory, fd):
                calls.append(name)
                if name=='apply': self.assertFalse(args.receipt.exists())
                return {'schema':1,'change_digest':'plan','tasks':[{'task':'test','status':'ok','changed':False}]}
            with patch.object(platform,'trusted_revision'), patch.object(platform,'check_inputs',return_value={}), patch.object(platform,'fingerprint',return_value='identity'), patch.object(platform,'run_action',side_effect=action), redirect_stdout(io.StringIO()):
                platform.execute(args)
            self.assertEqual(calls,['plan','apply','verify'])
            self.assertEqual(json.loads(args.report.read_text())['action'],'apply')

    def test_drift_never_reaches_apply(self):
        with tempfile.TemporaryDirectory() as tmp, patch.object(lock.Path,'home',return_value=Path(tmp)):
            args=argparse.Namespace(action='apply',receipt=Path(tmp)/'receipt',report=Path(tmp)/'report',revision='a'*40)
            platform.write_new(args.receipt,{'schema':1,'revision':args.revision,'inputs':'identity','created':time.time(),'change_digest':'old'})
            with patch.object(platform,'trusted_revision'), patch.object(platform,'check_inputs',return_value={}), patch.object(platform,'fingerprint',return_value='identity'), patch.object(platform,'run_action',return_value={'change_digest':'new'}) as action:
                with self.assertRaisesRegex(ValueError,'drift'): platform.execute(args)
                self.assertEqual(action.call_count,1)
                self.assertFalse(args.report.exists())

    def test_sudo_file_is_not_executed_and_password_is_not_in_argv(self):
        b=platform.bootstrap
        with tempfile.TemporaryDirectory() as tmp:
            password=Path(tmp)/'sudo-password';password.write_text('synthetic-password');password.chmod(0o600)
            args=argparse.Namespace(command='verify',vault=None,vault_password_file=None,become_password_file=password,local=True)
            with patch.object(b,'tools_bin',return_value=Path('/tools')),patch.object(b,'ansible_inventory',return_value={}),patch.object(b.subprocess,'run') as run:
                b.playbook(args,{}, {'sudo_noninteractive':False})
                command=run.call_args.args[0]
                self.assertIn('--become-password-file',command)
                self.assertNotIn('synthetic-password',' '.join(command))
                password.chmod(0o700)
                with self.assertRaises(ValueError):b.playbook(args,{}, {'sudo_noninteractive':False})

    def test_seed_url_rejects_plaintext_remote_and_embedded_credentials(self):
        for url in ['http://192.0.2.2:8080','https://user:password@example.com','file:///tmp/jenkins','https://example.com/?token=secret']:
            with self.subTest(url=url),self.assertRaises(ValueError):seed.Jenkins(url,'user','token')
        seed.Jenkins('http://127.0.0.1:8080','user','token')

    def test_seed_ownership_and_credentials_preservation(self):
        class Client:
            def __init__(self,current):self.current=current;self.writes=[]
            def request(self,path,data=None,missing=False):
                if data is not None:self.writes.append(path);return b''
                return self.current
        for value in [b'<flow-definition><description>unrelated</description></flow-definition>',b'<wrong><description>battlefield-platform-managed-v1</description></wrong>']:
            client=Client(value)
            with self.assertRaises(ValueError):seed.ensure_item(client,'job/platform','create',seed.job_xml())
            self.assertEqual(client.writes,[])
        client=Client(seed.folder_xml())
        seed.ensure_item(client,'job/platform','create',seed.folder_xml())
        self.assertEqual(client.writes,[])
        secret=b'private-fixture-password'
        root=ET.fromstring(seed.credential_xml('host-sudo-password',secret))
        self.assertEqual(base64.b64decode(root.findtext('secretBytes')),secret)
        self.assertEqual(root.findtext('description'),seed.MARKER)
        job=ET.fromstring(seed.job_xml())
        self.assertEqual(job.findtext('.//scriptPath'),'Jenkinsfile.platform')
        self.assertEqual(job.findtext('.//branches/hudson.plugins.git.BranchSpec/name'),'*/main')
        self.assertEqual(len(job.find('triggers')),0)

    def test_plugin_post_accepts_only_expected_same_origin_redirect_without_following(self):
        client = seed.Jenkins('http://127.0.0.1:8080', 'operator', 'synthetic-token')
        for location, accepted in [('/pluginManager/updates/', True),
                ('https://other.example/pluginManager/updates/', False), ('/login', False)]:
            error = urllib.error.HTTPError(client.url, 302, 'Found', {'Location': location}, None)
            with patch.object(client.opener, 'open', side_effect=error) as request:
                if accepted:
                    self.assertEqual(client.request('pluginManager/installNecessaryPlugins', b'<jenkins/>'), b'')
                else:
                    with self.assertRaises(ValueError):
                        client.request('pluginManager/installNecessaryPlugins', b'<jenkins/>')
                self.assertEqual(request.call_count, 1)
        with patch.object(client.opener, 'open', side_effect=error), self.assertRaises(ValueError):
            client.request('createItem?name=host', b'<jenkins/>')

    def test_installer_refuses_foreign_job_before_credential_writes(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            files = {key: directory/key for key in ['host-inventory', 'host-inputs', 'host-vault-password', 'host-sudo-password']}
            for key, path in files.items():
                platform.write_new(path, {'schema': 1, 'host': '192.0.2.10', 'user': 'operator',
                    'identity_file': '/tmp/key', 'machine_id': 'a'*32} if key == 'host-inventory' else {})
            files['host-inputs'].write_bytes(b'$ANSIBLE_VAULT;1.1;AES256\nsynthetic-only')
            client = Mock()
            client.request.side_effect = [seed.folder_xml(), b'<flow-definition><description>unowned</description></flow-definition>']
            with patch.object(seed, 'check_plugins'), self.assertRaisesRegex(ValueError, 'unowned'):
                seed.seed(client, files)
            self.assertTrue(all(len(call.args) == 1 for call in client.request.call_args_list))

    def test_setup_preserves_registration_and_refuses_disk_remapping(self):
        with tempfile.TemporaryDirectory() as tmp:
            inventory = Path(tmp)/'inventory.json'
            data = {'schema': 1, 'host': '192.0.2.10', 'user': 'operator',
                    'identity_file': '/tmp/key', 'machine_id': 'a'*32}
            platform.write_new(inventory, data)
            args = argparse.Namespace(inventory=inventory, host=data['host'], user=data['user'],
                identity_file=Path(data['identity_file']), data_uuid='a'*8+'-aaaa-aaaa-aaaa-'+'a'*12,
                data_mount='/srv/battlefield-data')
            with patch.object(setup.bootstrap, 'inspect_host'):
                setup.prepare_inventory(args)
                first = inventory.read_bytes()
                setup.prepare_inventory(args)
                self.assertEqual(inventory.read_bytes(), first)
                self.assertEqual(json.loads((Path(tmp)/'inventory.before-platform.json').read_text()), data)
                self.assertEqual(inventory.stat().st_mode & 0o777, 0o600)
                args.data_uuid = 'b'*8+'-bbbb-bbbb-bbbb-'+'b'*12
                with self.assertRaisesRegex(ValueError, 'differs'):
                    setup.prepare_inventory(args)
                self.assertEqual(inventory.read_bytes(), first)

    def test_setup_wrong_existing_passphrase_never_seeds_or_overwrites_bundle_and_cleans_temp(self):
        with tempfile.TemporaryDirectory() as tmp:
            inventory = Path(tmp)/'inventory.json'
            vault = Path(tmp)/'inputs.vault'
            platform.bootstrap.write_new(vault, b'existing encrypted bytes')
            captured = []
            def fail_view(command, **kwargs):
                self.assertEqual(command[:2], ['/tools/ansible-vault', 'view'])
                captured.append(Path(command[-1]))
                self.assertEqual(captured[-1].read_text(), 'wrong-vault-passphrase')
                raise subprocess.CalledProcessError(1, command)
            argv = ['setup-platform.py', '--host', '192.0.2.10', '--user', 'operator',
                    '--identity-file', '/tmp/key', '--inventory', str(inventory)]
            with patch.object(sys, 'argv', argv), patch.object(sys.stdin, 'isatty', return_value=True), \
                 patch.object(setup, 'prepare_inventory', return_value={}), \
                 patch.object(setup, 'tunnel', return_value=nullcontext('http://127.0.0.1:8080')), \
                 patch.object(setup.seed, 'check_plugins'), patch.object(setup.seed, 'seed') as send, \
                 patch.object(setup, 'password', side_effect=['synthetic-token','wrong-vault-passphrase']), \
                 patch.object(setup.bootstrap, 'tools_bin', return_value=Path('/tools')), \
                 patch.object(setup.subprocess, 'run', side_effect=fail_view):
                with self.assertRaises(subprocess.CalledProcessError):
                    setup.main()
                send.assert_not_called()
            self.assertEqual(vault.read_bytes(), b'existing encrypted bytes')
            self.assertEqual(len(captured), 1)
            self.assertFalse(captured[0].exists())

    @unittest.skipUnless(os.environ.get('BATTLEFIELD_TEST_ANSIBLE_BIN'),'run through test-server-bootstrap.sh for real Ansible')
    def test_real_ansible_reports_do_not_emit_secret_values_and_plans_are_stable(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory=Path(tmp);play=directory/'play.yml';secret='synthetic-secret-do-not-publish'
            play.write_text('''- hosts: all
  gather_facts: false
  tasks:
    - name: Private input
      ansible.builtin.debug:
        msg: synthetic-secret-do-not-publish
      no_log: true
    - name: Change preview
      ansible.builtin.copy:
        content: hidden-diff-content
        dest: '''+str(directory/'destination')+'\n')
            (directory/'fixture.j2').write_text('public deterministic {{ 1 + 1 }}')
            with play.open('a') as output:
                output.write('    - name: Template preview\n      ansible.builtin.template:\n        src: '+str(directory/'fixture.j2')+'\n        dest: '+str(directory/'template-destination')+'\n')
            reports=[]
            for i in range(2):
                report=directory/f'report{i}.json'
                env=platform.bootstrap.clean_ansible_env(report)
                result=subprocess.run([os.environ['BATTLEFIELD_TEST_ANSIBLE_BIN']+'/ansible-playbook','-i','localhost,','-c','local',str(play),'--check','--diff'],env=env,capture_output=True,text=True)
                self.assertEqual(result.returncode,0,result.stderr)
                output=result.stdout+result.stderr+report.read_text()
                self.assertNotIn(secret,output)
                self.assertNotIn('hidden-diff-content',output)
                reports.append(json.loads(report.read_text()))
            self.assertEqual(reports[0]['change_digest'],reports[1]['change_digest'])
            self.assertFalse((directory/'destination').exists())
            self.assertTrue(any(t['changed'] for t in reports[0]['tasks']))


if __name__=='__main__':unittest.main()
