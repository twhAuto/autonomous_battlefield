# Application delivery to K3s

Application delivery selects the cluster driver from the registered environment's
`BF_DRIVER`. Existing profiles without `BF_DRIVER` select Kind. Register a new
cluster explicitly and use the cutover procedure below to transfer existing data.
Merging delivery code does not change the active production profile or move data.

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
[application schema](../architecture/configuration.md). A disposable rehearsal uses its own database
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
`scripts/cd/rollback.sh`. Jenkins main uses the canonical commands and the same signed-digest,
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

This optional harness remains useful for delivery and recovery changes. While
production holds only test data, a direct migration is accepted without a dedicated
Jenkins rehearsal job or a complete clean-host rebuild. The final export, restore comparison, public verification and recovery
record remain required; see [the cutover procedure](#adoption-and-cutover-boundary).

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
together and must not be empty. Omitting all three retains the older same-digest
reapplication mode,
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
Checkpoints sample incomplete rollout status up to seven times at five-second intervals
so a concurrent HPA scale-up can settle; wrong images still fail immediately. Inspection
requests have a 15-second Kubernetes request timeout and a 30-second process timeout.
API and database inspection output, including failures, is kept in `inspection.private.log`.
Claim-identity and migration-ledger failures are reported separately.
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

The cutover procedure coordinates data transfer, environment registration,
Jenkins credentials and public traffic. Retain the source data and cluster until
the destination passes verification. Host and CI service reconstruction have
separate procedures in [server bootstrap](server-bootstrap.md) and
[CI bootstrap](ci-bootstrap.md); the [server rebuild contract](server-rebuild-contract.md)
defines their recovery boundaries.

Use an idle Jenkins window and hold the shared operator lock throughout the
mutation sequence. Jenkins has an additional resource lock; an external operator
must check that no main or platform operation is active or queued. Do not run a
normal main delivery while the registered profile and its Jenkins credential
point at different clusters.

1. Prepare the separate candidate registration with `TARGET_URL` set to the
   existing public origin; its `BF_URL` must match that origin. Prepare its
   application `.env` with the existing database, authentication, worker and
   capability credentials, but disable managed Players and every provider
   availability flag. Copy the old profile's `BF_TUNNEL_ID` and digest-pinned
   `BF_TUNNEL_IMAGE` into the candidate `deployment.env`, set
   `BF_TUNNEL_CREDENTIALS_FILE=tunnel-credentials.json`, and copy the existing
   tunnel credential file beside it with mode 0600. Keep the generated K3s
   identity, kubeconfig and kubelet CA. Validate without activating the tunnel:

   ```bash
   BATTLEFIELD_ENV_FILE="$HOME/.config/autonomous-battlefield/k3s-candidate/deployment.env" \
     ./scripts/ops/prepare-limited-production.sh
   ```

   Then deploy the currently verified signed image pair privately. Do not
   activate the candidate tunnel. Validate cluster UIDs, matching migration
   versions and database roles, retained claims, private readiness and anonymous
   API denial. Check there are no active battles before the final copy.
2. Save the old registration, recovery record, application configuration, HPA and
   replica counts. Pause both targets' application writers and stop every old
   tunnel connector. Temporarily remove the API HPA so scaling cannot restart
   writers. Wait for application pods and database client sessions to stop.
3. Take a mode-0600 custom-format `pg_dump --no-owner` on the server's data mount.
   Inspect it with `pg_restore --list`, record its SHA-256, and restore it into
   the candidate using `pg_restore --clean --if-exists --no-owner --exit-on-error
   --single-transaction`. This replaces the candidate database contents: verify
   its registered UID and that it contains no user data first. Preserve grants;
   database roles must exist with the required attributes and credentials.
   Compare every public table's row count and content fingerprint, sequence
   values and ordered migration ledger before starting new writers.
4. Update Jenkins's existing `battlefield-production-kubeconfig` file credential
   from the candidate kubeconfig, then adopt the matching registration using
   directory moves under the held lock. `setup-environment.sh` deliberately
   refuses to overwrite a registration; do not rerun it or delete the old profile.

   ```bash
   (
   set -eu
   profile_root="$HOME/.config/autonomous-battlefield"
   test -d "$profile_root/production"
   test -d "$profile_root/k3s-candidate"
   test ! -e "$profile_root/kind-retained-ab132"
   mv -- "$profile_root/production" "$profile_root/kind-retained-ab132"
   mv -- "$profile_root/k3s-candidate" "$profile_root/production"
   )
   ```

   On this server, operator commands and the Jenkins agent run as `twh09` and
   share that home/profile. Another installation must put the adopted profile in
   the actual agent's configuration directory as well. Relative `BF_KUBECONFIG`,
   `BF_KUBELET_CA_FILE` and tunnel credential paths resolve beside the moved
   `deployment.env`. Verify the adopted directory contains that profile,
   `kubeconfig`, `kubelet-ca.crt`, `tunnel-credentials.json` and `.env`, with mode
   0700 on the directory and 0600 on its private files.
   Restore the original production application configuration from the retained
   profile and
   deploy it through `scripts/cd/deploy.sh` with the verified digest pair. This
   recreates the HPA and application replicas. Run
   `./scripts/ops/prepare-limited-production.sh` with the default production
   profile before activating its tunnel. Do not copy the old cluster's
   recovery record into the new profile.
5. Retarget the existing [CLI VM bridge](agent-vm.md) to the production kubeconfig.
   Inspect the effective systemd unit and drop-ins. If the path/context are
   unchanged, restarting the bridge makes it read the adopted kubeconfig; no
   unit rewrite is necessary. Keep the existing matched 28084 bridge/Caddy pair
   during this cutover; do not reinstall the host bundle merely to retarget it.
   Verify the private MCP path from the CLI VM and
   preserve the existing isolation and provider authorization gates. Do not
   claim a new paid-provider inference test from a health response alone.
6. Confirm the old writers and connectors remain stopped, then run
   `scripts/cd/deploy-tunnel.sh` against the new registration. Reuse the existing
   tunnel UUID and credentials, hostname, DNS and Access policies. Never leave
   old and new connectors routing the same hostname to different databases.
7. Run the normal `autonomous-battlefield/main` job with
   `APPLY_CONFIGURATION=true`. Require successful private/public authenticated
   smoke, observability and runtime assurance, plus a new K3s-bound recovery
   record. A response from the public hostname before connector adoption is not
   evidence about K3s. Record the exact main revision, build and digest pair.

The application data moves; existing historical Elasticsearch logs stay on the
retained Kind volume. K3s starts a fresh logging volume. This is not a migration
of historical monitoring data. The two K3s PVC requests remain 10 GiB PostgreSQL
and 20 GiB Elasticsearch on the shared 250 GiB retained filesystem; these are not
quotas or automatic expansion limits.

### Recovery boundary and retained evidence

Before K3s writers start, an aborted migration can restore the saved Kind
registration and Jenkins kubeconfig, restart its original replicas/HPA and bridge,
and reactivate only the old tunnel. First stop any candidate writers/connectors.
Once K3s writers start, even background synchronization may change the database.
Switching traffic or kubeconfigs back alone then loses those changes: pause the
new writers, take another verified export, and reconcile or restore the latest
data with explicit migration compatibility review before returning to Kind.
Normal digest rollback also does not reverse database changes.

Keep the Kind cluster and its volumes for observation, with its application
writers and connectors paused, until an operator explicitly chooses cleanup. Do not delete them automatically after a green build. The
initial cutover retains private server evidence at:

- `/srv/battlefield-data/workspace/ab132-cutover/`: final dump, checksum and table
  comparisons, original application configuration, HPA/replica snapshots and
  private operation logs.
- `~/.config/autonomous-battlefield/kind-retained-ab132/`: original registration,
  kubeconfig, credentials and Kind-bound recovery record.
- `~/.config/autonomous-battlefield/production/`: adopted K3s registration and,
  after successful main verification, its own recovery record.

The native local HTTPS aliases are a separate cutover boundary. Until the local
HTTPS migration runs, they point at retained Kind: the application alias returns
503 and the monitoring aliases show the old cluster. Use the reviewed
[local HTTPS migration](local-https-platform.md) to redirect the application
alias to the Access-protected public origin and retarget Grafana/Kibana to K3s.
Do not restart old application writers to make that obsolete alias respond.

These paths contain secrets and data; keep them private and out of Git/Jenkins
artifacts. There is no new scheduled backup policy or off-host recovery copy.
The one-time export is on the same server and does not protect against losing
that server. Existing migration-time backups remain part of normal CD.
