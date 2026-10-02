#!/usr/bin/env bash
# Creates one dedicated VM from a locally verified Ubuntu cloud image. Run on the server.
set -Eeuo pipefail
if (( EUID != 0 )); then echo 'Run on the Ubuntu server as root after reviewing the generated VM configuration.' >&2; exit 1; fi
: "${BATTLEFIELD_VM_IMAGE:?Path to a verified Ubuntu 24.04 cloud image}"
: "${BATTLEFIELD_VM_IMAGE_SHA256:?Expected publisher-verified SHA256}"
: "${BATTLEFIELD_VM_ADMIN_KEY:?Path to the operator SSH public key}"
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
for cmd in virsh virt-host-validate qemu-img cloud-localds python3; do command -v "${cmd}" >/dev/null; done
[[ -c /dev/kvm ]] || { echo 'KVM unavailable; leave CLI disabled.' >&2; exit 1; }
virt-host-validate qemu
python3 - "${BATTLEFIELD_VM_IMAGE}" "${BATTLEFIELD_VM_IMAGE_SHA256}" <<'PY'
import hashlib, pathlib, sys
p=pathlib.Path(sys.argv[1]); h=hashlib.sha256()
with p.open('rb') as f:
    for block in iter(lambda:f.read(1<<20),b''): h.update(block)
if h.hexdigest()!=sys.argv[2].lower(): raise SystemExit('Ubuntu image checksum mismatch')
mem=int(next(l.split()[1] for l in pathlib.Path('/proc/meminfo').read_text().splitlines() if l.startswith('MemAvailable:')))
if mem<6*1024*1024: raise SystemExit('Require at least 6 GiB available RAM before reserving 4 GiB; leave CLI disabled')
PY
if virsh --connect qemu:///system dominfo battlefield-agent >/dev/null 2>&1; then echo 'VM already exists; refusing to overwrite it.' >&2; exit 1; fi
if virsh --connect qemu:///system net-info battlefield-agent >/dev/null 2>&1; then echo 'Network already exists; inspect it and provision manually.' >&2; exit 1; fi
vm_dir=/var/lib/libvirt/images/battlefield-agent
[[ ! -e "${vm_dir}" ]] || { echo 'VM directory already exists; refusing to overwrite it.' >&2; exit 1; }
python3 - <<'PY'
import shutil
if shutil.disk_usage('/var/lib/libvirt/images').free<30*1024**3: raise SystemExit('Require 30 GiB free disk; leave CLI disabled')
PY
install -d -m 0750 "${vm_dir}"
qemu-img convert -O qcow2 "${BATTLEFIELD_VM_IMAGE}" "${vm_dir}/root.qcow2"
qemu-img resize "${vm_dir}/root.qcow2" 24G
python3 - "${BATTLEFIELD_VM_ADMIN_KEY}" "${vm_dir}" <<'PY'
import json,pathlib,sys
key=pathlib.Path(sys.argv[1]).read_text().strip()
if not key.startswith(('ssh-ed25519 ','ssh-rsa ','ecdsa-sha2-')) or '\n' in key: raise SystemExit('Expected a single SSH public key')
out=pathlib.Path(sys.argv[2])
user={'users':[{'name':'vm-admin','lock_passwd':True,'shell':'/bin/bash','sudo':['ALL=(ALL) NOPASSWD:ALL'],'ssh_authorized_keys':[key]},
 {'name':'battlefield-worker','lock_passwd':True,'shell':'/bin/bash','homedir':'/var/lib/battlefield-worker'}],
 'disable_root':True,'ssh_pwauth':False,'package_update':False,'package_upgrade':False}
(out/'user-data').write_text('#cloud-config\n'+json.dumps(user))
(out/'meta-data').write_text('instance-id: battlefield-agent\nlocal-hostname: battlefield-agent\n')
(out/'network-config').write_text(json.dumps({'version':2,'ethernets':{'worker':{'match':{'macaddress':'52:54:00:ba:77:01'},'set-name':'eth0','dhcp4':False,'dhcp6':False,'accept-ra':False,'addresses':['192.168.234.2/24']}}}))
PY
cloud-localds --network-config="${vm_dir}/network-config" "${vm_dir}/seed.iso" "${vm_dir}/user-data" "${vm_dir}/meta-data"
cp -- "${project_dir}/deploy/agent-vm/domain.xml" "${vm_dir}/domain.xml"
chown -R libvirt-qemu:kvm "${vm_dir}"
virsh --connect qemu:///system nwfilter-define "${project_dir}/deploy/agent-vm/filter.xml"
virsh --connect qemu:///system net-define "${project_dir}/deploy/agent-vm/network.xml"
virsh --connect qemu:///system net-start battlefield-agent
virsh --connect qemu:///system net-autostart battlefield-agent
virsh --connect qemu:///system define "${vm_dir}/domain.xml"
virsh --connect qemu:///system start battlefield-agent
echo 'VM created. CLI remains disabled. Install the provider proxy, private MCP bridge, pinned worker/CLI binaries, then verify isolation using the launch runbook.'
