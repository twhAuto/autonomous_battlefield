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
import re
from pathlib import Path
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
# This directory contains our platform.py. Keep it from shadowing the standard
# library's platform module when uuid is loaded by the VM fixture on Python 3.12.
sys.path = [entry for entry in sys.path if Path(entry).resolve() != Path(__file__).resolve().parent]


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, ROOT / path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


platform = load('platform_rehearsal', 'scripts/infra/rehearse-k3s.py')
base, k3s = platform.base, platform.k3s
recovery = load('delivery_recovery', 'scripts/lib/production_recovery.py')


def releases(args):
    """Validate all release identifiers before creating any guest or credential files."""
    current = {name: getattr(args, name) for name in ('backend', 'frontend', 'revision')}
    previous = {name: getattr(args, 'previous_' + name) for name in current}
    supplied = any(value is not None for value in previous.values())
    if supplied and not all(previous.values()):
        raise ValueError('Supply all three previous-release arguments together')
    for release in (current, previous) if all(previous.values()) else (current,):
        if not re.fullmatch(r'[a-f0-9]{40}', release['revision']):
            raise ValueError('Release revisions must be full lowercase Git SHAs')
        if not all(recovery.DIGEST.fullmatch(release[name]) for name in ('backend', 'frontend')):
            raise ValueError('Release images must be immutable digest references')
    if all(previous.values()):
        if previous['revision'] == current['revision'] or all(previous[n] == current[n] for n in ('backend', 'frontend')):
            raise ValueError('Distinct-release recovery needs different revisions and image pairs')
    else:
        previous = current.copy()
    return previous, current


def inspect_delivery(environment, evidence_dir):
    kube = ['kubectl', '--request-timeout=15s', '--context', 'battlefield-production', '-n', 'battlefield']
    def read(*arguments):
        # Keep API/psql output private, including errors and partial timeout output.
        with (evidence_dir / 'inspection.private.log').open('a') as log:
            print(json.dumps([*kube, *arguments]), file=log, flush=True)
            try:
                result = subprocess.run([*kube, *arguments], env=environment, check=True,
                                        text=True, stdout=subprocess.PIPE, stderr=log, timeout=30)
            except (subprocess.CalledProcessError, subprocess.TimeoutExpired) as error:
                output = error.stdout or ''
                print(output.decode(errors='replace') if isinstance(output, bytes) else output, file=log)
                print(type(error).__name__, file=log)
                raise ValueError('Inspection failed; inspect inspection.private.log') from error
            print(result.stdout, file=log)
            return json.loads(result.stdout)
    return {
        'deployments': read('get', 'deployments', '-o', 'json'),
        'claims': read('get', 'pvc', '-o', 'json'),
        'migrations': read('exec', 'battlefield-postgres-0', '--', 'sh', '-ec',
            'exec psql -X -v ON_ERROR_STOP=1 -At --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" -c "$1"', '_',
            "SELECT COALESCE(json_agg(version_id ORDER BY version_id), '[]'::json) FROM "
            "(SELECT DISTINCT ON (version_id) version_id, is_applied FROM public.goose_db_version "
            "ORDER BY version_id, id DESC) v WHERE is_applied;")}


class RolloutPending(ValueError):
    """A correctly selected release is still converging to its desired replica count."""


def checkpoint(state, release):
    """Accept only converged exact images and the two bound retained application claims."""
    deployments = {d['metadata']['name']: d for d in state['deployments']['items']}
    images = {}
    for name in (*recovery.BACKENDS, 'battlefield-frontend'):
        item = deployments[name]
        desired = item['spec'].get('replicas', 1)
        status = item['status']
        expected = release['frontend' if name == 'battlefield-frontend' else 'backend']
        actual = next(c['image'] for c in item['spec']['template']['spec']['containers']
                      if c['name'] == name.removeprefix('battlefield-'))
        if actual != expected or desired < 1:
            raise ValueError('Release did not converge to the expected healthy images: ' + name)
        if (status.get('observedGeneration', 0) < item['metadata']['generation'] or
                any(status.get(field, 0) != desired for field in ('updatedReplicas', 'readyReplicas', 'availableReplicas'))):
            raise RolloutPending('Release did not converge to the expected healthy images: ' + name)
        images[name] = actual
    claims = {}
    for item in state['claims']['items']:
        if (item['status']['phase'] != 'Bound' or item['spec'].get('storageClassName') != 'battlefield-retained' or
                not item['metadata'].get('uid') or not item['spec'].get('volumeName')):
            raise ValueError('Application claims must be bound to retained storage')
        claims[item['metadata']['name']] = {'uid': item['metadata']['uid'], 'volume': item['spec']['volumeName']}
    if set(claims) != {'data-battlefield-postgres-0', 'battlefield-elasticsearch-data'}:
        raise ValueError('Expected both PostgreSQL and Elasticsearch retained claims')
    return {'revision': release['revision'], 'images': images, 'claims': claims,
            'database_migrations': recovery.migration_state(state['migrations'])}


def converged_checkpoint(inspect, release):
    # HPA changes can follow rollout status; retry readiness without weakening it.
    for attempt in range(7):
        try:
            return checkpoint(inspect(), release)
        except RolloutPending:
            if attempt == 6:
                raise
            time.sleep(5)


def exercise_delivery(command, inspect, probe, previous, current, report):
    """Use normal delivery and rollback commands against the caller's isolated registration."""
    report['checkpoints'] = {}
    def check(name, release):
        report['stage'] = name
        evidence = converged_checkpoint(inspect, release)
        baseline = report['checkpoints'].get('previous')
        if baseline and evidence['claims'] != baseline['claims']:
            report['checkpoint_failure'] = 'retained_claims_changed'
            raise ValueError(name + ': retained claim identities changed; recovery cannot be claimed')
        if baseline and evidence['database_migrations'] != baseline['database_migrations']:
            report['checkpoint_failure'] = 'migration_ledger_changed'
            raise ValueError(name + ': migration ledger changed; choose compatible releases; recovery cannot be claimed')
        probe()
        report['checkpoints'][name] = evidence

    def deploy(release, components):
        command('scripts/ci/with-ghcr-auth.sh', str(ROOT/'scripts/cd/deploy.sh'), release['revision'],
                DEPLOY_COMPONENTS=components, BACKEND_IMAGE_REF=release['backend'], FRONTEND_IMAGE_REF=release['frontend'],
                BACKEND_IMAGE_REVISION=release['revision'], FRONTEND_IMAGE_REVISION=release['revision'])

    print('Deploying and checking the previous verified release', flush=True)
    deploy(previous, 'all')
    check('previous', previous)
    # This record belongs only to the synthetic private fixture. It is never
    # copied to production or represented as public Access acceptance.
    command('scripts/cd/production-recovery.sh', 'record', previous['revision'])
    print('Checking configuration-only delivery preserves both images', flush=True)
    command('scripts/ci/with-ghcr-auth.sh', str(ROOT/'scripts/cd/deploy.sh'), previous['revision'],
            DEPLOY_COMPONENTS='application', BACKEND_IMAGE_REF='', FRONTEND_IMAGE_REF='',
            BACKEND_IMAGE_REVISION=previous['revision'], FRONTEND_IMAGE_REVISION=previous['revision'])
    check('configuration_only', previous)
    distinct = previous != current
    if distinct:
        print('Upgrading to the second verified release', flush=True)
        deploy(current, 'backend,frontend')
        check('current', current)
    print('Restoring the previous verified image pair', flush=True)
    command('scripts/cd/rollback.sh', previous['backend'], previous['frontend'],
            BATTLEFIELD_ACCESS_COMPATIBLE_BACKEND_DIGEST=previous['backend'])
    check('restored', previous)
    report.update(retained_claims=len(report['checkpoints']['restored']['claims']), private_readiness=True,
                  authenticated_public_smoke=False, cli_vm_bridge=False, database_bound_rollback=True,
                  distinct_release_rollback=distinct)


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
    for name in ('backend', 'frontend', 'revision'):
        parser.add_argument('--previous-' + name)
    args = parser.parse_args()
    previous, current = releases(args)
    for release in (previous, current):
        base.run('git', '-C', str(ROOT), 'cat-file', '-e', release['revision'] + '^{commit}')
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
                def probe():
                    for service, port, path, status in [
                            ('battlefield-api', 8080, '/readyz', 200),
                            ('battlefield-api', 8080, '/v1/scenarios', 401),
                            ('battlefield-frontend', 8080, '/livez', 200),
                            ('battlefield-mcp', 8084, '/healthz', 204)]:
                        private_probe(env, work, service, port, path, status)
                exercise_delivery(command, lambda: inspect_delivery(env, args.evidence_dir), probe, previous, current, report)

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
                report['passed'] = report.get('database_bound_rollback', False)
            finally:
                (args.evidence_dir/'report.json').write_text(json.dumps(report,indent=2)+'\n')
    print('Private delivery rehearsal passed; public cutover acceptance is separate',flush=True)


if __name__=='__main__':
    try:
        main()
    except (ValueError, OSError, KeyError, StopIteration, subprocess.SubprocessError):
        raise SystemExit('Delivery rehearsal failed; inspect private evidence. No credentials were published.') from None
