# Server rebuild contract

Decision recorded **2026-10-02**, for
[AB-124 — Validate the Ubuntu host and define the K3s VM rebuild contract](https://app.notion.com/p/3eab7a19f8f7819f85f7c0e2dab99efa),
within [AB-107 — Build reproducible K3s server with Terraform, Ansible, and Jenkins](https://app.notion.com/p/3e3b7a19f8f7816e8b29d8e532189e2d).
The implementation inventory uses dev `b1dfc66`; the deployed application is main
`86c00881195d72a8ce688bd72f36956c5135a535`, verified by
[main build 134](http://192.168.1.126:8080/job/autonomous-battlefield/job/main/134/).
This is a dated decision and rebuild boundary, not an instruction to apply every
playbook to a running server. Notion holds task status and completion evidence.

## Selected layout and capacity

Preserve the existing layout: Ubuntu hosts native Jenkins, its operator build
agent and Docker SonarQube/PostgreSQL. Terraform manages the separate K3s VM;
the isolated subscription CLI worker stays in its existing separate VM. Kind
continues to support workstation development. The old server Kind cluster and
its volumes remain a retained migration recovery source with writers/connectors
paused; removal requires a separate decision.

K3s directly on the host would avoid one guest OS, but would combine its runtime,
network and storage lifecycle with CI. The selected VM already hosts production,
provides a replaceable OS boundary and leaves the existing CI layout intact.
There is no capacity evidence requiring a topology change or a second server.

Read-only measurements at **09:57–10:03 UTC** (17:57–18:03 Singapore time):

| Resource | Observed value and interpretation |
| --- | --- |
| Host | Ubuntu **26.04.1 LTS**, x86-64, Python 3.14.4; KVM available |
| CPU | Ryzen 5 5600X, 6 cores / 12 logical CPUs; load averages 0.74 / 0.72 / 1.49 |
| Memory | 30.3 GiB total; 12.4 GiB available at inspection |
| CI memory | Jenkins about 1.33 GiB current / 1.36 GiB peak; operator agent 1.35 GiB current / 6.70 GiB peak, as reported by systemd since unit start |
| Root filesystem | `/dev/nvme0n1p5`, about 457 GiB usable, 145 GiB used, 289 GiB available |
| Data filesystem | `/dev/nvme0n1p3`, 499,080,232,960-byte partition; about 456 GiB usable, 13.7 GiB used, 419.5 GiB available |
| K3s guest | 4 vCPU, 8 GiB RAM; 32 GiB OS and 250 GiB retained data virtual disks |
| Guest data usage | `/dev/vdb` filesystem about 245 GiB usable; 10.2 GiB used, 222.3 GiB available, measured through the PostgreSQL pod's mounted data path |
| CLI guest | 2 vCPU, 4 GiB RAM; 24 GiB virtual OS disk, about 3.6 GiB physically allocated |
| Separate physical disk | `/dev/sda2`, ext4, about 931 GiB partition, **unmounted**; current free space and contents not inspected |

The systemd peaks are neither a simultaneous whole-host peak nor a CI load test;
Docker/container memory may be charged to other cgroups. No historical CPU peak,
network-throughput or disk-latency benchmark was collected. Retain the current
sizing; do not infer capacity for more concurrent CI jobs, model workloads or
Langfuse from this snapshot. The K3s VM preflight budgets full virtual disks,
4 GiB for its base image and 64 GiB host reserve; it also requires available RAM
plus 2 GiB for host services. These are operation-time checks, not reservations
against later writes. See the [VM resource contract](k3s-vm.md#prerequisites-and-storage).

Both root and data partitions are on **the same NVMe SSD**. The data mount is
`/srv/battlefield-data`, UUID `75db9b1d-5c46-4e5b-bd94-5391b2a35411`.
No disk resizing or storage migration is required for the measured workload.
The second physical disk is not currently a usable registered recovery target:
`backup_uuid` is empty, and its former desktop mount is absent.

## Network and service boundaries

| Boundary | Current contract |
| --- | --- |
| LAN | Host `192.168.1.126/24`, gateway `192.168.1.254`; observed DHCP Wi-Fi route. Preserve the address through a router reservation or deliberately update registration; a reservation was not verified. |
| K3s | `192.168.235.0/24`, `bf-k3s0`, NAT, reserved guest `192.168.235.10`; API and NodePorts remain private and firewall controlled. |
| CLI | `192.168.234.0/24`, `bf-agent0`, guest `.2`; **no NAT, no libvirt DNS**, with the existing NIC filter and host nftables boundary. |
| CLI host services | Squid `.234.1:3128`; dedicated Caddy `.234.1:18084` routes MCP through host loopback `28084`. Provider login homes stay in the CLI guest. |
| Other networks | Docker routes `172.17/16`, `172.18/16`, `172.19/16`; default libvirt `192.168.122/24`. No overlap with the selected guest subnets was observed. Recheck routes/VPNs on every replacement host. |
| CI access | Host SSH 22, native Jenkins 8080, Sonar loopback 9000. External automation uses a strict SSH tunnel to a loopback Jenkins URL. |
| Local HTTPS | Existing general `caddy.service` on 80/443, separate from the CLI proxy. `/etc/caddy/Caddyfile` names `battlefield.home.arpa`, `grafana.home.arpa`, `kibana.home.arpa`, `sonarqube.home.arpa` and uses internal TLS. Destination correctness and CA restoration still need the capture/verification below. |
| Public application | Healthy locally managed `ordinalfrontier-play` tunnel; proxied `play.ordinalfrontier.com` CNAME; existing self-hosted `play` Access app with six-hour sessions. Account App Launcher is unrelated. |

The Cloudflare inventory used GET requests only. Existing tunnel, DNS and Access
application IDs were retained privately. It did not export policy memberships,
rotate service identities or prove that a new reconciler can reproduce them.
The registered K3s application had all 13 Deployments ready; PostgreSQL and
Elasticsearch claims were Bound. These facts do not authorize old Kind deletion.

## State and credential locations

Paths below identify ownership; **do not publish their contents**. `~` means
`/home/twh09` on the host, except the explicitly named workstation inputs.
Protected files are identified from the role/runbook contract; this read-only
inventory did not bypass sudo to inspect Jenkins keys or private CA material.

| Component | Persistent state / configuration | Credential and recovery boundary |
| --- | --- | --- |
| Host foundation | Private `~/.config/autonomous-battlefield/server/inventory.json`; `/etc/battlefield-bootstrap/`; `/var/lib/battlefield-platform/` | Encrypted inputs and separate Vault passphrase; imported files under root-only `/etc/battlefield-bootstrap/inputs/`. Machine ID and mount UUID must match. |
| Jenkins controller | Complete `/var/lib/jenkins`, including jobs/build history/users/plugins; `/etc/battlefield-ci`; `/var/lib/battlefield-ci` | Preserve `secrets/` (including `master.key` and `hudson.util.Secret`), global/folder credential stores and the matching home together. XML alone is not recoverable. GitHub App, GHCR, Sonar, signing, production config/kubeconfig, Access and platform inputs are Jenkins credentials; maintenance captures their identities privately. |
| Jenkins agent | `~/jenkins-agent`; `~/.config/systemd/user/jenkins-agent.service`; pinned tools under `/opt/battlefield-ci/tools` | Agent enrollment and service inputs belong to the CI role. Workspace/cache deletion is not a substitute for controller recovery. Preserve the adopted private umask. |
| SonarQube | `/etc/battlefield-ci/sonar-compose.yaml`; Docker root `/var/lib/docker`; named `sonarqube_sonarqube_data`, `_extensions`, `_logs` volumes | Sonar PostgreSQL is `sonarqube_sonarqube_database`, independent of application PostgreSQL. Preserve database password and Jenkins/Sonar integration token; maintenance creates a database dump. |
| K3s Terraform | `~/.local/state/battlefield/k3s/terraform.tfstate`, generated inventory, admin key, known-hosts and encrypted `recovery/` bundles | Canonical state is outside Jenkins workspaces and outside the guest, **but on the host SSD**. The key is snapshotted separately from Terraform state; recover them together. |
| K3s disks / datastore | `/srv/battlefield-data/libvirt/images`; guest `/srv/battlefield/{k3s,kubelet,node,platform,storage}` | K3s uses its retained SQLite datastore. Certificates, token and node registration are retained with the data disk; guest OS replacement must not create a second cluster over missing storage. |
| Application PostgreSQL | `data-battlefield-postgres-0`, 10 GiB request on `battlefield-retained`; guest local-path directory beneath `/srv/battlefield/storage` | Runtime credentials in Kubernetes and private production `.env`; migration dumps in `~/.local/state/battlefield/backups`. Do not recreate the database password during rebuild. |
| Monitoring | Elasticsearch `battlefield-elasticsearch-data`, 20 GiB request on the same retained guest filesystem; Grafana, Prometheus, Tempo and other resources from deployment manifests | Only declared PVC data survives pod/OS changes. Other ephemeral telemetry is recreated. Old Kind telemetry was retained separately, not imported into new K3s. |
| Production registration | `~/.config/autonomous-battlefield/production/{deployment.env,kubeconfig,kubelet-ca.crt,recovery.json,.env,tunnel-credentials.json}` | Restore the matching cluster registration, signing/Access inputs and recovery record. `~/.kube/config` may still select Kind; always use the explicit production profile. |
| Cloudflare | External account/zone, locally managed tunnel, CNAME, Access application/policies/identity providers/service tokens; local connector JSON above | Workstation API token at `~/.config/autonomous-battlefield/bootstrap/cloudflare-api-token`, imported into Vault. It does not replace the connector secret or Access service-token secret. Preserve application audience/issuer and identity references. |
| CLI VM | `/var/lib/libvirt/images/battlefield-agent/{root.qcow2,seed.iso}`; private `server/cli.json`, `cli.files/`, optional `cli.guest/` and `cli-inputs.vault` on the controller used for capture | Guest `/var/lib/battlefield-worker` contains provider login homes; worker control secret is separate from app/signing/Kubernetes credentials. `~/.ssh/battlefield-vm-admin` is the host's admin key. Rebuild can require interactive provider sign-in; ordinary adoption must preserve homes. |
| Local TLS / trust | `/etc/caddy/Caddyfile`, service user `caddy`, home `/var/lib/caddy`; public roots under `/usr/local/share/ca-certificates/` | Expected Caddy storage beneath `/var/lib/caddy/.local/share/caddy`, including `pki/authorities/local`; protected storage/key paths still need confirmed capture. The foundation currently registers no `ca_files`. Installed public roots alone cannot recreate the issuing CA. |
| Optional Langfuse | No Langfuse Docker container or K3s workload in the inspected application; production file contains only commented Langfuse examples | No local Langfuse database/storage allocation is assumed. Re-audit external endpoint/API-key inputs and persistent services before enabling it. |
| Operator recovery inputs | Workstation `~/.config/autonomous-battlefield/server/` inventories/manifests/Vaults; `~/.config/jenkins/token`; `~/.config/sonarqube/token`; dedicated workstation SSH key | Keep outside Git. Current private copies are not a scheduled backup policy. A fresh host still requires available source credentials or newly issued external credentials. |

The 10/20 GiB PVC requests are **not filesystem quotas**. PostgreSQL,
Elasticsearch, container images and K3s state share the retained data filesystem.
A full guest disk can stop database writes and disrupt the cluster; a full host
partition can prevent qcow2 growth even when the guest reports free space. Check
both levels. No automatic disk expansion, pruning or scheduled retention is
implemented by this contract. Preserve database/VM volumes; reclaim only verified
disposable caches/logs or perform separately reviewed storage expansion.

## Ownership, locking and recovery objectives

Terraform owns the K3s VM/network/disks. Ansible owns host foundation, CI services,
CLI services and the K3s runtime/controllers. Application CD owns application and
monitoring manifests. Main does not invoke host, VM or K3s provisioning. Component
selection and monitoring update efficiency remain separate delivery work.

The host operator lock is `~/.local/state/battlefield/bootstrap.lock`; Jenkins
holds `battlefield-production-platform` through its reviewed platform operation
or whole production rollout. Terraform additionally locks its local state.
External applies must not run between Jenkins rollout commands or while a job is
waiting for approval. See [platform locking](platform-jobs.md#plan-apply-and-verify)
for interruption limits; a lost SSH connection is not proof every remote task stopped.

The selected recovery objective for this personal, unlaunched service is manual,
best-effort recovery with **no promised RTO or scheduled-backup RPO**:

- Failed application release: retain the pre-migration dump, use the verified
  image-pair/migration-ledger [recovery procedure](k3s-delivery.md), and verify the
  restored release. Application rollback does not reverse database migrations.
- Lost guest OS with retained disk and host state intact: reviewed replacement,
  reapply K3s configuration and verify the original cluster/PVC identity. Never
  format a nonempty disk or force a new ownership marker to make it pass.
- Lost Terraform control files with intact host/resources: restore a matching
  encrypted bundle through [VM state recovery](k3s-vm.md#state-recovery-and-the-second-disk).
- CI maintenance: keep the mandatory private controller archive and Sonar dump
  under `/srv/battlefield-data/maintenance/`; use the external
  [CI maintenance/recovery workflow](ci-bootstrap.md).
- Loss of the physical SSD or server: restoration is **not guaranteed**. Local
  snapshots, migration dumps and maintenance archives share that failure domain.
  Scheduled backups, retention and complete-server-loss recovery remain deferred
  under [AB-130](https://app.notion.com/p/3eab7a19f8f78112a657faf5aed3b880).
  If resumed, the owner's current destination choice is the separate server disk;
  it must first be registered/mounted and verified. No off-host copy was requested.

This explicitly supersedes AB-124's original proposal to place state outside the
physical host now. State is outside the guest and build workspace, with local
locking/recovery bundles; whole-host-loss protection is deferred. Reproducible
configuration cannot recover lost database bytes or missing external secrets.

## Bootstrap sequence and human prerequisites

Use a reviewed checkout. For recovery, first decide whether state survives or a
clean installation is intended; never run fresh installation against retained
unrecognized state. Existing runbooks own the exact commands:

1. **Operator bootstrap:** install supported Ubuntu, enable KVM, establish network,
   an SSH/sudo account and trusted host-key entry. Keep the current SSH key on the
   workstation; Jenkins need not exist there. The owner enters sudo interactively.
   Passphrase-protected guest keys may need unlocking in the server SSH agent.
2. **Register/import:** [host init, inspect and seal](server-bootstrap.md). Supply
   existing external credentials, public CA inputs and a separately retained Vault
   passphrase. Inspect/approve filesystem UUIDs; the automation does not choose or
   format an arbitrary physical partition. Restored CI needs the complete matching
   controller home and Sonar data, not just credential files.
3. **Host:** run foundation plan/apply/verify from WSL, or locally on the host.
   Supported tools are bootstrapped; no Jenkins installation is needed to run it.
4. **CI:** run external [CI registration and reviewed maintenance](ci-bootstrap.md).
   Seed jobs and credentials through the existing scripts; account/token issuance
   remains external. Never use a Jenkins job to replace its own controller.
5. **K3s:** seed/run reviewed [VM](k3s-vm.md) then [K3s](k3s-platform.md)
   plan/apply/verify. Generated inventory supplies guest address/SSH trust.
6. **Delivery:** register the intended production cluster and restore data/inputs
   when needed, then follow [K3s delivery](k3s-delivery.md). Verify signed images,
   database ledger, private/public Access, telemetry and the recovery record.
7. **CLI and access:** reproduce/adopt the existing CLI VM, then use
   [CLI bootstrap](cli-bootstrap.md). Provider subscription login and any later
   reauthorization remain interactive. Reconcile owned Cloudflare and local HTTPS
   configuration through their implementation work below before claiming a
   complete clean-server rebuild.

For a fresh installation, the human-only categories are base OS/network/SSH/sudo,
external account/token issuance, Vault/key unlock and storage identity selection,
local DNS/browser CA trust, and provider subscription sign-in. Reviewed apply
approvals remain intentional gates. This is not a promise of a fixed click count:
expired credentials or a replacement host can add account recovery steps.

## Remaining implementation and acceptance boundaries

The contract is implementable without redesigning the live layout. Its next
independent deliverables are explicitly tracked:

- [AB-140 — CLI VM provisioning/adoption](https://app.notion.com/p/3edb7a19f8f78120b73ae00b203182f4): consume host identity and preserve the current VM disk/network; then hand off to existing CLI Ansible. The dated inventory found `create-agent-vm.sh` was create-only. The subsequent [CLI VM platform](cli-vm-platform.md) adds reviewed Ansible/libvirt creation/adoption while Terraform continues to own K3s; live adoption evidence is still required.
- [AB-141 — Cloudflare reconciliation](https://app.notion.com/p/3edb7a19f8f781a19d2fd066ac4af410): privately capture owned policies/IDs, reviewed adopt/create, preserve audience/service identities and unrelated resources. The subsequent [Cloudflare platform](cloudflare-platform.md) supplies encrypted capture, reviewed adoption/creation and a scoped API fixture; live no-change adoption remains required. Account/domain ownership remains external.
- [AB-142 — Local HTTPS routes and CA trust](https://app.notion.com/p/3edb7a19f8f7810ebd5de5d75beaa267): capture protected Caddy configuration/CA state, verify the four current routes and reproduce them without changing trust or the dedicated CLI proxy.
- Reconcile existing platform/storage acceptance under AB-131/AB-134. Documented disposable foundation/VM/K3s rehearsal entry points must start with ordinary Python settings; `--help` checks prove startup, not a new live lifecycle test.

AB-140 consumes the host/CLI service and reviewed-job foundations; AB-141 consumes
host encrypted inputs; AB-142 consumes the host and CI service contract. Their
reviewed live adoption and clean-install/repeat/reboot evidence must precede a
claim that complete-server reconstruction is finished. Worker release-update
jobs (AB-139), monitoring lifecycle improvements and Player CLI enablement retain
their own acceptance gates. No protected CA capture, new VM, Cloudflare mutation,
production rehearsal or storage change was performed by this inventory.
