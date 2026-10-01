#!/usr/bin/env python3
"""Destructive CLI role fixture; only runs inside the UUID-bound disposable test VM."""
import argparse
import base64
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import uuid

ROOT = Path(__file__).resolve().parents[2]
PRIVATE = Path.home() / 'cli-fixture'
EVIDENCE = Path.home() / 'evidence'
IDENTITY = '22222222-2222-2222-2222-222222222222'
SECRET = 'synthetic-cli-bootstrap-secret-0123456789'


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


fixture = load('foundation_fixture', ROOT / 'scripts/test/rehearse-server-guest.py')
cli = load('cli_bootstrap', ROOT / 'scripts/infra/cli-bootstrap.py')


def run(*args, **kwargs):
    result = subprocess.run(args, capture_output=True, text=True, timeout=kwargs.pop('timeout', 600), **kwargs)
    if result.returncode:
        raise ValueError('Fixture command failed: ' + args[0] + '\n' + result.stdout[-3000:] + result.stderr[-1500:])
    return result.stdout.strip()


def install(path, content, mode='0644'):
    source = PRIVATE / 'staged'
    source.write_text(content)
    run('sudo', 'install', '-D', '-m', mode, str(source), path)


def virsh(*args):
    return run('sudo', 'virsh', '--connect', 'qemu:///system', *args)


def server(action, success=True, **kw):
    command = [str(ROOT / 'scripts/infra/server.sh'), action, '--local', '--inventory', str(PRIVATE / 'inventory.json'),
               '--cli-config', str(PRIVATE / 'cli.json')]
    if action != 'cli-register':
        command += ['--vault', str(PRIVATE / 'inputs.vault'), '--vault-password-file', str(PRIVATE / 'password')]
    else:
        command += ['--cli-install', '--cli-kubeconfig', str(PRIVATE / 'kubeconfig')]
    result = subprocess.run(command, capture_output=True, text=True, timeout=900, **kw)
    output = result.stdout + result.stderr
    if SECRET in output or base64.b64encode(SECRET.encode()).decode() in output:
        raise ValueError('Synthetic secret leaked; output will not be archived')
    sequence = len(list(EVIDENCE.glob(action + '-*.log')))
    (EVIDENCE / (action + '-' + str(sequence) + '.log')).write_text(output)
    if bool(result.returncode == 0) != success:
        (EVIDENCE / (action + '-failure.log')).write_text(output)
        raise ValueError('Unexpected ' + action + ' result; inspect private evidence')
    return output


def recap(output, changed=None):
    rows = re.findall(r'^\S+\s+:\s+ok=\d+\s+changed=(\d+)\s+unreachable=(\d+)\s+failed=(\d+)', output, re.M)
    if not rows or any(int(row[1]) or int(row[2]) for row in rows):
        raise ValueError('Expected a successful Ansible recap')
    total = sum(int(row[0]) for row in rows)
    if changed is not None and total != changed:
        raise ValueError('Expected changed=' + str(changed) + ', got ' + str(total))
    return total


def invocations():
    return {name: run('systemctl', 'show', name, '--property=InvocationID', '--value')
            for name in cli.boundary.HOST_UNITS + ['battlefield-worker']}


def digest(path):
    return run('sudo', 'sha256sum', path).split()[0]


def guest_playbook(action, inventory=None, success=True):
    """Run the actual optional guest play on this disposable VM, without nested SSH."""
    env = dict(os.environ, ANSIBLE_CONFIG=str(ROOT / 'infra/ansible/ansible.cfg'), ANSIBLE_NOCOLOR='1')
    binary = run(str(ROOT / 'scripts/infra/server.sh'), 'tools') + '/ansible-playbook'
    command = [binary, '-i', str(inventory or PRIVATE / 'guest-inventory.json'), str(ROOT / 'infra/ansible/cli.yml'),
               '-e', json.dumps({'cli_action': action, 'cli_vault_file': str(PRIVATE / 'inputs.vault')}),
               '--vault-password-file', str(PRIVATE / 'password')]
    if action == 'plan': command += ['--check', '--diff']
    result = subprocess.run(command, capture_output=True, text=True, env=env, timeout=600)
    output = result.stdout + result.stderr
    if SECRET in output or base64.b64encode((PRIVATE/'worker.env').read_bytes()).decode() in output:
        raise ValueError('Synthetic guest credentials leaked')
    (EVIDENCE / ('guest-' + action + '.log')).write_text(output)
    if bool(result.returncode == 0) != success: raise ValueError('Unexpected guest playbook result; inspect safe evidence')
    return output


def before():
    PRIVATE.mkdir(mode=0o700); EVIDENCE.mkdir(mode=0o700, exist_ok=True)
    run('sudo', 'apt-get', 'update', '-qq')
    run('sudo', 'env', 'DEBIAN_FRONTEND=noninteractive', 'apt-get', 'install', '-y', '-qq',
        'libvirt-daemon-system', 'libvirt-clients', 'qemu-system-x86', 'python3-venv', timeout=1200)
    run('sudo', 'systemctl', 'enable', '--now', 'libvirtd')
    # The outer rehearsal VM already uses the hypervisor's default NAT subnet.
    # Do not let nested libvirt claim that same route when this guest reboots.
    if 'default' in virsh('net-list', '--all', '--name').splitlines():
        virsh('net-autostart', 'default', '--disable')
        if 'default' in virsh('net-list', '--name').splitlines():
            virsh('net-destroy', 'default')
    run('sudo', 'usermod', '-aG', 'libvirt', 'rehearsal')
    # New SSH session in the outer driver supplies the new supplementary group.
    install('/usr/bin/kubectl', '''#!/usr/bin/python3
import http.server,sys
if 'port-forward' not in sys.argv:
 print('22222222-2222-2222-2222-222222222222'); sys.exit(0)
class Handler(http.server.BaseHTTPRequestHandler):
 def do_GET(self): self.send_response(401); self.end_headers()
 def log_message(self,*args): pass
http.server.HTTPServer(('127.0.0.1',28084),Handler).serve_forever()
''', '0755')
    for name in ('filter', 'network'):
        source = ROOT / ('deploy/agent-vm/' + name + '.xml')
        virsh('nwfilter-define' if name == 'filter' else 'net-define', str(source))
    virsh('net-start', 'battlefield-agent'); virsh('net-autostart', 'battlefield-agent')
    # Tiny TCG domain exercises real libvirt ownership/NIC checks. It has no workload OS.
    domain = PRIVATE / 'domain.xml'
    domain.write_text("""<domain type='qemu'><name>battlefield-agent</name><memory unit='MiB'>128</memory><vcpu>1</vcpu>
<os><type arch='x86_64'>hvm</type></os><devices><interface type='network'><mac address='52:54:00:ba:77:01'/>
<source network='battlefield-agent'/><model type='virtio'/><filterref filter='battlefield-agent'>
<parameter name='IP' value='192.168.234.2'/></filterref></interface></devices></domain>""")
    virsh('define', str(domain)); virsh('autostart', 'battlefield-agent'); virsh('start', 'battlefield-agent')
    machine = Path('/etc/machine-id').read_text().strip()
    (PRIVATE / 'inventory.json').write_text(json.dumps({'schema':1,'host':'127.0.0.1','user':'rehearsal',
        'identity_file':str(PRIVATE/'unused-key'),'machine_id':machine}))
    (PRIVATE/'kubeconfig').write_text('fixture namespace identity; kubectl stub used only inside disposable VM')
    (PRIVATE/'password').write_text('fixture-vault-password')
    (PRIVATE/'worker.env').write_text('BATTLEFIELD_AGENT_SECRET='+SECRET+'\nBATTLEFIELD_ENV=production\nBATTLEFIELD_CLI_ISOLATION_VERIFIED=false\n')
    binary = run(str(ROOT / 'scripts/infra/server.sh'), 'tools') + '/ansible-vault'
    payload = {'bootstrap_inputs':{'cli_worker_environment':base64.b64encode((PRIVATE/'worker.env').read_bytes()).decode()}}
    subprocess.run([binary,'encrypt','--output',str(PRIVATE/'inputs.vault'),'--vault-password-file',str(PRIVATE/'password')],
                   input=json.dumps(payload).encode(),check=True,stdout=subprocess.DEVNULL)
    # All synthetic private input files use the same strict modes as real controllers.
    for path in PRIVATE.iterdir():
        if path.is_file(): path.chmod(0o600)


def exercise():
    output = server('cli-register'); (EVIDENCE/'register.log').write_text(output)
    recap(server('cli-plan'))
    if Path('/etc/battlefield-cli-bootstrap').exists(): raise ValueError('Plan mutated ownership')
    output = server('cli-apply'); recap(output); (EVIDENCE/'host-first.log').write_text(output)
    host_ids = {name: run('systemctl','show',name,'--property=InvocationID','--value') for name in cli.boundary.HOST_UNITS}
    recap(server('cli-apply'),0); recap(server('cli-plan'),0); recap(server('cli-verify'),0)
    if host_ids != {name: run('systemctl','show',name,'--property=InvocationID','--value') for name in cli.boundary.HOST_UNITS}:
        raise ValueError('Healthy host service restarted on repeat apply')
    # Missing runtime firewall is drift even when its persisted file remains unchanged.
    run('sudo','nft','delete','table','inet','battlefield_agent')
    server('cli-verify', success=False)
    recap(server('cli-apply')); recap(server('cli-verify'),0)
    # Unknown override and wrong domain identity fail before persistent changes.
    install('/etc/systemd/system/battlefield-mcp-bridge.service.d/unknown.conf','[Service]\nNice=1\n')
    failure = server('cli-apply',success=False)
    if 'Unknown CLI service override' not in failure or 'changed=0' not in failure: raise ValueError('Override guard did not fail early')
    run('sudo','rm','/etc/systemd/system/battlefield-mcp-bridge.service.d/unknown.conf')
    cfg=json.loads((PRIVATE/'cli.json').read_text()); original=json.dumps(cfg)
    cfg['host']['domain_uuid']=str(uuid.uuid4()); (PRIVATE/'cli.json').write_text(json.dumps(cfg))
    server('cli-apply',success=False); (PRIVATE/'cli.json').write_text(original)
    # Guest lifecycle uses a synthetic health process: no real model invocation or credentials.
    worker=PRIVATE/'worker'
    worker.write_text('''#!/usr/bin/python3
import http.server
class Handler(http.server.BaseHTTPRequestHandler):
 def do_GET(self): self.send_response(204); self.end_headers()
 def log_message(self,*args): pass
http.server.HTTPServer(('127.0.0.1',8083),Handler).serve_forever()
''')
    guest_config=PRIVATE/'config.yaml'
    guest_config.write_text((ROOT/'deploy/agent-vm/config.yaml').read_text().replace('enabled: true','enabled: false'))
    artifacts={}
    for name in ('codex','codex-code-mode-host'):
        path=PRIVATE/name; path.write_text('#!/bin/sh\nprintf "codex-cli 0.155.0\\n"\n'); artifacts[name]=path
    files=[cli.file_entry(worker,'/usr/local/bin/battlefield-agent','0755'),
           cli.file_entry(ROOT/'deploy/agent-vm/worker.service','/etc/systemd/system/battlefield-worker.service','0644'),
           cli.file_entry(ROOT/'scripts/ops/verify-agent-isolation.py','/usr/local/bin/verify-agent-isolation.py','0755'),
           cli.file_entry(guest_config,'/etc/battlefield-agent/config.yaml','0644')]
    files += [cli.file_entry(path,'/usr/local/bin/'+name,'0755') for name,path in artifacts.items()]
    variables={'ansible_connection':'local','ansible_python_interpreter':'/usr/bin/python3',
               'cli_kind':'guest','cli_units':['battlefield-worker'],'cli_files':files,
               'cli_contract':{'machine_id':Path('/etc/machine-id').read_text().strip(),'domain_uuid':Path('/etc/battlefield-rehearsal').read_text().strip()},
               'cli_adopt_existing':False,'cli_config_source':str(guest_config),'cli_artifact_names':['battlefield-agent',*artifacts]}
    (PRIVATE/'guest-inventory.json').write_text(json.dumps({'all':{'children':{'cli_guest':{'hosts':{'worker':variables}}}}}))
    recap(guest_playbook('plan'))
    # A guest preflight failure must prevent repair of even harmless host drift.
    host_vars={'ansible_connection':'local','ansible_python_interpreter':'/usr/bin/python3',
               'ansible_become_exe':'/usr/bin/sudo.ws' if Path('/usr/bin/sudo.ws').exists() else '/usr/bin/sudo',
               'cli_kind':'host','cli_units':cli.boundary.HOST_UNITS,'cli_contract':cfg['host'],
               'cli_files':cli.host_files(cfg),'cli_filter_xml':(ROOT/'deploy/agent-vm/filter.xml').read_text(),
               'cli_firewall':(Path(cfg['bundle'])/'firewall.nft').read_text()}
    host_vars['cli_contract']['domain_uuid']=json.loads(original)['host']['domain_uuid']
    bad_guest=dict(variables,cli_contract=dict(variables['cli_contract'],machine_id='f'*32))
    both=PRIVATE/'barrier-inventory.json'
    both.write_text(json.dumps({'all':{'children':{'cli_host':{'hosts':{'server':host_vars}},
                                                'cli_guest':{'hosts':{'worker':bad_guest}}}}}))
    run('sudo','chmod','0640','/etc/battlefield-agent/mcp-proxy.Caddyfile')
    guest_playbook('apply',inventory=both,success=False)
    if run('sudo','stat','-c','%a','/etc/battlefield-agent/mcp-proxy.Caddyfile')!='640':
        raise ValueError('Host changed despite failed guest preflight')
    run('sudo','chmod','0644','/etc/battlefield-agent/mcp-proxy.Caddyfile')
    # Simulate interruption immediately after ownership recording, before any managed files exist.
    contract={'schema':1,'identity':variables['cli_contract'],'files':{x['dest']:x['sha256'] for x in files}}
    contract['files']['/etc/battlefield-agent/worker.env']=hashlib.sha256((PRIVATE/'worker.env').read_bytes()).hexdigest()
    install('/etc/battlefield-cli-bootstrap/guest.json',json.dumps(contract),'0600')
    recap(guest_playbook('apply'))
    install('/var/lib/battlefield-worker/.provider-login-sentinel','preserve-existing-login','0600')
    run('sudo','chown','battlefield-worker:battlefield-worker','/var/lib/battlefield-worker/.provider-login-sentinel')
    ids=invocations(); fingerprint=digest('/etc/battlefield-agent/worker.env')
    recap(guest_playbook('apply'),0); recap(guest_playbook('plan'),0); recap(guest_playbook('verify'),0)
    if invocations()!=ids or digest('/etc/battlefield-agent/worker.env')!=fingerprint:
        raise ValueError('Repeat guest apply changed a healthy service or secret')
    if run('sudo','cat','/var/lib/battlefield-worker/.provider-login-sentinel')!='preserve-existing-login':
        raise ValueError('Provider login state changed')
    (EVIDENCE/'before.json').write_text(json.dumps({'passed':True,'secret_sha256':fingerprint,'boot_id':Path('/proc/sys/kernel/random/boot_id').read_text().strip()}))


def after():
    previous=json.loads((EVIDENCE/'before.json').read_text())
    if previous['boot_id']==Path('/proc/sys/kernel/random/boot_id').read_text().strip(): raise ValueError('Guest did not reboot')
    recap(server('cli-verify'),0); recap(guest_playbook('verify'),0)
    ids=invocations(); recap(server('cli-apply'),0); recap(guest_playbook('apply'),0)
    if ids!=invocations() or previous['secret_sha256']!=digest('/etc/battlefield-agent/worker.env'):
        raise ValueError('Post-reboot repeat apply changed healthy services or secret')
    (EVIDENCE/'passed.json').write_text(json.dumps({'passed':True,'synthetic_backend':True,'reboot_verified':True}))


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('phase',choices=['prepare','exercise','after-reboot']); parser.add_argument('--identity',required=True)
    args=parser.parse_args(); os.umask(0o077); fixture.require_guest(args.identity)
    {'prepare':before,'exercise':exercise,'after-reboot':after}[args.phase]()
    print('PASS: CLI bootstrap fixture '+args.phase)
