#!/usr/bin/env python3
"""Exercise native HTTPS capture/restore in a disposable Ubuntu VM."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import shlex
import shutil
import sys
import tarfile
import tempfile

ROOT=Path(__file__).resolve().parents[2]
sys.path=[p for p in sys.path if Path(p).resolve()!=Path(__file__).resolve().parent]
spec=importlib.util.spec_from_file_location('https_rehearsal',ROOT/'scripts/infra/rehearse-server.py')
base=importlib.util.module_from_spec(spec);spec.loader.exec_module(base)


def inventory():
    return {kind:{name:base.virsh(command,name) for name in base.virsh(listing,'--all','--name').splitlines() if name}
            for kind,command,listing in [('domains','domuuid','list'),('networks','net-uuid','net-list'),('pools','pool-uuid','pool-list')]}


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image',type=Path,required=True)
    parser.add_argument('--evidence-dir',type=Path,required=True)
    args=parser.parse_args();os.umask(0o077)
    if args.evidence_dir.resolve().is_relative_to(ROOT):raise ValueError('Evidence must be outside the checkout')
    memory=dict(line.split(':',1) for line in Path('/proc/meminfo').read_text().splitlines())
    if int(memory['MemAvailable'].split()[0])<5*1024*1024:raise ValueError('Need 5 GiB available RAM for the disposable HTTPS host and headroom')
    if shutil.disk_usage('/var/lib/libvirt/images').free<20*1024**3:raise ValueError('Need 20 GiB free for disposable HTTPS verification')
    pin=json.loads((ROOT/'infra/ansible/rehearsal-image.json').read_text())['26.04']
    base.checked_image(args.image,pin['sha256'])
    before=inventory();args.evidence_dir.mkdir(mode=0o700,parents=True)
    result={'passed':False,'source_sha256':{}}
    with tempfile.TemporaryDirectory(prefix='bf-https-rehearsal-') as directory:
        work=Path(directory);guest=base.Rehearsal(work,args.evidence_dir,'default',memory_mib=3072,root_gib=16,cpus=2)
        try:
            print('Preparing disposable HTTPS host',flush=True);guest.prepare(args.image)
            with tarfile.open(work/'source.tar','w') as archive:
                for name in ('infra/ansible','scripts/infra','scripts/test/rehearse-https-guest.py',
                             'docs/operations/local-https-platform.md'):
                    archive.add(ROOT/name,arcname=name,filter=lambda item:None if '__pycache__' in item.name else item)
                    path=ROOT/name
                    for source in ([path] if path.is_file() else path.rglob('*')):
                        if source.is_file() and '__pycache__' not in str(source):result['source_sha256'][str(source.relative_to(ROOT))]=base.digest(source)
            guest.ssh('mkdir -m 700 source && tar -xf - -C source',input=(work/'source.tar').read_bytes(),text=False)
            tools=guest.ssh('./source/scripts/infra/server.sh tools',timeout=600).splitlines()[-1]
            for phase in ('before-reboot','after-reboot'):
                if phase=='after-reboot':guest.reboot()
                print('Running real Ansible HTTPS '+phase+' acceptance',flush=True)
                try:
                    output=guest.ssh('sudo python3 source/scripts/test/rehearse-https-guest.py '+phase+' --identity '+guest.identity+' --ansible '+shlex.quote(tools+'/ansible-playbook'),timeout=900)
                    (args.evidence_dir/(phase+'.log')).write_text(output)
                except Exception as error:
                    (args.evidence_dir/(phase+'.log')).write_text(str(getattr(error,'stdout','') or '')+str(getattr(error,'stderr','') or ''))
                    raise
            result['guest']=json.loads(guest.ssh('cat evidence/result.json'));result['passed']=result['guest']['passed'] is True
        finally:
            if guest.ip:
                try:(args.evidence_dir/'guest-evidence.tar.gz').write_bytes(guest.ssh('sudo tar -czf - -C /home/rehearsal/evidence .',text=False))
                except Exception:result['evidence_capture_failed']=True
            guest.cleanup();result['existing_resources_preserved']=inventory()==before;result['cleaned_up']=True
            (args.evidence_dir/'result.json').write_text(json.dumps(result,indent=2)+'\n')
            if not result['existing_resources_preserved']:raise ValueError('Existing hypervisor resource inventory changed')
    print('PASS: HTTPS restore/adoption/reboot; disposable resources cleaned',flush=True)


if __name__=='__main__':main()
