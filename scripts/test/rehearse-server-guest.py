#!/usr/bin/env python3
"""Destructive test fixture: refuses every machine except its marked disposable VM."""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import pwd
import re
import secrets
import signal
import subprocess
import sys
import uuid

ROOT = Path(__file__).resolve().parents[2]
PRIVATE = Path.home() / 'fixtures'
EVIDENCE = Path.home() / 'evidence'


def run(*args, **kwargs):
    return subprocess.run(args, check=True, capture_output=True, text=True,
                          timeout=kwargs.pop('timeout', 120), **kwargs).stdout.strip()


def require_guest(identity):
    canonical = str(uuid.UUID(identity))
    marker = Path('/etc/battlefield-rehearsal')
    if marker.is_symlink() or marker.stat().st_uid != 0 or marker.stat().st_mode & 0o022:
        raise ValueError('Unsafe rehearsal marker')
    if marker.read_text().strip() != canonical:
        raise ValueError('Not the registered disposable guest')
    actual = run('sudo', '-n', 'cat', '/sys/class/dmi/id/product_uuid').lower()
    if actual != canonical or pwd.getpwuid(os.getuid()).pw_name != 'rehearsal':
        raise ValueError('VM hardware identity or operator differs from the rehearsal')


def assert_recap(output, changed=0):
    recaps = re.findall(r'^server\s*:\s*ok=(\d+)\s+changed=(\d+)\s+unreachable=(\d+)\s+failed=(\d+)', output, re.M)
    if len(recaps) != 1 or any(int(value) for value in recaps[0][2:]):
        raise ValueError('Missing successful Ansible recap')
    if changed is not None and int(recaps[0][1]) != changed:
        raise ValueError('Unexpected Ansible changes: ' + recaps[0][1])
    return int(recaps[0][1])


def reject_secrets(output):
    # Include base64 strings: no_log must hide loop values as well as plaintext.
    for path in (PRIVATE / 'secret', PRIVATE / 'password'):
        value = path.read_text().strip()
        if value in output or base64.b64encode(path.read_bytes()).decode() in output:
            raise ValueError('Synthetic secret appeared in captured output; refusing to archive it')


def bootstrap(action, label, interrupt=False, changed=None):
    command = [str(ROOT / 'scripts/infra/server.sh'), action, '--local',
               '--inventory', str(PRIVATE / 'inventory.json')]
    if action in ('plan', 'apply', 'seal'):
        command += ['--vault', str(PRIVATE / 'inputs.vault'),
                    '--vault-password-file', str(PRIVATE / 'password')]
    with subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                          text=True, start_new_session=True) as process:
        lines, interrupted = [], False
        for line in process.stdout:
            lines.append(line)
            if interrupt and not interrupted and line.startswith('TASK [inputs : Create private input directory]'):
                # The previous ownership-marker task completed. SIGINT unwinds the wrapper,
                # terminates its Ansible process group and releases the host lock.
                process.send_signal(signal.SIGINT)
                interrupted = True
        code = process.wait(timeout=30)
    output = ''.join(lines)
    reject_secrets(output)
    (EVIDENCE / (label + '.log')).write_text(output)
    if interrupt:
        if not interrupted or code == 0:
            raise ValueError('Did not interrupt the real initial apply at the intended boundary')
        if run('sudo', '-n', 'cat', '/etc/battlefield-bootstrap/machine-id') != Path('/etc/machine-id').read_text().strip():
            raise ValueError('Interrupted installation did not persist its ownership marker')
    elif code:
        raise ValueError('Bootstrap failed; see ' + label + '.log')
    elif action in ('apply', 'plan', 'verify'):
        assert_recap(output, changed)
    print(label + ' passed', flush=True)


def snapshot():
    code = """import hashlib,json,pathlib,stat,subprocess
root=pathlib.Path('/etc/battlefield-bootstrap')
paths=[*sorted(root.rglob('*')), pathlib.Path('/var/lib/battlefield-platform/bootstrap-inputs.vault'),
 pathlib.Path('/etc/sysctl.d/90-battlefield-bootstrap.conf'),pathlib.Path('/etc/bf-unmanaged-rehearsal')]
files={str(p):{'sha256':hashlib.sha256(p.read_bytes()).hexdigest(),'mode':stat.S_IMODE(p.stat().st_mode),
 'uid':p.stat().st_uid,'gid':p.stat().st_gid} for p in paths if p.is_file()}
services={name:subprocess.check_output(['systemctl','show',name,'--property=InvocationID','--value'],text=True).strip()
 for name in ('docker','libvirtd')}
print(json.dumps({'files':files,'services':services,'boot_id':pathlib.Path('/proc/sys/kernel/random/boot_id').read_text().strip(),
 'mmap':int(subprocess.check_output(['sysctl','-n','vm.max_map_count'],text=True))}))
"""
    return json.loads(run('sudo', '-n', 'python3', '-c', code))


def same_state(before, after, reboot=False):
    if before['files'] != after['files'] or before['mmap'] != after['mmap']:
        raise ValueError('Managed credentials/configuration or unrelated sentinel changed')
    if not reboot and before['services'] != after['services']:
        raise ValueError('A healthy service was restarted')
    if reboot and before['boot_id'] == after['boot_id']:
        raise ValueError('The guest did not actually reboot')


def before_reboot():
    PRIVATE.mkdir(mode=0o700)
    EVIDENCE.mkdir(mode=0o700)
    (PRIVATE / 'secret').write_text('synthetic-' + secrets.token_hex(32) + '\n')
    (PRIVATE / 'password').write_text(secrets.token_hex(32) + '\n')
    run(str(ROOT / 'scripts/infra/server.sh'), 'init', '--local', '--host', '127.0.0.1',
        '--user', 'rehearsal', '--identity-file', '/dev/null', '--inventory', str(PRIVATE / 'inventory.json'))
    (PRIVATE / 'inputs.json').write_text(json.dumps({'rehearsal_secret': {'source': 'controller', 'path': str(PRIVATE / 'secret')}}))
    run('sudo', '-n', 'python3', '-c', "from pathlib import Path; Path('/etc/bf-unmanaged-rehearsal').write_text('preserve this unrelated configuration\\n')")
    bootstrap('seal', 'seal')
    bootstrap('apply', 'interrupted-apply', interrupt=True)
    bootstrap('apply', 'resumed-apply')
    bootstrap('verify', 'first-verify', changed=0)
    first = snapshot()
    secret_digest = hashlib.sha256((PRIVATE / 'secret').read_bytes()).hexdigest()
    if first['files']['/etc/battlefield-bootstrap/inputs/rehearsal_secret']['sha256'] != secret_digest:
        raise ValueError('Imported credential bytes differ from the original')
    if first['files']['/var/lib/battlefield-platform/bootstrap-inputs.vault']['sha256'] != hashlib.sha256((PRIVATE / 'inputs.vault').read_bytes()).hexdigest():
        raise ValueError('Recovery bundle differs from the original ciphertext')
    bootstrap('apply', 'repeat-apply', changed=0)
    same_state(first, snapshot())
    bootstrap('plan', 'check-diff', changed=0)
    same_state(first, snapshot())
    run('sudo', '-n', 'sysctl', '-w', 'vm.max_map_count=262144')
    run('sudo', '-n', 'chmod', '0644', '/etc/battlefield-bootstrap/inputs/rehearsal_secret')
    bootstrap('apply', 'repair-drift')
    same_state(first, snapshot())
    bootstrap('verify', 'drift-verify', changed=0)
    (EVIDENCE / 'before-reboot.json').write_text(json.dumps(first, indent=2) + '\n')


def after_reboot():
    before = json.loads((EVIDENCE / 'before-reboot.json').read_text())
    bootstrap('verify', 'reboot-verify', changed=0)
    after = snapshot()
    same_state(before, after, reboot=True)
    bootstrap('apply', 'reboot-apply', changed=0)
    same_state(after, snapshot())
    (EVIDENCE / 'result.json').write_text(json.dumps({
        'passed': True, 'os': Path('/etc/os-release').read_text(),
        'before_reboot': before, 'after_reboot': after,
        'checks': ['interrupted_initial_apply', 'resume', 'verify', 'repeat_zero_changes',
                   'no_service_restart', 'check_diff_no_changes', 'secret_safe_logs',
                   'credential_fingerprints', 'unchanged_encrypted_recovery_copy',
                   'drift_repair', 'unrelated_configuration_preserved', 'reboot', 'reboot_apply_zero_changes'],
    }, indent=2) + '\n')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('phase', choices=['before-reboot', 'after-reboot'])
    parser.add_argument('--identity', required=True)
    args = parser.parse_args()
    os.umask(0o077)
    require_guest(args.identity)
    if args.phase == 'before-reboot':
        before_reboot()
    else:
        after_reboot()


if __name__ == '__main__':
    main()
