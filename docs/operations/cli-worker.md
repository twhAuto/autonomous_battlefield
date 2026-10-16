# CLI worker operations

The CLI worker runs Claude, Codex and Antigravity subscription decisions on the
isolated `battlefield-agent` VM. Three things must hold for those battles to run:

- The VM is running.
- The host MCP bridge reaches the current AI gateway pod.
- The worker holds the same `BATTLEFIELD_AGENT_SECRET` as production.

Application delivery does not touch the VM. The manually started Jenkins job
`battlefield-platform/cli` keeps all three in order. It runs
[`scripts/infra/cli-worker.py`](../../scripts/infra/cli-worker.py) on
`battlefield-production` as the registered operator. It needs the CLI VM and guest
already registered through [CLI VM provisioning](cli-vm-platform.md) and
[CLI bootstrap](cli-bootstrap.md).

## One-time authorization

The registered VM admin key is passphrase-protected, so Jenkins uses a separate,
revocable key. On the physical host, from a checkout of `main`, unlock the admin
key and authorize the automation key once:

```bash
eval "$(ssh-agent -s)"
ssh-add -t 10m "$HOME/.ssh/battlefield-vm-admin"
python3 scripts/infra/cli-worker.py authorize
```

This creates `~/.ssh/battlefield-cli-automation` (mode 0600, no passphrase) and adds
its public key to `vm-admin`'s `authorized_keys`, pinned to the registered guest host
key. It then checks that `vm-admin` has passwordless sudo, which the job needs. To
revoke the key, delete the line ending in `battlefield-cli-automation` from
`~vm-admin/.ssh/authorized_keys`.

The VM must be running for this step. If it is not, start it once with
`virsh -c qemu:///system start battlefield-agent`; after the first apply, the boot
unit below starts it automatically.

## Seed the job

After this change reaches `main`, create the job with the existing platform
installer. `--jobs-only` reuses the folder credentials and starts no build:

```bash
python3 scripts/infra/seed-platform.py --jobs-only
```

It asks privately for a Jenkins API token (`--token-file` accepts a private file instead).

The job uses only the existing `host-sudo-password` folder credential.

## Verify, plan and apply

Open `battlefield-platform/cli` → **Build with Parameters**.

| Action | Effect |
| --- | --- |
| `verify` (default) | Read-only. Fails and lists every problem: VM state, host units, bridge reachability, whether the gateway accepts the production worker secret, guest identity, worker secret match, worker health, and the boot/watchdog units. |
| `plan` | Records the repairs that `apply` would make, in a private receipt valid for 30 minutes. |
| `apply` | Plans, pauses for `whtan` to approve, re-checks that the plan still matches and consumes the receipt. It then repairs, verifies, and fails if anything remains. Reports are archived even when the run fails. |

Apply repairs only what is needed:

- **`install-host-units`:** installs `battlefield-cli-vm-start.service`, which starts the VM after libvirt and the host firewall are ready and retries for two minutes. It also installs `battlefield-mcp-bridge-watchdog.service`, which restarts the bridge after three failed gateway checks.
- **`start-vm`:** starts the VM and waits for SSH.
- **`restart-bridge`:** restarts `battlefield-mcp-bridge`. Its `kubectl port-forward` stays bound to the gateway pod it started with, so it stops working after every gateway rollout until restarted. The watchdog does this automatically once installed.
- **`sync-worker-secret`:** replaces only the `BATTLEFIELD_AGENT_SECRET` line of `/etc/battlefield-agent/worker.env` with the value from `~/.config/autonomous-battlefield/production/.env`. It also updates the bootstrap ownership marker's fingerprint for that file and restarts `battlefield-worker`.
- **`restart-worker`:** restarts an inactive or unhealthy worker.
- **`enable-worker`:** re-enables a worker that is running but disabled, so it survives a reboot.
- **`sync-worker-config`:** applies [`infra/deploy/agent-vm/managed-settings.json`](../../infra/deploy/agent-vm/managed-settings.json) to `/etc/battlefield-agent/config.yaml`. That is `agentConcurrency` (parallel CLI decisions) and each CLI profile's `model`. `agentConcurrency` is currently `null`, which removes the setting: the adopted worker binary on the VM predates it, rejects a config containing it, and runs one decision at a time. Set it to 1–4 only after the worker binary is upgraded (AB-139 phase 2). Only those fields change. The job updates the guest file, its ownership-marker fingerprint and the host's registered copy (`cli.guest/config.yaml`) together, then restarts the worker. A restart ends any CLI decision in progress, which falls back for that turn; apply when no CLI battle is active.

Secret sync only proceeds when `worker.env` still matches its registered fingerprint
and contains only the three expected settings. A drifted `worker.env` blocks every
guest action, including restarts, so the worker never restarts on settings nobody
reviewed. Config sync only proceeds when the guest config matches the host's
registered copy. If a run is interrupted after updating the guest but before the
registered copy, the next plan proposes the sync again, and it completes without
rewriting the guest. `apply` performs exactly the reviewed repairs. Any other drift, such as a changed
isolation flag, is reported and left untouched; it needs a reviewed bootstrap change.

To change a CLI model or the concurrency, edit `managed-settings.json` and the matching
profile in the application ConfigMap (a test keeps their models equal), merge and
promote, then run `apply`.

`reach-guest` means the job cannot log in with the automation key. Run the
authorization step, then plan again. A guest whose machine or hardware identity
differs from the registration is never changed.

All runs hold the `battlefield-production-platform` lock, so they never overlap a
production rollout. Reports are archived as `cli-<action>.json`; they contain
states and fingerprint matches, never secrets. The secret reaches the guest only on
SSH stdin.

## Secret rotation

`BATTLEFIELD_AGENT_SECRET` in the production `.env` is the single source of truth.
After rotating it, run the main delivery (application pods) and then `apply` on this
job (CLI worker). The CLI bootstrap uses the same value, so its ownership checks stay
consistent with the job's changes.

## Main pipeline check

After production rollout, the main pipeline runs `cli-worker.py verify`. A
problem marks the build **unstable** and archives `cli-worker-verify.json`. It
never fails delivery or triggers a rollback. Repair with this job.

## Not covered yet

Worker binary releases, CLI tool updates, drain before restart and rollback remain
AB-139 phase 2. Provider subscription logins are still interactive inside the worker
account.
