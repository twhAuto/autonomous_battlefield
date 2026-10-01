# Application delivery to K3s

[AB-129 — Support K3s delivery while preserving Kind development](https://app.notion.com/p/3eab7a19f8f781e6bb1ad38d5d25ef0f)
adds an explicit driver to the registered production environment. Existing profiles
without `BF_DRIVER` continue to select Kind. This code does not change the active
production profile or move existing data when merged.

The [platform job](k3s-platform.md) owns the VM's K3s installation, retained
filesystem, storage class, firewall, Envoy and Kyverno controllers. Application
CD validates those resources and owns application manifests, registry credentials
and the image-verification policy. It never invokes Terraform or Ansible and
never installs the Kind Kyverno controller on K3s.

## Prepare a separate registration

Run registration on the server after a successful platform apply/verify. Keep the
current production registration for its existing cluster. A separate directory
lets the operator prepare the new target without redirecting Jenkins:

```bash
KUBECONFIG="$HOME/.local/state/battlefield/k3s/kubeconfig" \
KUBELET_CA_FILE="$HOME/.local/state/battlefield/k3s/kubelet-ca.crt" \
BATTLEFIELD_CONFIG_DIR="$HOME/.config/autonomous-battlefield/k3s-candidate" \
BATTLEFIELD_ENV_FILE="$HOME/.config/autonomous-battlefield/k3s-candidate/deployment.env" \
TARGET_URL=https://play.ordinalfrontier.com \
  ./scripts/cd/setup-environment.sh production default k3s
```

Registration reads the live namespace UID, single-node identity, versions,
controller readiness and retained storage contract. It copies the dedicated
kubeconfig and public kubelet CA into mode-0600 files outside the repository.
Existing files are never overwritten. A cluster replacement needs an explicit
new registration and reviewed recovery; editing the UID is not recovery.

Prepare the candidate's private `.env` beside its `deployment.env` using the
[application schema](environments.md). A disposable rehearsal uses its own database
and synthetic application secrets, with managed Players and all AI/CLI provider
profiles disabled. Copying production credentials into a second active database
can create unintended external effects. The current production file remains the
source for the later reviewed migration, not a substitute for migrating data.

`BF_DRIVER=k3s` requires production mode, a trusted `BF_KUBELET_CA_FILE`, and no
`BF_DOCKER_HOST`. K3s delivery never calls the Kind CLI or uses Docker to identify
the cluster. The controller still needs Docker for the existing Cosign verifier,
as well as Node.js, Python, Git, kubectl and authenticated GHCR configuration.
For K3s, the existing controller Docker context selects the Cosign container runtime;
it does not identify the Kubernetes cluster. Conflicting driver/context/namespace/image-source settings and wrong live UIDs
fail before application changes.

## Deliver and verify

The canonical commands are `scripts/cd/deploy.sh`, `scripts/cd/smoke-test.sh` and
`scripts/cd/rollback.sh`. The former `*-kind.sh` commands remain forwarding
launchers. Jenkins main uses the canonical commands and the same signed-digest,
provenance, migration-backup, smoke, runtime-assurance and recovery stages.

The registered profile selects the overlays. K3s composes shared manifests with:

| Resource | K3s configuration |
| --- | --- |
| PostgreSQL claim | `battlefield-retained`, 10 GiB request |
| Elasticsearch claim | `battlefield-retained`, 20 GiB request |
| Gateway | Private guest NodePort 30080, existing route names |
| Prometheus | Exported K3s serving CA, TLS verification enabled |
| Fluent Bit | CRI logs under `/var/log/containers` and `/var/log/pods` |
| Kyverno | Platform-owned controller; CD-owned policy and registry Secret |

These PVC requests are not filesystem quotas. They share the 250 GiB retained
filesystem with K3s and images. Existing incompatible claims cause preflight to
fail instead of attempting an in-place storage-class migration. Existing Kind
volumes require logical export/import during the migration ticket.

For K3s, monitoring smoke opens a temporary port forward to the selected
cluster's Envoy service on local port 18888 by default. It does not reuse the
old Kind listener on 8888. `SMOKE_TEST_GATEWAY_PORT` can select another free port.
Production application smoke still verifies Cloudflare denial and the enrolled
read-only CI service identity. Public smoke must run only after a reviewed
cutover; a public response from the old Kind target does not prove the candidate
serves traffic. Langfuse is explicitly unsupported on this K3s delivery profile.

The official [K3s logging locations](https://docs.k3s.io/faq) and
[Envoy service selectors](https://gateway.envoyproxy.io/docs/tasks/operations/deployment-mode/)
match the shared collector and the driver-specific port forward.

## Disposable delivery rehearsal

Run `scripts/infra/rehearse-k3s-delivery.py` on the libvirt controller to exercise
real delivery before adoption. It uses the pinned Ubuntu 24.04 cloud image, an
existing active `default` NAT network, 4 vCPUs, 8 GiB RAM, a 32 GiB OS disk and a
40 GiB retained data disk. It requires at least 10 GiB available RAM and 80 GiB
free space in `/var/lib/libvirt/images` before creating its owned VM. Existing
VMs and networks are recorded and checked after cleanup.

```bash
python3 scripts/infra/rehearse-k3s-delivery.py \
  --image /private/pinned-ubuntu-24.04.img \
  --registry-config /private/ghcr-pull-config.json \
  --public-key /private/cosign.pub \
  --backend "$VERIFIED_BACKEND_DIGEST" \
  --frontend "$VERIFIED_FRONTEND_DIGEST" \
  --revision "$VERIFIED_SOURCE_REVISION" \
  --previous-backend "$PREVIOUS_VERIFIED_BACKEND_DIGEST" \
  --previous-frontend "$PREVIOUS_VERIFIED_FRONTEND_DIGEST" \
  --previous-revision "$PREVIOUS_VERIFIED_SOURCE_REVISION" \
  --evidence-dir /private/new-delivery-rehearsal
```

Use previously signed `repository@sha256:...` images and a full revision available
in the checkout. The private registry file uses Docker's `auths.ghcr.io.auth`
format and read-only package access. The public verification key must be the
trusted signing key. Both full source revisions must exist in the checkout, differ,
and refer to different image pairs. The previous-release arguments must be supplied
together. Omitting all three retains the older same-digest reapplication mode,
which explicitly reports `distinct_release_rollback: false`.
The evidence directory must be new and outside the checkout.
The harness holds the existing operator lock and invokes the actual platform
roles, registration, signature/attestation checks, migration backup, migrations,
application/observability rollout, configuration-only delivery, upgrade to the
second signed release and database-bound rollback to the previous exact pair.
Each deployment verifies its expected source-revision signature annotations.
Choose releases with compatible migration state: the rehearsal fails before rollback
if the live ledger changes. It does not run down migrations or infer compatibility. Synthetic application
secrets and disabled managed Players prevent a second production application from being activated.

`report.json` records source hashes and checkpoints for the previous release,
configuration-only delivery, the new release and restoration. Each checkpoint
requires all five application Deployments to have the exact expected images and
completed healthy rollouts, both retained claims to stay bound to the same PVC/PV
identities, an unchanged migration ledger and passing private readiness/anonymous
denial checks. The previous recovery record is retained throughout the upgrade;
the fixture's private record never authorizes recovery of production. Overall
success also requires VM cleanup and preserved existing VM/network identities.
Command output stays in private logs because it may contain sensitive deployment diagnostics. Do not publish those logs. The VM and
its generated application credentials are removed on normal success or failure.
An interrupted controller or failed cleanup requires inspecting the owned VM
before another rehearsal.

This checks private application delivery. It does not run the complete Jenkins
main pipeline, authenticate through public Cloudflare Access, or retarget the real
CLI VM. Distinct-release rollback is proven only when the three previous-release
arguments are supplied and every corresponding checkpoint passes. The remaining
acceptance checks must be recorded explicitly; a private pass cannot stand in for them.

## Database-aware rollback

Successful production verification records the cluster UID, both immutable
images and the ordered applied Goose migration versions in private `recovery.json`.
Rollback re-reads the live migration ledger before setting images. It requires
the previous Access-compatible digest, verified signatures and attestations,
and an exact recovery-record/database match. A migration change fails closed;
review the old binary's database compatibility and perform database recovery as
needed. The tooling does not infer backward compatibility or run down migrations.
Manual schema edits are outside the migration-ledger check and require review.

Older records without migration state do not authorize automatic rollback. The
next successful main delivery writes the new format. A failed release before
that point requires operator recovery. Missing PostgreSQL or unreadable migration
metadata also prevents claiming a verified recovery target. A recovery `read`
returns no approved digest when the ledger is unavailable, empty or malformed,
so a forward deployment can still attempt its normal database/migration gates.
Recording a new verified target remains a hard failure in that situation.
Recovery reads and rollback check the registered cluster UID, node identity and
retained storage contract; they do not require controllers to finish restarting
or match delivery pins. Registration, new delivery and recording verified recovery
continue to require the pinned ready platform. Kubernetes admission still applies
to image changes.

## Adoption and cutover boundary

[AB-132 — Rehearse full rebuild and migrate production from Kind to K3s](https://app.notion.com/p/3eab7a19f8f7819b91b4c2f502e29e4f)
owns the separately approved production migration. Its concrete plan must:

1. Rehearse restore into the new PostgreSQL and compare domain data, identities,
   invitations and applicable persistent observability data.
2. Re-render the existing [CLI VM bridge](agent-vm.md) with the new kubeconfig,
   verify the actual private MCP path, and retain Player CLI authorization gates.
3. Hold the shared platform/delivery lock, stop writers, take and validate the
   final logical backup, restore and perform private validation.
4. Adopt the reviewed profile and matching Jenkins kubeconfig credential, stop
   old tunnel connectors, activate the new target, and verify public authenticated
   traffic and runtime assurance before resuming normal delivery.
5. Record the verified recovery target and retain old Kind until the agreed
   observation window ends. Routing back after new writes needs a data recovery
   plan; it cannot safely revert database divergence by itself.

There is no new scheduled backup policy. One-time migration backups and retained
database data are still necessary for this operation. This implementation and
its command-double regression tests do not claim the live cutover is complete.
