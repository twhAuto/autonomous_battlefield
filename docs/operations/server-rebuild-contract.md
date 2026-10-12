# Server rebuild contract

Use this contract to identify the state, credentials and service boundaries that
must survive a server rebuild. The linked runbooks own the exact procedures.
Before an operation, inspect the registered host and production profile; addresses,
capacity, route health and credential availability require fresh verification.
Notion holds task status and the
[original host inventory and decision evidence](https://app.notion.com/p/3eeb7a19f8f7811db7cac1a298399aa4).

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

The reviewed K3s defaults are 4 vCPUs, 8 GiB RAM, a 32 GiB OS disk and a 250 GiB
retained data disk. The separate CLI VM uses 2 vCPUs, 4 GiB RAM and a 24 GiB OS
disk. See the [VM defaults](../../infra/libvirt/vm.json) and
[CLI VM prerequisites](cli-vm-platform.md#prerequisites).
The K3s VM preflight budgets full virtual disks, 4 GiB for its base image and
64 GiB host reserve. Its memory check accounts for the existing guest allocation
and leaves at least 2 GiB available for host services. These are operation-time
checks, not reservations against later writes. See the
[VM resource contract](k3s-vm.md#prerequisites-and-storage).

The selected host layout puts root and data partitions on **the same NVMe SSD**.
The data mount is `/srv/battlefield-data`; its UUID must match the private host
inventory. Recheck filesystem identity and capacity before each operation.
Historical free-space readings and service memory peaks do not establish
capacity for more concurrent CI jobs, model workloads or Langfuse.
A separate physical disk is usable for recovery only after its filesystem is
registered, mounted and verified; its presence alone provides no backup protection.

## Network and service boundaries

| Boundary | Current contract |
| --- | --- |
| LAN | Host address, gateway and strict SSH trust come from the private registration. Preserve the address through a router reservation or deliberately update registration; verify the reservation on a replacement host. |
| K3s | `192.168.235.0/24`, `bf-k3s0`, NAT, reserved guest `192.168.235.10`; API and NodePorts remain private and firewall controlled. |
| CLI | `192.168.234.0/24`, `bf-agent0`, guest `.2`; **no NAT, no libvirt DNS**, with the existing NIC filter and host nftables boundary. |
| CLI host services | Squid `.234.1:3128`; dedicated Caddy `.234.1:18084` routes MCP through host loopback `28084`. Provider login homes stay in the CLI guest. |
| Other networks | Check Docker, libvirt, LAN and VPN routes for overlap with the selected guest subnets on every replacement host. |
| CI access | Host SSH 22, native Jenkins 8080, Sonar loopback 9000. External automation uses a strict SSH tunnel to a loopback Jenkins URL. |
| Local HTTPS | General `caddy.service` on 80/443, separate from the CLI proxy. `/etc/caddy/Caddyfile` owns the application, Grafana, Kibana and Sonar aliases with internal TLS. Use the [local HTTPS workflow](local-https-platform.md) to capture and verify routes and retained CA material. Route correction requires reviewed migration; a merge does not change live routes. |
| Public application | Locally managed tunnel, proxied application CNAME and self-hosted Access application. Use the matching production profile and [Cloudflare workflow](cloudflare-platform.md) to verify ownership, audience, issuer and policy identities. Account App Launcher is outside this ownership. |

Verify workload readiness and bound storage claims through the owning runbooks.
Healthy K3s workloads do not authorize deletion of the retained Kind recovery
source. Cloudflare capture and platform adoption also need their own verification.

## State and credential locations

Paths below identify ownership; **do not publish their contents**. `~` means the
registered operator's home on the host, except the explicitly named workstation
or controller inputs. These are role/runbook locations; confirm that complete,
matching recovery material exists before relying on it.

| Component | Persistent state / configuration | Credential and recovery boundary |
| --- | --- | --- |
| Host foundation | Private `~/.config/autonomous-battlefield/server/inventory.json`; `/etc/battlefield-bootstrap/`; `/var/lib/battlefield-platform/` | Encrypted inputs and separate Vault passphrase; imported files under root-only `/etc/battlefield-bootstrap/inputs/`. Machine ID and mount UUID must match. |
| Jenkins controller | Complete `/var/lib/jenkins`, including jobs/build history/users/plugins; `/etc/battlefield-ci`; `/var/lib/battlefield-ci` | Preserve `secrets/` (including `master.key` and `hudson.util.Secret`), global/folder credential stores and the matching home together. XML alone is not recoverable. GitHub App, GHCR, Sonar, signing, production config/kubeconfig, Access and platform inputs are Jenkins credentials; maintenance captures their identities privately. |
| Jenkins agent | `~/jenkins-agent`; `~/.config/systemd/user/jenkins-agent.service`; pinned tools under `/opt/battlefield-ci/tools` | Agent enrollment and service inputs belong to the CI role. Workspace/cache deletion is not a substitute for controller recovery. Preserve the adopted private umask. |
| SonarQube | `/etc/battlefield-ci/sonar-compose.yaml`; Docker root `/var/lib/docker`; named `sonarqube_sonarqube_data`, `_extensions`, `_logs` volumes | Sonar PostgreSQL is `sonarqube_sonarqube_database`, independent of application PostgreSQL. Preserve database password and Jenkins/Sonar integration token; maintenance creates a database dump. |
| K3s Terraform | `~/.local/state/battlefield/k3s/terraform.tfstate`, generated inventory, admin key, known-hosts and encrypted `recovery/` bundles | Canonical state is outside Jenkins workspaces and outside the guest, **but on the host SSD**. The key is snapshotted separately from Terraform state; recover them together. |
| K3s disks / datastore | `/srv/battlefield-data/libvirt/images`; guest `/srv/battlefield/{k3s,kubelet,node,platform,storage}` | K3s uses its retained SQLite datastore. Certificates, token and node registration are retained with the data disk; guest OS replacement must not create a second cluster over missing storage. |
| Application PostgreSQL | `data-battlefield-postgres-0`, 10 GiB request on `battlefield-retained`; guest local-path directory beneath `/srv/battlefield/storage` | Runtime credentials in Kubernetes and private production `.env`; migration dumps in `~/.local/state/battlefield/backups`. Do not recreate the database password during rebuild. |
| Monitoring | Elasticsearch `battlefield-elasticsearch-data`, 20 GiB request on the same retained guest filesystem; Grafana, Prometheus, Tempo and other resources from deployment manifests | Only declared PVC data survives pod/OS changes. Other ephemeral telemetry is recreated. Retained Kind telemetry is separate from K3s data. |
| Production registration | `~/.config/autonomous-battlefield/production/{deployment.env,kubeconfig,kubelet-ca.crt,recovery.json,.env,tunnel-credentials.json}` | Restore the matching cluster registration, signing/Access inputs and recovery record. `~/.kube/config` may still select Kind; always use the explicit production profile. |
| Cloudflare | External account/zone, locally managed tunnel, CNAME, Access application/policies/identity providers/service tokens; local connector JSON above | Workstation API token at `~/.config/autonomous-battlefield/bootstrap/cloudflare-api-token`, imported into Vault. It does not replace the connector secret or Access service-token secret. Preserve application audience/issuer and identity references. |
| CLI VM | `/var/lib/libvirt/images/battlefield-agent/{root.qcow2,seed.iso}`; private `server/cli.json`, `cli.files/`, optional `cli.guest/` and `cli-inputs.vault` on the controller used for capture | Guest `/var/lib/battlefield-worker` contains provider login homes; worker control secret is separate from app/signing/Kubernetes credentials. `~/.ssh/battlefield-vm-admin` is the host's admin key. Rebuild can require interactive provider sign-in; ordinary adoption must preserve homes. |
| Local TLS / trust | `/etc/caddy/Caddyfile`, service user `caddy`, home `/var/lib/caddy`; public roots under `/usr/local/share/ca-certificates/` | The [HTTPS capture](local-https-platform.md#prerequisites-and-capture) verifies and encrypts the matching root certificate/private key beneath `/var/lib/caddy/.local/share/caddy/pki/authorities/local/`. Foundation `ca_files` contains public trust inputs only. Installed public roots alone cannot recreate the issuing CA. |
| Optional Langfuse | Optional endpoint/API-key inputs and separately managed persistent services; see [Langfuse operations](langfuse.md) | No local Langfuse database/storage allocation is assumed by this contract. Audit configured inputs and persistent services before enabling it. |
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

The selected recovery objective for the limited-access service is manual,
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
  Scheduled backups, retention and complete-server-loss recovery are not supplied
  by this contract. The selected future destination is the separate server disk;
  it must first be registered/mounted and verified. That disk would protect against
  loss of the primary SSD, not loss of the complete server; no off-host copy is
  provided by this layout.

State is outside the guest and build workspace, with local locking/recovery
bundles; whole-host-loss protection is deferred. Reproducible configuration cannot
recover lost database bytes or missing external secrets.

## Bootstrap sequence and human prerequisites

Use a reviewed checkout. For recovery, first decide whether state survives or a
clean installation is intended; never run fresh installation against retained
unrecognized state. Existing runbooks own the exact commands:

1. **Operator bootstrap:** install supported Ubuntu, enable KVM, establish network,
   an SSH/sudo account and trusted host-key entry. Keep the current SSH key on the
   workstation; Jenkins need not exist there. The operator enters sudo interactively.
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
   reauthorization remain interactive. Reconcile owned [Cloudflare](cloudflare-platform.md)
   and [local HTTPS](local-https-platform.md) configuration through their explicit
   workflows before claiming a
   complete clean-server rebuild.

For a fresh installation, the human-only categories are base OS/network/SSH/sudo,
external account/token issuance, Vault/key unlock and storage identity selection,
local DNS/browser CA trust, and provider subscription sign-in. Reviewed apply
approvals remain intentional gates. This is not a promise of a fixed click count:
expired credentials or a replacement host can add account recovery steps.

## Verification boundaries

A documented workflow or merged change does not prove that a live host has been
adopted or that a replacement server can be reconstructed. Keep these checks
separate and retain their evidence privately:

- **Host, storage, VM and K3s:** use the runbooks' disposable lifecycle rehearsals
  to check clean installation, repeat apply, reboot, retained state, locking and
  identity/mount rejection. Command `--help` proves startup only.
- **CLI VM:** verify reviewed live no-change adoption, preserved disk/network and
  provider homes, then the [CLI service handoff](cli-vm-platform.md#automatic-ansible-handoff).
  A disposable fixture cannot verify production adoption or provider sign-in.
- **Cloudflare:** capture owned resources privately and verify reviewed adoption
  without changing audience, issuer, service identities or unrelated resources.
  External account/domain ownership and available secrets remain prerequisites.
- **Local HTTPS:** verify protected CA capture and reviewed route migration or
  restoration, including TLS trust and healthy destinations. Preserve SonarQube
  and the separate CLI proxy.
- **Complete reconstruction:** require clean-install, repeat-apply and reboot
  evidence for the required workflows plus verified application delivery and
  restored data/credentials. Worker release updates, monitoring lifecycle and
  Player CLI enablement require their own verification; a server rebuild does
  not establish them automatically.

Recovery remains constrained by the surviving state and credentials above.
Reproducible configuration alone is not evidence of whole-server-loss recovery.
