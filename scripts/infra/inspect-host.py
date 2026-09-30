#!/usr/bin/env python3
"""Read-only host metadata. No environment dumps, credentials, mounts or sudo."""
import json
import os
from pathlib import Path
import platform
import pwd
import shutil
import subprocess


def command(argv):
    if not shutil.which(argv[0]):
        return {"available": False}
    try:
        result = subprocess.run(argv, capture_output=True, text=True, timeout=15, check=False)
        # Errors may contain local configuration. Report only exit status.
        return {"available": True, "rc": result.returncode, "output": result.stdout[:20000]}
    except subprocess.TimeoutExpired:
        return {"available": True, "error": "timeout"}


def sudo_executable():
    # The pinned Ansible sudo plugin needs the traditional password prompt.
    # Ubuntu 26.04 can route /usr/bin/sudo to sudo-rs; keep that system choice
    # and select the already-installed compatibility binary for Ansible only.
    for executable in ('/usr/bin/sudo', '/usr/bin/sudo.ws'):
        version = command([executable, '--version'])
        if version.get('rc') == 0 and version.get('output', '').startswith('Sudo version '):
            return executable
    return ''


def inspect():
    release = platform.freedesktop_os_release()
    memory = dict(line.split(':', 1) for line in Path('/proc/meminfo').read_text().splitlines())
    account = pwd.getpwuid(os.getuid())
    sudo = sudo_executable()
    production = Path(account.pw_dir) / '.config/autonomous-battlefield/production'
    inputs = {name: str(production / filename) for name, filename in {
        'application_env': '.env', 'tunnel_credentials': 'tunnel-credentials.json',
        'kubeconfig': 'kubeconfig', 'kubelet_ca': 'kubelet-ca.crt',
        'deployment_profile': 'deployment.env', 'recovery_metadata': 'recovery.json',
    }.items() if (production / filename).is_file()}
    return {
        'input_files': inputs,
        "machine_id": Path('/etc/machine-id').read_text().strip(),
        "hostname": platform.node(), "os": release.get('ID'), "version": release.get('VERSION_ID'),
        "architecture": platform.machine(), "python": platform.python_version(),
        "user": account.pw_name, "home": account.pw_dir,
        "logical_cpus": os.cpu_count(), "memory_total": memory['MemTotal'].strip(),
        "memory_available": memory['MemAvailable'].strip(), "root_free_bytes": shutil.disk_usage('/').free,
        "kvm": Path('/dev/kvm').exists(),
        "disks": command(['lsblk', '--json', '-o', 'NAME,TYPE,SIZE,FSTYPE,UUID,MOUNTPOINTS']),
        "mounts": command(['findmnt', '--json', '-o', 'TARGET,SOURCE,FSTYPE,UUID']),
        "addresses": command(['ip', '-json', 'address', 'show']),
        "domains": command(['virsh', '--connect', 'qemu:///system', 'list', '--all']),
        "networks": command(['virsh', '--connect', 'qemu:///system', 'net-list', '--all']),
        "services": command(['systemctl', 'show', 'docker', 'libvirtd', 'jenkins',
                             'battlefield-mcp-bridge', 'battlefield-agent-firewall',
                             '--property=Id,LoadState,ActiveState,UnitFileState']),
        "sudo_executable": sudo,
        "sudo_noninteractive": bool(sudo) and command([sudo, '-n', 'true']).get('rc') == 0,
    }


if __name__ == '__main__':
    print(json.dumps(inspect(), indent=2))
