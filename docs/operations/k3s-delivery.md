# Application delivery to K3s

Application delivery selects the cluster driver from the registered environment's
`BF_DRIVER`; production uses K3s. A profile without `BF_DRIVER` selects Kind, which
only the retained recovery profile still uses. Register a new cluster explicitly
and [move production](#move-production-to-a-new-cluster) to transfer existing
data. Merging delivery code does not change the active production profile or move
data.

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
fail instead of attempting an in-place storage-class migration. Data from another
cluster moves by logical export and import; see
[move production to a new cluster](#move-production-to-a-new-cluster).

For K3s, monitoring smoke opens a temporary port forward to the selected
cluster's Envoy service on local port 18888 by default, separate from the Kind
listener on 8888. `SMOKE_TEST_GATEWAY_PORT` can select another free port.
Production application smoke still verifies Cloudflare denial and the enrolled
read-only CI service identity. Public smoke proves the candidate serves traffic
only after its tunnel is active; a public response from another cluster does not. Langfuse is explicitly unsupported on this K3s delivery profile.

The official [K3s logging locations](https://docs.k3s.io/faq) and
[Envoy service selectors](https://gateway.envoyproxy.io/docs/tasks/operations/deployment-mode/)
match the shared collector and the driver-specific port forward.

## Disposable delivery rehearsal

This optional harness exercises delivery and recovery changes on a disposable VM
before they reach production. It does not replace the export, restore comparison,
public verification and recovery record that a
[cluster move](#move-production-to-a-new-cluster) requires.

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

A record without migration state does not authorize automatic rollback; the
next successful `main` delivery writes a complete one. Missing PostgreSQL or unreadable migration
metadata also prevents claiming a verified recovery target. A recovery `read`
returns no approved digest when the ledger is unavailable, empty or malformed,
so a forward deployment can still attempt its normal database/migration gates.
Recording a new verified target remains a hard failure in that situation.
Recovery reads and rollback check the registered cluster UID, node identity and
retained storage contract; they do not require controllers to finish restarting
or match delivery pins. Registration, new delivery and recording verified recovery
continue to require the pinned ready platform. Kubernetes admission still applies
to image changes.

## Move production to a new cluster

Use this procedure to move production to a newly registered cluster, for example a
replacement K3s VM or a cluster rebuilt during recovery. It coordinates the data
transfer, environment registration, Jenkins credentials and public traffic. Keep the
old cluster and its data until the new one passes verification. Host and CI service
reconstruction have separate procedures in [server bootstrap](server-bootstrap.md)
and [CI bootstrap](ci-bootstrap.md); the
[server rebuild contract](server-rebuild-contract.md) defines their recovery
boundaries.

Use an idle Jenkins window and hold the shared operator lock throughout the
mutation sequence. Jenkins has an additional resource lock; an external operator
must check that no `main` or platform operation is active or queued. Do not run a
normal `main` delivery while the registered profile and its Jenkins credential
point at different clusters. Pick a name for the profile the old registration
will move to (`<retained-profile>`, for example `retained-YYYYMMDD`) and a new
private evidence directory under `/srv/battlefield-data/workspace/`.

1. **Prepare the candidate.** Create the [separate registration](#prepare-a-separate-registration)
   with `TARGET_URL` set to the existing public origin; its `BF_URL` must match it.
   Prepare its application `.env` with the existing database, authentication,
   worker and capability credentials, but disable managed Players and every
   provider availability flag. Copy the old profile's `BF_TUNNEL_ID` and
   digest-pinned `BF_TUNNEL_IMAGE` into the candidate `deployment.env`, set
   `BF_TUNNEL_CREDENTIALS_FILE=tunnel-credentials.json`, and copy the existing
   tunnel credential file beside it with mode 0600. Keep the candidate's own
   cluster identity, kubeconfig and kubelet CA. Validate without activating the
   tunnel:

   ```bash
   BATTLEFIELD_ENV_FILE="$HOME/.config/autonomous-battlefield/k3s-candidate/deployment.env" \
     ./scripts/ops/prepare-limited-production.sh
   ```

   Then deploy the currently verified signed image pair privately, without
   activating the candidate tunnel. Check the cluster UIDs, matching migration
   versions and database roles, retained claims, private readiness and anonymous
   API denial, and that no battles are active before the final copy.
2. **Stop writers.** Save the old registration, recovery record, application
   configuration, HPA and replica counts. Pause both clusters' application writers
   and stop every old tunnel connector. Temporarily remove the API HPA so scaling
   cannot restart writers, and wait for application pods and database client
   sessions to stop.
3. **Copy the database.** Take a mode-0600 custom-format `pg_dump --no-owner` into
   the evidence directory. Inspect it with `pg_restore --list`, record its SHA-256,
   and restore it into the candidate with `pg_restore --clean --if-exists
   --no-owner --exit-on-error --single-transaction`. This replaces the candidate
   database, so first verify its registered UID and that it holds no user data.
   Preserve grants: database roles must already exist with the required attributes
   and credentials. Compare every public table's row count and content
   fingerprint, the sequence values and the ordered migration ledger before
   starting new writers.
4. **Adopt the registration.** Update Jenkins's `battlefield-production-kubeconfig`
   file credential from the candidate kubeconfig, then swap the registrations with
   directory moves under the held lock. `setup-environment.sh` deliberately refuses
   to overwrite a registration; do not rerun it or delete the old profile.

   ```bash
   (
   set -eu
   retained='<retained-profile>'
   profile_root="$HOME/.config/autonomous-battlefield"
   test -d "$profile_root/production"
   test -d "$profile_root/k3s-candidate"
   test ! -e "$profile_root/$retained"
   mv -- "$profile_root/production" "$profile_root/$retained"
   mv -- "$profile_root/k3s-candidate" "$profile_root/production"
   )
   ```

   On the registered server, operator commands and the Jenkins agent run as the
   same [registered operator](server-rebuild-contract.md#registered-facts) and
   share this home and profile. Another installation must also put the adopted
   profile in the actual agent's configuration directory. Relative
   `BF_KUBECONFIG`, `BF_KUBELET_CA_FILE` and tunnel credential paths resolve beside
   the moved `deployment.env`. Check that the adopted directory contains that
   profile, `kubeconfig`, `kubelet-ca.crt`, `tunnel-credentials.json` and `.env`,
   with mode 0700 on the directory and 0600 on its private files.

   Restore the original production application configuration from the retained
   profile and deploy it through `scripts/cd/deploy.sh` with the verified digest
   pair, which recreates the HPA and application replicas. Run
   `./scripts/ops/prepare-limited-production.sh` with the default production
   profile before activating its tunnel. Never copy the old cluster's recovery
   record into the new profile.
5. **Retarget the CLI bridge.** Point the existing
   [CLI VM bridge](cli-worker-verification.md) at the production kubeconfig.
   Inspect the effective systemd unit and drop-ins: if the path and context are
   unchanged, restarting the bridge makes it read the adopted kubeconfig, and no
   unit rewrite is needed. Keep the existing matched 28084 bridge/Caddy pair; do
   not reinstall the host bundle just to retarget it. Check the private AI gateway
   path from the CLI VM and keep the existing isolation and provider authorization
   gates. A health response alone is not a paid-provider inference test.
6. **Switch traffic.** Confirm the old writers and connectors are still stopped,
   then run `scripts/cd/deploy-tunnel.sh` against the new registration. Reuse the
   existing tunnel UUID and credentials, hostname, DNS and Access policies. Never
   let old and new connectors route the same hostname to different databases.
7. **Verify through `main`.** Run `autonomous-battlefield/main` with
   `APPLY_CONFIGURATION=true`. Require successful private and public authenticated
   smoke, observability and runtime assurance, and a new recovery record bound to
   the new cluster. A public response before the connector moved is not evidence
   about the new cluster. Record the exact `main` revision, build and digest pair.
8. **Retarget local HTTPS.** The host's Grafana and Kibana aliases are bound to
   the registered cluster's identity. Follow
   [recover ownership after an intentional change](local-https-platform.md#recover-ownership-after-an-intentional-change)
   and run a reviewed `migrate --k3s-routes` so they reach the new cluster's
   gateway. The application alias keeps redirecting to the public origin.

Only application data moves. Historical Elasticsearch logs stay on the old
cluster's volume, and the new cluster starts a fresh logging volume. The PVC
requests (10 GiB PostgreSQL, 20 GiB Elasticsearch) share the retained filesystem;
they are not quotas or expansion limits.

### Abort and retained evidence

Before the new cluster's writers start, an aborted move can restore the saved
registration and Jenkins kubeconfig, restart the original replicas, HPA and
bridge, and reactivate only the old tunnel; stop any candidate writers and
connectors first. Once the new writers start, even background synchronization can
change the database. Switching traffic or kubeconfigs back then loses those
changes: pause the new writers, take another verified export, and reconcile or
restore the latest data with an explicit migration-compatibility review before
returning. Digest rollback does not reverse database changes either.

Keep the old cluster and its volumes, with application writers and connectors
paused, until an operator explicitly chooses cleanup; never delete them
automatically after a green build. Keep the move's private evidence: the dump,
checksum and table comparisons, original application configuration, HPA and
replica snapshots and operation logs in the evidence directory, and the old
registration, kubeconfig, credentials and recovery record in
`~/.config/autonomous-battlefield/<retained-profile>/`. These contain secrets and
data, so keep them out of Git and Jenkins artifacts. The export lives on the same
server, so it does not protect against losing that server, and the move adds no
scheduled or off-host backup.
