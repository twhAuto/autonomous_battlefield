#!/usr/bin/env python3
"""Privileged HTTPS acceptance fixture, restricted to the disposable rehearsal VM."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import pwd
import shlex
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('https_runtime', ROOT/'scripts/infra/https-runtime.py')
r = importlib.util.module_from_spec(spec); spec.loader.exec_module(r)


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('phase', choices=['before-reboot','after-reboot'])
    parser.add_argument('--identity',required=True)
    parser.add_argument('--ansible',required=True)
    args=parser.parse_args();os.umask(0o077)
    assert os.geteuid()==0
    assert Path('/etc/battlefield-rehearsal').read_text().strip()==args.identity
    account=pwd.getpwnam('rehearsal')
    evidence=Path(account.pw_dir)/'evidence'
    evidence.mkdir(mode=0o700,exist_ok=True);os.chown(evidence,account.pw_uid,account.pw_gid)
    def run(*cmd):return subprocess.run(cmd,check=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE).stdout
    def lifecycle(action, request, name, review=''):
        inputs=evidence/(name+'-input.json');output=evidence/(name+'.json')
        inputs.write_text(json.dumps({'action':action,'request':request,'report':str(output),'review_id':review}))
        inventory=evidence/'inventory.json'
        inventory.write_text(json.dumps({'all':{'children':{'bootstrap':{'hosts':{'fixture':{'ansible_connection':'local','ansible_python_interpreter':'/usr/bin/python3','ansible_become':False}}}}}}))
        command=[args.ansible,'-i',str(inventory),str(ROOT/'infra/ansible/https.yml'),'-e',json.dumps({'https_runtime':str(ROOT/'scripts/infra/https-runtime.py'),'https_inputs_file':str(inputs)})]
        result=subprocess.run(command,env=dict(os.environ,ANSIBLE_CONFIG=str(ROOT/'infra/ansible/ansible.cfg')),capture_output=True,text=True)
        (evidence/(name+'.log')).write_text(result.stdout+result.stderr)
        assert result.returncode==0, 'Ansible '+name+' failed; see private fixture log'
        assert request['config']['root_private_key'] not in result.stdout+result.stderr
        return json.loads(output.read_text())
    def fails(call, message):
        try:call()
        except (ValueError,FileNotFoundError,subprocess.CalledProcessError):return
        raise AssertionError(message)
    def archive_ownership():
        # Execute the actual documented operator command, only inside this fixture.
        guide=(ROOT/'docs/operations/local-https-platform.md').read_text()
        section=guide.split('## Recover ownership after an intentional change\n',1)[1]
        script=section.split('```bash\n',1)[1].split('```',1)[0]
        if not Path('/usr/bin/sudo.ws').exists():script=script.replace('/usr/bin/sudo.ws','/usr/bin/sudo')
        return run('runuser','-u','rehearsal','--','bash','-c','cd '+shlex.quote(str(ROOT))+' && '+script)
    if args.phase=='before-reboot':
        run('systemctl','mask','caddy.service')
        run('apt-get','update','--quiet')
        subprocess.run(['apt-get','install','--yes','--no-install-recommends','caddy','openssl'],env=dict(os.environ,DEBIAN_FRONTEND='noninteractive'),stdout=subprocess.PIPE,stderr=subprocess.PIPE,check=True)
        default=r.CONFIG.read_bytes()
        server=evidence/'upstreams.py'
        server.write_text('''import http.server,threading,time
class Handler(http.server.BaseHTTPRequestHandler):
 def do_GET(self):
  from pathlib import Path
  unavailable=self.server.server_port==8888 and self.headers.get('Host')=='battlefield.localhost' and Path('/tmp/retired-kind').exists()
  self.send_response(503 if unavailable else 200);self.end_headers();self.wfile.write(self.headers.get('Host','').encode())
 def log_message(self,*args):pass
for port in (8888,9000,9999,30080):
 server=http.server.ThreadingHTTPServer(('0.0.0.0' if port==30080 else '127.0.0.1',port),Handler)
 threading.Thread(target=server.serve_forever,daemon=True).start()
while True:time.sleep(60)
''')
        unit=Path('/etc/systemd/system/https-fixture-upstreams.service')
        unit.write_text('[Unit]\nAfter=network.target\n[Service]\nExecStart=/usr/bin/python3 '+str(server)+'\n[Install]\nWantedBy=multi-user.target\n')
        run('systemctl','daemon-reload');run('systemctl','enable','--now',unit.name)
        text='\n'.join(host+' {\n tls internal\n reverse_proxy '+upstream+(' { header_up Host '+header+' }' if header else '')+'\n}\n' for host,(upstream,header) in r.REQUIRED.items())
        # Multiline Caddy syntax keeps this independent of the runtime's parser.
        text=text.replace(' { header_up Host ',' {\n header_up Host ').replace(' }','\n }')
        text+='\nnotes.home.arpa {\n tls internal\n reverse_proxy 127.0.0.1:9999\n}\n'
        r.CONFIG.write_text(text)
        run('systemctl','unmask','caddy.service');run('systemctl','enable','--now','caddy.service')
        # Wait for initial internal-CA issuance before capturing the synthetic inputs.
        import time
        for attempt in range(12):
            try:config=r.capture()['config'];break
            except (ValueError,OSError):
                if attempt==11:raise
                time.sleep(2)
        (evidence/'config.json').write_text(json.dumps(config))
        request={'config':config,'machine_id':Path('/etc/machine-id').read_text().strip(),'operator':'rehearsal','mode':'adopt','revision':'a'*40,'routing':None}
        plan=lifecycle('plan',request,'adopt-plan')
        pid=run('systemctl','show','caddy','--property=MainPID','--value')
        applied=lifecycle('apply',request,'adopt-apply',plan['review_id'])
        assert applied['applied_changes']==['record HTTPS ownership']
        assert run('systemctl','show','caddy','--property=MainPID','--value')==pid
        lifecycle('verify',request,'adopt-verify')
        repeat=lifecycle('plan',request,'adopt-repeat-plan');assert repeat['changes']==[]
        assert lifecycle('apply',request,'adopt-repeat-apply',repeat['review_id'])['changed'] is False
        engine=r.Engine(request);stale=engine.plan()
        r.CONFIG.write_text(text+'\n# changed after review\n')
        fails(lambda:engine.apply(stale['review_id']),'Accepted changed configuration')
        r.CONFIG.write_text(text)
        fails(lambda:r.Engine(dict(request,machine_id='f'*32)),'Accepted wrong host')
        # Model the actual migration: Kind application retired, monitoring moved to K3s.
        Path('/tmp/retired-kind').touch()
        captured=r.capture();assert captured['upstreams_healthy'] is False
        assert captured['routes']['battlefield.home.arpa']['status']==503
        fails(lambda:r.Engine(request).plan(),'Accepted unhealthy adoption')
        address=run('hostname','-I').decode().split()[0]
        request.update(mode='migrate',routing={'gateway':address+':30080','public_origin':'https://play.example.com',
                       'cluster_uid':'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee','node_uid':'ffffffff-aaaa-bbbb-cccc-dddddddddddd'})
        fails(lambda:r.Engine(request),'Silently changed adopted routing ownership')
        owner=(r.STATE/'owner.json').read_bytes()
        (r.STATE/'incomplete.json').write_text('{}')
        fails(archive_ownership,'Archived an incomplete operation')
        assert (r.STATE/'owner.json').read_bytes()==owner
        (r.STATE/'incomplete.json').unlink()  # Fixture-created negative marker only.
        archived=Path(archive_ownership().decode().strip().split(': ',1)[1])
        assert (archived/'owner.json').read_bytes()==owner
        assert (archived/'review.json').exists() and not r.STATE.exists()
        assert r.CONFIG.read_text()==text and (r.CA/'root.key').read_text()==config['root_private_key']
        desired=r.desired_configuration(config,request['routing'])
        plan=lifecycle('plan',request,'migrate-plan')
        applied=lifecycle('apply',request,'migrate-apply',plan['review_id']);assert applied['changed']
        assert run('systemctl','show','caddy','--property=MainPID','--value')==pid
        assert r.CONFIG.read_text()==desired['caddyfile']
        assert (r.CA/'root.key').read_text()==config['root_private_key']
        assert r.routes(r.CONFIG.read_text())['notes.home.arpa']==['127.0.0.1:9999',None]
        verified=lifecycle('verify',request,'migrate-verify')
        assert verified['tls']['battlefield.home.arpa']['status']==307
        repeat=lifecycle('plan',request,'migrate-repeat-plan');assert repeat['changes']==[]
        assert lifecycle('apply',request,'migrate-repeat-apply',repeat['review_id'])['changed'] is False
        # A fresh capture and verified replacement node require reviewed ownership,
        # but must not reload Caddy when the desired configuration already matches.
        recaptured=r.capture()['config']
        assert r.validate(recaptured)==r.validate(config)
        fails(lambda:r.Engine(dict(request,config=recaptured)),'Silently accepted new capture')
        new_routing=dict(request['routing'],node_uid='11111111-2222-3333-4444-555555555555')
        fails(lambda:r.Engine(dict(request,routing=new_routing)),'Silently accepted replacement node')
        archive_ownership()
        request.update(config=recaptured,routing=new_routing,mode='adopt')
        plan=lifecycle('plan',request,'rebind-plan');assert plan['changes']==['record HTTPS ownership']
        applied=lifecycle('apply',request,'rebind-apply',plan['review_id'])
        assert applied['applied_changes']==['record HTTPS ownership']
        lifecycle('verify',request,'rebind-verify')
        assert lifecycle('plan',request,'rebind-repeat-plan')['changes']==[]
        assert run('systemctl','show','caddy','--property=MainPID','--value')==pid
        assert r.CONFIG.read_text()==desired['caddyfile'] and (r.CA/'root.key').read_text()==config['root_private_key']
        # Rebuild only this disposable native service, retaining its captured root.
        run('systemctl','mask','--now','caddy.service')
        shutil.rmtree(r.HOME/'.local/share/caddy')
        shutil.rmtree(r.STATE)
        r.CONFIG.write_bytes(default)
        request.update(mode='install',config=config)
        missing=dict(config);missing.pop('root_private_key')
        fails(lambda:r.Engine(dict(request,config=missing)),'Accepted missing CA key')
        foreign=r.CONFIG.parent/'unrelated.conf';foreign.write_text('preserve me')
        fails(lambda:r.Engine(request).plan(),'Overwrote unrelated configuration')
        assert foreign.read_text()=='preserve me';foreign.unlink()
        plan=lifecycle('plan',request,'install-plan')
        applied=lifecycle('apply',request,'install-apply',plan['review_id']);assert applied['changed']
        assert (r.CA/'root.crt').read_text()==config['root_certificate']
        assert (r.CA/'root.key').read_text()==config['root_private_key']
        assert r.CONFIG.read_text()==desired['caddyfile']
        verified=lifecycle('verify',request,'install-verify')
        assert all(x['verified_tls'] for x in verified['tls'].values()) and len(verified['tls'])==5
        repeat=lifecycle('plan',request,'install-repeat-plan');assert repeat['changes']==[]
        assert lifecycle('apply',request,'install-repeat-apply',repeat['review_id'])['changed'] is False
        fails(lambda:r.Engine(request).apply(repeat['review_id']),'Accepted replayed receipt')
        (evidence/'request.json').write_text(json.dumps(request))
        print('PASS: real Ansible capture/adopt/migrate/rebind/install/repeat, retained CA and unrelated route, negative guards')
    else:
        request=json.loads((evidence/'request.json').read_text())
        verified=lifecycle('verify',request,'reboot-verify')
        repeat=lifecycle('plan',request,'reboot-plan');assert repeat['changes']==[]
        assert (r.CA/'root.key').read_text()==request['config']['root_private_key']
        (evidence/'result.json').write_text(json.dumps({'passed':True,'ca_sha256':verified['ca_sha256'],'routes':verified['routes'],'reboot_verified':True}))
        print('PASS: reboot retained root trust, routes and no-change configuration')
    for p in evidence.iterdir():
        if p.is_file():os.chown(p,account.pw_uid,account.pw_gid);p.chmod(0o600)


if __name__=='__main__':main()
