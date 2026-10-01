#!/usr/bin/env python3
"""Install an owned disposable platform and exercise real private application delivery.

Inputs are verified GHCR digests and a read-only registry credential. Public Access
cutover and the existing CLI VM are deliberately outside this private rehearsal.
"""
import argparse
import base64
import importlib.util
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[2]


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, ROOT / path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


platform = load('platform_rehearsal', 'scripts/infra/rehearse-k3s.py')
base, k3s = platform.base, platform.k3s


def private_probe(environment, work, service, port, path, expected):
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        local = sock.getsockname()[1]
    with (work / (service + '.log')).open('w') as log:
        command = ['kubectl', '--context', 'battlefield-production', '-n', 'battlefield',
                   'port-forward', '--address', '127.0.0.1', 'service/' + service, f'{local}:{port}']
        process = subprocess.Popen(command, env=environment, stdout=log, stderr=log)
        try:
            client = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            for _ in range(30):
                if process.poll() is not None:
                    raise ValueError('Private port forward exited: ' + service)
                try:
                    response = client.open(f'http://127.0.0.1:{local}{path}', timeout=3)
                except urllib.error.HTTPError as error:
                    response = error
                except OSError:
                    time.sleep(1)
                    continue
                with response:
                    if response.status != expected:
                        raise ValueError('Unexpected private response for ' + service + path)
                return
            raise ValueError('Private readiness timed out: ' + service)
        finally:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('image', 'registry-config', 'public-key', 'evidence-dir'):
        parser.add_argument('--' + name, type=Path, required=True)
    for name in ('backend', 'frontend', 'revision'):
        parser.add_argument('--' + name, required=True)
    args = parser.parse_args()
    os.umask(0o077)
    if args.evidence_dir.resolve().is_relative_to(ROOT):
        raise ValueError('Evidence must be outside the checkout')
    auth = json.loads(k3s.bootstrap.private_file(args.registry_config).read_text())['auths']['ghcr.io']['auth']
    username, password = base64.b64decode(auth, validate=True).decode().split(':', 1)
    credentials = k3s.config.registry({'username': username, 'password': password, 'probe_image': args.backend})
    pins = json.loads((ROOT / 'infra/ansible/rehearsal-image.json').read_text())['24.04']
    base.checked_image(args.image, pins['sha256'])
    available = int(dict(line.split(':', 1) for line in Path('/proc/meminfo').read_text().splitlines())['MemAvailable'].split()[0])
    if available < 10 * 1024**2 or shutil.disk_usage('/var/lib/libvirt/images').free < 80 * 1024**3:
        raise ValueError('Need 10 GiB available RAM and 80 GiB free disk for this rehearsal')
    args.evidence_dir.mkdir(mode=0o700)
    normal_env = k3s.bootstrap.clean_ansible_env
    def diagnostic_env(report=None):
        environment = normal_env(report)
        if report:
            environment['ANSIBLE_STDOUT_CALLBACK'] = 'rehearsal_report'
            environment['ANSIBLE_CALLBACK_PLUGINS'] += ':' + str(ROOT/'scripts/test/k3s_callback_plugins')
            environment['BATTLEFIELD_REHEARSAL_DIAGNOSTIC'] = str(args.evidence_dir/'failure-private.jsonl')
        return environment
    k3s.bootstrap.clean_ansible_env = diagnostic_env
    before = {name: base.virsh('domuuid', name) for name in base.virsh('list', '--all', '--name').splitlines() if name}
    networks = {name: base.virsh('net-uuid', name) for name in base.virsh('net-list', '--all', '--name').splitlines() if name}
    report = {'passed': False, 'cleaned_up': False, 'scope': 'private application delivery; no public cutover or CLI VM retargeting',
              'source_sha256': {str(p.relative_to(ROOT)): base.digest(p) for directory in ('scripts/cd','scripts/lib','scripts/infra','deploy','infra/ansible','infra/k3s')
                                 for p in (ROOT/directory).rglob('*') if p.is_file() and '__pycache__' not in p.parts}}
    with tempfile.TemporaryDirectory(prefix='bf-delivery-rehearsal-') as temporary:
        work = Path(temporary)
        guest = base.Rehearsal(work, args.evidence_dir, 'default', memory_mib=8192, root_gib=32, cpus=4)
        try:
            print('Creating owned disposable delivery guest ' + guest.name, flush=True)
            guest.prepare(args.image)
            base.virsh('vol-create-as', guest.identity, 'data.qcow2', '40G', '--format', 'qcow2', '--allocation', '0')
            guest.volumes.append('data.qcow2')
            base.virsh('attach-disk', guest.identity, guest.pool_path + '/data.qcow2', 'vdb', '--serial', 'battlefield-k3s-data',
                       '--subdriver', 'qcow2', '--targetbus', 'virtio', '--live', '--config')
            settings = k3s.config.validate(json.loads((ROOT/'infra/k3s/settings.json').read_text()), [str(guest.subnet)])
            cache = k3s.tools.private_directory(Path.home()/'.cache/autonomous-battlefield/k3s-rehearsal')
            runtime_pins, assets = k3s.pins_and_assets(cache)
            contract = k3s.config.contract({'machine_id':guest.identity,'data_uuid':guest.identity,'gateway':str(guest.subnet.network_address+1)},
                {'domain_uuid':guest.identity,'data_volume':guest.pool_path+'/data.qcow2','address':guest.ip},40*1024**3,settings,runtime_pins)
            inventory = {'all':{'children':{'k3s':{'hosts':{'battlefield-k3s':{'ansible_host':guest.ip,'ansible_user':'rehearsal',
                'ansible_ssh_private_key_file':str(work/'client'),'ansible_ssh_common_args':'-o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile='+str(work/'known_hosts')}}}}}}
            k3s.tools.atomic(work/'inventory.json',k3s.vm.json_bytes(inventory))
            with k3s.lock.acquire() as fd:
                values=k3s.variables(contract,runtime_pins,assets,credentials)
                print('Installing the same managed K3s roles',flush=True)
                k3s.run_action('apply',work/'inventory.json',values,work,fd)
                k3s.run_action('verify',work/'inventory.json',values,work,fd)
                config=guest.ssh('sudo cat /etc/rancher/k3s/k3s.yaml').replace('https://127.0.0.1:6443','https://'+guest.ip+':6443')
                k3s.tools.atomic(work/'source-kubeconfig',config.encode())
                k3s.tools.atomic(work/'ca.crt',guest.ssh('sudo cat /srv/battlefield/k3s/server/tls/server-ca.crt').encode())
                registration=work/'registration'
                env={k:v for k,v in os.environ.items() if not k.startswith(('BF_','BATTLEFIELD_','DEPLOY_','KUBERNETES_','KIND_','DOCKER_','BACKEND_','FRONTEND_','SMOKE_'))
                     and k not in ('KUBECONFIG','CLUSTER_DRIVER','TARGET_URL','ZAP_TARGET_URL','IMAGE_SOURCE','ENFORCE_SUPPLY_CHAIN_POLICY','LANGFUSE_ENABLED')}
                env.update(DEPLOY_ENV='production',BATTLEFIELD_ENV_FILE=str(registration/'deployment.env'),BATTLEFIELD_CONFIG_DIR=str(registration),
                    KUBECONFIG=str(work/'source-kubeconfig'),KUBELET_CA_FILE=str(work/'ca.crt'),TARGET_URL='https://fixture.example.com',
                    GHCR_USER=username,GHCR_TOKEN=password,COSIGN_PUBLIC_KEY_FILE=str(args.public_key.resolve()),
                    BACKEND_IMAGE_REF=args.backend,FRONTEND_IMAGE_REF=args.frontend,LANGFUSE_ENABLED='0',
                    BATTLEFIELD_BACKUP_DIR=str(work/'backups'),VERIFY_ARTIFACT_DIR=str(work/'verification'))
                env[k3s.lock.ENV]=str(fd)
                def command(name,*arguments,**changes):
                    report['stage'] = name
                    with (args.evidence_dir/(name.replace('/','-')+'.private.log')).open('a') as log:
                        with k3s.lock.supervised([str(ROOT/name),*arguments], env=env|changes, pass_fds=(fd,),
                                                 stdout=log, stderr=log) as child:
                            code = child.wait(timeout=1800)
                            if code:
                                report['command_exit'] = code
                                raise ValueError('Rehearsal command failed')
                command('scripts/cd/setup-environment.sh','production','default','k3s')
                env['KUBECONFIG']=str(registration/'kubeconfig')
                app={'POSTGRES_DB':'battlefield','POSTGRES_USER':'battlefield','POSTGRES_PASSWORD':'synthetic-delivery-only',
                    'DATABASE_URL':'postgres://battlefield:synthetic-delivery-only@battlefield-postgres:5432/battlefield?sslmode=disable',
                    'BATTLEFIELD_AUTH_MODE':'cloudflare','BATTLEFIELD_ACCESS_ISSUER':'https://fixture.cloudflareaccess.com',
                    'BATTLEFIELD_ACCESS_AUDIENCE':'a'*64,'BATTLEFIELD_PUBLIC_ORIGIN':'https://fixture.example.com',
                    'BATTLEFIELD_ACCESS_ROLES':'{"admin":["owner@example.com"]}','BATTLEFIELD_AGENT_SECRET':'b'*32,
                    'BATTLEFIELD_MCP_CAPABILITY_SECRET':'c'*32,'BATTLEFIELD_MANAGED_PLAYERS':'false','BATTLEFIELD_CLI_ISOLATION_VERIFIED':'false'}
                k3s.tools.atomic(registration/'.env',''.join(k+'='+v+'\n' for k,v in app.items()).encode())
                print('Deploying real verified images, migrations and observability',flush=True)
                command('scripts/ci/with-ghcr-auth.sh',str(ROOT/'scripts/cd/deploy.sh'),args.revision)
                report['stage'] = 'private readiness and denial'
                for service,port,path,status in [('battlefield-api',8080,'/readyz',200),('battlefield-api',8080,'/v1/scenarios',401),
                                                ('battlefield-frontend',8080,'/livez',200),('battlefield-mcp',8084,'/healthz',204)]:
                    private_probe(env,work,service,port,path,status)
                before_claims=guest.ssh('sudo k3s kubectl -n battlefield get pvc -o json')
                command('scripts/cd/production-recovery.sh','record',args.revision)
                print('Checking configuration-only delivery and database-bound digest rollback',flush=True)
                command('scripts/ci/with-ghcr-auth.sh',str(ROOT/'scripts/cd/deploy.sh'),args.revision,DEPLOY_COMPONENTS='application',BACKEND_IMAGE_REF='',FRONTEND_IMAGE_REF='')
                command('scripts/cd/rollback.sh',args.backend,args.frontend,BATTLEFIELD_ACCESS_COMPATIBLE_BACKEND_DIGEST=args.backend)
                after_claims=guest.ssh('sudo k3s kubectl -n battlefield get pvc -o json')
                identity=lambda raw: sorted((x['metadata']['uid'],x['spec']['volumeName']) for x in json.loads(raw)['items'])
                if identity(before_claims)!=identity(after_claims): raise ValueError('Repeat delivery replaced application data claims')
                report.update(passed=True,retained_claims=len(identity(after_claims)),private_readiness=True,
                              authenticated_public_smoke=False,cli_vm_bridge=False,database_bound_rollback=True)
        finally:
            try:
                if guest.ip:
                    try:
                        pods=json.loads(guest.ssh('sudo k3s kubectl get pods -A -o json',timeout=30))
                        report['pods']=[{'name':p['metadata']['name'],'namespace':p['metadata']['namespace'],
                            'phase':p['status']['phase'],'containers':[{'name':c['name'],'ready':c['ready'],
                            'restarts':c['restartCount'],'reason':c.get('state',{}).get('waiting',{}).get('reason','')}
                            for c in p['status'].get('containerStatuses',[])]} for p in pods['items']]
                    except (ValueError,OSError,subprocess.SubprocessError):
                        report['pod_inspection_failed']=True
                guest.cleanup()
                report['cleaned_up']=True
                report['existing_resources_preserved']=before=={name:base.virsh('domuuid',name) for name in base.virsh('list','--all','--name').splitlines() if name} and networks=={name:base.virsh('net-uuid',name) for name in base.virsh('net-list','--all','--name').splitlines() if name}
                if not report['existing_resources_preserved']: raise ValueError('Existing resource inventory changed')
            finally:
                (args.evidence_dir/'report.json').write_text(json.dumps(report,indent=2)+'\n')
    print('Private delivery rehearsal passed; public cutover acceptance is separate',flush=True)


if __name__=='__main__':
    try:
        main()
    except (ValueError, OSError, KeyError, subprocess.SubprocessError):
        raise SystemExit('Delivery rehearsal failed; inspect private evidence. No credentials were published.') from None
