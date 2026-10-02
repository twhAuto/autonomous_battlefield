# CLI VM provisioning and adoption

AB-140 adds a reviewed Ansible/libvirt workflow for the existing `battlefield-agent`
layout. It preserves the isolated `192.168.234.0/24` network, `bf-agent0` bridge,
NIC filter, 2 vCPU / 4 GiB allocation and standalone 24 GiB root disk at
`/var/lib/libvirt/images/battlefield-agent/root.qcow2`. Existing provider login
homes stay inside that disk. It does not move storage to the data partition.

Terraform continues to own the K3s VM. The CLI VM has unpooled file-backed disks,
and the [pinned libvirt 0.9.9 provider](https://github.com/dmacvicar/terraform-provider-libvirt/tree/v0.9.9/docs/resources)
has no network-filter resource.
Ansible therefore owns this VM's create-once/adoption boundary directly. Neither
application delivery nor ordinary host bootstrap invokes this workflow. Worker
release updates, subscription login and Player enablement remain separate.

## Prerequisites

Run on the registered Ubuntu physical host as its operator, from a clean checkout
of the reviewed `origin/main` revision. Jenkins is not needed. The existing
[host bootstrap](server-bootstrap.md) supplies libvirt, QEMU, cloud image tools
and the host inventory. KVM must be enabled in firmware. Fresh creation requires
6 GiB available RAM and 30 GiB free on the existing root filesystem; the capacity
check budgets for the disk's full virtual size rather than its initial sparse
allocation. It does not reserve blocks or impose a filesystem quota. The command prompts for sudo when needed and uses the existing
Ubuntu-compatible sudo selection.

Adoption additionally requires the host-local AB-125 `server/cli.json` registration,
including its captured guest identity, existing admin key and already trusted SSH
host key. It does not bootstrap trust with `ssh-keyscan`. The VM/network must
already be running, persistent and configured for autostart. If the existing
admin key is encrypted, unlock it in the **host's** SSH agent before apply/verify:

```bash
eval "$(ssh-agent -s)"
ssh-add -t 1h "$HOME/.ssh/battlefield-vm-admin"
```

Do not copy a private key into a checkout, WSL or a Jenkins credential for this
workflow. A fresh VM receives a newly generated host-local administration key.

## Plan, review, apply and verify

From the host checkout, choose fresh report names outside Git:

```bash
umask 077
private_dir="$HOME/.config/autonomous-battlefield/server"
revision="$(git rev-parse origin/main)"
stamp="$(date +%Y%m%dT%H%M%S)"
python3 scripts/infra/cli-vm.py plan --mode adopt --revision "$revision" \
  --report "$private_dir/cli-vm-$stamp-plan.json"
```

Review the printed JSON and saved report. On first adoption, expect only ownership
registration and trusted guest-connection export. `vm_replacement` must be false;
the recorded domain/network/filter UUIDs and root-disk path must match the existing
VM. The plan also shows `storage_modes` for the retained disk and seed; adoption
preserves those permissions, including any broader legacy modes. Fresh files use
0600. Tightening legacy permissions is a separate reviewed maintenance change.
No disk, network definition, service or login home changes during adoption.
A plan writes only a root-owned review receipt and its operator report.

After reviewing, apply that exact plan within 30 minutes:

```bash
python3 scripts/infra/cli-vm.py apply --mode adopt --revision "$revision" \
  --review "$private_dir/cli-vm-$stamp-plan.json" \
  --report "$private_dir/cli-vm-$stamp-apply.json"
python3 scripts/infra/cli-vm.py verify --mode adopt --revision "$revision" \
  --report "$private_dir/cli-vm-$stamp-verify.json"
```

Apply re-inspects identities, XML, retained-file inodes, guest trust and inputs
under the same host operation lock as platform/deployment commands. It rejects
stale, changed or consumed plans before mutation. A new plan supersedes the
previous receipt. An unchanged repeat plan reports `changes: []`; repeat apply
never redefines, resizes, replaces or restarts an existing VM. Apply reports persist `changed` and `applied_changes` separately from the
remaining `changes`, so fresh creation and metadata adoption remain distinguishable
from a no-op repeat. A stopped or inconsistent VM requires explicit operator
investigation.

For a **clean replacement host only**, use `--mode create` for all three commands.
Creation refuses any existing matching domain, network, filter, disk directory or
connection state. It downloads the checksum-pinned Ubuntu 24.04 image declared in
`infra/ansible/rehearsal-image.json`, makes a standalone qcow2 disk, creates the
same isolated network/filter and enables VM/network autostart. The domain explicitly
selects the `pc` machine family; unexpected device normalization is rejected. It installs no
worker binary or provider login and makes no outbound guest package downloads.
Both downloaded and cached image inputs are capped at 4 GiB before conversion.
Changing the image pin never upgrades an existing disk.

Root-owned state lives in `/var/lib/battlefield-cli-vm/`. The receipt binds the
source revision, source/input fingerprints and observed resources. Command
failures retain root-only diagnostics in `last-error.log` inside that directory. Ownership binds
the host/operator, domain/network/filter UUIDs, persistent XML, retained disk/seed
file identities and guest SSH trust. The running disk is **not** hashed or copied;
normal guest writes must remain possible. Verification reads guest machine/DMI
identity over strict SSH and does not invoke a model or inspect login tokens.

## Automatic Ansible handoff

Successful apply/verify exports these private, operator-owned host-local files:

- `~/.local/state/battlefield/cli-vm/handoff.json`: verified host/guest identities,
  address and existing/generated admin-key path; no private key contents.
- `~/.local/state/battlefield/cli-vm/inventory.json`: ready-to-use Ansible inventory.
- `~/.local/state/battlefield/cli-vm/known_hosts`: the trusted guest public keys.

The [CLI service bootstrap](cli-bootstrap.md) automatically consumes the handoff
when run with `--local`. Existing registrations must match it. For a fresh service
registration, leave `guest.machine_id` and `guest.identity_file` empty; the handoff
supplies them in memory. Continue supplying the pinned worker/CLI artifacts,
disabled profile configuration and encrypted dedicated worker environment as
that guide requires. `--cli-vm-handoff /private/handoff.json` selects another
verified host-local handoff. No guest IP or private-key copying is needed.

Provider subscription sign-in is still interactive inside the isolated worker
account. Adoption preserves current profiles and login homes; a fresh service
install keeps profiles disabled until its separate verification gates pass.

## Failure and recovery boundary

Missing resources, changed disk inodes, redirected paths, altered persistent or
live isolation, unexpected devices, network overlap and identity mismatches stop
before infrastructure changes. Never delete the ownership record to force a
recreation. If an apply is interrupted, keep its evidence and create a new plan.
An incomplete fresh creation leaves `/var/lib/battlefield-cli-vm/incomplete.json`
with the allocated UUIDs; automatic retries refuse to guess ownership. Inspect
that journal and the retained resources before an explicit recovery decision.
Manual recovery is deliberate for an interrupted fresh creation: compare the
journal's UUIDs with `sudo virsh domuuid battlefield-agent`,
`sudo virsh net-uuid battlefield-agent` and `sudo virsh nwfilter-dumpxml battlefield-agent`
before deciding how to recover. Do not remove the journal or resources to make a
retry pass. The workflow offers no destroy, resize, disk relocation or automatic
replacement.

If the SSH readiness task fails, its output is intentionally censored. Check that
the existing key is unlocked, then use the exported `known_hosts` path and admin
key with strict SSH to inspect `sudo cloud-init status --long` inside the guest.
Do not disable host-key checking or print provider login files to diagnose it.
For a failed fresh apply, those connection files exist even if the final handoff
has not yet been exported.

Fresh cloud-init supplies a generated guest SSH host key so its first connection
has established trust. Its private host key remains in the root/guest seed image,
readable only by root/libvirt QEMU, and the guest itself. Protect the VM disk,
seed, host admin key and root-owned state as sensitive local recovery material.
This is not a backup policy: scheduled backups and whole-host-loss protection
remain deferred under [AB-130](https://app.notion.com/p/3eab7a19f8f78112a657faf5aed3b880).

## Verification before production adoption

`./scripts/test/test-deployment.sh` includes the offline layout, receipt and
handoff tests plus Ansible syntax checks. The opt-in disposable lifecycle test is:

```bash
python3 scripts/infra/rehearse-cli-vm.py \
  --image /private/pinned-ubuntu-26.04.img \
  --evidence-dir /private/new-cli-vm-evidence
```

It requires 10 GiB available host RAM, 60 GiB free in libvirt storage and nested
KVM. Its 8 GiB disposable hypervisor creates the real inner 2 CPU / 4 GiB CLI VM,
checks repeated applies, adoption, negative identities/missing disk/network drift,
stale/replayed plans and provider-home fixture bytes, then reboots the disposable
hypervisor. UUID-bound cleanup verifies existing outer domains, networks and pools
were preserved. It never logs into providers or changes production resources.
A reviewed live no-change adoption remains a separate post-promotion acceptance
step; passing the disposable fixture alone does not mark AB-140 complete.
