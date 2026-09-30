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

    def test_ansible_child_keeps_lock_after_controller_group_is_killed(self):
        worker = '''import importlib.util,os,pathlib,sys
spec=importlib.util.spec_from_file_location('bootstrap',sys.argv[1]); m=importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
directory=pathlib.Path(sys.argv[2]); m.operation_lock.Path.home=lambda:directory
os.environ.pop(m.operation_lock.ENV,None)
m.LOCK_CODE='import pathlib; pathlib.Path.home=lambda:pathlib.Path('+repr(str(directory))+')\\n'+m.LOCK_CODE
command=[sys.executable,'-c','import os,pathlib,sys,time; pathlib.Path(sys.argv[1]).write_text(str(os.getpid())); time.sleep(60)',str(directory/'started')]
if sys.argv[3]=='inherited':
 with m.operation_lock.acquire() as fd:
  os.environ[m.operation_lock.ENV]=str(fd)
  with m.host_lock({},True) as held: m.run_playbook(command,held)
else:
 with m.host_lock({},True) as held:
  (directory/'helper').write_text(str(held.pid))
  m.run_playbook(command,held)
'''
        for mode in ('inherited', 'proxy'):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as tmp:
                directory = Path(tmp)
                controller = subprocess.Popen(
                    [sys.executable, '-c', worker, str(ROOT/'scripts/infra/server.py'), tmp, mode],
                    start_new_session=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                try:
                    for _ in range(150):
                        if (directory/'started').exists():
                            break
                        time.sleep(.02)
                    self.assertTrue((directory/'started').exists(), 'Operation child did not start')
                    operation = int((directory/'started').read_text())
                    os.killpg(controller.pid, signal.SIGKILL)
                    controller.wait(timeout=5)
                    os.kill(operation, 0)
                    with patch.object(lock.Path, 'home', return_value=directory):
                        with self.assertRaises(ValueError), lock.acquire():
                            pass
                        os.killpg(operation, signal.SIGTERM)
                        # The proxy exits on EOF once the operation closes its writer.
                        for _ in range(150):
                            try:
                                with lock.acquire():
                                    break
                            except ValueError:
                                time.sleep(.02)
                        else:
                            self.fail('Lock remained held after the operation stopped')
                finally:
                    if controller.poll() is None:
                        os.killpg(controller.pid, signal.SIGKILL)
                        controller.wait(timeout=5)
                    for marker in ('started', 'helper'):
                        if (directory/marker).exists():
                            try:
                                os.kill(int((directory/marker).read_text()), signal.SIGKILL)
                            except ProcessLookupError:
                                pass

    def test_cancellation_during_spawn_reaps_child_and_restores_handlers(self):
        original = subprocess.Popen
        for runner in ('operation', 'ansible'):
            for sig in (signal.SIGINT, signal.SIGTERM):
                with self.subTest(runner=runner, signal=sig), tempfile.TemporaryDirectory() as tmp, \
                        patch.object(lock.Path, 'home', return_value=Path(tmp)):
                    children = []
                    handlers = {number: signal.getsignal(number) for number in (signal.SIGINT, signal.SIGTERM)}
                    def interrupt_before_return(*args, **kwargs):
                        child = original(*args, **kwargs)
                        children.append(child)
                        os.kill(os.getpid(), sig)
                        return child
                    try:
                        with lock.acquire() as fd, patch.dict(os.environ, {lock.ENV: str(fd)}), \
                                patch.object(subprocess, 'Popen', side_effect=interrupt_before_return), \
                                self.assertRaises(KeyboardInterrupt):
                            command = [sys.executable, '-c', 'import time; time.sleep(60)']
                            if runner == 'operation':
                                lock.run(command, fd, quiet=True)
                            else:
                                platform.bootstrap.run_playbook(command, None)
                        self.assertEqual(len(children), 1)
                        self.assertIsNotNone(children[0].poll())
                        self.assertEqual(handlers, {number: signal.getsignal(number) for number in handlers})
                    finally:
                        for child in children:
                            if child.poll() is None:
                                os.killpg(child.pid, signal.SIGKILL)
                                child.wait()

    def test_signalled_command_returns_conventional_status(self):
        with tempfile.TemporaryDirectory() as tmp, patch.object(lock.Path, 'home', return_value=Path(tmp)), \
                lock.acquire() as fd:
            self.assertEqual(lock.run([sys.executable, '-c',
                'import os,signal; os.kill(os.getpid(),signal.SIGTERM)'], fd), 143)

    def test_lock_check_rejects_forged_descriptors_without_traceback(self):
        code = '''import pathlib,runpy,sys
directory=pathlib.Path(sys.argv.pop(1)); pathlib.Path.home=lambda:directory
runpy.run_path(sys.argv.pop(1),run_name='__main__')
'''
        with tempfile.TemporaryDirectory() as tmp, patch.object(lock.Path, 'home', return_value=Path(tmp)):
            command = [sys.executable, '-c', code, tmp, str(ROOT/'scripts/infra/operation-lock.py'), '--check']
            for value in ('', '999999'):
                result = subprocess.run(command, env=dict(os.environ, **{lock.ENV:value}), capture_output=True, text=True)
                self.assertEqual(result.returncode, 1)
                self.assertIn('Operation lock:', result.stderr)
                self.assertNotIn('Traceback', result.stderr)
            with lock.acquire() as fd:
                result = subprocess.run(command, env=dict(os.environ, **{lock.ENV:str(fd)}),
                                        pass_fds=(fd,), capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stderr)

    @patch.dict(os.environ, {'CHANGE_ID': '', 'CHANGE_TARGET': ''})
    def test_revision_guard_rejects_pr_dirty_and_other_branch(self):
        # Each case owns its PR context even when the suite runs inside Jenkins.
        revision='a'*40
        for marker, value in [('CHANGE_ID', '131'), ('CHANGE_TARGET', 'dev')]:
            with self.subTest(marker=marker), patch.dict(os.environ, {marker:value}), self.assertRaisesRegex(ValueError,'PR'):
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
            with patch.object(platform,'trusted_revision'), patch.object(platform,'check_inputs',return_value={'machine_id':'c'*32}), patch.object(platform,'fingerprint',return_value='identity'), patch.object(platform,'run_action',side_effect=action), redirect_stdout(io.StringIO()):
                platform.execute(args)
            self.assertEqual(calls,['plan','apply','verify'])
            report=json.loads(args.report.read_text())
            self.assertEqual(report['action'],'apply')
            self.assertNotIn('machine_id',report['target'])
            self.assertNotIn('c'*32,args.report.read_text())
            self.assertRegex(report['target']['machine_fingerprint'],r'^[a-f0-9]{64}$')

    def test_drift_never_reaches_apply(self):
        with tempfile.TemporaryDirectory() as tmp, patch.object(lock.Path,'home',return_value=Path(tmp)):
            args=argparse.Namespace(action='apply',receipt=Path(tmp)/'receipt',report=Path(tmp)/'report',revision='a'*40)
            platform.write_new(args.receipt,{'schema':1,'revision':args.revision,'inputs':'identity','created':time.time(),'change_digest':'old'})
            with patch.object(platform,'trusted_revision'), patch.object(platform,'check_inputs',return_value={'machine_id':'c'*32}), patch.object(platform,'fingerprint',return_value='identity'), patch.object(platform,'run_action',return_value={'change_digest':'new'}) as action:
                with self.assertRaisesRegex(ValueError,'drift'): platform.execute(args)
                self.assertEqual(action.call_count,1)
                self.assertFalse(args.report.exists())

    def test_failed_action_reports_only_sanitized_task_outcomes(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            args = argparse.Namespace(inventory=directory/'inventory', vault=directory/'vault',
                vault_password_file=directory/'vault-password', become_password_file=directory/'sudo-password')
            (directory/'plan.json').write_text(json.dumps({'schema':1, 'failed':True, 'tasks':[
                {'task':'Gathering Facts', 'status':'unreachable', 'changed':False,
                 'msg':'private-module-output-must-not-escape'}]}))
            with patch.object(platform.lock, 'run', return_value=4), self.assertRaises(ValueError) as failed:
                platform.run_action(args, 'plan', directory, 0)
            self.assertIn('Gathering Facts (unreachable)', str(failed.exception))
            self.assertNotIn('private-module-output', str(failed.exception))
            (directory/'plan.json').unlink()
            with patch.object(platform.lock, 'run', return_value=1), self.assertRaisesRegex(ValueError, 'no captured output'):
                platform.run_action(args, 'plan', directory, 0)

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
                output.write('    - name: Loop preview\n      ansible.builtin.copy:\n        content: loop-desired\n        dest: '+str(directory)+'/{{ item }}\n      loop: [first, second]\n')
            reports=[]
            for i in range(3):
                if i == 2:
                    (directory/'first').write_text('loop-desired')
                report=directory/f'report{i}.json'
                env=platform.bootstrap.clean_ansible_env(report)
                result=subprocess.run([os.environ['BATTLEFIELD_TEST_ANSIBLE_BIN']+'/ansible-playbook','-i','localhost,','-c','local',str(play),'--check','--diff'],env=env,capture_output=True,text=True)
                self.assertEqual(result.returncode,0,result.stderr)
                output=result.stdout+result.stderr+report.read_text()
                self.assertNotIn(secret,output)
                self.assertNotIn('hidden-diff-content',output)
                reports.append(json.loads(report.read_text()))
            self.assertEqual(reports[0]['change_digest'],reports[1]['change_digest'])
            self.assertEqual(reports[1]['tasks'],reports[2]['tasks'])
            self.assertNotEqual(reports[1]['change_digest'],reports[2]['change_digest'])
            self.assertFalse((directory/'destination').exists())
            self.assertTrue(any(t['changed'] for t in reports[0]['tasks']))

    @unittest.skipUnless(os.environ.get('BATTLEFIELD_TEST_ANSIBLE_BIN'),'run through test-server-bootstrap.sh for real Ansible')
    def test_real_ansible_reports_recovered_and_unrecovered_failures(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            play = directory/'failures.json'
            recoverable = [
                {'name':'Ignored failure', 'ansible.builtin.fail':{'msg':'synthetic ignored error'}, 'ignore_errors':True},
                {'block':[{'name':'Rescued failure', 'ansible.builtin.fail':{'msg':'synthetic rescued error'}}],
                 'rescue':[{'name':'Recovery', 'ansible.builtin.debug':{'msg':'recovered'}}]}]
            for fatal in (False, True):
                tasks = recoverable + ([{'name':'Fatal failure', 'ansible.builtin.fail':{'msg':'synthetic fatal error'}}] if fatal else [])
                play.write_text(json.dumps([{'hosts':'all','gather_facts':False,'tasks':tasks}]))
                report = directory/('fatal.json' if fatal else 'recovered.json')
                result = subprocess.run([os.environ['BATTLEFIELD_TEST_ANSIBLE_BIN']+'/ansible-playbook',
                    '-i','localhost,','-c','local',str(play)], env=platform.bootstrap.clean_ansible_env(report),
                    capture_output=True, text=True)
                self.assertEqual(result.returncode == 0, not fatal, result.stderr)
                data = json.loads(report.read_text())
                self.assertEqual(data['failed'], fatal)
                self.assertIn({'task':'Ignored failure','status':'ignored','changed':False}, data['tasks'])
                self.assertNotIn('synthetic', result.stdout + report.read_text())


if __name__=='__main__':unittest.main()
