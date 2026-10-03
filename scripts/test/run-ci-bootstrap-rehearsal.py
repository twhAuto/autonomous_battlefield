#!/usr/bin/env python3
"""Run CI Ansible lifecycle checks in a disposable libvirt Ubuntu VM."""
import argparse
import importlib.util
import json
import subprocess
from pathlib import Path
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('rehearsal', ROOT/'scripts/infra/rehearse-server.py')
rehearsal = importlib.util.module_from_spec(spec)
spec.loader.exec_module(rehearsal)


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--evidence-dir',type=Path,required=True)
    parser.add_argument('--ubuntu',choices=['24.04','26.04'],default='26.04')
    args=parser.parse_args()
    args.evidence_dir.mkdir(mode=0o700,parents=True,exist_ok=False)
    pin=json.loads((ROOT/'infra/ansible/rehearsal-image.json').read_text())[args.ubuntu]
    image=Path.home()/'.cache/autonomous-battlefield/rehearsal'/(pin['sha256']+'.img')
    rehearsal.checked_image(image,pin['sha256'])
    with tempfile.TemporaryDirectory(prefix='bf-ci-rehearsal-') as directory:
        work=Path(directory)
        vm=rehearsal.Rehearsal(work,args.evidence_dir,'default',memory_mib=8192,root_gib=40,cpus=4)
        metadata={'image':pin,'source_sha256':{},'synthetic_credentials':True}
        with tarfile.open(work/'source.tar','w') as archive:
            for name in ('infra/ansible','infra/jenkins','infra/jenkins/Jenkinsfile.platform','infra/jenkins/Jenkinsfile.vm','infra/jenkins/Jenkinsfile.k3s','scripts/infra','scripts/ops','scripts/test/rehearse-ci-bootstrap.py','scripts/test/rehearse-server-guest.py'):
                archive.add(ROOT/name,arcname=name,filter=lambda item: None if '__pycache__' in item.name else item)
                path=ROOT/name
                for source in ([path] if path.is_file() else path.rglob('*')):
                    if source.is_file() and '__pycache__' not in str(source):
                        metadata['source_sha256'][str(source.relative_to(ROOT))]=rehearsal.digest(source)
        try:
            print('Creating disposable CI test VM',flush=True); vm.prepare(image)
            vm.ssh('mkdir -m 700 source && tar -xf - -C source',input=(work/'source.tar').read_bytes(),text=False)
            for phase in ('prepare','exercise','adoption'):
                print('Running CI fixture '+phase,flush=True)
                output=vm.ssh('python3 source/scripts/test/rehearse-ci-bootstrap.py '+phase+' --identity '+vm.identity,timeout=3000)
                (args.evidence_dir/(phase+'.log')).write_text(output)
            before_evidence=vm.ssh('tar -czf - -C evidence .',text=False)
            (args.evidence_dir/'guest-evidence.tar.gz').write_bytes(before_evidence)
            print('Rebooting only the disposable CI test VM',flush=True); vm.reboot()
            output=vm.ssh('python3 source/scripts/test/rehearse-ci-bootstrap.py after-reboot --identity '+vm.identity,timeout=900)
            (args.evidence_dir/'after-reboot.log').write_text(output)
            metadata['passed']=True
        except subprocess.CalledProcessError as error:
            (args.evidence_dir/'phase-failure.log').write_text((error.stdout or '') + (error.stderr or ''))
            raise ValueError('CI rehearsal failed; inspect its private phase-failure.log and guest evidence') from None
        finally:
            if vm.ip:
                try:
                    evidence=vm.ssh('tar -czf - -C evidence .',text=False)
                    (args.evidence_dir/'guest-evidence.tar.gz').write_bytes(evidence)
                except Exception:
                    metadata['evidence_capture_failed']=True
            print('Cleaning up this rehearsal VM and pool',flush=True)
            vm.cleanup()
            (args.evidence_dir/'result.json').write_text(json.dumps(metadata,indent=2)+'\n')
    print('PASS: CI bootstrap rehearsal and cleanup',flush=True)


if __name__=='__main__':
    main()
