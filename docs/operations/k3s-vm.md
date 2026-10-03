# Persistent K3s VM provisioning

The Terraform/libvirt workflow creates an Ubuntu 24.04 guest and exports Ansible
connection facts. [K3s platform configuration](k3s-platform.md) owns installation
and mounting guest data; [K3s delivery](k3s-delivery.md) owns application rollout
and database cutover. See the [rebuild contract](server-rebuild-contract.md) for
the production layout and state locations. A VM provisioning operation alone
never migrates an application or its database.

## Prerequisites and storage

Apply and verify [the host job](platform-jobs.md) with the selected ext4 data
partition first. Run VM operations locally as the registered Ubuntu operator;
Jenkins uses the same account. Host packages include libvirt/KVM, qemu-img,
cloud-localds and OpenSSH. The runner downloads a checksum-pinned Terraform
binary and Ubuntu image; the committed Terraform dependency lock pins the
libvirt provider. The Ubuntu 24.04 pin is shared with host rehearsal in
`infra/ansible/rehearsal-image.json` and uses a dated release-build URL. Outbound
HTTPS is needed for these downloads and guest apt.

The reviewed defaults in [`infra/libvirt/vm.json`](../../infra/libvirt/vm.json) are:

| Resource | Default |
| --- | --- |
| VM | `battlefield-k3s`, autostart enabled |
| CPU / RAM | 4 vCPUs / 8192 MiB |
| Network | Dedicated NAT `192.168.235.0/24`, bridge `bf-k3s0` |
| Guest address | DHCP reservation `192.168.235.10` |
| OS disk | 32 GiB, replaceable |
| Data disk | 250 GiB, retained across OS/domain replacement |
| Host reserve | 64 GiB plus 4 GiB allowance for the base image |

Images live under the [host storage contract](server-bootstrap.md#terraform-and-guest-storage-contract)
at `/srv/battlefield-data/libvirt/images`.
The guard verifies the actual mount UUID before planning and again before apply.
There is no fallback to the root filesystem. The storage pool uses the prepared
directory without building, formatting or deleting it. Ubuntu/Jenkins/SonarQube
and current Docker storage keep their existing locations.

The disks use sparse qcow2 allocation, but preflight budgets their full virtual
capacity plus reserve. It also checks network overlap, CPU count and available
memory, leaving at least 2 GiB for host services. This is an operation-time check,
not continuous disk monitoring or a quota. Other writes can consume the reserve;
there is no automatic expansion, pruning or database retention in this slice.

The guest data volume starts blank. The [K3s platform role](k3s-platform.md) identifies it by
`/dev/disk/by-id/virtio-battlefield-k3s-data`, validates existing filesystem identity,
and mounts it before K3s starts. Routine provisioning never formats this volume.

## Install or upgrade the Jenkins job

After the reviewed change is promoted to `main`, use the existing
[platform setup procedure](platform-jobs.md) to seed both `host` and `vm`. Existing
folder credentials are retained. For an already configured folder, an
administrator can update only the job definitions through a strict SSH tunnel:

```bash
ssh -N -L 127.0.0.1:18080:127.0.0.1:8080 \
  -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes \
  -i ~/.ssh/battlefield-automation twh09@192.168.1.126
```

In another workstation terminal, from the reviewed checkout:

```bash
python3 scripts/infra/seed-platform.py --jobs-only \
  --url http://127.0.0.1:18080 --user whtan
```

The API token is prompted without echo. No credential rotation, build or apply is
triggered. The new `battlefield-platform/vm` job uses existing `host-inventory`,
`host-sudo-password` and `host-vault-password` folder credentials. It does not
read the application credential bundle. Stop the tunnel after seeding.

## Plan, review, apply and verify

1. Run `battlefield-platform/vm` with `ACTION=plan`, `REPLACE_OS=false`.
2. Review archived `vm-plan.json`: revision, CPU/memory/storage/network settings
   and resource actions. The first plan creates the dedicated network, pool,
   base image, OS/data/seed volumes and domain. Other domains must not appear.
3. Run `ACTION=apply`. This creates a fresh plan and pauses for `whtan` to approve
   its artifact. The approval expires after ten minutes. A merge to main does
   not trigger this job.
4. After approval, the runner rechecks the revision, inputs, canonical state,
   saved-plan hash and a fresh plan. Drift or a changed main requires a new review.
   It consumes the receipt, applies the saved plan and verifies guest SSH,
   cloud-init completion and the retained data-device attachment.
5. Run `ACTION=plan` again for convergence, or `ACTION=verify` to also require
   zero pending Terraform changes.

Plan/verify never create or modify libvirt resources. They may prepare local
controller files: private directories, checksum-verified downloads, provider
cache, the dedicated administration key on first use, cloud-init seed and
Terraform plan files. `verify` also refreshes the generated Ansible inventory.
These files live outside the repository and are not archived in Jenkins.
Terraform provider processes use a short, private temporary socket directory
under `/tmp`, cleaned after each command, because Jenkins workspace paths can
exceed UNIX socket limits. Canonical state and saved plans keep their private
locations described here.

State is canonical at `~/.local/state/battlefield/k3s/terraform.tfstate`, outside
build workspaces. Terraform's local backend lock protects this file; the shared
operator lock serializes VM, host and application operations. Jenkins holds
`battlefield-production-platform` through plan, approval and apply. Direct
operator commands must not be started during a pending Jenkins approval or
between application rollout stages. Child processes inherit the operator lock;
cancellation reaps them before releasing it.

A failed or interrupted apply needs a fresh plan and approval. Inspect private
`~/.local/state/battlefield/k3s/last-error.log` on the server when an operation
fails; it is never a public Jenkins artifact. Canonical state may contain partial
progress and must be retained. A missing or unrecognized resource produces a
recovery error rather than automatic adoption of a same-named object.

## SSH and Ansible handoff

The runner generates one dedicated Ed25519 key under the canonical private state
directory and never rotates it during reruns. Only its public half enters
cloud-init. No application credentials, sudo passwords or private host keys enter
Terraform variables, state or plan files. The encrypted recovery bundle includes
the administration private key separately from Terraform state.

After apply, the runner reads the guest's public SSH host key through the QEMU
agent channel of the Terraform-owned domain UUID. It then uses strict SSH host
checking to verify cloud-init and the data attachment; it does not accept an
unverified network key. A verify-only run rejects a changed host key.

Generated `~/.local/state/battlefield/k3s/inventory.json` supplies the guest address,
user, key path, strict known-hosts path, domain UUID and stable data-device path
to Ansible. No IP copying or workstation private-key upload is necessary.

## OS replacement and protected data

`REPLACE_OS=true` explicitly plans replacement of the OS disk and domain. Review
that downtime and finish the [K3s platform data-mount and restore procedure](k3s-platform.md)
before using this on an active K3s service. The seed volume may also be replaced after reviewed
configuration changes. Persistent data, network, pool and base image have
Terraform `prevent_destroy` protection; the wrapper additionally rejects their
deletion or recreation after a missing-resource refresh. It has no destroy action.

Data-disk resize, subnet replacement, image-base retirement and intentional data
deletion need separate reviewed procedures. Do not remove lifecycle protections,
clear state or run raw `terraform destroy` to make an ordinary apply pass.

## State recovery and the second disk

Each apply creates encrypted, versioned recovery bundles before and after any
state-changing attempt under `~/.local/state/battlefield/k3s/recovery/`. They
contain Terraform state, the administration key, host/storage ownership, VM
settings and revision. Encryption uses the existing Vault passphrase. These are
control-plane recovery bundles, not database or VM-data backups. Keep the
passphrase outside this server. Local snapshots have no scheduled retention;
inspect their size as part of manual maintenance.

The owner selected only the server's separate second disk for recovery copies.
Scheduled application backups and whole-server-loss recovery remain deferred.
Register its `backup_uuid` in the existing inventory and mount it deliberately;
this command neither mounts nor formats disks. Once an operator-owned mode-0700
directory exists on that filesystem, copy a selected completed bundle:

```bash
python3 scripts/infra/vm-state.py copy \
  --inventory /absolute/private/inventory.json \
  --bundle /home/twh09/.local/state/battlefield/k3s/recovery/SELECTED.vault \
  --destination /absolute/mounted-backup/private-directory
```

If canonical state is lost but the host and libvirt resources remain, restore a
selected bundle using private file paths (mode 0600):

```bash
python3 scripts/infra/vm-state.py restore \
  --inventory /absolute/private/inventory.json \
  --bundle /absolute/mounted-backup/private-directory/SELECTED.vault \
  --vault-password-file /absolute/private/vault-password
```

Restore validates the host/storage identity and live domain/network/pool UUIDs
before publishing files. It refuses to replace differing local state or keys;
preserve and inspect those files first. Recovered settings are written to
`recovered-config.json`; reconcile them with the reviewed configuration before
planning. Run a reviewed no-change apply to re-establish guest SSH trust if its
known-hosts file was lost, then verify. Changing machine ID or recovering onto a
replacement physical host requires explicit identity/import review; this command
does not silently rebind resources or recover lost virtual disks.

## Validation

The deployment gate runs pinned Terraform format/schema validation without a
libvirt connection, exercises mocked-provider contracts and native local-backend
lock contention, and includes `scripts/test/test-vm.py` for plan/receipt policy,
protected disks, private paths, SSH identity and job configuration. Run Terraform
format/validate with the pinned binary and committed provider lock. Provider
packages are cached under `~/.cache/autonomous-battlefield/terraform-check/providers`;
initialization writes are serialized across concurrent checks and packages remain
verified against the dependency lock. Warm checks avoid downloading the provider
archive again; registry metadata access is still required.

For a real disposable drill on an existing hypervisor with nested KVM, at least
8 GiB available memory and 110 GiB available sparse-disk capacity:

```bash
python3 scripts/infra/rehearse-vm.py \
  --image /absolute/path/to/pinned-ubuntu-26.04.img \
  --evidence-dir /absolute/private/new-evidence-directory
```

The outer guest uses the pinned Ubuntu 26.04 image from
`infra/ansible/rehearsal-image.json`, 6 GiB RAM, a 48 GiB root disk and a separate
48 GiB data disk. The nested VM uses reduced test sizes. The fixture performs
actual plan/apply/reapply, rejected destroy, OS replacement with a retained data
marker, encrypted state restore and hypervisor reboot/autostart. Destructive
formatting is restricted to the marked disposable hypervisor's newly attached
fixture disk. Source hashes and cleanup results are recorded with the evidence.
This drill is opt-in and never runs as ordinary shared-host PR verification.

Provider schema and lifecycle behaviour are checked against the pinned
[libvirt provider source](https://github.com/dmacvicar/terraform-provider-libvirt/tree/v0.9.9)
and [Terraform local backend documentation](https://developer.hashicorp.com/terraform/language/backend/local).
