# CLI worker verification

The CLI worker runs subscription CLI decisions (Codex, Claude and Antigravity) in
the isolated `battlefield-agent` VM. This page explains how the pieces fit, how to
reach the VM, which release inputs and logins it needs, and the isolation gate that
must pass before any CLI profile is advertised. Run commands from the repository
root unless a step says otherwise, stop active test battles before restarting
services, and never copy the host kubeconfig or Kubernetes signing secret into the
VM.

| Task | Guide |
| --- | --- |
| Create or adopt the VM, its isolated network and NIC filter | [CLI VM provisioning and adoption](cli-vm-platform.md) |
| Install or adopt the host bridge, proxy, firewall and guest worker files | [CLI worker bootstrap](cli-bootstrap.md) |
| Reach the VM, gather release inputs, sign in to providers, pass the isolation gate | This page |
| Keep a registered worker running: secret sync, managed settings, upgrades, bridge repair | [CLI worker operations](cli-worker.md) (`battlefield-platform/cli`) |

The provisioning and bootstrap workflows own image pins, capacity checks, SSH
trust, service activation and interruption recovery. Neither enables public CLI
availability or provisions provider logins. Preserve existing disks, provider
homes, bridge overrides and the matched proxy/bridge pair, and never delete
ownership markers or rerun fresh creation against retained resources.

## What runs where

The CLI worker has its own KVM VM. A separate KVM VM runs Kubernetes (K3s), the
application, engine, AI gateway and database. Docker remains on the physical
Ubuntu host for CI and the retained old Kind cluster.

```text
Physical Ubuntu PC
  KVM battlefield-k3s -> Kubernetes -> Battlefield services and database
  Docker -> CI tooling; retained Kind recovery data
  Squid 192.168.234.1:3128 -> approved HTTPS provider domains
  Caddy 192.168.234.1:18084 -> host loopback kubectl bridge -> AI gateway :8084
  KVM battlefield-agent 192.168.234.2
    vm-admin: administration
    battlefield-worker: worker, provider logins, Codex (+ code-mode helper),
                        Claude and Antigravity CLIs for enabled profiles
```

The worker claims a request through the bridge. The CLI receives a scoped
capability, reads the decision context through the AI gateway's MCP tools, calls
its provider through Squid, and submits a plan through those tools. Acceptance and
execution are separate: the engine executes accepted orders, and a plain model
reply is not a submitted battle plan.

The renderer uses loopback port 28084 by default, matching the existing server's
`20-loopback.conf` and Caddy upstream. `--bridge-port` selects another free
unprivileged port and changes both rendered files together. Keep the bridge
separate from Jenkins smoke ports 18081–18085; 18085 is the production frontend
liveness check, so choosing it requires changing `SMOKE_TEST_FRONTEND_PORT` in the
Jenkins job first. The bridge reads the registered production kubeconfig;
restarting it after K3s profile adoption retargets the connection without
reinstalling the worker or copying cluster credentials into the VM. See
[moving production to a new cluster](k3s-delivery.md#move-production-to-a-new-cluster).

The host units keep their pre-rename names (`battlefield-mcp-bridge` and
`battlefield-mcp-proxy`), and the bridge still forwards `service/battlefield-mcp`.
In-cluster agents use the same name through `ai.mcpUrl`. That Service is an alias
for the AI gateway: the deploy script points it at the new pods only once their
rollout is ready, then retires the legacy Deployment. It is removed after the units
and `ai.mcpUrl` switch to `battlefield-ai-gateway`.

## Isolation boundary

The VM has **no default route, NAT, DNS server, host mounts or Docker socket**.
`bf-agent0` is `192.168.234.1/24` and the guest is `192.168.234.2`. The external
NIC filter permits only guest connections to the host proxy on 3128 and the AI
gateway bridge on 18084, plus operator SSH from the host; IPv6 is dropped. The
host firewall replaces only its owned table, leaving unrelated Docker and libvirt
rules and public Caddy configuration intact, and keeps a narrow loopback exception
for Jenkins smoke forwards on `127.0.0.1:18084`.

- **Proxy:** Squid resolves approved provider and update names, so the guest needs
  no direct DNS or internet access; HTTPS package updates use it too. Review the
  exact provider allowlist, and never broadly permit HTTPS or remove the private-IP
  deny.
- **Bridge:** Caddy binds `192.168.234.1:18084` and forwards to the loopback-only
  kubectl port-forward, setting the upstream Host to `127.0.0.1:8084`, because the
  Go MCP SDK rejects a non-loopback Host. Bearer authentication and MCP
  origin/localhost protection stay enabled. Never use a direct guest-facing kubectl
  forward or disable SDK protection globally.
- **Credentials:** the kubeconfig and deployment credentials never enter the VM.
  Bootstrap installs `/etc/battlefield-agent/worker.env` as **root:root 0400**
  containing only `BATTLEFIELD_AGENT_SECRET`, `BATTLEFIELD_ENV=production` and the
  isolation flag. Never put the capability-signing secret, database URLs, API
  passwords, personal files, SSH keys or deployment credentials in the worker's
  home.
- **Process isolation:** Linux workers disable dumpability so same-user CLI
  children cannot read the parent's environment or memory. Children receive an
  explicit allowlist of transport and login settings plus their scoped capability.

The VM's 2 vCPU / 4 GiB / 24 GiB allocation is a one-slot starting size, not
validated four-CLI capacity. Leave CLI disabled if the host cannot sustain the
game, database, observability and one CLI together.

## Access the provisioned VM

From the physical host:

```bash
guest_key="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["identity_file"])' \
  "$HOME/.local/state/battlefield/cli-vm/handoff.json")"
ssh -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes \
  -o UserKnownHostsFile="$HOME/.local/state/battlefield/cli-vm/known_hosts" \
  -i "$guest_key" vm-admin@192.168.234.2
```

The [managed handoff](cli-vm-platform.md#automatic-ansible-handoff) supplies the
trusted key path and host keys: fresh creation generates a host-local admin key,
while adoption preserves the existing one. Unlock an encrypted adopted key in
the host's SSH agent first; do not substitute a new key or accept unknown host keys.

Inside the VM:

```bash
sudo cloud-init status --wait
ip -brief address
ip route
getent passwd battlefield-worker
```

`vm-admin@battlefield-agent` is the VM administrator. `sudo -iu battlefield-worker`
switches to the worker account in the same VM; it is not another SSH connection.
`exit` returns to vm-admin, and another `exit` returns to the physical host. When
accessing the PC remotely, the initial SSH to the PC is one additional hop.

## Pinned release inputs

A fresh bootstrap needs these artifacts. Once a worker is registered,
[`battlefield-platform/cli`](cli-worker.md#worker-upgrades) upgrades its binary to
each production release.

On the host, obtain the `battlefield-agent` binary from the same verified backend
release deployed in production. For example, extract `/app/agent` from the running
AI gateway container into a private staging directory; check the command succeeds before
registering the file. Record its checksum for the CLI bootstrap guest inputs;
Ansible installs it root-owned at `/usr/local/bin/battlefield-agent`, mode 0755.

The supported Codex release is **0.155.0**; the supplied profile requires the
`codex-cli 0.155.` version family (0.152 is rejected by the provider). Download **both** version-matched Linux amd64
musl assets from the official `openai/codex` release on the host:

- `codex-x86_64-unknown-linux-musl.tar.gz`
- `codex-code-mode-host-x86_64-unknown-linux-musl.tar.gz`

Use the release's published digests/provenance to verify each asset. A versioned
URL alone is not checksum verification. The helper archive's release SHA256 for
0.155.0 is `328c1bebe09fc727053794576efea353d3b18381ee8883f5283b37ae4a7854d4`.
The main archive's release SHA256 is
`e415cc3adb94ade16e8d44b4dd58a9201cc34b2ee51a5d6eddf2a3a00aecb6c0`
(GitHub release digests, re-verified by download on 2026-09-29).
Retain both expected digests with your release inventory. For this exact release,
on the host (do not reuse these hashes for another version):

```bash
(
set -eu
mkdir -p "$HOME/battlefield-cli-install"
cd "$HOME/battlefield-cli-install"
base=https://github.com/openai/codex/releases/download/rust-v0.155.0
for name in codex codex-code-mode-host; do
  curl --fail --location --output "$name-x86_64-unknown-linux-musl.tar.gz" \
    "$base/$name-x86_64-unknown-linux-musl.tar.gz"
done
sha256sum --check <<'SUMS'
e415cc3adb94ade16e8d44b4dd58a9201cc34b2ee51a5d6eddf2a3a00aecb6c0  codex-x86_64-unknown-linux-musl.tar.gz
328c1bebe09fc727053794576efea353d3b18381ee8883f5283b37ae4a7854d4  codex-code-mode-host-x86_64-unknown-linux-musl.tar.gz
SUMS
for name in codex codex-code-mode-host; do
  tar -xzf "$name-x86_64-unknown-linux-musl.tar.gz"
done
)
```

Supply the extracted binaries, their independently verified SHA256 digests, the
worker binary and disabled configuration to
[CLI bootstrap's fresh-service inputs](cli-bootstrap.md#install-services-on-a-fresh-already-provisioned-cli-vm).
Ansible transfers the artifacts and installs the worker, matching Codex/helper,
isolation verifier, configuration and service unit. Its encrypted guest environment
contains only the dedicated worker secret, production environment and initial
false isolation flag. Every fresh profile stays disabled until verification.
Never copy the complete application environment or Kubernetes Secret into the VM.

## Provider login

Inside the VM, switch to `battlefield-worker`, set transport variables and perform
the interactive login for the dedicated account:

```bash
sudo -iu battlefield-worker
export HTTPS_PROXY=http://192.168.234.1:3128
export HTTP_PROXY=http://192.168.234.1:3128
export NO_PROXY=192.168.234.1,127.0.0.1,localhost
codex -c 'cli_auth_credentials_store="file"' login --device-auth
codex login status
```

For an enabled Claude profile, use the same shell and transport variables, then
sign in with the Claude subscription by opening the printed URL in any browser and
pasting the code back:

```bash
claude auth login
claude auth status --json
```

The status must show `"loggedIn": true` and `"authMethod": "claude.ai"`; the worker
rejects API-key and Console logins. A worker whose saved login has lapsed reports
`"loggedIn": false` and fails every Claude decision even though its version and
profile are correct, so check this status first when Claude decisions fail. The
worker checks logins when it starts, so after signing in again return to vm-admin
and run `sudo systemctl restart battlefield-worker`. Worker logs are under the
`battlefield-worker` unit.

Return to vm-admin with `exit`. This login step is deliberately interactive;
type the commands one at a time rather than pasting them with the SSH command,
because lines pasted ahead of the remote shell or a `sudo` prompt are lost. Never
bake login tokens into the image or rendered bundle.

## Isolation gate

No CLI profile may be advertised until this gate passes. Record the evidence from
each step; it is what justifies setting `BATTLEFIELD_CLI_ISOLATION_VERIFIED=true`.
The [production access runbook](access.md) owns the surrounding access and limit
controls.

Keep **every VM profile disabled** and the **API's CLI availability false**
throughout the boundary checks.

1. Inside the VM, check an allowed and a denied destination through Squid, and the
   bridge:

   ```bash
   curl --noproxy '' --proxy http://192.168.234.1:3128 --connect-timeout 10 --max-time 30 -I https://archive.ubuntu.com/ubuntu/
   curl --noproxy '' --proxy http://192.168.234.1:3128 --connect-timeout 10 --max-time 30 -I https://example.com/
   curl --noproxy '*' --connect-timeout 10 --max-time 15 -sS -o /dev/null -w 'HTTP %{http_code}\n' http://192.168.234.1:18084/mcp
   ```

   Expect success, a denied CONNECT (403) and an unauthenticated MCP 401. **A 401
   does not prove MCP works**: capability authentication runs before the SDK Host
   check, so only an accepted plan (step 6) tests that path.

2. In the vm-admin shell, start the worker and run the isolation verifier. Set
   `ROUTER_IP` and `HOST_LAN_IP` to the real LAN addresses of your router and the
   physical host, not the VM bridge addresses; add a `--blocked-host` for any other
   private administration service.

   ```bash
   : "${ROUTER_IP:?Set ROUTER_IP to your router LAN address}"
   : "${HOST_LAN_IP:?Set HOST_LAN_IP to your physical host LAN address}"
   sudo systemctl enable --now battlefield-worker.service
   worker_pid="$(sudo systemctl show battlefield-worker.service --property=MainPID --value)"
   sudo -u battlefield-worker python3 /usr/local/bin/verify-agent-isolation.py \
     --worker-pid "$worker_pid" --blocked-host "$ROUTER_IP" --blocked-host "$HOST_LAN_IP"
   curl --noproxy '*' -sS -o /dev/null -w 'HTTP %{http_code}\n' http://127.0.0.1:8083/healthz
   ```

   Health should return 204.

3. On the host, inspect the NIC filter attachment, the firewall table and services:

   ```bash
   sudo virsh --connect qemu:///system dumpxml battlefield-agent
   sudo nft list table inet battlefield_agent
   sudo systemctl is-active battlefield-agent-firewall battlefield-mcp-bridge battlefield-mcp-proxy squid
   ```

4. Test real provider requests through the proxy, and try private IPv6
   destinations when the network has them. Check each installed CLI's actual
   version against its profile's `supportedVersionPrefix`.
5. Test hostile input: faction text that requests extra tools, another request ID,
   files or network destinations. The server must reject unrelated MCP contexts,
   and the VM boundary must hold however the model responds.
6. With a profile enabled for administrators only (see below), run a battle with
   two model factions on a single slot. One faction may fall back if both cannot
   finish before the window deadline, but the battle must continue and the slot
   must never overlap; bounded deadlines deliberately win over waiting.

A **freshly bootstrapped** worker cannot pass beyond step 5 yet: bootstrap
registers every profile disabled with the isolation flag false, and there is no
managed way to change that. Do not hand-edit its environment or profiles; see the
[managed profile enablement boundary](cli-bootstrap.md#managed-profile-enablement-boundary).

Only after recording these checks, set `BATTLEFIELD_CLI_ISOLATION_VERIFIED=true` in
the API's private AI configuration and enable the desired CLI availability flag.
Production startup rejects advertised CLI profiles without this flag, but the flag
is an operator attestation, not proof of isolation. Invite Developers only after
the gate passes; Players can be invited earlier while CLI stays off.

## Verify before advertising availability

These launch checks apply to an existing enabled installation that was adopted
with matching files and previously recorded isolation evidence.

1. Confirm the VM environment already has `BATTLEFIELD_CLI_ISOLATION_VERIFIED=true`
   and only the intended Codex profile is enabled.
2. In the host's private production `.env`, set that flag and
   `CODEX_CLI_PROFILE_AVAILABLE=true`, leaving Gemini and Claude false.
3. Run `./scripts/ops/prepare-limited-production.sh`, which changes nothing live,
   then deploy the configuration through `main` with `APPLY_CONFIGURATION=true`.
   Keep initial profile access limited to administrators.
4. Run one Codex-versus-Scripted battle. Check that the opening and tick 10 both
   have accepted plans with no fallback or error, and that CLI usage is eventually
   populated (token usage can arrive after acceptance). Check execution separately
   in the battle event stream.

A single successful run does not establish latency, capacity, reboot recovery,
cancellation or two-player behavior; test those separately before wider access.

## Troubleshooting and recovery

| Symptom | Check or correction |
| --- | --- |
| `qemu-kvm` has no candidate | Install the explicit `qemu-system-x86` package. |
| Worker connection refused during boot | Inspect bridge/proxy logs and listeners; both services retry, as does the worker. |
| `invalid Host header "192.168.234.1:18084"` | Verify Caddy owns that listener, kubectl owns 127.0.0.1:28084 (or the configured bridge port), and the Host rewrite is installed. |
| Plain reply works, code-mode host missing | Install the matching helper beside Codex; a no-tools reply does not test MCP. |
| `plan_not_submitted` | It means no accepted plan, not a specific root cause. Check MCP tool rejection logs and private CLI diagnostics. |
| `provider_rate_limited` or `cli` failures within seconds | Read the `error` field of the worker's `decision_runner_failed` log: it holds the CLI's own message, one line of at most 240 characters with network addresses, hostnames and bearer credentials redacted. A rate or usage limit is a subscription allowance, often per model; choose another model until it resets. |
| Profile On but unavailable | Check deployed availability/isolation flags, worker connectivity and role policy; editing `.env` alone does not deploy it. |
| Jenkins localhost 18084 timeout | Preserve the narrow firewall loopback exception; do not flush firewall rules. |
| cAdvisor IP SAN failure | Separate Kubernetes serving-certificate issue; inspect the live kubelet cert and trusted CA. Do not disable TLS verification or approve an uninspected CSR. |

Host diagnostics:

```bash
sudo journalctl -u battlefield-mcp-bridge -u battlefield-mcp-proxy --since '10 minutes ago' --no-pager
sudo ss -lntp '( sport = :3128 or sport = :18084 or sport = :28084 )'
```

VM diagnostics:

```bash
sudo journalctl -u battlefield-worker --since '10 minutes ago' --no-pager -o cat
```

Use the same UTC time window and request ID in the AI gateway pod logs. The worker's
normal journal omits raw CLI output. If capturing it temporarily, keep it under the
worker's private home with mode 0600, never log environment variables or capability
tokens, inspect locally and redact before sharing. Record the exact temporary
drop-ins and private logs you create. After the investigation, remove only those
diagnostic files and restore the normal worker configuration. If a systemd
drop-in changed, run `sudo systemctl daemon-reload` and restart the worker during
a maintenance window, then repeat its health and accepted-plan checks.

For a planned restart test, stop battles, reboot during a maintenance window, then
repeat listener, firewall, health and accepted-plan checks. Verify the VM/network
and all four host services are enabled. Repeat the isolation gate's
two-model-faction test and the [stop-switch](access.md#limits-and-the-stop-switch)
cancellation check before broader access. Keep durable configuration, release digests
and the host bundle in operator records; keep secrets in private stores. This guide
and the generator replace ad hoc unit-file editing, not the verification gates.

## Concurrent decisions

`ai.agentConcurrency` sets how many independent jobs one worker runs, across all
enabled CLI providers rather than per provider. The worker accepts 1–8 and an
omitted value means one slot; the managed VM settings accept 1–4 until more slots
are verified on the VM. The VM template sets 4, but the managed VM
currently omits it ([`managed-settings.json`](../../infra/deploy/agent-vm/managed-settings.json)
has `agentConcurrency: null`), so it runs one job at a time. The database caps CLI
executions across all workers, decisions and background jobs at
`runtime_policy.cli_concurrency` (default 4).

Before raising the managed value, exercise four concurrent CLI sessions on the VM
and check memory and task headroom, that saved logins stay valid through concurrent
refreshes, and that provider throttling does not cause repeated fallbacks. A
successful single session does not establish this; increase VM resources
separately if the current limits cannot sustain four.

To change it, never copy a binary or edit the configuration by hand. Run `apply`
on [`battlefield-platform/cli`](cli-worker.md) so the worker runs the production
release, set `agentConcurrency` in `managed-settings.json`, merge and promote, and
run `apply` again; the job drains running decisions before each restart. Then
check that the `agent_worker_ready` log reports the new concurrency and health
returns 204. Use independent queued decisions to observe overlap: a single decision
stays sequential, and a slot stays occupied until execution completes, even after
its plan is accepted. While testing, watch
`systemctl status battlefield-worker.service` and the journal for OOM failures.
Lower the value the same way.

For a fleet-wide reduction, set the database limit with
`scripts/ops/ai-policy.sh cli-slots 1` and restore it the same way; see
[limits and the stop switch](access.md#limits-and-the-stop-switch).

Transport errors, HTTP 429 and server errors during claiming retry after
`PollInterval` without cancelling sibling jobs. Authentication and protocol errors
stay fatal, and execution policy checks still fail closed.
