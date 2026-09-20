# Production CLI VM setup and recovery

Use this guide with the [limited-launch gates](limited-launch.md#isolated-cli-vm-enable-only-in-the-last-phase).
Run commands from the repository root unless a step says otherwise. Keep CLI
availability false until isolation checks pass. Commands below provision services;
stop active test battles before restarting them. Never copy the host kubeconfig or
Kubernetes signing secret into the VM.

## What runs where

There is one KVM VM, not nested VMs. The physical Ubuntu PC runs Docker, and kind
runs its Kubernetes node as a Docker container on that PC. Kubernetes hosts the
application, engine, MCP server and database. The separate Ubuntu VM runs the
worker and Codex processes. The code-mode helper is a process, not another VM.

```text
Physical Ubuntu PC
  Docker -> kind Kubernetes container -> Battlefield services
  Squid 192.168.234.1:3128 -> approved HTTPS provider domains
  Caddy 192.168.234.1:18084 -> kubectl 127.0.0.1:18085 -> MCP :8084
  KVM VM 192.168.234.2
    vm-admin: administration
    battlefield-worker: worker, Codex login, Codex + code-mode helper
```

The worker claims a request through the bridge. Codex receives a scoped capability,
reads the decision context through MCP, calls the provider through Squid, and
submits a plan through MCP. Acceptance and execution are separate: the engine
executes accepted orders. A plain model reply is not a submitted battle plan.

## 1. Prepare the physical host and image

On the Ubuntu host:

```bash
sudo apt update
sudo apt install qemu-system-x86 libvirt-daemon-system libvirt-clients \
  qemu-utils cloud-image-utils python3 curl nftables ubuntu-cloudimage-keyring
```
 Some Ubuntu releases expose `qemu-kvm` only as a
virtual package; explicitly selecting `qemu-system-x86` avoids that installation
failure. Install Caddy and kubectl using the project's existing host setup.

Before installing Squid on a fresh host, mask `squid.service` so its default
configuration cannot start an unintended listener. Do not replace a shared Squid
installation with this dedicated configuration. On a fresh host:

```bash
sudo systemctl mask squid.service
sudo apt install squid
```

Keep it masked until step 3 has installed the firewall and reviewed configuration.

```bash
sudo systemctl enable --now libvirtd
sudo virt-host-validate qemu
free -h
df -h /var/lib/libvirt/images
```

Enable AMD SVM or Intel virtualization in firmware if KVM is unavailable. A warning
about missing SEV/TDX does not itself prevent ordinary KVM operation. No host reboot
is needed merely to enter an already-running VM.

Download the Ubuntu 24.04 amd64 cloud image and verify the publisher's signed
checksum list before using its image digest. Do not treat a digest calculated from
an unverified download as publisher verification. Retain the verified digest in
operator records.

Create a dedicated SSH key if it does not already exist (never overwrite an
existing private key):

```bash
ssh-keygen -t ed25519 -f "$HOME/.ssh/battlefield-vm-admin"
cp "$HOME/.ssh/battlefield-vm-admin.pub" "$HOME/battlefield-vm-admin.pub"
```

Set `BATTLEFIELD_VM_IMAGE` to the absolute image path,
`BATTLEFIELD_VM_IMAGE_SHA256` to the verified expected digest, and
`BATTLEFIELD_VM_ADMIN_KEY` to the public key path. From the repository root:

```bash
sudo --preserve-env=BATTLEFIELD_VM_IMAGE,BATTLEFIELD_VM_IMAGE_SHA256,BATTLEFIELD_VM_ADMIN_KEY \
  ./scripts/ops/create-agent-vm.sh
sudo virsh --connect qemu:///system autostart battlefield-agent
sudo virsh --connect qemu:///system list --all
```

The creation script intentionally refuses existing domains, networks and VM
storage. To resume an interrupted installation, inspect the existing resources;
do not delete them or rerun provisioning blindly. Default sizing is 2 vCPU,
4 GiB RAM and 24 GiB disk. The VM has no default route, NAT or DNS server.

## 2. Enter the VM and understand the accounts

From the physical host:

```bash
ssh -i "$HOME/.ssh/battlefield-vm-admin" vm-admin@192.168.234.2
```

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

## 3. Render and install the host services

The renderer is safe to run on a development workstation: it creates files only,
contains no credentials and does not contact Kubernetes or start services. Supply
the actual physical-host user, home and absolute kubeconfig path. On the host:

```bash
python3 scripts/ops/render-agent-vm-host.py \
  --user "$(id -un)" --home "$HOME" \
  --kubeconfig "$HOME/.config/autonomous-battlefield/production/kubeconfig" \
  --context battlefield-production \
  --output "$HOME/battlefield-agent-host-bundle"
```

The output directory must be new. To regenerate, use a new directory and compare
it with the installed files. Review all output, especially the provider allowlist.
Confirm `/usr/bin/caddy`, `/usr/bin/kubectl`, `/usr/sbin/nft` and the service user's
kubeconfig exist. Check the kubeconfig context and kube-system UID against the
registered production deployment before proceeding:

```bash
kubectl --kubeconfig="$HOME/.config/autonomous-battlefield/production/kubeconfig" \
  --context=battlefield-production get namespace kube-system \
  -o jsonpath='{.metadata.uid}{"\n"}'
sudo ss -lntp '( sport = :3128 or sport = :18084 or sport = :18085 )'
```

Port 18085 must be free or already belong to this bridge. Port 18084 can also be
used on **127.0.0.1 only** by Jenkins smoke checks; the VM proxy binds only to
192.168.234.1. Back up existing dedicated configuration and units before replacing
them. Inspect `systemctl cat` for all three Battlefield units and Squid: historical
drop-ins override main units. In particular, remove an obsolete
`battlefield-mcp-bridge.service.d/20-loopback.conf` only after confirming the new
main unit contains the intended loopback forward. Preserve unrelated overrides.

On the host, install the reviewed bundle (these are intentional live configuration
replacements). The Squid package must already be installed:

```bash
(
set -eu
bundle="$HOME/battlefield-agent-host-bundle"
sudo install -d -m 0755 /etc/battlefield-agent /etc/systemd/system/squid.service.d
for file in firewall.nft mcp-proxy.Caddyfile provider-domains.txt; do
  sudo install -o root -g root -m 0644 "$bundle/$file" "/etc/battlefield-agent/$file"
done
sudo install -o root -g root -m 0644 "$bundle/squid.conf" /etc/squid/squid.conf
for unit in battlefield-agent-firewall battlefield-mcp-bridge battlefield-mcp-proxy; do
  sudo install -o root -g root -m 0644 "$bundle/$unit.service" "/etc/systemd/system/$unit.service"
done
sudo install -o root -g root -m 0644 "$bundle/squid-override.conf" \
  /etc/systemd/system/squid.service.d/battlefield.conf
sudo nft --check --file /etc/battlefield-agent/firewall.nft
sudo squid -k parse -f /etc/squid/squid.conf
sudo caddy validate --config /etc/battlefield-agent/mcp-proxy.Caddyfile --adapter caddyfile
sudo systemctl daemon-reload
sudo systemctl enable battlefield-agent-firewall battlefield-mcp-bridge battlefield-mcp-proxy
sudo systemctl restart battlefield-agent-firewall
sudo systemctl restart battlefield-mcp-bridge
sudo systemctl restart battlefield-mcp-proxy
sudo systemctl unmask squid
sudo systemctl enable squid
sudo systemctl restart squid
)
```

Caddy runs as a separate service with its admin endpoint disabled. The unit gives
its autosaved configuration a private runtime directory; `persist_config off` is
not used as a Caddyfile option. Existing public Caddy configuration is untouched.
The firewall reload replaces only its own table, not Docker/libvirt/other rules.
Its localhost exception on 18084 is needed by Jenkins. IPv6 cannot use either
protected port through the catch-all drop; the VM NIC filter also denies IPv6.

Why two bridge services? Kubernetes port-forward reaches MCP over loopback while
preserving the original Host header. The Go MCP SDK rejects a non-loopback Host
on that connection. Caddy changes only the upstream Host to `127.0.0.1:8084`;
bearer authentication and MCP origin/localhost protection remain enabled. Do not
use a direct guest-facing kubectl forward or disable SDK protection globally.

## 4. Install the worker and the complete pinned Codex release

On the host, obtain the `battlefield-agent` binary from the same verified backend
release deployed in production. For example, extract `/app/agent` from the running
MCP container into a private staging directory; check the command succeeds before
transferring the file. Record its checksum and verify it again in the VM. Install
it root-owned at `/usr/local/bin/battlefield-agent`, mode 0755.

The working Codex release was **0.152.0**; the supplied profile requires the
`codex-cli 0.152.` version family. Download **both** version-matched Linux amd64
musl assets from the official `openai/codex` release on the host:

- `codex-x86_64-unknown-linux-musl.tar.gz`
- `codex-code-mode-host-x86_64-unknown-linux-musl.tar.gz`

Use the release's published digests/provenance to verify each asset. A versioned
URL alone is not checksum verification. The helper archive's release SHA256 for
0.152.0 was `449cefe35b9f347e3fdbf121e816339b37825eb0bfee7de8298a0a61b6687cba`.
The main archive's release SHA256 was
`05f942d3d3c5b5acd9edad56ce2797b6fe72dbb1462b24e5c9bf7dcec9a28a11`.
Retain both expected digests with your release inventory. For this exact release,
on the host (do not reuse these hashes for another version):

```bash
(
set -eu
mkdir -p "$HOME/battlefield-cli-install"
cd "$HOME/battlefield-cli-install"
base=https://github.com/openai/codex/releases/download/rust-v0.152.0
for name in codex codex-code-mode-host; do
  curl --fail --location --output "$name-x86_64-unknown-linux-musl.tar.gz" \
    "$base/$name-x86_64-unknown-linux-musl.tar.gz"
done
sha256sum --check <<'SUMS'
05f942d3d3c5b5acd9edad56ce2797b6fe72dbb1462b24e5c9bf7dcec9a28a11  codex-x86_64-unknown-linux-musl.tar.gz
449cefe35b9f347e3fdbf121e816339b37825eb0bfee7de8298a0a61b6687cba  codex-code-mode-host-x86_64-unknown-linux-musl.tar.gz
SUMS
for name in codex codex-code-mode-host; do
  tar -xzf "$name-x86_64-unknown-linux-musl.tar.gz"
done
scp -i "$HOME/.ssh/battlefield-vm-admin" \
  codex-x86_64-unknown-linux-musl codex-code-mode-host-x86_64-unknown-linux-musl \
  vm-admin@192.168.234.2:
)
```

Install inside the VM:

```bash
sudo install -o root -g root -m 0755 "$HOME/codex-x86_64-unknown-linux-musl" /usr/local/bin/codex
sudo install -o root -g root -m 0755 "$HOME/codex-code-mode-host-x86_64-unknown-linux-musl" /usr/local/bin/codex-code-mode-host
/usr/local/bin/codex --version
ls -l /usr/local/bin/codex /usr/local/bin/codex-code-mode-host
```

Copy `deploy/agent-vm/config.yaml`, `worker.service` and
`scripts/ops/verify-agent-isolation.py` to vm-admin's home. Inside the VM:

```bash
sudo install -d -m 0755 /etc/battlefield-agent
sudo install -o root -g root -m 0644 "$HOME/config.yaml" /etc/battlefield-agent/config.yaml
sudo install -o root -g root -m 0644 "$HOME/worker.service" /etc/systemd/system/battlefield-worker.service
sudo install -o root -g root -m 0755 "$HOME/verify-agent-isolation.py" /usr/local/bin/verify-agent-isolation.py
sudo systemctl daemon-reload
```

Securely provision `/etc/battlefield-agent/worker.env`, root:root mode 0400,
using `worker.env.example`, but initially set its isolation flag to **false**.
Set **both profiles to `enabled: false`** in the VM configuration during boundary
verification: production startup refuses enabled CLI profiles without the isolation
attestation. Transfer only the dedicated `BATTLEFIELD_AGENT_SECRET`
from the production secret store, without printing it or putting it in shell
history. Do not transfer the full Kubernetes Secret. Systemd reads this file;
the worker account need not be able to read it directly.

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

## 5. Verify before advertising availability

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
the boundary. Keep both VM profiles disabled for this initial check. In the
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

Complete the remaining isolation gates in the limited-launch guide. Then set
`BATTLEFIELD_CLI_ISOLATION_VERIFIED=true` in the VM worker environment, enable
only the Codex profile in the VM configuration, and restart the worker. In the host's private production `.env`, set that flag and
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
| Creation script not found | Run from the repository root, not the image-download directory. |
| Worker connection refused during boot | Inspect bridge/proxy logs and listeners; both services retry, as does the worker. |
| `invalid Host header "192.168.234.1:18084"` | Verify Caddy owns that listener, kubectl owns 127.0.0.1:18085, and the Host rewrite is installed. |
| Plain reply works, code-mode host missing | Install the matching helper beside Codex; a no-tools reply does not test MCP. |
| `plan_not_submitted` | It means no accepted plan, not a specific root cause. Check MCP tool rejection logs and private CLI diagnostics. |
| Profile On but unavailable | Check deployed availability/isolation flags, worker connectivity and role policy; editing `.env` alone does not deploy it. |
| Jenkins localhost 18084 timeout | Preserve the narrow firewall loopback exception; do not flush firewall rules. |
| cAdvisor IP SAN failure | Separate Kubernetes serving-certificate issue; inspect the live kubelet cert and trusted CA. Do not disable TLS verification or approve an uninspected CSR. |

Host diagnostics:

```bash
sudo journalctl -u battlefield-mcp-bridge -u battlefield-mcp-proxy --since '10 minutes ago' --no-pager
sudo ss -lntp '( sport = :3128 or sport = :18084 or sport = :18085 )'
```

VM diagnostics:

```bash
sudo journalctl -u battlefield-worker --since '10 minutes ago' --no-pager -o cat
```

Use the same UTC time window and request ID in the MCP pod logs. The worker's
normal journal omits raw CLI output. If capturing it temporarily, keep it under the
worker's private home with mode 0600, never log environment variables or capability
tokens, inspect locally and redact before sharing. Remove diagnostic PATH overrides
and private logs after the investigation. For the temporary override used during
this incident, cleanup inside the VM is:

```bash
sudo rm -f /run/systemd/system/battlefield-worker.service.d/90-diagnostics.conf
sudo systemctl daemon-reload
sudo systemctl restart battlefield-worker
sudo rm -f /var/lib/battlefield-worker/codex-diagnostic.log
```

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
