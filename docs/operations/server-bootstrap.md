# Ubuntu server bootstrap

`./scripts/infra/server.sh` registers and inspects a server, imports existing
credentials into Ansible Vault, and configures the Ubuntu host foundation. Run it
from a Linux/WSL workstation for first setup, or as the registered operator on the
server with `--local`. Jenkins is not required on the workstation.

This is the first implementation slice of the complete-server automation. The
current production application still runs in Kind. This playbook does not yet
provision K3s, configure Jenkins/SonarQube, reconcile Cloudflare, install the CLI
VM bundle, or schedule backups. Their existing configuration is preserved. Keep
CLI access disabled until the existing isolation verification is complete.

## Prerequisites and human inputs

- Linux control environment with Python 3.12–3.14 and OpenSSH; target Ubuntu 24.04
  or 26.04 with Python 3 and an existing SSH/sudo operator account.
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
available. It never mounts a disk or prints credential contents. Keep reports
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
`--vault-password-file`; values never appear in arguments. Do not grant blanket
passwordless sudo just to bypass the first interactive bootstrap. Jenkins
credential bindings and scoped operational privileges belong to subsequent setup.

The foundation:

- Installs host prerequisites for libvirt/KVM and CI. It installs Ubuntu Docker
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
Ansible process. Plans are advisory and do not acquire this mutation lock. This
bootstrap lock does not yet coordinate application delivery or recovery jobs;
those jobs need the later shared platform-operation integration.

Reruns repair managed permissions/settings and preserve unrelated files. If a run
stops after creating an empty ownership directory, it can resume. Registration also
rolls back its newly created inventory if publishing the input manifest fails or
is interrupted. An unknown nonempty directory, mismatched marker, or redirected managed input path requires
inspection. Do not delete an ownership marker to force adoption. A fresh-host plan
skips group changes and starting not-yet-installed services; it is not evidence of
a successful apply/reboot. `verify` checks installed services, Buildx, KVM and
input permissions; it does not prove service/application restoration.

## Backup drive and recovery boundary

Initial owner choice: use only the server's second physical disk, with a 100 GB
starting budget. Proposed recovery automation will keep 7 daily and 4 weekly
points, adjusted to measured sizes, plus checkpoints around infrastructure changes.
Nightly backups and retention are not active in this foundation slice.

Leave `backup_uuid` empty until the intended filesystem is inspected. Once set,
inspection reports whether it identifies a unique existing ext4 filesystem on a
different physical disk from `/`. A primary-disk partition, missing disk or
ambiguous UUID is reported with `usable: false`; a valid candidate has `usable: true`,
and no configured UUID produces `null`. This flag confirms disk identity only.
Foundation inspection, credential import and configuration remain available when the backup disk is unavailable; a future
backup operation must reject that selection before writing. Inspection does not
establish that the filesystem is empty or that it has 100 GB free. No command here mounts, formats, partitions or expands it. The future
backup role must verify the expected mounted UUID before writing, preserve other
data, and fail visibly when the disk is absent.

The proposed Terraform backend will have one canonical state on the host, with
locking and recovery copies on the second disk. Terraform itself is not installed
by this slice. Ordinary application delivery must not run host provisioning;
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
five Ansible checks.

A complete disposable Ubuntu host apply/interruption/reboot and package/service
adoption rehearsal remains required before production apply. The 2026-09-30 live
check exercised only `init`/`inspect` over SSH against Ubuntu 26.04/Python 3.14.4;
no live configuration was changed. Jenkins/SonarQube rebuilds, CLI bundle adoption,
K3s, Cloudflare adoption, and backup/restore drills remain subsequent work. Track
acceptance and status in Notion, not this runbook.
