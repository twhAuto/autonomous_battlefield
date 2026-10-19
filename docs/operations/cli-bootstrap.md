# CLI worker bootstrap and adoption

The optional CLI commands in `scripts/infra/server.sh` install or adopt the host
bridge, dedicated Caddy/Squid configuration, firewall and worker files using
Ansible. Normal `server.sh apply` and application deployments do not invoke them.
They work without Jenkins. Jenkins and SonarQube reconstruction use the separate
[CI bootstrap workflow](ci-bootstrap.md).

Provision or adopt the VM first with [CLI VM platform](cli-vm-platform.md).
The CLI VM must already exist with the [documented isolated network and NIC
filter](agent-vm.md). This command does not create, replace or resize a VM. Host
foundation and production Kubernetes registration must already work, including
`/usr/bin/kubectl`. The registered operator must belong to the host `libvirt`
group; start a new login after adding group membership. Managed files and their
existing parent directories must be root-owned and not writable by group/others.
The guard rejects an unsafe shared directory rather than changing its permissions.
The controller needs trusted SSH entries for the physical host
and, for guest operations, the VM; host-side execution uses the existing VM admin
key. Keep Player CLI access disabled until the [CLI isolation gates](agent-vm.md#isolation-gate)
pass. Installation or a healthy worker is not evidence that these gates passed.

## Adopt the existing installation

Use the same checkout and controller throughout registration, planning and
applying: captured file paths belong to that controller. For the existing server,
run from its checkout as the registered operator with `--local`. From WSL, omit
`--local`; guest SSH uses the physical host as its jump connection, without agent
forwarding. The guest admin private key must then exist on WSL. The CLI VM managed handoff
is host-local: use `--local` on the physical host to consume it without copying
keys. It supplies strict host-key trust and refuses a different guest or key.
`--cli-vm-handoff` can select an explicit private handoff path; it is rejected
for a remote controller.

```bash
./scripts/infra/server.sh cli-register --local
```

Registration reads the existing public host files, VM/network UUIDs and production
cluster UID. It writes `cli.json` and `cli.files/` alongside the private host
inventory, without changing services. It preserves the matched `20-loopback.conf`
override and provider allowlist. It refuses an existing registration, incomplete
bundle, changed boundary or unknown bundle files. Other systemd overrides are
rejected during target preflight, before any package or configuration mutation.
Use `--cli-config /private/path/cli.json` for a different registration location.

Host-only plan/apply/verify do not need a Vault. To include the existing guest,
unlock its admin key in an SSH agent on the controller if it is passphrase
protected. Keep the key and passphrase private. Then capture the guest once:

```bash
private_dir="${XDG_CONFIG_HOME:-$HOME/.config}/autonomous-battlefield/server"
./scripts/infra/server.sh cli-capture-guest --local \
  --cli-guest-key "$HOME/.ssh/battlefield-vm-admin" \
  --vault "$private_dir/cli-inputs.vault"
```

This copies the existing worker and installed CLI executables, records their
SHA256 fingerprints, and captures the worker configuration, unit and installed
`verify-agent-isolation.py` in a private
`cli.guest/` directory. It encrypts only the dedicated worker environment through
Ansible Vault stdin; there is no plaintext secret staging file. Existing bundles
are never overwritten. Provider login directories remain on the guest and are
neither read nor modified. Retain the Vault password separately. Captured hashes
identify the adopted installation; they do not add release-signature provenance
or prove that an old worker is compatible with a newer backend.

Review the public registration and captured configuration, then run:

```bash
./scripts/infra/server.sh cli-plan --local --vault "$private_dir/cli-inputs.vault"
./scripts/infra/server.sh cli-apply --local --vault "$private_dir/cli-inputs.vault"
./scripts/infra/server.sh cli-verify --local --vault "$private_dir/cli-inputs.vault"
```

Omit `--vault` for host-only registration. The normal private
`--vault-password-file`, `--become-password-file` and fresh `--report-file` options
are supported. Root privileges on the physical host use the existing sudo
prompt; vm-admin uses its existing sudo policy. Ubuntu 26.04 selects `sudo.ws`
when the default is sudo-rs. No sudo policy is expanded by these commands.

Adoption requires matching existing files and validates both active and persistent
libvirt XML, the NIC filter, machine identities and cluster UID. Initial guest
adoption requires a complete matching installation, so an adopted enabled profile
cannot silently become a fresh installation. Root-owned markers under
`/etc/battlefield-cli-bootstrap/` bind identities and desired file hashes. A repeat
apply preserves healthy service invocation IDs, credentials and provider logins.
A newly installed vendor `caddy.service` is masked before package installation,
so its default web listener cannot appear after reboot. Existing Caddy executables
and services are preserved; a missing executable is installed from the host's
configured Ubuntu apt repositories. The dedicated VM proxy uses its own unit.
New or interrupted unconfigured Squid installations stay masked until configuration
validation completes, including across a reboot. Before the first ownership marker,
conflicting existing files fail preflight. Once the marker exists, managed content
drift is repaired from the registered snapshot. Foreign service overrides and
conflicting worker credentials are always rejected. The guard also verifies the live dedicated nftables table;
owned firewall drift can be repaired without flushing unrelated host rules.

An apply takes the same operator host lock as the foundation. Run it in a
maintenance window with CLI battles idle and no application rollout in progress:
the external lock excludes individual deployment commands, not an entire Jenkins
pipeline. Changed service content is activated through handlers; healthy unchanged
services are only checked/enabled. A deliberately changed release or desired
configuration is rejected by the immutable ownership contract. Bootstrap does not
provide an update/rollback command; changing these registered inputs needs a
separate reviewed workflow. Deleting ownership markers bypasses this protection
and is unsupported.

The worker's `BATTLEFIELD_AGENT_SECRET` is not taken from the Vault. Bootstrap
substitutes the value from the host's production `.env`
(`~/.config/autonomous-battlefield/production/.env`), read on the host and passed to
Ansible in a private vars file. [`battlefield-platform/cli`](cli-worker.md) applies
the same substitution and updates the ownership marker, so a secret rotation
reaches the worker without breaking these checks.

## Install services on a fresh, already provisioned CLI VM

For an existing isolated VM with no host bundle, register a new rendered bundle.
An inactive, unmodified Squid package-default configuration is also accepted: the
registration binds its SHA256 and preflight rechecks dpkg metadata before the first
replacement. Active or customized Squid services are refused; they are not silently
converted into a dedicated proxy. A pre-existing `/dev/null` mask is safe even
when Squid has not been installed yet; preflight still rejects real foreign units.

```bash
./scripts/infra/server.sh cli-register --local --cli-install
```

Set the optional `guest` object using
[`cli.example.json`](../../infra/ansible/cli.example.json). Required inputs are the
new guest machine ID and trusted admin key (automatically supplied by the
CLI VM host-local handoff when those two guest fields are empty); a worker binary from a verified
backend release; both matching Codex executables; their independently verified
SHA256 digests; the guest config and worker service source paths. Binary transfer,
account creation, permissions, units and activation are handled by Ansible.
For the managed handoff, set `"machine_id": ""` and `"identity_file": ""` in the
example's `guest` object. `known_hosts_file` is a managed connection field supplied
by CLI VM provisioning in memory (or saved during capture); do not add it by hand to the example.
Without a managed handoff, keep the example's explicit trusted connection fields.
Keep the copied guest configuration and unit sources owned by the controller user
with mode 0600. Use the [pinned release inputs](agent-vm.md#pinned-release-inputs)
to obtain artifacts. Optional native `claude` and `agy` executable artifacts can be registered too;
verify checks each installed CLI against the configured version family. Every
profile needs a unique `id` and nonempty `supportedVersionPrefix`; a failed version
probe names these declared labels without exposing CLI output. Use
standalone releases compatible with the target Ubuntu image. Bootstrap does not
install arbitrary provider package trees. Provider account login remains
interactive; follow the [Claude](claude-cli.md) and [Antigravity](antigravity-cli.md)
guides inside the isolated worker account, keeping profiles disabled until
isolation and provider checks pass.

Start from `infra/deploy/agent-vm/config.yaml` with **every profile disabled**, use
`infra/deploy/agent-vm/worker.service`, and set `adopt_existing: false`. Use the existing
`seal` command with a private manifest whose `cli_worker_environment` entry points
to a mode-0600 file containing only these three assignments:

```text
BATTLEFIELD_AGENT_SECRET=<existing dedicated worker control secret>
BATTLEFIELD_ENV=production
BATTLEFIELD_CLI_ISOLATION_VERIFIED=false
```

Never supply the application environment, capability-signing secret or kubeconfig
as guest inputs. The playbook rejects extra environment keys, public MCP/health
addresses and enabled fresh-install profiles. Both targets finish preflight before
either is changed. A host setup failure stops the guest play as well. Only Ubuntu 24.04/26.04 x86-64 is supported. The worker account's
existing home must match `/var/lib/battlefield-worker`; files beneath it are not
recursively reconciled. Provider device/browser login remains an interactive step
inside the VM, followed by the [worker isolation checks](agent-vm.md).
Fresh workers must stop at the enablement boundary below before advertising
availability or attempting accepted-plan checks.

## Managed profile enablement boundary

Fresh bootstrap requires every profile disabled and
`BATTLEFIELD_CLI_ISOLATION_VERIFIED=false`. Passing isolation checks does not
change these registered inputs. There is currently no managed command to enable
a freshly bootstrapped worker; keep its profiles disabled and the API's CLI
availability false until a reviewed configuration-update and rollback workflow
is available.

Do not hand-edit `/etc/battlefield-agent/worker.env` or `config.yaml`, or replace
the worker binary; use [`battlefield-platform/cli`](cli-worker.md) to sync the
worker secret and managed settings and to upgrade the worker. It updates the
ownership marker and the registration (`cli.json` and the `cli.guest/` copies) with
each change, so bootstrap stays consistent. Worker
environment drift fails preflight, and an apply restores configuration drift from
the registered snapshot. Re-registering with new inputs, including
`adopt_existing: true`, cannot update an owned installation: the marker binds the
original file hashes and rejects the changed contract. Do not remove markers to
bypass this check.

Initial adoption can preserve an **already enabled** installation only when its
existing files exactly match the registration, the guest isolation flag is true
and the configured CLI artifacts are present. That is preservation of existing
state, not a transition from disabled to enabled. Confirm its recorded isolation
evidence and complete the [accepted-plan and launch checks](agent-vm.md#verify-before-advertising-availability)
before advertising API availability. Neither adoption nor bootstrap verification
proves those launch gates passed.

## Validation and failure recovery

`cli-plan` uses check/diff mode and does not mutate targets. It validates current
ownership and reports file counts; it cannot prove package installation or service
startup on a fresh target. Secrets and configuration diffs are suppressed. Verify
checks desired files, active/enabled services, unauthenticated MCP rejection and,
when selected, the worker's loopback health endpoint. None of these invokes a model.

If Squid's package default changes between fresh registration and the first
apply, the recorded fingerprint intentionally refuses the new bytes. After
confirming with `sudo test ! -e /etc/battlefield-cli-bootstrap/host.json` that no
host apply has recorded ownership, run `cli-register --cli-install` with a new
`--cli-config /private/new-cli.json` to create a new registration, then pass that same
`--cli-config` to plan/apply. Keep the old bundle for comparison; do not delete
ownership markers or use this procedure after ownership has been recorded.

An interrupted installation can resume against its registered marker. Missing
managed files and ordinary owned drift are repaired; a differing worker secret
requires recovering the matching bundle, never automatic rotation. Existing VM
disks, provider homes, Kubernetes credentials and unrelated services are preserved.
Do not delete a marker to force an update or run fresh VM creation against retained resources.

For a disposable lifecycle test on a libvirt host with the pinned Ubuntu cloud
image already cached by the [foundation rehearsal](server-bootstrap.md):

```bash
python3 scripts/test/run-cli-bootstrap-rehearsal.py \
  --ubuntu 26.04 --evidence-dir /private/new-cli-evidence
```

Add `--existing-caddy` to exercise preservation of an unrelated existing Caddy
service instead of the fresh vendor-service mask, and `--preinstall-squid` to
exercise an inactive package default plus rejection of a modified default. The
test reboots once immediately after package installation is interrupted and again
after completion. It checks real Ansible package/service installation, shared
directory metadata preservation, failure barriers, nested libvirt identity
and filter inspection, repeat/check/verify runs, firewall drift, conflict refusal,
interrupted-install resumption, credential/login preservation and reboot. A
synthetic Kubernetes bridge and worker health process avoid production credentials
and provider calls. Production isolation and inference tests remain separate
requirements. The runner cleans up only its UUID-bound
VM and storage pool and records source hashes with the evidence.
