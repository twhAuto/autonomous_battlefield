# CLI worker operations

The CLI worker runs Claude, Codex and Antigravity subscription decisions on the
isolated `battlefield-agent` VM. Three things must hold for those battles to run:

- The VM is running.
- The host MCP bridge reaches the current AI gateway pod.
- The worker holds the same `BATTLEFIELD_AGENT_SECRET` as production.

The worker binary should also match the release production runs, so it speaks the
same protocol as the gateway and supports current settings. Codex, Claude Code and
Antigravity should match the releases pinned in
[`infra/deploy/agent-vm/cli-releases.json`](../../infra/deploy/agent-vm/cli-releases.json),
which their selectable models need.

Application delivery does not touch the VM. The manually started Jenkins job
`battlefield-platform/cli` keeps all of this in order. It runs
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

Create the job with the existing platform installer from a checkout of `main`. `--jobs-only` reuses the folder credentials and starts no build:

```bash
python3 scripts/infra/seed-platform.py --jobs-only
```

It asks privately for a Jenkins API token (`--token-file` accepts a private file instead).

The job uses the existing `host-sudo-password` folder credential and the global
`battlefield-ghcr` registry credential, which it needs to read the worker release.

## Verify, plan and apply

Open `battlefield-platform/cli` → **Build with Parameters**.

| Action | Effect |
| --- | --- |
| `verify` (default) | Read-only. Fails and lists every problem: VM state, host units, bridge reachability, whether the gateway accepts the production worker secret, guest identity, worker secret match, worker health, worker binary and CLI drift, a worker left draining, and the boot/watchdog units. A worker binary older than the production release, or a CLI other than its pinned release, is a notice: it is listed but does not fail. |
| `plan` | Records the repairs that `apply` would make, and the production release digest, in a private receipt valid for 30 minutes. |
| `apply` | Plans, pauses for the registered approver to approve, re-checks that the plan still matches and consumes the receipt. It then repairs, verifies, and fails if anything remains. Reports are archived even when the run fails. |

Apply repairs only what is needed:

- **`install-host-units`:** installs `battlefield-cli-vm-start.service`, which starts the VM after libvirt and the host firewall are ready and retries for two minutes. It also installs `battlefield-mcp-bridge-watchdog.service`, which restarts the bridge after three failed gateway checks.
- **`start-vm`:** starts the VM and waits for SSH.
- **`restart-bridge`:** restarts `battlefield-mcp-bridge`. Its `kubectl port-forward` stays bound to the gateway pod it started with, so it stops working after every gateway rollout until restarted. The watchdog does this automatically once installed.
- **`sync-worker-secret`:** replaces only the `BATTLEFIELD_AGENT_SECRET` line of `/etc/battlefield-agent/worker.env` with the value from the production `.env`, which Jenkins binds from its [`battlefield-production-application-env` credential](jenkins.md#production-application-configuration). It also updates the bootstrap ownership marker's fingerprint for that file and restarts `battlefield-worker`.
- **`restart-worker`:** restarts an inactive or unhealthy worker.
- **`enable-worker`:** re-enables a worker that is running but disabled, so it survives a reboot.
- **`resume-worker`:** resumes claims on a worker that an interrupted run left draining.
- **`upgrade-worker`:** installs the production worker release; see [worker upgrades](#worker-upgrades).
- **`upgrade-codex`, `upgrade-claude`, `upgrade-agy`:** install the pinned Codex, Claude Code or Antigravity release; see [CLI updates](#cli-updates).
- **`sync-worker-config`:** applies [`infra/deploy/agent-vm/managed-settings.json`](../../infra/deploy/agent-vm/managed-settings.json) to `/etc/battlefield-agent/config.yaml`. That is `agentConcurrency` (parallel CLI decisions), each CLI profile's `model`, and the managed profiles' `modelOptions` (including reasoning efforts). `null` removes `agentConcurrency`, so the worker runs one decision at a time. Workers older than the drain support reject the setting, so a value of 1–4 is applied only once the worker runs the production release, or in the same apply that upgrades it. Model options also require the production release binary before configuration sync. Only those fields change. When the worker runs the production release, it validates the new settings (`-check-config`) before they replace the file. The job drains the worker, then updates the guest file, its ownership-marker fingerprint and the host's registered copy (`cli.guest/config.yaml`) together, and restarts the worker.

Secret sync only proceeds when `worker.env` still matches its registered fingerprint
and contains only the three expected settings. A drifted `worker.env` blocks every
guest action, including restarts, so the worker never restarts on settings nobody
reviewed. Config sync only proceeds when the guest config matches the host's
registered copy. If a run is interrupted after updating the guest but before the
registered copy, the next plan proposes the sync again, and it completes without
rewriting the guest. `apply` performs exactly the reviewed repairs. Any other drift, such as a changed
isolation flag, is reported and left untouched; it needs a reviewed bootstrap change.

To change a CLI model, its selectable models/reasoning efforts, or the concurrency, edit `managed-settings.json` and the matching
profile in the application ConfigMap and VM template (a test keeps their models and options equal), merge and
promote, then run `apply`.

## Worker upgrades

The release is the `/app/agent` binary in the backend image that the in-cluster
`battlefield-agent` deployment runs. The job reads that deployment's digest-pinned
image reference through the registered kubeconfig and context in `cli.json`, after
checking that the cluster's `kube-system` UID matches the registration. It then
pulls the image by digest and atomically copies the binary into a private cache,
`~/.cache/autonomous-battlefield/cli-worker/<digest>`. The digest pins the
image content, so the copy matches production. Plan proposes `upgrade-worker` when
the guest binary is the registered one and differs from the release. An apply after
each backend release therefore upgrades the worker to the same code. If the release
cannot be read, verify reports the upgrade status as unknown and plan proposes no
upgrade.

`upgrade-worker` performs these steps in order:

1. Copies the release to the guest and checks its SHA-256.
2. Runs the new binary with `-check-config` against the installed configuration and
   worker environment. A rejected configuration stops the upgrade with nothing changed.
3. Drains the worker (below).
4. Replaces `/usr/local/bin/battlefield-agent` and its ownership-marker fingerprint,
   then restarts the worker.
5. Waits up to 90 seconds for the new worker to report healthy and not draining. If
   it does not, or step 4 fails, the job reinstalls the registered binary from the
   host copy and fails. If that restore fails too, it resumes claims and reports it.
6. Only then records the release as the registered artifact: the host copy in
   `cli.guest/battlefield-agent` and its SHA-256 in `cli.json`, which the CLI
   bootstrap checks.

If a run stops after step 4, the next plan proposes the upgrade again. That run
finds the release installed and finishes the registration. A guest binary that
matches neither the registration nor the release is reported and never replaced.

### Draining

Restarting the worker cancels running decisions. Each reports its failure as the
worker stops, so the engine falls back for that turn without waiting out the decision
timeout.
Before a restart for a new worker binary, CLI release or settings, the job waits
until no CLI decision is running, for at most five minutes:

- **Workers with drain support** serve `GET /status` on the
  guest's health port with `draining`, `active` and `concurrency`. The job sends
  `SIGUSR1`, so the worker stops claiming and finishes running decisions, then
  waits for `active` to reach 0. On a timeout, or if a later step fails, it sends
  `SIGUSR2` to resume claims and reports that nothing was changed.
- **Older workers** cannot stop claiming, and `SIGUSR1` would stop them. The job
  never signals them. It waits until the worker has no child processes in two checks
  in a row, then restarts at once. A decision claimed in that moment still falls back.
  This applies only to upgrading a worker that predates drain support.

Secret syncs and restarts of an unhealthy worker do not drain: such a worker cannot
claim decisions.

`reach-guest` means the job cannot log in with the automation key. Run the
authorization step, then plan again. A guest whose machine or hardware identity
differs from the registration is never changed.

All runs hold the `battlefield-production-platform` lock, so they never overlap a
production rollout. Reports are archived as `cli-<action>.json`; they contain
states and fingerprint matches, never secrets. The secret reaches the guest only on
SSH stdin.

## CLI updates

[`infra/deploy/agent-vm/cli-releases.json`](../../infra/deploy/agent-vm/cli-releases.json)
pins the release of each CLI on the guest, with the digest its publisher lists:

| CLI | Pin | Source |
| --- | --- | --- |
| Codex (`codex`, required) | Version and the SHA-256 of both Linux amd64 musl archives, `codex` and its `codex-code-mode-host` helper | The `openai/codex` GitHub release assets |
| Claude Code (`claude`) | Version and the SHA-256 of the `linux-x64` executable | `https://downloads.claude.ai/claude-code-releases/<version>/manifest.json` |
| Antigravity (`agy`) | Version, the build's archive URL and its SHA-512 | The manifest Antigravity's installer reads, `https://antigravity-cli-auto-updater-974169037036.us-central1.run.app/manifests/linux_amd64.json`; the archive holds one executable, `antigravity`, installed as `agy` |

Verify and plan download each pinned release on the host once, into a private cache
in `~/.cache/autonomous-battlefield/<cli>`, and check it against its pin every time
they read it. A cached file that fails the check is deleted and downloaded again on
the next run. The guest needs no access to these download hosts. If a release cannot
be downloaded or does not match, verify reports that CLI's status as unknown and plan
proposes no update for it.

Plan proposes `upgrade-<cli>` when the guest's executables in `/usr/local/bin` are
the registered ones and differ from the pinned release, or when the registered config
does not yet accept its version. A Claude Code or Antigravity executable installed
after the CLI bootstrap registered the guest is adopted by its update (step 1 below);
a guest without the executable gets nothing installed. Only the production worker
release reports which profiles it serves, so updates wait until the worker runs that
release, or run in the same apply right after `upgrade-worker`. Codex, Claude Code and
Antigravity update in that order, each finishing before the next.

Each update performs these steps in order:

1. For an executable nobody registered, copies it from the guest to the host beside
   the other registered artifacts (`cli.guest/<executable>`), checks it against the
   SHA-256 the inspection read, adds it to the guest's ownership marker, then
   registers it in `cli.json`, so that a failed update can restore it.
2. Copies the pinned executables to the guest, checks their SHA-256, and checks that
   the staged executable's `--version` prints the pinned version. The check runs with
   an empty home and the CLI's self-update turned off. A mismatch stops the update
   with nothing changed.
3. Records which of the CLI's enabled profiles the worker serves in `GET /status`,
   then drains the worker.
4. Replaces the executables and sets the `supportedVersionPrefix` of that CLI's
   profiles in `/etc/battlefield-agent/config.yaml`, with their ownership-marker
   fingerprints, then restarts the worker. The worker accepts only a CLI whose version
   starts with that prefix, so the executables and the prefix change together. Codex
   and Antigravity accept the release's minor version family, for example
   `codex-cli 0.159.` for 0.159.2 and `1.5.` for 1.5.9. Claude Code accepts only its
   exact release, for example `2.1.325 `, because each release maps the model aliases
   to fixed models.
5. Waits up to 90 seconds for the worker to report healthy, not draining and serving
   every profile from step 3. A profile it did not serve before, such as one whose
   login lapsed, does not block the update. If the worker does not, or step 4 fails,
   the job reinstalls the registered executables and config from the host copies and
   fails.
6. Only then records the release: the host copies in `cli.guest/<executable>`, their
   SHA-256 in `cli.json`, and the registered `cli.guest/config.yaml`.

A run interrupted after step 4 is proposed again by the next plan and finishes the
registration. When an interrupted settings sync left the guest config ahead of the
registered copy, the update starts from the synced settings, so it records both
changes. Executables that match neither the registration nor the pinned release are
reported and never replaced. Provider logins in the worker's home are not touched;
sign in again with the [provider login steps](cli-worker-verification.md#provider-login)
when one lapses.

To change a release, including a rollback, set its version and digests in
`cli-releases.json` from the sources above. Antigravity's manifest names only the
latest build, so keep an earlier build's URL and digest from this file's history to
roll back. Set the matching `supportedVersionPrefix` on that CLI's profiles in
`backend/config.yaml`, the application ConfigMap and the VM template, and the local
minimum in `scripts/dev/run-k8s-cli-agent.sh`; a test keeps them equal. Check every
selectable model of that CLI with the new release, then merge, promote and run `apply`.

## Secret rotation

`BATTLEFIELD_AGENT_SECRET` in the production `.env` is the single source of truth.
After rotating it and [updating the Jenkins credential](jenkins.md#production-application-configuration),
run the main delivery (application pods) and then `apply` on this
job (CLI worker). The CLI bootstrap uses the same value, so its ownership checks stay
consistent with the job's changes.

## Main pipeline check

After production rollout, the main pipeline runs `cli-worker.py verify`. A
problem marks the build **unstable** and archives `cli-worker-verify.json`. It
never fails delivery or triggers a rollback. Repair with this job. A worker binary
older than the new release, or a CLI other than its pinned release, only prints a
notice; run `apply` to update them.

## Not covered yet

- Installing a CLI the guest does not have.
- A manual rollback to an earlier worker release. A failed upgrade restores the
  registered binary automatically, but a release that passes its health check
  stays installed until a later release replaces it.
- Provider subscription logins, which are still interactive inside the worker account.

## Model selection rollout

The gateway requires `selectionVersion: 1` in agent claims. Old workers receive
HTTP 409 before leasing jobs. The supported Jenkins job extracts the worker from
the **deployed production image**, so this release needs a short CLI maintenance
window: finish active CLI battles and pause new CLI battles, promote `dev` to
`main` and let production delivery complete, then run `battlefield-platform/cli`
with `ACTION=plan`, review its repairs, and run `ACTION=apply`. Its approval step
precedes the upgrade and configuration sync. Until that finishes, old workers
cannot claim from the new gateway. Run `ACTION=verify` and a CLI battle before
resuming normal use. This PR does not deploy or restart the production VM.

Keep `modelOptions` and each model's `reasoningEfforts` synchronized across
`backend/config.yaml`, the application ConfigMap, the VM's `config.yaml`, and
`managed-settings.json`, plus the host launcher `scripts/dev/run-k8s-cli-agent.sh`.
The [shipped catalog](../architecture/ai-decisions.md#model-selection-and-latency)
lists the choices and their efforts.
Options are operator-approved capabilities, not account-access guarantees; validate
provider/account access before advertising changes. The default model and default
effort remain unchanged. Antigravity has no separate effort selector.

`sync-worker-config` installs the managed model options after the worker upgrade
and validates them with `agent -check-config`. Other profile settings, including
credentials, enabled status and version constraints, remain under their existing
ownership. A catalog/worker mismatch fails visibly as `model_selection_unavailable`.
Verify a battle for each advertised model/effort and confirm selection, accepted
plan and timing in history before enabling users. Use full provider model IDs when
version reproducibility matters; aliases can move.

## Context prefetch comparison

First, the [comparison harness](../developer/ai-comparisons.md#live-comparisons)
can run matched frozen synthetic contexts without changing a cluster. Its total
session timings include worker completion and are separate from battle timings.

Prefetch is the default for Codex/Claude battle profiles on every CLI worker,
including production. On the registered test cluster, start a tool-fetch baseline
with `HOST_CLI_PREFETCH_CONTEXT=false ./scripts/dev/run-k8s-cli-agent.sh codex` (or
`claude`); `true` or an omitted variable gives prefetch. It is validated as
`true`/`false` and written to the selected Codex/Claude profiles as `prefetchContext`.
API workers and background generation retain their existing delivery paths.
Do not run baseline and prefetch workers against the same profile concurrently:
finish current battles and drain the existing worker before switching modes.

Run short battles using identical scenario, factions, seed, model, effort and
concurrency. Compare accepted-plan median/p95, failure/fallback count and tokens;
record the code/prompt revision and `contextDelivery`. The context interval ends
before CLI startup in prefetch mode and cannot by itself establish a speedup.
Prompt v11 applies `tables-v1` compaction automatically whenever an encoding is
smaller. There is no profile, session or environment toggle for compaction, and
it is independent of the opt-in prefetch delivery mode described above. The
comparison harness has no compaction variant: compare the old and new code/prompt
revisions against the same frozen observations to evaluate this format change.
Offline byte savings do not establish token savings or model decision quality.

Deploy compact-capable workers first, then the gateway. New workers accept both
context shapes; older prefetch workers can reject compact catalogs. The gateway
starts serving the new format immediately on deployment. A coordinated deployment
must ensure no old prefetch worker receives context from the new gateway. Full IDs
and submission arguments are unchanged. If quality regresses, rollback requires
reverting the compaction code and matching decoding instructions, assigning a new
prompt version, and redeploying; there is no runtime switch. Keep the new workers
while reverting the gateway because they also accept the old shape.

From prompt v10 (retained in v11), prefetch puts frozen reference data ahead of identity and changing
state, and removes repeated rules only for reviewed mechanics. Record serialized
bytes separately from model tokens, and reported cached input separately from
uncached input; a longer identical prefix alone does not establish a cache hit.
API and tool-fetch CLI paths still put a request ID before the context tool result.
Restart the worker without the `false` override after a baseline comparison. A
production worker adopts prefetch when its binary is upgraded, because its
configuration omits the setting. To return a production profile to tool-fetched
context, add `prefetchContext: false` to that profile in the guest
`/etc/battlefield-agent/config.yaml` and restart the worker; the Jenkins managed
settings sync does not own this field.
