#!/usr/bin/env python3
"""One-time conversion to .env/deployment.env; never touches databases or workloads."""
import argparse
import json
import os
from pathlib import Path
import shlex
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]
HELPER = ROOT/'scripts/lib/application-env.cjs'

def read(file):
    result = subprocess.run(['node',str(HELPER),'json',str(file)],capture_output=True,text=True)
    if result.returncode: raise ValueError('Invalid application input')
    return json.loads(result.stdout)

def convert(directory, environment, apply=False):
    old = directory/'environment.env'
    new = directory/'deployment.env'
    app = ROOT/'.env' if environment == 'dev' else directory/'.env'
    profile = {}
    # Decode the old shell-quoted format as data; never execute it.
    for line in old.read_text().splitlines():
        if not line.strip() or line.lstrip().startswith('#'): continue
        tokens = shlex.split(line, comments=True)
        if len(tokens) != 1 or '=' not in tokens[0]: raise ValueError('Invalid old profile')
        key,value=tokens[0].split('=',1)
        if key in profile: raise ValueError('Duplicate profile setting')
        profile[key]=value
    if profile.get('BF_ENV') != environment: raise ValueError('Environment mismatch')
    values=read(app) if app.exists() else {}
    # An existing dev .env is already authoritative; retired files may be stale.
    dev_is_consolidated=environment == 'dev' and app.exists()
    retired=[]
    for key in ('BF_DATABASE_FILE','BF_AUTH_FILE','BF_AI_FILE'):
        source=Path(profile.pop(key))
        if not source.is_absolute(): source=directory/source
        if source.exists():
            for name,value in ([] if dev_is_consolidated else read(source).items()):
                if name in values and values[name] != value: raise ValueError('Conflicting application setting: '+name)
                values[name]=value
            retired.append(source)
    if not values.get('BATTLEFIELD_AUTH_MODE'):
        values['BATTLEFIELD_AUTH_MODE']='local-password' if values.get('BATTLEFIELD_AUTH_ENABLED') == 'true' else 'disabled-development'
    kube_source=Path(profile['BF_KUBECONFIG'])
    if not kube_source.is_absolute(): kube_source=directory/kube_source
    kube_data=kube_source.read_bytes()
    kube_target=directory/'kubeconfig'
    if kube_target.exists() and kube_target.read_bytes()!=kube_data: raise ValueError('Kubeconfig destination differs')
    profile['BF_KUBECONFIG']='kubeconfig'
    # Recover settings from the application's old generated launch resources.
    generated=[]
    if environment == 'production':
        candidates={p.parent/'tunnel.json' for p in retired}
        candidates.update(directory/name/'tunnel.json' for name in ('limited-launch','friends-launch'))
        for file in sorted(candidates):
            if not file.exists(): continue
            resources=json.loads(file.read_text())['items']
            cm=next(r for r in resources if r.get('kind')=='ConfigMap' and r.get('metadata',{}).get('name')=='battlefield-tunnel')
            deployment=next(r for r in resources if r.get('kind')=='Deployment' and r.get('metadata',{}).get('name')=='battlefield-tunnel')
            config=json.loads(cm['data']['config.yml'])
            settings={'BF_TUNNEL_ID':config['tunnel'],'BF_TUNNEL_IMAGE':deployment['spec']['template']['spec']['containers'][0]['image']}
            for key,value in settings.items():
                if key in profile and profile[key]!=value: raise ValueError('Conflicting tunnel setting')
                profile[key]=value
            generated.append(file)
            auth=file.parent/'auth.env'
            if auth.exists():
                if any(values.get(k)!=v for k,v in read(auth).items()): raise ValueError('Generated auth settings conflict')
                generated.append(auth)
    tunnel_source=None
    tunnel_data=None
    if profile.get('BF_TUNNEL_ID'):
        tunnel_source=Path(profile.get('BF_TUNNEL_CREDENTIALS_FILE',str(Path.home()/'.cloudflared'/(profile['BF_TUNNEL_ID']+'.json'))))
        if not tunnel_source.is_absolute(): tunnel_source=directory/tunnel_source
        tunnel_data=tunnel_source.read_bytes()
        credentials=json.loads(tunnel_data)
        if credentials.get('TunnelID')!=profile['BF_TUNNEL_ID'] or not credentials.get('TunnelSecret') or not credentials.get('AccountTag'): raise ValueError('Tunnel credential mismatch')
        target=directory/'tunnel-credentials.json'
        if target.exists() and target.read_bytes()!=tunnel_data: raise ValueError('Tunnel destination differs')
        profile['BF_TUNNEL_CREDENTIALS_FILE']='tunnel-credentials.json'
    with tempfile.TemporaryDirectory(prefix='battlefield-config-') as tmp:
        staged=Path(tmp)/'.env'; staged_profile=Path(tmp)/'deployment.env'
        staged.write_text(''.join(f'{k}={v}\n' for k,v in values.items()))
        staged_profile.write_text(''.join(f'{k}={v}\n' for k,v in profile.items()))
        for command,file,extra in [('validate',staged,[environment]),('profile',staged_profile,[])]:
            result=subprocess.run(['node',str(HELPER),command,str(file),*extra],capture_output=True)
            if result.returncode: raise ValueError('Consolidated configuration failed validation')
        if new.exists() and new.read_bytes()!=staged_profile.read_bytes(): raise ValueError('Deployment destination differs')
        if not apply:
            print('Configuration validated; --apply writes final files and removes superseded inputs.')
            return
        os.umask(0o077)
        # All data is validated before replacing files. No backup or fallback files.
        directory.chmod(0o700)
        kube_target.write_bytes(kube_data); kube_target.chmod(0o600)
        if tunnel_source:
            target=directory/'tunnel-credentials.json'
            target.write_bytes(tunnel_data); target.chmod(0o600)
        app.write_bytes(staged.read_bytes()); app.chmod(0o600)
        new.write_bytes(staged_profile.read_bytes()); new.chmod(0o600)
        for source in set(retired+generated):
            if source.resolve()!=app.resolve(): source.unlink()
        old.unlink()
        if tunnel_source and tunnel_source.resolve()!=(directory/'tunnel-credentials.json').resolve(): tunnel_source.unlink()
        for parent in {p.parent for p in generated}:
            if parent.parent==directory and not any(parent.iterdir()): parent.rmdir()
    print('Consolidated configuration; removed the old profile and split application files. No backups created.')

if __name__ == '__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('environment',choices=['dev','production'])
    parser.add_argument('--directory',type=Path)
    parser.add_argument('--apply',action='store_true')
    args=parser.parse_args()
    try: convert(args.directory or Path.home()/'.config/autonomous-battlefield'/args.environment,args.environment,args.apply)
    except ValueError as error: raise SystemExit('Consolidation failed: '+str(error))
    except (OSError,KeyError,StopIteration): raise SystemExit('Consolidation failed: check required source files. No secret values printed.')
