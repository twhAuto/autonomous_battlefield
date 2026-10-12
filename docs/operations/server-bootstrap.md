# Ubuntu server bootstrap

`./scripts/infra/server.sh` registers and inspects a server, imports existing
credentials into Ansible Vault, and configures the Ubuntu host foundation. Run it
from a Linux/WSL workstation for first setup, or as the registered operator on the
server with `--local`. Jenkins is not required on the workstation. For routine
operations from the existing Jenkins controller, install the separate
[reviewed host job](platform-jobs.md).

The [server rebuild contract](server-rebuild-contract.md) records the complete
layout, state locations, capacity guards and recovery verification. Production
runs in the separately managed K3s VM. This foundation playbook does not invoke
[K3s provisioning](k3s-platform.md) or [CI service configuration](ci-bootstrap.md);
those have separate entry points. [Cloudflare capture and lifecycle](cloudflare-platform.md)
also has its own opt-in Ansible entry point, as does
[native local HTTPS capture, restoration and route migration](local-https-platform.md).
Scheduled backups remain deferred. The separate opt-in
[CLI bootstrap commands](cli-bootstrap.md) install/adopt the CLI host bundle and
optional guest; ordinary host apply does not invoke them. Their existing configuration is preserved. Keep
CLI access disabled until the existing isolation verification is complete.

## Prerequisites and human inputs

- Linux control environment with Python 3.12–3.14 and OpenSSH; target Ubuntu 24.04
  or 26.04 on x86-64, with Python 3 and an existing SSH/sudo operator account.
- A dedicated SSH key already authorized for that account. Verify the host key
  through a trusted channel and establish its `known_hosts` entry. The wrapper
  enforces strict host-key checking; it does not accept unknown keys automatically.
- Existing external credentials, such as the Cloudflare API token. Never paste
  private keys, tokens or sudo passwords into a ticket or command argument.
- One Vault passphrase retained in a password manager/recovery store. This is
  distinct from the SSH key and the server sudo password.

The entry point installs hash-locked Ansible 2.21.4 and its dependencies into a
private cache under `${XDG_CACHE_HOME:-$HOME/.cache}/autonomous-battlefield/`.
It verifies a pinned pip wheel and uses an isolated virtual environment, including
on Ubuntu installations missing `ensurepip`. No system pip or workstation sudo is
needed. Download access to PyPI is required on the first run; subsequent runs
reuse the completed environment. Package installation on the target uses its
configured Ubuntu apt repositories, without requesting a distribution upgrade.

## Register and inspect

From the repository checkout, substitute the actual server/account/key:

```bash
./scripts/infra/server.sh init \
  --host 192.0.2.10 --user operator \
  --identity-file "$HOME/.ssh/battlefield-automation"
./scripts/infra/server.sh inspect
```

`init` writes private `inventory.json` and `inputs.json` files under
`${XDG_CONFIG_HOME:-$HOME/.config}/autonomous-battlefield/server/`. It refuses to
replace existing files. The inventory records the machine ID read over trusted
SSH; later operations reject a different machine ID or operator. Replacing a host
requires inspecting and deliberately registering its new identity, not blindly
reusing its former IP. `--inventory /absolute/path/inventory.json` selects another
private inventory outside the checkout; `inputs.json` lives alongside it.
Jenkins `@tmp` paths are supported; whitespace, shell/template syntax and parent
traversals remain invalid.

Inspection reports OS, CPU/memory/disk metadata, network addresses, libvirt
resources, selected service states, and whether passwordless sudo is already
available through the selected compatible sudo executable. Ansible uses
`/usr/bin/sudo` when it is traditional sudo, otherwise `/usr/bin/sudo.ws` if
installed. Ubuntu 26.04's sudo-rs prompt is incompatible with the pinned Ansible
sudo plugin; this target-side selection works for workstation SSH and Jenkins
local execution without changing the system default or granting passwordless
sudo. If neither compatible executable is available, install Ubuntu's `sudo`
package before bootstrapping. See the [upstream prompt compatibility issue](https://github.com/ansible/ansible/issues/85837).
Inspection never mounts a disk or prints credential contents. Keep reports
private because they contain internal addressing and filesystem identifiers.

Registration discovers the operator's existing production `.env`, tunnel JSON,
kubeconfig, kubelet CA, deployment profile and recovery metadata. It also adds the
local `bootstrap/cloudflare-api-token` when present under the project's config
root. Discovery records file locations only. Review the generated input manifest;
it does not discover protected Jenkins controller keys or all provider credentials.
For additional UTF-8 secret/config files, use the format in
[`inputs.example.json`](../../infra/ansible/inputs.example.json). Each named file
has `source: controller` or `source: host` and an absolute path. Local secret files
must be owned by the invoking user and have mode 0600 or stricter. Remote imports
must be readable regular files, at most 8 MiB each; symlinks are rejected.

## Encrypt once, plan, then apply

```bash
private_dir="${XDG_CONFIG_HOME:-$HOME/.config}/autonomous-battlefield/server"
./scripts/infra/server.sh seal --vault "$private_dir/inputs.vault"
./scripts/infra/server.sh plan --vault "$private_dir/inputs.vault"
./scripts/infra/server.sh apply --vault "$private_dir/inputs.vault"
./scripts/infra/server.sh verify
```

`seal` requires a terminal or an explicit private `--vault-password-file` before
importing anything. It imports the manifest's files over SSH or from local private files,
then encrypts them in memory/stdin without a plaintext staging file. It prompts
for a Vault passphrase and refuses to replace an existing bundle. Retain the
passphrase independently of the primary disk; an encrypted bundle alone cannot
recover the inputs. No SSH private key is copied to the server.

`plan` runs Ansible check/diff mode. `apply` prompts for sudo when necessary and
uses the same roles. An unattended controller can supply an existing private
`--vault-password-file` and non-executable `--become-password-file`; values never
appear in arguments. The [platform job installer](platform-jobs.md) binds those
files through Jenkins folder credentials. Do not grant blanket
passwordless sudo just to bypass the first interactive bootstrap. The current job
uses the existing sudo account; a narrower operational privilege policy remains
separate work.

The foundation:

- Installs host prerequisites for libvirt/KVM and CI, selecting the concrete
  `qemu-system-x86` and `qemu-utils` packages. Ubuntu 26.04 has multiple providers
  for the virtual `qemu-kvm` package, so it cannot be used for unattended selection.
  An existing QEMU/libvirt HWE stack is rejected before package changes because it
  uses a different dependency family. Apt operations also refuse package removals;
  review a compatible package profile before adopting such a host.
  It installs Ubuntu Docker
  and Buildx only if no Docker service exists; an existing Docker installation
  must already provide Buildx. It does not switch package vendors underneath a
  running daemon.
- Adds the existing operator to Docker/libvirt/KVM groups, preserving other groups;
  a new login may be needed for that membership to take effect. Docker access is
  privileged host access.
- Enables Docker/libvirt and starts stopped services, without restarting healthy
  services. Host package installation can start newly installed services.
- Writes only its own mmap sysctl file, retains a higher existing live value, and
  reloads that setting through a handler. Declared public CA `.crt` files are
  installed under a project prefix and update system trust through a handler.
- Stages imported inputs under root-only `/etc/battlefield-bootstrap/inputs/`,
  with an ownership marker tied to the registered machine ID. Secret tasks use
  `no_log` and disable diff. Existing differing credentials or omitted previously
  imported inputs fail before package/configuration changes; no password is
  regenerated and no existing application credential is rotated.
- Creates `/var/lib/battlefield-platform/` for private platform state and preserves
  an encrypted copy of the bundle there. Subsequent service roles will consume the
  staged inputs; copying them here does not reconfigure production services.

Applies from WSL and the server share an exclusive host lock in the registered
operator's `~/.local/state/battlefield/bootstrap.lock`. Run host-side commands as
that same operator with `--local`; they do not need an SSH private key. The
workstation retains a connection for the lock lifetime; losing it aborts the local
Ansible process while its controller is running. The Ansible child inherits the
lock or the proxy's keepalive pipe, so killing the controller's process group does
not release the lock while that child is still running. If the entire workstation
or its SSH connection fails, remote tasks may finish after the connection closes;
inspect the host before retrying. Plans are advisory and do not acquire this mutation lock. The reviewed
Jenkins runner locks plan/apply/verify and uses this same lock for individual
production deployment, tunnel and rollback commands. Jenkins also serializes its
whole rollout and platform job with a shared resource. External CLI applies must
not run between application pipeline commands; see the
[locking contract](platform-jobs.md#plan-apply-and-verify).

Reruns repair managed permissions/settings and preserve unrelated files. If a run
stops after creating an empty ownership directory, it can resume. Registration also
rolls back its newly created inventory if publishing the input manifest fails or
is interrupted. An unknown nonempty directory, mismatched marker, or redirected managed input path requires
inspection. Do not delete an ownership marker to force adoption. A fresh-host plan
skips group changes and starting not-yet-installed services; it is not evidence of
a successful apply/reboot. `verify` checks installed services, Buildx, KVM and
input permissions; it does not prove service/application restoration.

## Optional host data partition

Set `data_uuid` in the private inventory to adopt an **existing ext4 partition**.
`data_mount` defaults to `/srv/battlefield-data`; only a direct `/srv` child with
lowercase letters, digits and hyphens is accepted. Empty/omitted `data_uuid` keeps
the existing foundation-only behavior. This is separate from `backup_uuid`: a
partition on the Ubuntu SSD is suitable for application storage, but is not a
second-drive backup.

For the inspected server, the selected inventory additions are:

```json
"data_uuid": "75db9b1d-5c46-4e5b-bd94-5391b2a35411",
"data_mount": "/srv/battlefield-data"
```

These are properties of the existing inventory JSON object, not a replacement
inventory. Keep the machine identity, operator, SSH key and encrypted inputs.
Use the same `server.sh plan`, `apply` and `verify` commands described above;
first adoption requires the ordinary sudo prompt. No new token or key is needed.
`plan` inspects storage without changing its mount, fstab or permissions. `verify`
requires the declared mount, directories, guard and activation record to match.

The data role validates the UUID is unique, identifies an ext4 partition and is
not mounted elsewhere. It rejects occupied unmounted directories, symlinked paths,
conflicting fstab entries and read-only/wrong mounts. It never formats, partitions,
resizes, recursively changes ownership, or moves existing service data. It adds
only its UUID-based entry to `/etc/fstab`, with a private pre-change copy beside
fstab when an edit is needed. Validation checks the added entry independently of
unrelated entries; existing fstab problems remain visible for separate repair.
An already matching entry is retained unchanged.
After adoption `/etc/battlefield-data-storage.json` records the storage identity;
a different UUID/path/operator requires a separate reviewed migration. Clearing
`data_uuid` does not remove this contract, unmount storage or uninstall the guard.

| Path | Ownership and use |
| --- | --- |
| `/srv/battlefield-data` | `root:root`, mode `0711`; traversal for scoped subdirectories |
| `workspace/` | Registered operator, mode `0700`; private files |
| `libvirt/` | `root:root`, mode `0711` |
| `libvirt/images/` | `libvirt-qemu` account/group, mode `0750`; future VM images |

Only these directory entries have ownership/mode reconciled. Existing files and
unrelated directories remain in place. Free bytes are reported in the storage
result; there is no automatic expansion, cache pruning or disk-capacity alert
in this role.

### Terraform and guest storage contract

Ubuntu, Jenkins, SonarQube and host CI/Docker storage remain on `/`. The
Terraform-managed domain must be named `battlefield-k3s`, with its separate OS
and retained data images in the reported `images` directory.
[VM provisioning](k3s-vm.md) owns the retained-volume lifecycle and state locking;
[K3s platform configuration](k3s-platform.md) owns mounting retained data
**inside** the guest before K3s and configuring its persistent-volume path.
Neither VM provisioning nor application/database
migration is performed by this host role. The separate
[application cutover](k3s-delivery.md#adoption-and-cutover-boundary) moves the
application to K3s while retaining the old Kind data.

The root-installed `/etc/battlefield-data-storage/guard.py` provides the
read-only command consumers must run on the host **before storage mutations**:

```bash
sudo /etc/battlefield-data-storage/guard.py guard \
  75db9b1d-5c46-4e5b-bd94-5391b2a35411 /srv/battlefield-data twh09
```

Use inventory values on other hosts. Success emits no output; failure is nonzero.
Do not create a libvirt pool or allocate image files after a failed guard. The
guard checks the ownership contract, exact mount UUID, writable ext4 filesystem
and image-directory filesystem. It is also installed as a separate executable in
`/etc/libvirt/hooks/qemu.d/50-battlefield-data-storage`. Only preparation/start,
restore and incoming migration of `battlefield-k3s` are gated; unrelated guests,
existing hooks and reconnect/stop events are preserved. The check prevents a new
VM start against missing/wrong storage, but is not a running-VM disk-failure
monitor or a capacity quota. Terraform must still place its volumes correctly.

A systemd drop-in orders libvirt after the configured mount at boot. It uses
`Wants`/`After` so an unavailable data partition does not disable unrelated guests;
the K3s-specific hook still rejects its start in that case.

The guard lives in its own root-owned directory under `/etc`; shared `/usr/local`
permissions and existing libvirt hook-directory permissions are preserved.

Libvirt hook discovery is reloaded with SIGHUP after installation, without
restarting running guests. A private activation record makes an interrupted
install resume that reload. The behavior follows [libvirt's hook interface](https://libvirt.org/hooks.html)
and its [hook discovery implementation](https://github.com/libvirt/libvirt/blob/master/src/util/virhook.c).

### Storage validation

`./scripts/test/test-server-bootstrap.sh` includes input, fstab preservation,
read-only plan/verify, remapping, symlink, mount identity and hook-scope regression
tests. For privileged acceptance, `scripts/test/rehearse-data-storage.py` is a
**disposable-guest-only** fixture: it requires the matching rehearsal UUID and
operator, and an empty 1 GiB `/dev/vdb` before partitioning that test disk. Never
run that fixture on the production host. It exercises the actual Ansible role,
libvirt hook invocation, absent/wrong/read-only mounts, repeat apply, interrupted
activation and persistence after a real guest reboot. These acceptance tests are
separate from the unprivileged CI suite.

## Backup drive and recovery boundary

Scheduled backup and retention are not configured. Pre-migration database dumps
remain enabled. If backup automation is added, the target is the server's second
physical disk only; the earlier 100 GB / 7 daily / 4 weekly proposal is a planning
baseline, not an active policy.

Leave `backup_uuid` empty until the intended filesystem is inspected. Once set,
inspection reports whether it identifies a unique existing ext4 filesystem on a
different physical disk from `/`. A primary-disk partition, missing disk or
ambiguous UUID is reported with `usable: false`; a valid candidate has `usable: true`,
and no configured UUID produces `null`. This flag confirms disk identity only.
Foundation inspection, credential import and configuration remain available when the backup disk is unavailable; a future
backup operation must reject that selection before writing. Inspection does not
establish that the filesystem is empty or that it has 100 GB free. The backup selection does not mount, format, partition or expand that drive. The future
backup role must verify the expected mounted UUID before writing, preserve other
data, and fail visibly when the disk is absent.

The [VM workflow](k3s-vm.md) maintains canonical Terraform state on the host, with
locking, encrypted local recovery snapshots and an explicit copy command for the
registered second disk. Terraform is installed by that workflow, not the host job. Ordinary application delivery must not run host provisioning;
changes limited to `infra/ansible/` or `scripts/infra/` select no application
components, while regression checks still run.

Second-disk backups protect against loss of the primary disk when the backup and
recovery keys survive. Whole-server-loss recovery and off-host copies are deferred.
Existing local database dumps are not a verified complete-server backup. Jenkins
recovery requires matching encryption material, with `master.key` separately
protected; the unprivileged import manifest cannot collect it automatically.

## Validation and remaining rollout evidence

```bash
./scripts/test/test-server-bootstrap.sh
./scripts/test/test-deployment.sh
```

The first command installs the pinned tools, checks playbook syntax, and runs
real Vault/Ansible tests against synthetic files under a private temporary
directory. It verifies first/second apply, check mode, permission drift repair,
credential conflict/omission rejection with the affected input names, preserved
encrypted recovery bundles, literal secret content and secret-safe output. A pseudo-terminal check runs the
actual Ansible Vault/sudo prompts through the apply process launcher and verifies
that the synthetic password is not echoed. It does not execute privileged tasks. Wrapper tests cover inventory/SSH injection, machine identity, disk
selection, private file handling and concurrent lock exclusion. The deployment
suite invokes the complete bootstrap check, so Jenkins exercises real input
reconciliation too. Running the Python test file directly without
`BATTLEFIELD_TEST_ANSIBLE_BIN` runs only wrapper tests and explicitly skips the
real Ansible checks.

## Disposable full-host rehearsal

Before the first production foundation apply, require a recorded successful
rehearsal of the matching Ubuntu release using the exact source revision being
applied. Repeat it after changes to the foundation, controller, fixture or image
pin. A green ordinary CI run alone does not satisfy this prerequisite.

On a Linux libvirt host with `virsh`, `qemu-img`, `cloud-localds`, OpenSSH and
Python 3.12 or newer already available, run from this checkout:

```bash
python3 scripts/infra/rehearse-server.py \
  --evidence-dir /tmp/battlefield-rehearsal-evidence
```

The evidence directory must be new and outside Git. The invoking account needs
access to `qemu:///system`; no hypervisor sudo password is used. The command
requires an existing active IPv4 NAT/DHCP network (default `default`; select
another with `--network NAME`), nested virtualization, 5 GiB available RAM,
20 GiB free in `/var/lib/libvirt/images` and 8 GiB in temporary storage. It adds
no host packages, network rules or persistent services. Run it when those
resources are available alongside existing workloads. The guest needs working
DNS and outbound access to its configured Ubuntu package archives and to PyPI
(including its package download hosts) for the pinned controller dependencies.
Dual-stack NAT networks work when exactly one IPv4 DHCP subnet is available.
Degraded cloud-init completion is a failed rehearsal with an initialization
error; inspect guest networking and initialization rather than accepting it.

The runner defaults to a checksum-pinned Ubuntu 26.04 cloud image from the
[official dated image directory](https://cloud-images.ubuntu.com/resolute/20260927/).
Use `--ubuntu 24.04` to rehearse the other supported host release, using its
[separate pinned image](https://cloud-images.ubuntu.com/releases/noble/release-20260926/).
The verified image is cached under
`${XDG_CACHE_HOME:-$HOME/.cache}/autonomous-battlefield/rehearsal/`; every run
checks it again. An optional `--image /path/to/downloaded.img` reuses another local copy only when its
SHA-256 matches `infra/ansible/rehearsal-image.json`.

Dated upstream URLs may become unavailable. An existing verified cache entry or
`--image` copy can still be used with its exact pinned digest; no permanent
upstream archive is assumed. To refresh a pin:

1. Select a dated amd64 `.img` build for the same supported release from the
   official Ubuntu cloud-image listing. Download that image and its `SHA256SUMS`
   and `SHA256SUMS.gpg` files outside Git.
2. Authenticate the checksum manifest and verify the image following
   [Ubuntu's cloud-image verification procedure](https://ubuntu.com/docs/public-images/public-images-how-to/verify-image-checksum/).
   Update that release's URL and SHA-256 together in
   `infra/ansible/rehearsal-image.json`; preserve the release field. Update the
   dated runbook link above. Never substitute a floating URL or disable verification.
3. Run `./scripts/test/test-deployment.sh` and the complete VM rehearsal with
   `--ubuntu RELEASE --image /path/to/verified.img` and a new evidence directory.
   Require actual boot, reboot, apply/resume/repeat and cleanup success. Commit
   the manifest/runbook changes together and retain matching source-hash evidence.
   A 24.04 pin refresh also requires the [K3s VM drill](k3s-vm.md#validation)
   (`scripts/infra/rehearse-vm.py` with its pinned 26.04 outer image), because
   the production guest and the host rehearsal share that 24.04 image pin.

The VM uses 2 vCPUs, 3 GiB
RAM and a sparse 16 GiB disk. It has temporary SSH client/host keys, a synthetic
Vault password and synthetic input; no production inventory or credentials are
copied. Host keys are pinned before the first SSH connection. The fixture checks
both its root-owned marker and hardware UUID before privileged operations.
Passwordless sudo is granted only to this disposable guest's test account.

Inside the guest, the real `server.sh --local` entry point installs its pinned
controller tools without Jenkins. A temporary callback in the disposable
operator's Ansible plugin directory holds execution after the ownership marker
is written. The fixture then interrupts the real wrapper; a missed signal causes
the callback to exit unsuccessfully, never to continue into later tasks. The
callback is removed before ordinary resume. Production roles and controller
configuration receive no interruption hook. The fixture verifies the foundation
and requires zero Ansible changes on the next apply and check/diff.
It compares credential/ciphertext hashes, permissions, unrelated configuration
and service invocation IDs. Lowering the live mmap limit and changing an input's
mode must be repaired without restarting healthy services. Live mmap drift is
exercised when the initial value is the pinned-image baseline of 1048576; a
higher initial value is preserved and the result records that mmap drift was
skipped. Input-permission drift is always exercised. The host runner tolerates
an SSH disconnect during reboot and waits for a changed boot ID before starting
verification and another zero-change apply. This is a host
foundation drill; it does not exercise application or database recovery. The
26.04 fixture also installs the alternative QEMU HWE provider in its disposable
guest and requires an actionable refusal before any package/configuration changes.

`result.json` records the exact source hashes, pinned image, guest UUID, outcome
and cleanup status. `guest-evidence.tar.gz` contains test logs and state
fingerprints, after checking logs for the synthetic secret/password and their
base64 representations. Credentials and private SSH keys are excluded. Keep the
evidence directory private. Fixture failures include a secret-checked `failure.json`
when guest evidence is available. A nonzero exit is a failed rehearsal, even if some
logs show passing tasks.

Normal completion and handled failures remove the UUID-checked VM, its recorded
volumes and its now-empty storage pool. Existing domains, networks and disks
are never selected for cleanup. If the process or host is forcibly killed,
use the UUID/name in `result.json` to inspect leftover resources with `virsh`
before deleting anything; no automatic sweep of similarly named VMs is made.
The evidence stays available after cleanup.

Offline cleanup/integrity/evidence tests run in `test-server-bootstrap.sh` and
therefore in the normal deployment gate. The VM rehearsal is opt-in and requires
its own recorded result; ordinary CI does not prove it passed. Rehearse the release
matching the intended target; one release does not prove the other passed.

Jenkins/SonarQube rebuilds, CLI bundle adoption, K3s, Cloudflare adoption, native
local HTTPS and backup/restore drills have separate workflows. Track acceptance and actual
rehearsal results in Notion, not this runbook.
