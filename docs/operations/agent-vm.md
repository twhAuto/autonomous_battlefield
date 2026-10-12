# Production CLI worker verification and recovery

Use this guide with the [limited-launch gates](limited-launch.md#isolated-cli-vm-enable-only-in-the-last-phase).
Run commands from the repository root unless a step says otherwise. Keep CLI
availability false until isolation checks pass. Use the provisioning and service
runbooks below before verification; stop active test battles before restarting
services. Never copy the host kubeconfig or Kubernetes signing secret into the VM.

## What runs where

The CLI worker has its own KVM VM. A separate KVM VM runs Kubernetes (K3s), the application, engine, MCP server and database.
Docker remains on the physical Ubuntu host for CI and the retained old Kind
cluster. The code-mode helper is a process inside the worker VM.

```text
Physical Ubuntu PC
  KVM battlefield-k3s -> Kubernetes -> Battlefield services and database
  Docker -> CI tooling; retained Kind recovery data
  Squid 192.168.234.1:3128 -> approved HTTPS provider domains
  Caddy 192.168.234.1:18084 -> host loopback kubectl bridge -> MCP :8084
  KVM battlefield-agent 192.168.234.2
    vm-admin: administration
    battlefield-worker: worker, Codex login, Codex + code-mode helper
```

The renderer uses loopback port 28084 by default, matching the existing server's
`20-loopback.conf` and Caddy upstream. `--bridge-port`
selects another free unprivileged port and changes both rendered files together.
Keep the bridge separate from Jenkins smoke ports 18081–18085; in particular,
18085 is used by the production frontend liveness check. A custom choice of
18085 requires changing `SMOKE_TEST_FRONTEND_PORT` in the Jenkins job first.
The bridge reads the registered production kubeconfig; restarting it after K3s
profile adoption retargets the connection without reinstalling the CLI worker
or copying cluster credentials into its VM. See
[cutover and recovery](k3s-delivery.md#adoption-and-cutover-boundary).

The worker claims a request through the bridge. Codex receives a scoped capability,
reads the decision context through MCP, calls the provider through Squid, and
submits a plan through MCP. Acceptance and execution are separate: the engine
executes accepted orders. A plain model reply is not a submitted battle plan.

## Provision and install

Use [CLI VM provisioning and adoption](cli-vm-platform.md) for reviewed VM
creation or adoption, then [CLI worker bootstrap](cli-bootstrap.md) for the host
bridge, proxy, firewall and guest service files. The workflows own image pins,
capacity checks, SSH trust, service activation and interruption recovery. Preserve
existing disks, provider homes, bridge overrides and the matched proxy/bridge pair.
Never delete ownership markers or rerun fresh creation against retained resources.

The VM has no default route, NAT or DNS server. The host firewall replaces only
its owned table; unrelated Docker/libvirt rules and public Caddy configuration
remain intact. Its narrow loopback exception permits Jenkins smoke checks on
18084. IPv6 cannot reach protected ports; the external NIC filter also denies it.

Kubernetes port-forward reaches MCP over loopback while preserving the original
Host header. The Go MCP SDK rejects a non-loopback Host on that connection. The
separate Caddy proxy sets upstream Host to `127.0.0.1:8084`; bearer authentication
and MCP origin/localhost protection remain enabled. Do not use a direct
guest-facing kubectl forward or disable SDK protection globally.

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

On the host, obtain the `battlefield-agent` binary from the same verified backend
release deployed in production. For example, extract `/app/agent` from the running
MCP container into a private staging directory; check the command succeeds before
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

Return to vm-admin with `exit`. This login step is deliberately interactive;
never bake login tokens into the image or rendered bundle.

## Verify before advertising availability

Inside the VM, test an allowed HTTPS destination through Squid and a denied one:

```bash
curl --noproxy '' --proxy http://192.168.234.1:3128 --connect-timeout 10 --max-time 30 -I https://archive.ubuntu.com/ubuntu/
curl --noproxy '' --proxy http://192.168.234.1:3128 --connect-timeout 10 --max-time 30 -I https://example.com/
curl --noproxy '*' --connect-timeout 10 --max-time 15 -sS -o /dev/null -w 'HTTP %{http_code}\n' http://192.168.234.1:18084/mcp
```

Expect allowed HTTPS success, denied CONNECT 403, and unauthenticated MCP 401.
**401 does not prove MCP initialization works**: capability authentication runs
before the SDK Host check. A real accepted plan is required to test that path.

Keep production API CLI availability false while starting the worker and checking
the boundary. Keep all VM profiles disabled for this initial check. In the
vm-admin shell, set `ROUTER_IP` to your router's LAN address and `HOST_LAN_IP`
to the physical host's LAN address, then run:

```bash
: "${ROUTER_IP:?Set ROUTER_IP to your router LAN address}"
: "${HOST_LAN_IP:?Set HOST_LAN_IP to your physical host LAN address}"
sudo systemctl enable --now battlefield-worker.service
worker_pid="$(sudo systemctl show battlefield-worker.service --property=MainPID --value)"
sudo -u battlefield-worker python3 /usr/local/bin/verify-agent-isolation.py \
  --worker-pid "$worker_pid" --blocked-host "$ROUTER_IP" --blocked-host "$HOST_LAN_IP"
curl --noproxy '*' -sS -o /dev/null -w 'HTTP %{http_code}\n' http://127.0.0.1:8083/healthz
```

Use the actual LAN addresses, not the dedicated VM bridge addresses. Health
should be 204. Also inspect host NIC filter attachment and firewall state:

```bash
sudo virsh --connect qemu:///system dumpxml battlefield-agent
sudo nft list table inet battlefield_agent
sudo systemctl is-active battlefield-agent-firewall battlefield-mcp-bridge battlefield-mcp-proxy squid
```

Complete the remaining isolation gates in the limited-launch guide. A freshly
bootstrapped managed worker must remain disabled: bootstrap cannot change its
registered environment or profile configuration. Follow the
[managed profile enablement boundary](cli-bootstrap.md#managed-profile-enablement-boundary);
do not hand-edit the managed files, re-register to change their hashes or delete
ownership markers.

The launch checks below apply to an existing enabled installation adopted with
matching files and previously recorded isolation evidence. Confirm that its VM
environment already has `BATTLEFIELD_CLI_ISOLATION_VERIFIED=true` and only the
intended Codex profile is enabled. In the host's private production `.env`, set
that flag and
`CODEX_CLI_PROFILE_AVAILABLE=true`, leaving Gemini and Claude false. Run
`./scripts/ops/prepare-limited-production.sh`, then use the normal production
configuration deployment (`APPLY_CONFIGURATION=true`). Preparation alone explicitly
makes no live workload changes. Keep initial profile access limited to administrators.

Run one Codex-versus-Scripted battle. Verify opening and tick 10 both have accepted
plans, no fallback/error, and eventually CLI usage populated. Token usage can arrive
after plan acceptance. Verify execution separately in the battle event stream.

The September 18, 2026 manual run accepted opening and tick 10 in about 44 and
64 seconds, with roughly 50,000 input tokens per decision and one CLI invocation
each. These are observations, not latency or capacity guarantees. Reboot recovery,
cancellation and two-Codex-player behavior were **not verified by that run**.

## Troubleshooting and recovery

| Symptom | Check or correction |
| --- | --- |
| `qemu-kvm` has no candidate | Install the explicit `qemu-system-x86` package. |
| Worker connection refused during boot | Inspect bridge/proxy logs and listeners; both services retry, as does the worker. |
| `invalid Host header "192.168.234.1:18084"` | Verify Caddy owns that listener, kubectl owns 127.0.0.1:28084 (or the configured bridge port), and the Host rewrite is installed. |
| Plain reply works, code-mode host missing | Install the matching helper beside Codex; a no-tools reply does not test MCP. |
| `plan_not_submitted` | It means no accepted plan, not a specific root cause. Check MCP tool rejection logs and private CLI diagnostics. |
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

Use the same UTC time window and request ID in the MCP pod logs. The worker's
normal journal omits raw CLI output. If capturing it temporarily, keep it under the
worker's private home with mode 0600, never log environment variables or capability
tokens, inspect locally and redact before sharing. Record the exact temporary
drop-ins and private logs you create. After the investigation, remove only those
diagnostic files and restore the normal worker configuration. If a systemd
drop-in changed, run `sudo systemctl daemon-reload` and restart the worker during
a maintenance window, then repeat its health and accepted-plan checks.

For a planned restart test, stop battles, reboot during a maintenance window, then
repeat listener, firewall, health and accepted-plan checks. Verify the VM/network
and all four host services are enabled. Follow the limited-launch cancellation and
two-player tests before broader access. Keep durable configuration, release digests
and the host bundle in operator records; keep secrets in private stores. This guide
and the generator replace ad hoc unit-file editing, not the verification gates.

## Concurrent decisions

Set `ai.agentConcurrency: 4` to allow four independent jobs
across all enabled CLI providers, not four per provider. The template defaults to
one until resource validation; omitted configuration
keeps the worker at one slot; explicit values outside 1–4 are rejected.
Migration `00014_cli_concurrency.sql` raises the shared database CLI capacity to
four. Both the migration and updated worker binary are required. The database
limit also includes faction background jobs and other worker instances.

For an existing VM, deploy the release containing the migration through the normal
pipeline, copy its `/app/agent` binary as described above, and add
`agentConcurrency: 4` under `ai:` in `/etc/battlefield-agent/config.yaml`.
Preserve the existing provider profiles and credentials. Restart
`battlefield-worker.service` while no CLI jobs are running and verify the
`agent_worker_ready` log reports `"concurrency":4` and health returns HTTP 204.
Use independent queued decisions to verify overlap; a single decision remains
sequential. Slots remain occupied until execution completes, even after a plan
is accepted. The existing VM memory and systemd limits remain in force: monitor
`systemctl status battlefield-worker.service` and the journal for OOM failures
when testing four CLI processes. To reduce load, set `agentConcurrency: 1` and
restart after active work completes; a database downgrade is unnecessary.

For a fleet-wide reduction, a database administrator can run
`UPDATE runtime_policy SET cli_concurrency = 1 WHERE id;` through the existing
private database administration connection. Values 1–4 are supported. Restore
four with `UPDATE runtime_policy SET cli_concurrency = 4 WHERE id;`. Reducing the
limit prevents new claims until capacity frees; it does not cancel active jobs.

Before enabling four slots, exercise four concurrent CLI sessions on the target
VM and verify memory/task headroom, saved logins remain valid after concurrent
refresh, and provider throttling does not cause repeated fallbacks. A successful
single CLI session does not establish this. Keep the default one slot until that
check; increase VM resources separately if the existing limits cannot sustain
four. Transport errors, HTTP 429 and server errors during claiming retry after
`PollInterval` without cancelling sibling jobs. Authentication/protocol errors
remain fatal, and execution policy checks still fail closed.
