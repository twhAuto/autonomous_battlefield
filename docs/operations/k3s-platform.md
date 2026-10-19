# Configure the private K3s platform

The K3s platform job configures the guest created by
[the Terraform VM job](k3s-vm.md). It installs
K3s, retained storage, Envoy Gateway, Kyverno and private validation workloads.
Application delivery and registration are described in [K3s delivery](k3s-delivery.md).
Production application traffic and PostgreSQL now use K3s; the Cloudflare
connector runs there too. The old Kind resources remain a retained recovery
source. See the [rebuild contract](server-rebuild-contract.md). Merging
platform code does not run an apply.

## Run the job

Prerequisites: successful host and VM verification; the existing Jenkins
`battlefield-ghcr` username/password credential; and a successful main delivery
that recorded the verified backend digest in the registered production
`recovery.json`. The job uses that image for its private pull check. It needs no
new Cloudflare token or workstation SSH key.

After promotion to `main`, use the existing SSH tunnel and update job definitions:

```bash
python3 scripts/infra/seed-platform.py --jobs-only \
  --url http://127.0.0.1:18080 --user whtan
```

The command prompts privately for a Jenkins API token. It validates all three
pipelines and preserves credentials; it starts no builds. Then open
`battlefield-platform/k3s` and choose **Build with Parameters**:

1. Run `ACTION=plan`. Download `k3s-plan.json` from the build artifacts. Check the
   revision, pinned versions and changed tasks. The first plan declares retained
   disk initialization and platform installation. If K3s is stopped, the plan
   declares restoring the managed runtime before Kubernetes diff is available.
   Inspecting a blank disk does
   not mount or format it.
2. Run `ACTION=apply`. This build generates another fresh plan and pauses for
   `whtan` to approve **Apply reviewed plan** within ten minutes. Open the build's
   approval page, review its artifact, then approve there.
3. Require a successful apply and verification. The job compares a fresh plan
   with the approved change digest before consuming its receipt. Changed main,
   inputs, VM state or guest configuration require a new plan. Approval expires
   after thirty minutes; a failed/interrupted apply needs a new approval.
4. Run `ACTION=verify` after a guest reboot or maintenance. It checks readiness,
   cluster/PVC identity, networking, private pulls and zero configuration drift.

The job holds the same Jenkins resource and operator lock as host/VM operations
and production delivery. It therefore queues behind a deployment, or temporarily
queues delivery while awaiting review. An expired approval releases the lock.
The job has a 90-minute overall limit, including the approval wait. First install
reads the entire 250 GiB retained disk to prove it is empty before formatting;
that scan has a 30-minute limit, so allow substantially longer than a repeat
apply. Duration depends on storage throughput and image downloads. If a run
is interrupted, review a new plan; ownership markers preserve completed storage
initialization rather than formatting it again.

Plans may download verified assets into private controller caches and transfer
Ansible inspection helpers; they do not install the guest runtime.

## Storage and identity

| Location | Purpose |
| --- | --- |
| Host `/srv/battlefield-data/libvirt/images` | Existing VM OS and retained data images |
| Guest `/srv/battlefield` | ext4 mount of the retained virtual disk |
| Guest `/srv/battlefield/k3s` | K3s SQLite datastore, certificates, token and container images |
| Guest `/srv/battlefield/storage` | Persistent volume directories |
| Guest `/srv/battlefield/kubelet` | Kubelet state and mounted workload volumes |
| Guest `/srv/battlefield/node` | Retained node registration password, linked from `/etc/rancher/node` |
| Guest `/srv/battlefield/platform` | Cluster/PVC ownership records |
| Guest OS disk | Ubuntu, packages and managed configuration |

The default Terraform allocation is 250 GiB for retained guest data and 32 GiB
for its OS; actual host usage grows with the sparse images. This is a capacity
allocation, not an additional physical disk. Existing Docker/Kind/PostgreSQL
files still use their current host filesystem. Moving application data between
clusters requires the reviewed [migration procedure](k3s-delivery.md#adoption-and-cutover-boundary).

Only the exact stable `virtio-battlefield-k3s-data` device is accepted. The role
checks guest UUID, disk serial, capacity, filesystem UUID, ownership marker and
mount. First formatting requires an unpartitioned disk with no signatures and
all bytes zero; unrelated filesystems are rejected. A guest storage lock is
inherited by formatting children, preventing concurrent initialization after a
controller disconnect. The retained filesystem
identity derives from host storage and volume path, so replacing only the VM OS
preserves its identity. The [node registration password](https://docs.k3s.io/architecture)
also stays on retained storage, preventing a replacement OS from generating a
different password for the existing Kubernetes node. An interrupted initial format can resume only with its
matching ownership intent. Existing mounts and fstab conflicts are rejected.

K3s requires the registered mount through systemd dependencies and startup
checks. Missing, wrong or read-only storage prevents startup. Registered SQLite
state/token loss prevents a silent new cluster. Repeat apply preserves the
server token, namespace UID, registry secret and validation PVC, and changes
restart K3s only when the managed runtime configuration needs it.

`battlefield-retained` uses K3s local-path provisioning, `Retain` reclaim policy
and `WaitForFirstConsumer`. Deleting a claim does not automatically erase its PV.
Local-path PVC requests **do not enforce disk quotas**. Images, logs and volumes
share the retained filesystem; a full disk can stop database writes and cluster
operations. Check both `df -h /srv/battlefield-data` on the host and
`df -h /srv/battlefield` inside the guest. Maintain headroom, remove only reviewed
unused images/logs, and plan expansion before capacity is exhausted. There is no
automatic disk expansion, eviction guarantee, or new scheduled backup policy.

SQLite recovery needs both the datastore and server token; see
[the K3s backup requirements](https://docs.k3s.io/datastore/backup-restore).
The encrypted Terraform recovery bundle contains control-plane provisioning
state and SSH access, **not** K3s state, application databases or retained disk
contents. Scheduled backups are not configured. This workflow is reproducible
infrastructure, not a claim of data recovery after disk failure.

## Private networking and version ownership

[`settings.json`](../../infra/k3s/settings.json) declares nonoverlapping pod and
service ranges, DNS, SQLite and optional dependency settings. Host route overlap
fails before guest changes. Cluster CIDR/datastore changes require an explicit
migration once initialized. The guest uses embedded Flannel and K3s network-policy
enforcement, CoreDNS and metrics-server. Traefik and ServiceLB are disabled on
first start. Kind is not installed inside the guest.

The guest firewall owns one nftables table and service. Host/operator access and
pod traffic can reach private API, SSH, kubelet, kube-proxy health and NodePort ports; other sources
are blocked, including NodePort checks before kube-proxy destination translation.
Verification compares the live owned rules with their recorded applied state.
Runtime drift fails verification and appears as a reload in the next plan; the
reload changes only this table and preserves the running K3s service.
It does not edit the host firewall, Docker networks or the existing CLI VM.
The validation gateway is private and has no public DNS/tunnel route. Application
delivery owns the CLI/AI gateway route through the intended private bridge; verify that
route before enabling CLI execution on K3s. Player CLI access requires the separate
[isolation and accepted-plan checks](cli-worker-verification.md#verify-before-advertising-availability).

[`pins.json`](../../infra/k3s/pins.json) pins and checksums K3s, Envoy Gateway's
controller/CRDs and Kyverno. The chosen Kubernetes version is within both
[Envoy's compatibility matrix](https://gateway.envoyproxy.io/news/releases/matrix/)
and [Kyverno's supported range](https://kyverno.io/docs/installation/releases/).
The Envoy certificate setup Job is retained after completion and named by its
specification hash, so reruns do not recreate it and upgrades do not patch an
immutable Job. Old completed versions can be removed after reviewed upgrades.
Any new controller Job requires explicit lifecycle review, including Jobs without
a cleanup TTL. These roles own controller installation; application CD owns routes
and signature policies. K3s delivery validates the managed versions and does not
install the Kind pipeline's Kyverno controller over this cluster.

Observability prerequisites default on: kernel memory-map limit, the metrics API,
and verified kubelet TLS using the actual serving CA and IP SAN. No kubelet TLS
verification bypass is introduced. Langfuse is explicitly disabled and remains
unsupported by the K3s delivery profile. Supporting it requires a compatible
optional delivery profile and controller dependency tests. Neither application observability stacks nor
Langfuse data stores are installed by this platform job.

## Verification and private outputs

The isolated `battlefield-platform-checks` namespace holds a small retained
claim, HTTP server, allowed/denied clients and private-image probe. Verification
waits for an active service, API readiness and current controller generations
through bounded read-only retries, so brief API reconnects after boot do not
fail immediately; persistent outages still fail. It checks a healthy allowed
path before requiring the denied path to fail, DNS
resolution, gateway routing, private image readiness and a fresh authenticated
registry manifest request using the current pull secret, retained data identity,
controller readiness, metrics and K3s service state. The private-image probe
runs a static test command instead of starting the backend application. Registry
verification uses HTTPS, rejects credential-forwarding redirects, and limits
Bearer-token exchange to the GHCR token endpoint; cached pods alone do not prove
the current credential still works.

Successful apply/verify writes separate private files under
`~/.local/state/battlefield/k3s/`: `kubeconfig` and `kubelet-ca.crt`. The existing
Kind profile is preserved. [K3s registration](k3s-delivery.md#prepare-a-separate-registration)
consumes these files to create an explicit delivery profile. Jenkins artifacts contain task outcomes and
version/storage declarations, never kubeconfig, credentials or raw diffs.
Registry inputs live in a temporary mode-0600 file and a no-log Ansible task. Pull
secrets are created only in the application and validation namespaces. Application
CD owns Kyverno image-verification credentials alongside the admission policy; this
platform job does not pre-create an incompatible pull secret in that namespace.

## Disposable acceptance test

The opt-in rehearsal needs libvirt access, 10 GiB available RAM, an existing
`default` NAT network, 44 GiB free in `/var/lib/libvirt/images`, a new (not yet
existing) private evidence directory, and the checksum-pinned Ubuntu 24.04 image from
[`rehearsal-image.json`](../../infra/ansible/rehearsal-image.json):

```bash
python3 scripts/infra/rehearse-k3s.py \
  --image /absolute/path/to/pinned-ubuntu-24.04.img \
  --evidence-dir /absolute/private/new-evidence-directory
```

It creates an owned 8 GiB RAM guest with a separate empty 8 GiB virtual data disk,
runs the actual roles using an authenticated TLS registry fixture with synthetic
credentials and the same single-platform BusyBox pin as the platform probes. It
checks stable initial plans, refuses nonzero unformatted data, detects and repairs
live firewall drift, and checks fresh registry authentication (including revoked access), apply/reapply,
zero drift, unchanged secret identity and K3s start time, reboot persistence,
reviewed restoration of a stopped service and missing OS node-registration link,
and absent/wrong-mount guard refusal. Cleanup addresses
only the generated domain/pool identities and compares existing domain/network
inventories. Reports remain private outside Git; failed guest journal evidence
may contain operational information. The production CLI still requires clean
trusted `origin/main`, canonical Terraform ownership and normal Jenkins approval.
